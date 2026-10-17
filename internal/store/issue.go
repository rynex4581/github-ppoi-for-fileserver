package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// イベントの種類。
const (
	EventCreated  = "created"   // Issueの作成。title と body を持つ
	EventComment  = "comment"   // コメント。body を持つ
	EventClosed   = "closed"    // クローズ。body があればコメントも兼ねる
	EventReopened = "reopened"  // 再オープン。body があればコメントも兼ねる
	EventTitle    = "title"     // タイトルの変更。title を持つ
	EventLabels   = "labels"    // ラベルの変更。変更後のラベルID全体を labels に持つ
	EventAssigned = "assignees" // 担当者の変更。変更後のログオン名全体を assignees に持つ

	// 以下は Pull Request 用
	EventReview        = "review"         // レビュー。state（approved / changes_requested / comment）、body、commit_sha を持つ
	EventReviewComment = "review_comment" // 行コメント。body、commit_sha、path、line、side、diff_hunk を持つ
	EventMerged        = "merged"         // マージ。merge_sha と、マージした時点の head_sha を持つ
	EventReverted      = "reverted"       // マージの取り消し。revert_sha と merge_sha を持つ
)

// レビューの状態。
const (
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewComment          = "comment"
)

// Event はIssueに対する操作1件。events フォルダの1ファイルに対応する。
type Event struct {
	Type      string    `json:"type"`
	User      string    `json:"user"`
	Time      time.Time `json:"time"`
	Title     string    `json:"title,omitempty"`
	Body      string    `json:"body,omitempty"`
	Labels    []int     `json:"labels,omitempty"`    // type が labels のとき、変更後のラベルID全体（空なら全解除）
	Assignees []string  `json:"assignees,omitempty"` // type が assignees のとき、変更後の担当者全体（空なら全解除）

	// Pull Request 用。created で is_pull が true のものが Pull Request
	IsPull    bool   `json:"is_pull,omitempty"`
	Head      string `json:"head,omitempty"`       // created: 取り込む側のブランチ
	Base      string `json:"base,omitempty"`       // created: 取り込まれる側のブランチ
	HeadSHA   string `json:"head_sha,omitempty"`   // created: 作成時の head の先頭。merged: マージした head の先頭
	CommitSHA string `json:"commit_sha,omitempty"` // review / review_comment: 対象にした head のコミット
	State     string `json:"state,omitempty"`      // review: approved / changes_requested / comment
	Path      string `json:"path,omitempty"`       // review_comment: ファイルのパス
	Line      int    `json:"line,omitempty"`       // review_comment: 行番号
	Side      string `json:"side,omitempty"`       // review_comment: new（追加・文脈の行）か old（削除の行）
	DiffHunk  string `json:"diff_hunk,omitempty"`  // review_comment: 当該行の前後の差分の断片
	MergeSHA  string `json:"merge_sha,omitempty"`  // merged / reverted: マージコミット
	RevertSHA string `json:"revert_sha,omitempty"` // reverted: 取り消しのコミット

	// File は読み込み元のファイル名。書き込み時は空でよい。
	File string `json:"-"`
}

// Issue はイベントを時系列に適用して組み立てた現在の状態。
type Issue struct {
	Number    int
	Title     string
	Body      string
	State     string // "open" または "closed"
	User      string // 作成者のログオン名
	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  time.Time // openのときはゼロ値
	Comments  int       // コメント数（クローズ・再オープン時の本文も数える）
	Labels    []int     // 付いているラベルのID（削除済みのラベルのIDが残ることがある）
	Assignees []string  // 担当者のログオン名
	Events    []Event

	// Pull Request 用
	IsPull        bool
	Head          string
	Base          string
	HeadSHA       string // 作成時の head の先頭
	Merged        bool
	MergeSHA      string
	MergedHeadSHA string // マージした時点の head の先頭
	MergedBy      string
	MergedAt      time.Time
	RevertSHA     string
	RevertedBy    string
	RevertedAt    time.Time
	Reviews       []Review // 利用者ごとの最新の承認状態（approved / changes_requested のみ）
}

// Review は利用者1人の最新の承認状態。
type Review struct {
	User      string
	State     string
	Time      time.Time
	CommitSHA string
}

// Approved は承認（最新の状態が approved）が1つ以上あるかを返す。
func (is Issue) Approved() bool {
	for _, r := range is.Reviews {
		if r.State == ReviewApproved {
			return true
		}
	}
	return false
}

// HasLabel はラベルが付いているかを返す。
func (is Issue) HasLabel(id int) bool {
	for _, x := range is.Labels {
		if x == id {
			return true
		}
	}
	return false
}

// HasAssignee は担当者に含まれるかを返す。
func (is Issue) HasAssignee(login string) bool {
	for _, x := range is.Assignees {
		if x == login {
			return true
		}
	}
	return false
}

// ErrIssueNotFound はIssueが無いときのエラー。
var ErrIssueNotFound = errors.New("Issueが無い")

// 採番の再試行の上限。他の端末が連続して採番していても、ここまで試せば十分とみなす。
const allocateRetryLimit = 1000

func (s *Store) issuesDir(repo string) string {
	return filepath.Join(s.repoMetaDir(repo), "issues")
}

func (s *Store) issueDir(repo string, number int) string {
	return filepath.Join(s.issuesDir(repo), fmt.Sprintf("%06d", number))
}

func (s *Store) eventsDir(repo string, number int) string {
	return filepath.Join(s.issueDir(repo, number), "events")
}

// allocateNumber はIssue番号を採番する。
// issues 配下に番号のフォルダを「既に存在すれば失敗する」方式で作り、作成できた番号を返す。
// 既存の最大＋1から試し、失敗したら＋1して再試行する。
func (s *Store) allocateNumber(repo string) (int, error) {
	dir := s.issuesDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	numbers, err := s.listIssueNumbers(repo)
	if err != nil {
		return 0, err
	}
	next := 1
	if len(numbers) > 0 {
		next = numbers[len(numbers)-1] + 1
	}
	for n := next; n < next+allocateRetryLimit; n++ {
		err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("%06d", n)), 0o755)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return 0, err
		}
	}
	return 0, errors.New("Issue番号の採番に失敗した（再試行の上限に達した）")
}

// listIssueNumbers は issues 配下の番号フォルダを昇順で返す。フォルダが無ければ空。
func (s *Store) listIssueNumbers(repo string) ([]int, error) {
	entries, err := os.ReadDir(s.issuesDir(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var numbers []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.Atoi(e.Name())
		if err != nil || n <= 0 {
			continue
		}
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	return numbers, nil
}

// eventFileName はイベントのファイル名を作る。
// 「UTC時刻（ミリ秒まで）_端末名_端末内連番.json」で、他の端末と重複しないようにする。
func (s *Store) eventFileName(t time.Time) string {
	t = t.UTC()
	seq := s.seq.Add(1)
	return fmt.Sprintf("%s%03dZ_%s_%03d.json", t.Format("20060102T150405"), t.Nanosecond()/1e6, s.host, seq)
}

// writeEvent はイベントを1ファイルとして書く。Time が空なら現在時刻を入れる。
func (s *Store) writeEvent(dir string, ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	// ファイル名と同じミリ秒までに揃える
	ev.Time = ev.Time.UTC().Truncate(time.Millisecond)
	return writeJSONAtomic(filepath.Join(dir, s.eventFileName(ev.Time)), ev)
}

// CreateIssue は番号を採番し、created イベントを書いて、組み立てたIssueを返す。
// labels と assignees が空でなければ、続けてそれぞれのイベントも書く。
func (s *Store) CreateIssue(repo, user, title, body string, labels []int, assignees []string) (Issue, error) {
	if _, err := s.GetRepo(repo); err != nil {
		return Issue{}, err
	}
	number, err := s.allocateNumber(repo)
	if err != nil {
		return Issue{}, err
	}
	dir := s.eventsDir(repo, number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Issue{}, err
	}
	ev := Event{Type: EventCreated, User: user, Title: title, Body: body}
	if err := s.writeEvent(dir, ev); err != nil {
		return Issue{}, err
	}
	if len(labels) > 0 {
		if err := s.writeEvent(dir, Event{Type: EventLabels, User: user, Labels: labels}); err != nil {
			return Issue{}, err
		}
	}
	if len(assignees) > 0 {
		if err := s.writeEvent(dir, Event{Type: EventAssigned, User: user, Assignees: assignees}); err != nil {
			return Issue{}, err
		}
	}
	return s.GetIssue(repo, number)
}

// AppendEvent は既存のIssueにイベントを追記する。
func (s *Store) AppendEvent(repo string, number int, ev Event) error {
	dir := s.eventsDir(repo, number)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ErrIssueNotFound
	}
	return s.writeEvent(dir, ev)
}

// ReadEvents はIssueのイベントをファイル名順（時刻順）に読む。
// 拡張子が .json のファイルだけを対象とし、書きかけの一時ファイルは拾わない。
// 読めないファイルがあれば、そのファイルだけ飛ばす。
func (s *Store) ReadEvents(repo string, number int) ([]Event, error) {
	dir := s.eventsDir(repo, number)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var ev Event
		if err := readJSON(filepath.Join(dir, e.Name()), &ev); err != nil {
			continue
		}
		ev.File = e.Name()
		events = append(events, ev)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].File < events[j].File })
	return events, nil
}

// BuildIssue はイベントを時系列に適用してIssueを組み立てる。
// created イベントが無ければ false を返す（採番だけ済んで書き込みに失敗した残骸）。
func BuildIssue(number int, events []Event) (Issue, bool) {
	is := Issue{Number: number, State: "open", Events: events}
	created := false
	reviews := map[string]Review{}
	for _, ev := range events {
		switch ev.Type {
		case EventCreated:
			if created {
				continue
			}
			created = true
			is.Title = ev.Title
			is.Body = ev.Body
			is.User = ev.User
			is.CreatedAt = ev.Time
			is.IsPull = ev.IsPull
			is.Head = ev.Head
			is.Base = ev.Base
			is.HeadSHA = ev.HeadSHA
		case EventComment:
			is.Comments++
		case EventClosed:
			is.State = "closed"
			is.ClosedAt = ev.Time
			if ev.Body != "" {
				is.Comments++
			}
		case EventReopened:
			is.State = "open"
			is.ClosedAt = time.Time{}
			if ev.Body != "" {
				is.Comments++
			}
		case EventTitle:
			is.Title = ev.Title
		case EventLabels:
			is.Labels = append([]int(nil), ev.Labels...)
		case EventAssigned:
			is.Assignees = append([]string(nil), ev.Assignees...)
		case EventReview:
			if ev.Body != "" {
				is.Comments++
			}
			if ev.State == ReviewApproved || ev.State == ReviewChangesRequested {
				reviews[ev.User] = Review{User: ev.User, State: ev.State, Time: ev.Time, CommitSHA: ev.CommitSHA}
			}
		case EventReviewComment:
			is.Comments++
		case EventMerged:
			is.Merged = true
			is.MergeSHA = ev.MergeSHA
			is.MergedHeadSHA = ev.HeadSHA
			is.MergedBy = ev.User
			is.MergedAt = ev.Time
			is.State = "closed"
			is.ClosedAt = ev.Time
		case EventReverted:
			is.RevertSHA = ev.RevertSHA
			is.RevertedBy = ev.User
			is.RevertedAt = ev.Time
		}
		if ev.Time.After(is.UpdatedAt) {
			is.UpdatedAt = ev.Time
		}
	}
	for _, r := range reviews {
		is.Reviews = append(is.Reviews, r)
	}
	sort.Slice(is.Reviews, func(i, j int) bool { return is.Reviews[i].User < is.Reviews[j].User })
	return is, created
}

// CreatePull は番号を採番し、Pull Request の created イベントを書いて、組み立てた結果を返す。
// 番号は Issue と共通の連番。
func (s *Store) CreatePull(repo, user, title, body, head, base, headSHA string, labels []int, assignees []string) (Issue, error) {
	if _, err := s.GetRepo(repo); err != nil {
		return Issue{}, err
	}
	number, err := s.allocateNumber(repo)
	if err != nil {
		return Issue{}, err
	}
	dir := s.eventsDir(repo, number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Issue{}, err
	}
	ev := Event{Type: EventCreated, User: user, Title: title, Body: body, IsPull: true, Head: head, Base: base, HeadSHA: headSHA}
	if err := s.writeEvent(dir, ev); err != nil {
		return Issue{}, err
	}
	if len(labels) > 0 {
		if err := s.writeEvent(dir, Event{Type: EventLabels, User: user, Labels: labels}); err != nil {
			return Issue{}, err
		}
	}
	if len(assignees) > 0 {
		if err := s.writeEvent(dir, Event{Type: EventAssigned, User: user, Assignees: assignees}); err != nil {
			return Issue{}, err
		}
	}
	return s.GetIssue(repo, number)
}

// GetIssue は1件のIssueを読んで組み立てる。
func (s *Store) GetIssue(repo string, number int) (Issue, error) {
	if !isSafeName(repo) || number <= 0 {
		return Issue{}, ErrIssueNotFound
	}
	events, err := s.ReadEvents(repo, number)
	if err != nil {
		return Issue{}, err
	}
	is, ok := BuildIssue(number, events)
	if !ok {
		return Issue{}, ErrIssueNotFound
	}
	return is, nil
}

// ListIssues はリポジトリの全Issueを番号の降順で返す。
func (s *Store) ListIssues(repo string) ([]Issue, error) {
	if !isSafeName(repo) {
		return nil, ErrRepoNotFound
	}
	numbers, err := s.listIssueNumbers(repo)
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, 0, len(numbers))
	for i := len(numbers) - 1; i >= 0; i-- {
		is, err := s.GetIssue(repo, numbers[i])
		if err != nil {
			continue
		}
		issues = append(issues, is)
	}
	return issues, nil
}
