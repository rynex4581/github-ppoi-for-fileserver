package web

import (
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// 行コメントに保存する差分の断片の、前後の行数。
const hunkContext = 3

// cloneFor はツール専用 clone を返す。置き場所は %LOCALAPPDATA%\github-ppoi\repos\<owner>\<リポジトリ名>。
func (s *Server) cloneFor(st *store.Store, repo store.Repo) (gitcmd.Clone, error) {
	dir, err := localconfig.Dir()
	if err != nil {
		return gitcmd.Clone{}, err
	}
	return gitcmd.Clone{
		Dir:       filepath.Join(dir, "repos", st.Config().Owner, repo.Name),
		Remote:    s.gitRepo(st, repo).Dir,
		UserName:  st.DisplayName(s.login),
		UserEmail: s.login + "@github-ppoi.invalid",
	}, nil
}

func (s *Server) pullURL(st *store.Store, repo store.Repo, number int) string {
	return fmt.Sprintf("%s/pulls/%d", s.repoURL(st, repo), number)
}

// ---- 一覧 ----

func (s *Server) handlePulls(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	s.listIssues(w, r, st, repo, true)
}

// ---- 新規（比較） ----

type pullNewForm struct {
	Base      string
	Head      string
	Branches  []gitcmd.Branch
	Title     string
	Body      string
	Labels    []int
	Assignees []string
	AllLabels []store.Label
	AllUsers  []store.User
	Commits   []commitRow
	Files     []gitcmd.DiffFile
	Stat      gitcmd.DiffStat
	StatsHTML template.HTML
	Same      bool // base と head が同じ
	Nothing   bool // 差分が無い
	TooFew    bool // ブランチが2つ未満
	Existing  *store.Issue
}

func (f pullNewForm) HasLabel(id int) bool {
	for _, x := range f.Labels {
		if x == id {
			return true
		}
	}
	return false
}

func (f pullNewForm) HasAssignee(login string) bool {
	for _, x := range f.Assignees {
		if strings.EqualFold(x, login) {
			return true
		}
	}
	return false
}

func branchExists(branches []gitcmd.Branch, name string) bool {
	for _, b := range branches {
		if b.Name == name {
			return true
		}
	}
	return false
}

// preparePullNew は base と head を決め、比較結果を揃える。
func (s *Server) preparePullNew(st *store.Store, repo store.Repo, f *pullNewForm) error {
	g := s.gitRepo(st, repo)
	branches, err := g.Branches()
	if err != nil {
		return err
	}
	f.Branches = branches
	if len(branches) < 2 {
		f.TooFew = true
	}
	def := s.defaultBranch(g, repo)
	if !branchExists(branches, f.Base) {
		f.Base = def
	}
	if !branchExists(branches, f.Head) {
		f.Head = ""
		for _, b := range branches {
			if b.Name != f.Base {
				f.Head = b.Name
				break
			}
		}
	}
	if f.AllLabels, err = st.ListLabels(repo.Name); err != nil {
		return err
	}
	if f.AllUsers, err = st.ListUsers(); err != nil {
		return err
	}
	if f.Head == "" || f.Base == f.Head {
		f.Same = f.Head != ""
		return nil
	}
	commits, err := g.RangeLog(f.Base, f.Head, 250)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		f.Nothing = true
		return nil
	}
	base := s.repoURL(st, repo)
	for _, c := range commits {
		f.Commits = append(f.Commits, commitRow{Commit: c, URL: base + "/commit/" + c.SHA})
	}
	raw, err := g.RangeDiff(f.Base, f.Head)
	if err != nil {
		return err
	}
	f.Files = gitcmd.ParseDiff(raw)
	f.Stat = gitcmd.Stat(f.Files)
	f.StatsHTML = s.trh("repo.diff.stats_desc", f.Stat.Files, f.Stat.Additions, f.Stat.Deletions)

	// 同じ組み合わせのオープン中の PR があれば知らせる
	all, err := st.ListIssues(repo.Name)
	if err != nil {
		return err
	}
	for i := range all {
		if all[i].IsPull && all[i].State == "open" && all[i].Base == f.Base && all[i].Head == f.Head {
			f.Existing = &all[i]
			break
		}
	}
	return nil
}

func (s *Server) handlePullNew(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	f := pullNewForm{Base: r.URL.Query().Get("base"), Head: r.URL.Query().Get("head")}
	if err := s.preparePullNew(st, repo, &f); err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := s.repoPage(s.tr("repo.pulls.new"), repo, "pulls")
	p.Data = f
	s.render(w, http.StatusOK, "pull_new", p)
}

func (s *Server) handlePullCreate(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	f := pullNewForm{
		Base:  r.FormValue("base"),
		Head:  r.FormValue("head"),
		Title: strings.TrimSpace(r.FormValue("title")),
		Body:  normalizeNewlines(r.FormValue("body")),
	}
	// 送られてきたブランチ名はそのまま検証に回す。preparePullNew は表示用に存在するブランチへ
	// 差し替えるので、その前に控えておく
	head, base := f.Head, f.Base
	if err := s.preparePullNew(st, repo, &f); err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f.Labels = parseLabelIDs(r.Form["labels"], f.AllLabels)
	f.Assignees = parseAssignees(r.Form["assignees"], f.AllUsers)

	fail := func(msg string) {
		p := s.repoPage(s.tr("repo.pulls.new"), repo, "pulls")
		p.Error = msg
		p.Data = f
		s.render(w, http.StatusUnprocessableEntity, "pull_new", p)
	}
	if f.TooFew {
		fail(s.tr("ppoi.pulls.needs_branches"))
		return
	}
	if f.Title == "" {
		fail(s.tr("ppoi.error.title_required"))
		return
	}
	base, headSHA, err := s.validatePull(st, repo, head, base)
	if err != nil {
		fail(s.opMessage(err))
		return
	}
	f.Head, f.Base = head, base
	number, err := st.AllocateIssue(repo.Name)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := s.saveAttachments(st, repo, number, r, f.Body)
	if err != nil {
		st.DiscardIssue(repo.Name, number)
		s.renderError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ev := store.Event{User: s.login, Title: f.Title, Body: body, IsPull: true, Head: f.Head, Base: f.Base, HeadSHA: headSHA}
	pr, err := st.WriteCreated(repo.Name, number, ev, f.Labels, f.Assignees)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, s.pullURL(st, repo, pr.Number), http.StatusSeeOther)
}

// ---- 詳細 ----

// lineCommentView は行コメントの表示用。
type lineCommentView struct {
	User      string
	Time      time.Time
	Body      string
	Path      string
	Line      int
	Side      string
	DiffHunk  string
	CommitSHA string
	Outdated  bool
}

// prDiffData は差分（行コメント付き）のテンプレートに渡す。
type prDiffData struct {
	Files      []gitcmd.DiffFile
	Comments   map[string][]lineCommentView
	CommitSHA  string
	CommentURL string
	CanComment bool
}

func lineKey(path, side string, line int) string {
	return path + "|" + side + "|" + strconv.Itoa(line)
}

type pullView struct {
	Issue     store.Issue
	Tab       string
	PullURL   string
	Labels    []store.Label
	Assignees []string
	Timeline  []timelineItem
	AllLabels []store.Label
	AllUsers  []store.User

	HeadExists  bool
	HeadSHA     string // 今の head の先頭（head が消えていればマージ時か作成時の先頭）
	Commits     []commitRow
	CommitCount int
	Diff        prDiffData
	Stat        gitcmd.DiffStat
	StatsHTML   template.HTML

	Conflict       bool
	NothingToMerge bool
	ForcePushed    bool
	NeedsApproval  bool // 承認必須の設定で、まだ承認が無い
	CanMerge       bool
	CanRevert      bool
	IsAuthor       bool
	MergeMessage   string
	Outdated       []lineCommentView
	BodyEdit       editInfo
	ApproveCount   int
	RejectCount    int
}

func (s *Server) buildPullView(st *store.Store, repo store.Repo, is store.Issue, tab string) (pullView, error) {
	g := s.gitRepo(st, repo)
	base := s.repoURL(st, repo)
	v := pullView{Issue: is, Tab: tab, PullURL: s.pullURL(st, repo, is.Number), Assignees: is.Assignees, IsAuthor: strings.EqualFold(is.User, s.login), BodyEdit: bodyEditInfo(is)}
	var err error
	if v.AllLabels, err = st.ListLabels(repo.Name); err != nil {
		return v, err
	}
	if v.AllUsers, err = st.ListUsers(); err != nil {
		return v, err
	}
	byID := labelsByID(v.AllLabels)
	v.Labels = resolveLabels(is.Labels, byID)

	// 比較の範囲を決める
	var from, to string
	if sha, err := g.ResolveCommit("refs/heads/" + is.Head); err == nil {
		v.HeadExists = true
		v.HeadSHA = sha
	}
	switch {
	case is.Merged && is.MergeSHA != "":
		from, to = is.MergeSHA+"^1", is.MergedHeadSHA
		if !v.HeadExists {
			v.HeadSHA = is.MergedHeadSHA
		}
	case v.HeadExists:
		from, to = "refs/heads/"+is.Base, v.HeadSHA
	default:
		v.HeadSHA = is.HeadSHA
		if _, err := g.ResolveCommit(is.HeadSHA); err == nil {
			from, to = "refs/heads/"+is.Base, is.HeadSHA
		}
	}
	if to != "" {
		if commits, err := g.RangeLog(from, to, 250); err == nil {
			v.CommitCount = len(commits)
			for _, c := range commits {
				v.Commits = append(v.Commits, commitRow{Commit: c, URL: base + "/commit/" + c.SHA})
			}
		}
		if raw, err := g.RangeDiff(from, to); err == nil {
			v.Diff.Files = gitcmd.ParseDiff(raw)
		}
	}
	v.Stat = gitcmd.Stat(v.Diff.Files)
	v.StatsHTML = s.trh("repo.diff.stats_desc", v.Stat.Files, v.Stat.Additions, v.Stat.Deletions)
	v.Diff.CommitSHA = v.HeadSHA
	v.Diff.CommentURL = v.PullURL + "/review_comments"
	v.Diff.CanComment = is.State == "open" && v.HeadExists
	v.Diff.Comments = map[string][]lineCommentView{}

	// マージできるかの判定
	if is.State == "open" && v.HeadExists {
		v.NothingToMerge = g.IsAncestor(v.HeadSHA, "refs/heads/"+is.Base)
		if !v.NothingToMerge {
			v.Conflict, _ = g.HasConflict("refs/heads/"+is.Base, v.HeadSHA)
		}
		if is.HeadSHA != "" && !g.IsAncestor(is.HeadSHA, v.HeadSHA) {
			v.ForcePushed = true
		}
	}
	approved := is.Approved()
	if repo.DismissStaleApproval && approved {
		// 承認した時点から PR の差分の中身が変わっていない承認だけを数える（Gitea と同じ）
		approved = false
		for _, rv := range is.Reviews {
			if rv.State == store.ReviewApproved && rv.CommitSHA != "" && v.HeadSHA != "" &&
				approvalStillValid(g, is.Base, rv.CommitSHA, v.HeadSHA) {
				approved = true
				break
			}
		}
	}
	v.NeedsApproval = repo.RequireApproval && !approved
	v.CanMerge = is.State == "open" && v.HeadExists && !v.Conflict && !v.NothingToMerge && !v.NeedsApproval
	v.CanRevert = is.Merged && is.RevertSHA == ""
	v.MergeMessage = fmt.Sprintf("Merge pull request #%d from %s\n\n%s", is.Number, is.Head, is.Title)
	for _, rv := range is.Reviews {
		if rv.State == store.ReviewApproved {
			v.ApproveCount++
		} else {
			v.RejectCount++
		}
	}

	// タイムラインと行コメント
	v.Timeline = buildTimeline(is, byID)
	for i := range v.Timeline {
		it := &v.Timeline[i]
		if it.Kind == "review_comment" {
			it.Outdated = it.CommitSHA != v.HeadSHA
			if v.ForcePushed && !g.IsAncestor(it.CommitSHA, v.HeadSHA) {
				it.Outdated = true
			}
			lc := lineCommentView{User: it.User, Time: it.Time, Body: it.Body, Path: it.Path, Line: it.Line, Side: it.Side, DiffHunk: it.DiffHunk, CommitSHA: it.CommitSHA, Outdated: it.Outdated}
			if it.Outdated {
				v.Outdated = append(v.Outdated, lc)
			} else {
				k := lineKey(lc.Path, lc.Side, lc.Line)
				v.Diff.Comments[k] = append(v.Diff.Comments[k], lc)
			}
		}
	}
	return v, nil
}

func (s *Server) loadPull(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) (store.Issue, bool) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return is, false
	}
	if !is.IsPull {
		http.Redirect(w, r, fmt.Sprintf("%s/issues/%d", s.repoURL(st, repo), is.Number), http.StatusSeeOther)
		return is, false
	}
	return is, true
}

func (s *Server) renderPull(w http.ResponseWriter, status int, st *store.Store, repo store.Repo, is store.Issue, tab, errMsg string) {
	v, err := s.buildPullView(st, repo, is, tab)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := s.repoPage(fmt.Sprintf("%s · #%d", is.Title, is.Number), repo, "pulls")
	p.Error = errMsg
	p.Data = v
	s.render(w, status, "pull", p)
}

func (s *Server) pullTab(tab string) repoHandler {
	return func(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
		is, ok := s.loadPull(w, r, st, repo)
		if !ok {
			return
		}
		s.renderPull(w, http.StatusOK, st, repo, is, tab, "")
	}
}

// ---- レビュー ----

func (s *Server) handlePullReview(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadPull(w, r, st, repo)
	if !ok {
		return
	}
	body := normalizeNewlines(r.FormValue("body"))
	state := store.ReviewComment
	switch r.FormValue("state") {
	case "approve":
		state = store.ReviewApproved
	case "reject":
		state = store.ReviewChangesRequested
	}
	body, err := s.saveAttachments(st, repo, is.Number, r, body)
	if err != nil {
		s.renderPull(w, http.StatusUnprocessableEntity, st, repo, is, "files", err.Error())
		return
	}
	if err := s.addReview(st, repo, is, state, body); err != nil {
		s.renderPull(w, opStatus(err), st, repo, is, "files", s.opMessage(err))
		return
	}
	http.Redirect(w, r, s.pullURL(st, repo, is.Number), http.StatusSeeOther)
}

// extractHunk は差分から、行コメントの位置の前後数行を文字列にして返す。
// approvalStillValid は、承認したコミット（reviewSHA）から今の head までで PR の差分の中身が
// 変わっていないかを返す。同じコミットなら真。base を取り込んだだけの push では差分は変わらないので
// 承認は残り、差分が変わる push や force push（承認したコミットが無くなる）では偽になる。
func approvalStillValid(g gitcmd.Repo, base, reviewSHA, headSHA string) bool {
	if reviewSHA == headSHA {
		return true
	}
	before, err := g.RangeDiff("refs/heads/"+base, reviewSHA)
	if err != nil {
		return false
	}
	after, err := g.RangeDiff("refs/heads/"+base, headSHA)
	if err != nil {
		return false
	}
	return stripIndexLines(before) == stripIndexLines(after)
}

// stripIndexLines は diff の index 行（blob の ID）を除く。中身が同じでも ID が違うことがあるため。
func stripIndexLines(diff string) string {
	lines := strings.Split(diff, "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(l, "index ") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func extractHunk(files []gitcmd.DiffFile, path, side string, line int) string {
	for _, f := range files {
		if f.NewPath != path && f.OldPath != path {
			continue
		}
		for _, h := range f.Hunks {
			for i, l := range h.Lines {
				hit := (side == "old" && l.Kind == "del" && l.OldNum == line) ||
					(side == "new" && l.Kind != "del" && l.NewNum == line)
				if !hit {
					continue
				}
				lo, hi := i-hunkContext, i+1
				if lo < 0 {
					lo = 0
				}
				if hi > len(h.Lines) {
					hi = len(h.Lines)
				}
				var b strings.Builder
				b.WriteString(h.Header + "\n")
				for _, x := range h.Lines[lo:hi] {
					switch x.Kind {
					case "add":
						b.WriteString("+")
					case "del":
						b.WriteString("-")
					default:
						b.WriteString(" ")
					}
					b.WriteString(x.Text + "\n")
				}
				return strings.TrimRight(b.String(), "\n")
			}
		}
	}
	return ""
}

func (s *Server) handlePullReviewComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadPull(w, r, st, repo)
	if !ok {
		return
	}
	body := normalizeNewlines(r.FormValue("body"))
	path := strings.TrimSpace(r.FormValue("path"))
	side := r.FormValue("side")
	line, _ := strconv.Atoi(r.FormValue("line"))
	body, err := s.saveAttachments(st, repo, is.Number, r, body)
	if err != nil {
		s.renderPull(w, http.StatusUnprocessableEntity, st, repo, is, "files", err.Error())
		return
	}
	if err := s.addLineComment(st, repo, is, path, side, line, body); err != nil {
		s.renderPull(w, opStatus(err), st, repo, is, "files", s.opMessage(err))
		return
	}
	http.Redirect(w, r, s.pullURL(st, repo, is.Number)+"/files", http.StatusSeeOther)
}

// ---- マージと Revert ----

func (s *Server) handlePullMerge(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadPull(w, r, st, repo)
	if !ok {
		return
	}
	warn, err := s.mergePull(st, repo, is, r.FormValue("message"))
	if err != nil {
		s.renderPull(w, http.StatusUnprocessableEntity, st, repo, is, "conversation", s.opMessage(err))
		return
	}
	if warn != "" {
		is, _ = st.GetIssue(repo.Name, is.Number)
		s.renderPull(w, http.StatusOK, st, repo, is, "conversation", warn)
		return
	}
	http.Redirect(w, r, s.pullURL(st, repo, is.Number), http.StatusSeeOther)
}

func (s *Server) handlePullRevert(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadPull(w, r, st, repo)
	if !ok {
		return
	}
	if !is.Merged || is.RevertSHA != "" {
		http.Redirect(w, r, s.pullURL(st, repo, is.Number), http.StatusSeeOther)
		return
	}
	if err := s.revertPull(st, repo, is); err != nil {
		s.renderPull(w, http.StatusUnprocessableEntity, st, repo, is, "conversation", s.opMessage(err))
		return
	}
	http.Redirect(w, r, s.pullURL(st, repo, is.Number), http.StatusSeeOther)
}
