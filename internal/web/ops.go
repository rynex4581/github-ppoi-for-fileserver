package web

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// このファイルの関数は、画面と Gitea 互換 API の両方から呼ぶ。
// 入力の確認と保存の手順をここに集め、画面と API で動きが食い違わないようにする。

// opError は利用者の入力に問題があるときのエラー。画面では 422、API では 422 か 404 で返す。
type opError struct {
	Status int    // HTTP の状態コード
	Key    string // 文言のキー
	Args   []any
}

func (e *opError) Error() string { return e.Key }

func opErr(status int, key string, args ...any) error {
	return &opError{Status: status, Key: key, Args: args}
}

// opMessage はエラーを画面向けの文言にする。opError 以外はそのまま。
func (s *Server) opMessage(err error) string {
	var oe *opError
	if errors.As(err, &oe) {
		return s.tr(oe.Key, oe.Args...)
	}
	return err.Error()
}

func opStatus(err error) int {
	var oe *opError
	if errors.As(err, &oe) {
		return oe.Status
	}
	return 500
}

// ---- Issue ----

// createIssue は Issue を作る。添付の置き換えは呼ぶ側で済ませておく。
func (s *Server) createIssue(st *store.Store, repo store.Repo, title, body string, labels []int, assignees []string) (store.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return store.Issue{}, opErr(422, "ppoi.error.title_required")
	}
	return st.CreateIssue(repo.Name, s.login, title, normalizeNewlines(body), labels, assignees)
}

// addComment はコメントを付ける。空は拒む。
func (s *Server) addComment(st *store.Store, repo store.Repo, is store.Issue, body string) error {
	body = normalizeNewlines(body)
	if strings.TrimSpace(body) == "" {
		return opErr(422, "ppoi.error.comment_required")
	}
	return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventComment, User: s.login, Body: body})
}

// setState はクローズまたは再オープンする。body があればコメントも兼ねる。
// 既にその状態なら何もしない。マージ済みの PR は再オープンできない。
func (s *Server) setState(st *store.Store, repo store.Repo, is store.Issue, state, body string) error {
	body = normalizeNewlines(body)
	switch state {
	case "closed":
		if is.State == "closed" {
			return nil
		}
		return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventClosed, User: s.login, Body: body})
	case "open":
		if is.State == "open" {
			return nil
		}
		if is.Merged {
			return opErr(422, "repo.pulls.is_closed")
		}
		return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventReopened, User: s.login, Body: body})
	}
	return opErr(422, "ppoi.api.bad_state")
}

// setTitle はタイトルを変える。同じなら何もしない。
func (s *Server) setTitle(st *store.Store, repo store.Repo, is store.Issue, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return opErr(422, "ppoi.error.title_required")
	}
	if title == is.Title {
		return nil
	}
	return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventTitle, User: s.login, Title: title})
}

// setLabels はラベルを置き換える。無い ID は落とす。変化が無ければ何もしない。
func (s *Server) setLabels(st *store.Store, repo store.Repo, is store.Issue, ids []int) error {
	labels, err := st.ListLabels(repo.Name)
	if err != nil {
		return err
	}
	byID := labelsByID(labels)
	var next []int
	for _, id := range ids {
		if _, ok := byID[id]; ok && !containsInt(next, id) {
			next = append(next, id)
		}
	}
	sort.Ints(next)
	cur := append([]int(nil), is.Labels...)
	sort.Ints(cur)
	if equalInts(cur, next) {
		return nil
	}
	return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventLabels, User: s.login, Labels: next})
}

// setAssignees は担当者を置き換える。登録の無いログオン名は落とす。変化が無ければ何もしない。
func (s *Server) setAssignees(st *store.Store, repo store.Repo, is store.Issue, logins []string) error {
	users, err := st.ListUsers()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, u := range users {
		known[u.Login] = true
	}
	var next []string
	for _, l := range logins {
		if known[l] && !containsString(next, l) {
			next = append(next, l)
		}
	}
	sort.Strings(next)
	cur := append([]string(nil), is.Assignees...)
	sort.Strings(cur)
	if strings.Join(cur, "\n") == strings.Join(next, "\n") {
		return nil
	}
	return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventAssigned, User: s.login, Assignees: next})
}

// ---- Pull Request ----

// createPull は Pull Request を作る。base と head の確認と、差分の有無の確認を含む。
func (s *Server) createPull(st *store.Store, repo store.Repo, title, body, head, base string, labels []int, assignees []string) (store.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return store.Issue{}, opErr(422, "ppoi.error.title_required")
	}
	base, headSHA, err := s.validatePull(st, repo, head, base)
	if err != nil {
		return store.Issue{}, err
	}
	return st.CreatePull(repo.Name, s.login, title, normalizeNewlines(body), head, base, headSHA, labels, assignees)
}

// validatePull は PR の head と base を確かめる。base が空なら既定のブランチにする。
// 両方が存在し、同じでなく、取り込むコミットがあることを見て、head の先頭のコミットを返す。
// 画面と API の両方がこれを通る（存在しないブランチを黙って差し替えない）。
func (s *Server) validatePull(st *store.Store, repo store.Repo, head, base string) (string, string, error) {
	g := s.gitRepo(st, repo)
	branches, err := g.Branches()
	if err != nil {
		return "", "", err
	}
	if base == "" {
		base = s.defaultBranch(g, repo)
	}
	if !branchExists(branches, base) || !branchExists(branches, head) {
		return "", "", opErr(422, "ppoi.code.ref_not_found")
	}
	if base == head {
		return "", "", opErr(422, "ppoi.pulls.same_branch")
	}
	commits, err := g.RangeLog(base, head, 1)
	if err != nil {
		return "", "", err
	}
	if len(commits) == 0 {
		return "", "", opErr(422, "repo.pulls.nothing_to_compare")
	}
	headSHA, err := g.ResolveCommit("refs/heads/" + head)
	if err != nil {
		return "", "", opErr(422, "ppoi.code.ref_not_found")
	}
	return base, headSHA, nil
}

// validateReview はレビューの入力を確かめる。書き込みの前に通す（API は行コメントを先に書くので、
// ここで弾けば行コメントだけ残ることが無い）。
// 自分の PR は承認も変更要請もできない。承認と変更要請は open の PR にしか付けられない
// （クローズ中に付けた承認が再オープン後に効いてしまわないようにする）。コメントは open 以外でも付けられる。
func (s *Server) validateReview(is store.Issue, state, body string) error {
	if !is.IsPull {
		return opErr(404, "ppoi.error.issue_not_found")
	}
	switch state {
	case store.ReviewApproved:
		if is.State != "open" {
			return opErr(405, "ppoi.pulls.not_open")
		}
		if strings.EqualFold(is.User, s.login) {
			return opErr(422, "repo.diff.review.self_approve")
		}
	case store.ReviewChangesRequested:
		if is.State != "open" {
			return opErr(405, "ppoi.pulls.not_open")
		}
		if strings.EqualFold(is.User, s.login) {
			return opErr(422, "repo.diff.review.self_reject")
		}
		if strings.TrimSpace(body) == "" {
			return opErr(422, "repo.issues.review.content.empty")
		}
	case store.ReviewComment:
		if strings.TrimSpace(body) == "" {
			return opErr(422, "repo.issues.review.content.empty")
		}
	default:
		return opErr(422, "ppoi.api.bad_state")
	}
	return nil
}

// addReview はレビュー（承認／変更要請／コメント）を付ける。
func (s *Server) addReview(st *store.Store, repo store.Repo, is store.Issue, state, body string) error {
	body = normalizeNewlines(body)
	if err := s.validateReview(is, state, body); err != nil {
		return err
	}
	sha, _ := s.gitRepo(st, repo).ResolveCommit("refs/heads/" + is.Head)
	return st.AppendEvent(repo.Name, is.Number, store.Event{Type: store.EventReview, User: s.login, State: state, Body: body, CommitSHA: sha})
}

// addLineComment は差分の行にコメントを付ける。side は new か old、line は行番号。
func (s *Server) addLineComment(st *store.Store, repo store.Repo, is store.Issue, path, side string, line int, body string) error {
	body = normalizeNewlines(body)
	if !is.IsPull {
		return opErr(404, "ppoi.error.issue_not_found")
	}
	if side != "old" {
		side = "new"
	}
	if strings.TrimSpace(body) == "" || strings.TrimSpace(path) == "" || line <= 0 {
		return opErr(422, "ppoi.error.comment_required")
	}
	v, err := s.buildPullView(st, repo, is, "files")
	if err != nil {
		return err
	}
	ev := store.Event{
		Type: store.EventReviewComment, User: s.login, Body: body, CommitSHA: v.HeadSHA,
		Path: path, Line: line, Side: side, DiffHunk: extractHunk(v.Diff.Files, path, side, line),
	}
	return st.AppendEvent(repo.Name, is.Number, ev)
}

// mergePull は PR をマージする。マージできる状態かの判定、専用 clone でのマージと push、
// merged イベントの記録、head ブランチの削除まで行う。
// head ブランチの削除に失敗したときは、マージは済んでいるので nil と一緒に警告の文言を返す。
func (s *Server) mergePull(st *store.Store, repo store.Repo, is store.Issue, message string) (warn string, err error) {
	unlock, err := s.locks.lock(st, repo)
	if err != nil {
		return "", err
	}
	defer unlock()
	v, err := s.buildPullView(st, repo, is, "conversation")
	if err != nil {
		return "", err
	}
	if !v.CanMerge {
		switch {
		case is.State != "open":
			return "", opErr(405, "repo.pulls.is_closed")
		case !v.HeadExists:
			return "", opErr(405, "ppoi.pulls.head_deleted", is.Head)
		case v.NothingToMerge:
			// 前回のマージで push は通ったが記録に失敗した可能性がある。base 上に head を親に持つ
			// マージコミットがあれば、それを記録して済ませる
			if sha, ferr := s.gitRepo(st, repo).MergeCommitOf(is.Base, v.HeadSHA); ferr == nil {
				return s.recordMerge(st, repo, is, sha, v.HeadSHA)
			}
			return "", opErr(405, "repo.pulls.is_ancestor")
		case v.Conflict:
			return "", opErr(409, "repo.pulls.cannot_auto_merge_desc")
		case v.NeedsApproval:
			return "", opErr(405, "ppoi.pulls.approval_required")
		}
		return "", opErr(405, "repo.pulls.cannot_auto_merge_desc")
	}
	message = normalizeNewlines(message)
	if strings.TrimSpace(message) == "" {
		message = v.MergeMessage
	}
	c, err := s.cloneFor(st, repo)
	if err != nil {
		return "", err
	}
	mergeSHA, err := c.Merge(is.Base, is.Head, message)
	if err != nil {
		switch {
		case errors.Is(err, gitcmd.ErrConflict):
			return "", opErr(409, "repo.pulls.cannot_auto_merge_desc")
		case errors.Is(err, gitcmd.ErrPushRejected):
			return "", opErr(409, "ppoi.pulls.push_rejected")
		}
		return "", opErr(500, "ppoi.pulls.merge_failed", err.Error())
	}
	return s.recordMerge(st, repo, is, mergeSHA, v.HeadSHA)
}

// recordMerge はマージ済みのイベントを書き、head ブランチを消す。
// push は済んでいるので、記録に失敗したときはその旨を伝える（次にマージを押せば記録だけやり直す）。
func (s *Server) recordMerge(st *store.Store, repo store.Repo, is store.Issue, mergeSHA, headSHA string) (string, error) {
	ev := store.Event{Type: store.EventMerged, User: s.login, MergeSHA: mergeSHA, HeadSHA: headSHA}
	if err := st.AppendEvent(repo.Name, is.Number, ev); err != nil {
		return "", opErr(500, "ppoi.pulls.merged_but_not_recorded", err.Error())
	}
	if s.headUsedByOtherPull(st, repo, is) {
		return s.tr("ppoi.pulls.head_kept", is.Head), nil
	}
	c, err := s.cloneFor(st, repo)
	if err != nil {
		return s.tr("ppoi.pulls.head_delete_failed", is.Head, err.Error()), nil
	}
	if err := c.DeleteRemoteBranch(is.Head); err != nil {
		return s.tr("ppoi.pulls.head_delete_failed", is.Head, err.Error()), nil
	}
	return "", nil
}

// headUsedByOtherPull は、この PR の head ブランチが他の open な PR の base になっているかを返す。
// なっていればマージ後も消さない。
func (s *Server) headUsedByOtherPull(st *store.Store, repo store.Repo, is store.Issue) bool {
	all, err := st.ListIssues(repo.Name)
	if err != nil {
		return false
	}
	for _, other := range all {
		if other.IsPull && other.Number != is.Number && other.State == "open" && other.Base == is.Head {
			return true
		}
	}
	return false
}

// revertPull はマージ済みの PR のマージコミットを打ち消す。1回だけ。
func (s *Server) revertPull(st *store.Store, repo store.Repo, is store.Issue) error {
	if !is.Merged || is.RevertSHA != "" {
		return opErr(405, "repo.pulls.is_closed")
	}
	unlock, err := s.locks.lock(st, repo)
	if err != nil {
		return err
	}
	defer unlock()
	// 前回の Revert で push は通ったが記録に失敗した可能性がある。base 上に打ち消しのコミットが
	// あれば、それを記録して済ませる（2回目の revert を作らない）
	if sha, ferr := s.gitRepo(st, repo).RevertCommitOf(is.Base, is.MergeSHA); ferr == nil {
		return s.recordRevert(st, repo, is, sha)
	}
	c, err := s.cloneFor(st, repo)
	if err != nil {
		return err
	}
	message := fmt.Sprintf("Revert \"Merge pull request #%d from %s\"\n\nThis reverts commit %s.", is.Number, is.Head, is.MergeSHA)
	revertSHA, err := c.Revert(is.Base, is.MergeSHA, message)
	if err != nil {
		switch {
		case errors.Is(err, gitcmd.ErrConflict):
			return opErr(409, "ppoi.pulls.revert_failed", s.tr("repo.pulls.cannot_auto_merge_helper"))
		case errors.Is(err, gitcmd.ErrPushRejected):
			return opErr(409, "ppoi.pulls.push_rejected")
		}
		return opErr(500, "ppoi.pulls.revert_failed", err.Error())
	}
	return s.recordRevert(st, repo, is, revertSHA)
}

// recordRevert は取り消しのイベントを書く。push は済んでいるので、失敗したときはその旨を伝える。
func (s *Server) recordRevert(st *store.Store, repo store.Repo, is store.Issue, revertSHA string) error {
	ev := store.Event{Type: store.EventReverted, User: s.login, MergeSHA: is.MergeSHA, RevertSHA: revertSHA}
	if err := st.AppendEvent(repo.Name, is.Number, ev); err != nil {
		return opErr(500, "ppoi.pulls.reverted_but_not_recorded", err.Error())
	}
	return nil
}
