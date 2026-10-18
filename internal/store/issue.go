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

	// 編集と削除。元のイベントは消さず、対象のファイル名を target に持つイベントを追記する
	EventEdited  = "edited"  // 本文またはコメントの編集。body に新しい本文
	EventDeleted = "deleted" // コメントの削除。表示しなくなるだけで、ファイルは残る
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

	// 編集と削除用
	Target string `json:"target,omitempty"` // edited / deleted: 対象のイベントのファイル名

	// File は読み込み元のファイル名。書き込み時は空でよい。
	File string `json:"-"`

	// 以下は読み込み時に edited / deleted を適用した結果。ファイルには書かない
	History  []Revision `json:"-"` // 編集前の本文（古い順）
	EditedBy string     `json:"-"` // 最後に編集した人。未編集なら空
	EditedAt time.Time  `json:"-"`
	Deleted  bool       `json:"-"` // 削除済み。表示しない
}

// Revision は編集前の本文1つ。User と Time はその本文を書いた人と時刻。
type Revision struct {
	User string
	Time time.Time
	Body string
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
	Events    []Event   // edited / deleted を適用した後のイベント

	// 本文の編集
	BodyFile     string     // created イベントのファイル名。本文の編集の target
	BodyHistory  []Revision // 編集前の本文（古い順）
	BodyEditedBy string     // 最後に本文を編集した人。未編集なら空
	BodyEditedAt time.Time

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
		if strings.EqualFold(x, login) {
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

// IssuesDir は issues フォルダのパスを返す（検証プログラムが直接走査するために公開する）。
func (s *Store) IssuesDir(repo string) string { return s.issuesDir(repo) }

// EventsDir は Issue の events フォルダのパスを返す（検証プログラム用）。
func (s *Store) EventsDir(repo string, number int) string { return s.eventsDir(repo, number) }

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
// 「UTC時刻（ミリ秒まで）_端末名_プロセスID_プロセス内連番.json」で、他の端末とも、
// 同じ端末の別のプロセスとも重複しないようにする。
func (s *Store) eventFileName(t time.Time) string {
	t = t.UTC()
	seq := s.seq.Add(1)
	return fmt.Sprintf("%s%03dZ_%s_%d_%03d.json", t.Format("20060102T150405"), t.Nanosecond()/1e6, s.host, s.pid, seq)
}

// writeEvent はイベントを1ファイルとして書く。Time が空なら現在時刻を入れる。
func (s *Store) writeEvent(repo string, number int, ev Event) error {
	if err := s.writable(); err != nil {
		return err
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	// ファイル名と同じミリ秒までに揃える
	ev.Time = ev.Time.UTC().Truncate(time.Millisecond)
	name := s.eventFileName(ev.Time)
	if err := writeJSONNew(filepath.Join(s.eventsDir(repo, number), name), ev); err != nil {
		return err
	}
	s.rememberEvent(repo, number, name, ev)
	return nil
}

// AllocateIssue は番号を採番してフォルダを作り、番号を返す。created イベントはまだ書かない。
// 添付ファイルを先に保存して本文に参照を入れたいときに使う。続けて WriteCreated を呼ぶ。
func (s *Store) AllocateIssue(repo string) (int, error) {
	if err := s.writable(); err != nil {
		return 0, err
	}
	if _, err := s.GetRepo(repo); err != nil {
		return 0, err
	}
	number, err := s.allocateNumber(repo)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(s.eventsDir(repo, number), 0o755); err != nil {
		return 0, err
	}
	return number, nil
}

// DiscardIssue は AllocateIssue で取ったがまだイベントを書いていない番号のフォルダを消す。
// 添付の保存に失敗したときの後始末で、イベントが1つでもあれば何もしない。
func (s *Store) DiscardIssue(repo string, number int) error {
	entries, err := os.ReadDir(s.eventsDir(repo, number))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			return nil
		}
	}
	return os.RemoveAll(s.issueDir(repo, number))
}

// WriteCreated は AllocateIssue で取った番号に created イベントを書き、組み立てた結果を返す。
// labels と assignees が空でなければ、続けてそれぞれのイベントも書く。
func (s *Store) WriteCreated(repo string, number int, ev Event, labels []int, assignees []string) (Issue, error) {
	ev.Type = EventCreated
	if err := s.writeEvent(repo, number, ev); err != nil {
		return Issue{}, err
	}
	if len(labels) > 0 {
		if err := s.writeEvent(repo, number, Event{Type: EventLabels, User: ev.User, Labels: labels}); err != nil {
			return Issue{}, err
		}
	}
	if len(assignees) > 0 {
		if err := s.writeEvent(repo, number, Event{Type: EventAssigned, User: ev.User, Assignees: assignees}); err != nil {
			return Issue{}, err
		}
	}
	return s.GetIssue(repo, number)
}

// CreateIssue は番号を採番し、created イベントを書いて、組み立てたIssueを返す。
// labels と assignees が空でなければ、続けてそれぞれのイベントも書く。
func (s *Store) CreateIssue(repo, user, title, body string, labels []int, assignees []string) (Issue, error) {
	number, err := s.AllocateIssue(repo)
	if err != nil {
		return Issue{}, err
	}
	return s.WriteCreated(repo, number, Event{User: user, Title: title, Body: body}, labels, assignees)
}

// AppendEvent は既存のIssueにイベントを追記する。
func (s *Store) AppendEvent(repo string, number int, ev Event) error {
	info, err := os.Stat(s.eventsDir(repo, number))
	if err != nil || !info.IsDir() {
		return ErrIssueNotFound
	}
	return s.writeEvent(repo, number, ev)
}

// ReadEvents はIssueのイベントをファイル名順（時刻順）に読む。
// events フォルダの一覧を見て、キャッシュに無いファイルだけを読み足す（cache.go）。
// 拡張子が .json のファイルだけを対象とし、書きかけの一時ファイルは拾わない。
// 読めないファイルがあれば、そのファイルだけ飛ばす。
func (s *Store) ReadEvents(repo string, number int) ([]Event, error) {
	return s.cachedEvents(repo, number)
}

// BuildIssue はイベントを時系列に適用してIssueを組み立てる。
// created イベントが無ければ false を返す（採番だけ済んで書き込みに失敗した残骸）。
func BuildIssue(number int, events []Event) (Issue, bool) {
	events = applyEdits(events)
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
			is.BodyFile = ev.File
			is.BodyHistory = ev.History
			is.BodyEditedBy = ev.EditedBy
			is.BodyEditedAt = ev.EditedAt
		case EventComment:
			if !ev.Deleted {
				is.Comments++
			}
		case EventClosed:
			is.State = "closed"
			is.ClosedAt = ev.Time
			if ev.Body != "" && !ev.Deleted {
				is.Comments++
			}
		case EventReopened:
			is.State = "open"
			is.ClosedAt = time.Time{}
			if ev.Body != "" && !ev.Deleted {
				is.Comments++
			}
		case EventTitle:
			is.Title = ev.Title
		case EventLabels:
			is.Labels = append([]int(nil), ev.Labels...)
		case EventAssigned:
			is.Assignees = append([]string(nil), ev.Assignees...)
		case EventReview:
			if ev.Body != "" && !ev.Deleted {
				is.Comments++
			}
			if ev.State == ReviewApproved || ev.State == ReviewChangesRequested {
				reviews[ev.User] = Review{User: ev.User, State: ev.State, Time: ev.Time, CommitSHA: ev.CommitSHA}
			}
		case EventReviewComment:
			if !ev.Deleted {
				is.Comments++
			}
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

// applyEdits は edited と deleted を対象のイベントに反映した写しを返す。
// 対象が無い、または編集できない種類のイベントを指す edited / deleted は無視する。
func applyEdits(events []Event) []Event {
	out := make([]Event, len(events))
	copy(out, events)
	index := map[string]int{}
	for i, ev := range out {
		if ev.File != "" {
			index[ev.File] = i
		}
	}
	for _, ev := range out {
		if ev.Type != EventEdited && ev.Type != EventDeleted {
			continue
		}
		i, ok := index[ev.Target]
		if !ok || !editableEvent(out[i].Type) {
			continue
		}
		t := &out[i]
		if ev.Type == EventDeleted {
			if deletableEvent(t.Type) {
				t.Deleted = true
			}
			continue
		}
		writer, at := t.User, t.Time
		if t.EditedBy != "" {
			writer, at = t.EditedBy, t.EditedAt
		}
		t.History = append(t.History, Revision{User: writer, Time: at, Body: t.Body})
		t.Body = ev.Body
		t.EditedBy, t.EditedAt = ev.User, ev.Time
	}
	return out
}

// editableEvent は本文を編集できる種類のイベントかを返す。
func editableEvent(typ string) bool {
	switch typ {
	case EventCreated, EventComment, EventClosed, EventReopened, EventReview, EventReviewComment:
		return true
	}
	return false
}

// deletableEvent は削除できる種類（コメントと行コメント）かを返す。本文や状態の変更は消せない。
func deletableEvent(typ string) bool {
	return typ == EventComment || typ == EventReviewComment
}

// ErrEventNotFound は編集や削除の対象のイベントが無い（または対象にできない）ときのエラー。
var ErrEventNotFound = errors.New("対象のコメントが無い")

// EditBody は本文またはコメントを書き換える。target は対象のイベントのファイル名。
// 元のイベントは消さず、edited イベントを追記する。
func (s *Store) EditBody(repo string, number int, user, target, body string) error {
	if err := s.checkEditTarget(repo, number, target, false); err != nil {
		return err
	}
	return s.AppendEvent(repo, number, Event{Type: EventEdited, User: user, Target: target, Body: body})
}

// DeleteComment はコメントまたは行コメントを削除する。表示しなくなるだけで、ファイルは残る。
func (s *Store) DeleteComment(repo string, number int, user, target string) error {
	if err := s.checkEditTarget(repo, number, target, true); err != nil {
		return err
	}
	return s.AppendEvent(repo, number, Event{Type: EventDeleted, User: user, Target: target})
}

func (s *Store) checkEditTarget(repo string, number int, target string, del bool) error {
	events, err := s.ReadEvents(repo, number)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if ev.File != target {
			continue
		}
		if del {
			if deletableEvent(ev.Type) {
				return nil
			}
		} else if editableEvent(ev.Type) {
			return nil
		}
		return ErrEventNotFound
	}
	return ErrEventNotFound
}

// CreatePull は番号を採番し、Pull Request の created イベントを書いて、組み立てた結果を返す。
// 番号は Issue と共通の連番。
func (s *Store) CreatePull(repo, user, title, body, head, base, headSHA string, labels []int, assignees []string) (Issue, error) {
	number, err := s.AllocateIssue(repo)
	if err != nil {
		return Issue{}, err
	}
	ev := Event{User: user, Title: title, Body: body, IsPull: true, Head: head, Base: base, HeadSHA: headSHA}
	return s.WriteCreated(repo, number, ev, labels, assignees)
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
