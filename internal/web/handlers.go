package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/store"
)

// ---- トップ ----

// ownerGroup はトップ画面の owner 1つ分。
type ownerGroup struct {
	Owner string
	Name  string
	Root  string
	Repos []store.Repo
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !s.requireStores(w, r) {
		return
	}
	var groups []ownerGroup
	for _, st := range s.allStores() {
		repos, err := st.ListRepos()
		if err != nil {
			s.renderError(w, http.StatusInternalServerError, err.Error())
			return
		}
		groups = append(groups, ownerGroup{Owner: st.Config().Owner, Name: st.Config().DisplayOwner(), Root: st.Root(), Repos: repos})
	}
	p := s.newPage(s.tr("home.my_repos"))
	p.Data = map[string]any{"Groups": groups}
	s.render(w, http.StatusOK, "index", p)
}

// ---- 新規リポジトリ ----

type ownerChoice struct {
	Owner string
	Name  string
}

type repoForm struct {
	Owner       string
	Name        string
	Description string
	Owners      []ownerChoice
}

func (s *Server) ownerChoices() []ownerChoice {
	var out []ownerChoice
	for _, st := range s.allStores() {
		out = append(out, ownerChoice{Owner: st.Config().Owner, Name: st.Config().DisplayOwner()})
	}
	return out
}

func (s *Server) handleRepoNew(w http.ResponseWriter, r *http.Request) {
	if !s.requireStores(w, r) {
		return
	}
	f := repoForm{Owner: r.URL.Query().Get("owner"), Owners: s.ownerChoices()}
	if f.Owner == "" && len(f.Owners) > 0 {
		f.Owner = f.Owners[0].Owner
	}
	p := s.newPage(s.tr("new_repo"))
	p.Data = f
	s.render(w, http.StatusOK, "repo_new", p)
}

func (s *Server) handleRepoCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireStores(w, r) {
		return
	}
	form := repoForm{
		Owner:       strings.TrimSpace(r.FormValue("owner")),
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Owners:      s.ownerChoices(),
	}
	fail := func(msg string) {
		p := s.newPage(s.tr("new_repo"))
		p.Error = msg
		p.Data = form
		s.render(w, http.StatusUnprocessableEntity, "repo_new", p)
	}
	st := s.storeByOwner(form.Owner)
	if st == nil {
		fail(s.tr("ppoi.error.owner_not_found"))
		return
	}
	repo, err := st.CreateRepo(form.Name, form.Description)
	if err != nil {
		fail(err.Error())
		return
	}
	http.Redirect(w, r, s.repoURL(st, repo), http.StatusSeeOther)
}

// ---- Issues 一覧 ----

// filterItem は絞り込みメニューの1項目。
type filterItem struct {
	Text     string
	URL      string
	Selected bool
	Label    *store.Label
}

type issueRow struct {
	store.Issue
	Labels    []store.Label
	Assignees []string
}

type issuesView struct {
	IsPull         bool
	ListURL        string
	State          string
	Issues         []issueRow
	OpenCount      int
	ClosedCount    int
	LabelCount     int
	OpenURL        string
	ClosedURL      string
	ClearURL       string
	Filtered       bool
	LabelItems     []filterItem
	AssigneeItems  []filterItem
	NewIssueURL    string
	LabelsURL      string
	SelectedLabels []store.Label
}

// issueFilter は一覧の絞り込み条件。クエリ文字列と相互に変換する。
type issueFilter struct {
	State    string
	Labels   []int  // 付いているラベルのID（すべて付いているものだけ出す）。0 は「ラベルなし」
	Assignee string // ログオン名。"0" は「担当者なし」
	Author   string // 作成者のログオン名
}

func parseIssueFilter(q url.Values) issueFilter {
	f := issueFilter{State: q.Get("state"), Assignee: q.Get("assignee"), Author: q.Get("author")}
	if f.State != "closed" {
		f.State = "open"
	}
	for _, part := range strings.Split(q.Get("labels"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n >= 0 {
			f.Labels = append(f.Labels, n)
		}
	}
	return f
}

func (f issueFilter) query() string {
	q := url.Values{}
	q.Set("state", f.State)
	if len(f.Labels) > 0 {
		parts := make([]string, len(f.Labels))
		for i, n := range f.Labels {
			parts[i] = strconv.Itoa(n)
		}
		q.Set("labels", strings.Join(parts, ","))
	}
	if f.Assignee != "" {
		q.Set("assignee", f.Assignee)
	}
	return "?" + q.Encode()
}

func (f issueFilter) hasLabel(id int) bool {
	for _, x := range f.Labels {
		if x == id {
			return true
		}
	}
	return false
}

// toggleLabel はラベルの選択を反転した条件を返す。「ラベルなし」は他と同時に選べない。
func (f issueFilter) toggleLabel(id int) issueFilter {
	g := f
	g.Labels = nil
	if id == 0 {
		if !f.hasLabel(0) {
			g.Labels = []int{0}
		}
		return g
	}
	for _, x := range f.Labels {
		if x != id && x != 0 {
			g.Labels = append(g.Labels, x)
		}
	}
	if !f.hasLabel(id) {
		g.Labels = append(g.Labels, id)
	}
	return g
}

func (f issueFilter) matches(is store.Issue) bool {
	if is.State != f.State {
		return false
	}
	for _, id := range f.Labels {
		if id == 0 {
			if len(is.Labels) > 0 {
				return false
			}
			continue
		}
		if !is.HasLabel(id) {
			return false
		}
	}
	switch f.Assignee {
	case "":
	case "0":
		if len(is.Assignees) > 0 {
			return false
		}
	default:
		if !is.HasAssignee(f.Assignee) {
			return false
		}
	}
	if f.Author != "" && !strings.EqualFold(is.User, f.Author) {
		return false
	}
	return true
}

func labelsByID(labels []store.Label) map[int]store.Label {
	m := make(map[int]store.Label, len(labels))
	for _, l := range labels {
		m[l.ID] = l
	}
	return m
}

// resolveLabels はIDの列を、今あるラベルだけに解決する。
func resolveLabels(ids []int, byID map[int]store.Label) []store.Label {
	var out []store.Label
	for _, id := range ids {
		if l, ok := byID[id]; ok {
			out = append(out, l)
		}
	}
	return out
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	s.listIssues(w, r, st, repo, false)
}

// listIssues は Issue（isPull が false）または Pull Request（true）の一覧を出す。
func (s *Server) listIssues(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, isPull bool) {
	all, err := st.ListIssues(repo.Name)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels, err := st.ListLabels(repo.Name)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	users, err := st.ListUsers()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byID := labelsByID(labels)
	f := parseIssueFilter(r.URL.Query())
	base := s.repoURL(st, repo) + "/issues"
	tab := "issues"
	if isPull {
		base = s.repoURL(st, repo) + "/pulls"
		tab = "pulls"
	}

	v := issuesView{
		IsPull:      isPull,
		ListURL:     base,
		State:       f.State,
		LabelCount:  len(labels),
		NewIssueURL: base + "/new",
		LabelsURL:   s.repoURL(st, repo) + "/labels",
		Filtered:    len(f.Labels) > 0 || f.Assignee != "",
	}
	for _, is := range all {
		if is.IsPull != isPull {
			continue
		}
		// 件数は open/closed 以外の条件を掛けた上で数える（GitHubと同じ）
		g := f
		g.State = is.State
		if !g.matches(is) {
			continue
		}
		if is.State == "open" {
			v.OpenCount++
		} else {
			v.ClosedCount++
		}
		if is.State == f.State {
			v.Issues = append(v.Issues, issueRow{Issue: is, Labels: resolveLabels(is.Labels, byID), Assignees: is.Assignees})
		}
	}

	open, closed := f, f
	open.State, closed.State = "open", "closed"
	v.OpenURL = base + open.query()
	v.ClosedURL = base + closed.query()
	v.ClearURL = base + issueFilter{State: f.State}.query()

	v.LabelItems = append(v.LabelItems, filterItem{
		Text: s.tr("repo.issues.filter_label_select_no_label"), URL: base + f.toggleLabel(0).query(), Selected: f.hasLabel(0),
	})
	for i := range labels {
		l := labels[i]
		v.LabelItems = append(v.LabelItems, filterItem{
			Text: l.Name, URL: base + f.toggleLabel(l.ID).query(), Selected: f.hasLabel(l.ID), Label: &l,
		})
		if f.hasLabel(l.ID) {
			v.SelectedLabels = append(v.SelectedLabels, l)
		}
	}
	noAssignee := f
	noAssignee.Assignee = "0"
	if f.Assignee == "0" {
		noAssignee.Assignee = ""
	}
	v.AssigneeItems = append(v.AssigneeItems, filterItem{
		Text: s.tr("repo.issues.filter_assignee_no_assignee"), URL: base + noAssignee.query(), Selected: f.Assignee == "0",
	})
	for _, u := range users {
		g := f
		g.Assignee = u.Login
		if f.Assignee == u.Login {
			g.Assignee = ""
		}
		v.AssigneeItems = append(v.AssigneeItems, filterItem{Text: st.DisplayName(u.Login), URL: base + g.query(), Selected: f.Assignee == u.Login})
	}

	title := s.tr("repo.issues")
	if isPull {
		title = s.tr("repo.pulls")
	}
	p := s.repoPage(title, repo, tab)
	p.Data = v
	s.render(w, http.StatusOK, "issues", p)
}

// ---- Issue 新規 ----

type issueForm struct {
	Title     string
	Body      string
	Labels    []int
	Assignees []string
	AllLabels []store.Label
	AllUsers  []store.User
}

func (f issueForm) HasLabel(id int) bool {
	for _, x := range f.Labels {
		if x == id {
			return true
		}
	}
	return false
}

func (f issueForm) HasAssignee(login string) bool {
	for _, x := range f.Assignees {
		if strings.EqualFold(x, login) {
			return true
		}
	}
	return false
}

func (s *Server) issueFormChoices(st *store.Store, repo store.Repo, f *issueForm) error {
	var err error
	if f.AllLabels, err = st.ListLabels(repo.Name); err != nil {
		return err
	}
	f.AllUsers, err = st.ListUsers()
	return err
}

// parseLabelIDs はフォームの labels の値を、今あるラベルのIDだけに絞って返す。
func parseLabelIDs(values []string, labels []store.Label) []int {
	byID := labelsByID(labels)
	var out []int
	for _, v := range values {
		id, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		if _, ok := byID[id]; ok {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// parseAssignees はフォームの assignees の値を、登録のある利用者だけに絞って返す。
func parseAssignees(values []string, users []store.User) []string {
	known := map[string]bool{}
	for _, u := range users {
		known[u.Login] = true
	}
	var out []string
	for _, v := range values {
		if known[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) handleIssueNew(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	f := issueForm{}
	if err := s.issueFormChoices(st, repo, &f); err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := s.repoPage(s.tr("repo.issues.new"), repo, "issues")
	p.Data = f
	s.render(w, http.StatusOK, "issue_new", p)
}

func (s *Server) handleIssueCreate(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	f := issueForm{
		Title: strings.TrimSpace(r.FormValue("title")),
		Body:  normalizeNewlines(r.FormValue("body")),
	}
	if err := s.issueFormChoices(st, repo, &f); err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f.Labels = parseLabelIDs(r.Form["labels"], f.AllLabels)
	f.Assignees = parseAssignees(r.Form["assignees"], f.AllUsers)
	if f.Title == "" {
		p := s.repoPage(s.tr("repo.issues.new"), repo, "issues")
		p.Error = s.tr("ppoi.error.title_required")
		p.Data = f
		s.render(w, http.StatusUnprocessableEntity, "issue_new", p)
		return
	}
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
	is, err := st.WriteCreated(repo.Name, number, store.Event{User: s.login, Title: f.Title, Body: body}, f.Labels, f.Assignees)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("%s/issues/%d", s.repoURL(st, repo), is.Number), http.StatusSeeOther)
}

// ---- Issue 詳細 ----

// timelineItem はIssue詳細のタイムラインに出す1項目。
type timelineItem struct {
	Kind      string // comment / closed / reopened / title / labels / assignee
	User      string // 操作した人
	Time      time.Time
	Body      string
	File      string   // 元のイベントのファイル名。編集と削除の target
	Edit      editInfo // 編集の有無と履歴
	CanDelete bool     // 削除できる（コメントと行コメント）
	Title     string
	OldTitle  string
	Added     []store.Label // labels
	Removed   []store.Label // labels
	Assignee  string        // assignee: 対象の人
	Assigned  bool          // assignee: true なら指名、false なら解除

	// Pull Request 用
	State     string // review: approved / changes_requested / comment
	CommitSHA string // review_comment
	Path      string
	Line      int
	Side      string
	DiffHunk  string
	Outdated  bool   // review_comment: 今の head に無い版へのコメント
	SHA       string // merged: マージコミット。reverted: 取り消しのコミット
}

// editInfo は本文やコメントの編集の有無と履歴。画面の「編集済み」の表示に使う。
type editInfo struct {
	Edited  bool
	By      string
	At      time.Time
	History []store.Revision
}

func editInfoOf(ev store.Event) editInfo {
	return editInfo{Edited: ev.EditedBy != "", By: ev.EditedBy, At: ev.EditedAt, History: ev.History}
}

func bodyEditInfo(is store.Issue) editInfo {
	return editInfo{Edited: is.BodyEditedBy != "", By: is.BodyEditedBy, At: is.BodyEditedAt, History: is.BodyHistory}
}

type issueView struct {
	Issue     store.Issue
	BodyEdit  editInfo
	Labels    []store.Label
	Assignees []string
	Timeline  []timelineItem
	AllLabels []store.Label
	AllUsers  []store.User
}

func buildTimeline(is store.Issue, byID map[int]store.Label) []timelineItem {
	var items []timelineItem
	seenCreated := false
	title := ""
	var labels []int
	var assignees []string
	for _, ev := range is.Events {
		switch ev.Type {
		case store.EventCreated:
			if !seenCreated {
				seenCreated = true
				title = ev.Title
			}
		case store.EventComment:
			if ev.Deleted {
				continue
			}
			items = append(items, timelineItem{Kind: "comment", User: ev.User, Time: ev.Time, Body: ev.Body, File: ev.File, Edit: editInfoOf(ev), CanDelete: true})
		case store.EventClosed, store.EventReopened:
			if ev.Body != "" && !ev.Deleted {
				items = append(items, timelineItem{Kind: "comment", User: ev.User, Time: ev.Time, Body: ev.Body, File: ev.File, Edit: editInfoOf(ev)})
			}
			items = append(items, timelineItem{Kind: ev.Type, User: ev.User, Time: ev.Time})
		case store.EventTitle:
			items = append(items, timelineItem{Kind: "title", User: ev.User, Time: ev.Time, Title: ev.Title, OldTitle: title})
			title = ev.Title
		case store.EventLabels:
			it := timelineItem{Kind: "labels", User: ev.User, Time: ev.Time}
			for _, id := range ev.Labels {
				if !containsInt(labels, id) {
					if l, ok := byID[id]; ok {
						it.Added = append(it.Added, l)
					}
				}
			}
			for _, id := range labels {
				if !containsInt(ev.Labels, id) {
					if l, ok := byID[id]; ok {
						it.Removed = append(it.Removed, l)
					}
				}
			}
			labels = append([]int(nil), ev.Labels...)
			if len(it.Added)+len(it.Removed) > 0 {
				items = append(items, it)
			}
		case store.EventReview:
			items = append(items, timelineItem{Kind: "review", User: ev.User, Time: ev.Time, Body: ev.Body, State: ev.State, CommitSHA: ev.CommitSHA, File: ev.File, Edit: editInfoOf(ev)})
		case store.EventReviewComment:
			if ev.Deleted {
				continue
			}
			items = append(items, timelineItem{Kind: "review_comment", User: ev.User, Time: ev.Time, Body: ev.Body, CommitSHA: ev.CommitSHA, Path: ev.Path, Line: ev.Line, Side: ev.Side, DiffHunk: ev.DiffHunk, File: ev.File, Edit: editInfoOf(ev), CanDelete: true})
		case store.EventMerged:
			items = append(items, timelineItem{Kind: "merged", User: ev.User, Time: ev.Time, SHA: ev.MergeSHA})
		case store.EventReverted:
			items = append(items, timelineItem{Kind: "reverted", User: ev.User, Time: ev.Time, SHA: ev.RevertSHA})
		case store.EventAssigned:
			for _, login := range ev.Assignees {
				if !containsString(assignees, login) {
					items = append(items, timelineItem{Kind: "assignee", User: ev.User, Time: ev.Time, Assignee: login, Assigned: true})
				}
			}
			for _, login := range assignees {
				if !containsString(ev.Assignees, login) {
					items = append(items, timelineItem{Kind: "assignee", User: ev.User, Time: ev.Time, Assignee: login})
				}
			}
			assignees = append([]string(nil), ev.Assignees...)
		}
	}
	return items
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *Server) loadIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) (store.Issue, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number <= 0 {
		s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.issue_not_found"))
		return store.Issue{}, false
	}
	is, err := st.GetIssue(repo.Name, number)
	if errors.Is(err, store.ErrIssueNotFound) {
		s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.issue_not_found"))
		return store.Issue{}, false
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return store.Issue{}, false
	}
	return is, true
}

func (s *Server) issuePage(st *store.Store, repo store.Repo, is store.Issue) (page, error) {
	v := issueView{Issue: is, Assignees: is.Assignees, BodyEdit: bodyEditInfo(is)}
	var err error
	if v.AllLabels, err = st.ListLabels(repo.Name); err != nil {
		return page{}, err
	}
	if v.AllUsers, err = st.ListUsers(); err != nil {
		return page{}, err
	}
	byID := labelsByID(v.AllLabels)
	v.Labels = resolveLabels(is.Labels, byID)
	v.Timeline = buildTimeline(is, byID)
	p := s.repoPage(fmt.Sprintf("%s · #%d", is.Title, is.Number), repo, "issues")
	p.Data = v
	return p, nil
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	if is.IsPull {
		http.Redirect(w, r, s.pullURL(st, repo, is.Number), http.StatusSeeOther)
		return
	}
	p, err := s.issuePage(st, repo, is)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, http.StatusOK, "issue", p)
}

func (s *Server) redirectIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	if is.IsPull {
		http.Redirect(w, r, s.pullURL(st, repo, is.Number), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("%s/issues/%d", s.repoURL(st, repo), is.Number), http.StatusSeeOther)
}

func (s *Server) handleIssueComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	body := normalizeNewlines(r.FormValue("body"))
	action := r.FormValue("action")
	body, err := s.saveAttachments(st, repo, is.Number, r, body)
	if err != nil {
		s.renderError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	switch action {
	case "close":
		err = s.setState(st, repo, is, "closed", body)
	case "reopen":
		err = s.setState(st, repo, is, "open", body)
	default:
		err = s.addComment(st, repo, is, body)
	}
	if err != nil {
		s.renderIssueError(w, st, repo, is, err)
		return
	}
	s.redirectIssue(w, r, st, repo, is)
}

// ---- 本文とコメントの編集・削除 ----

// issueEditView は編集画面の内容。
type issueEditView struct {
	Issue   store.Issue
	Target  string // 対象のイベントのファイル名。本文なら空
	IsBody  bool   // 本文（タイトルも一緒に直せる）
	Title   string
	Body    string
	Action  string // フォームの送信先
	BackURL string // キャンセルで戻る先
}

// issueBase は Issue または PR の画面の URL。
func (s *Server) issueBase(st *store.Store, repo store.Repo, is store.Issue) string {
	if is.IsPull {
		return s.pullURL(st, repo, is.Number)
	}
	return fmt.Sprintf("%s/issues/%d", s.repoURL(st, repo), is.Number)
}

func (s *Server) issueEditView(st *store.Store, repo store.Repo, is store.Issue, target string) (issueEditView, error) {
	base := s.issueBase(st, repo, is)
	v := issueEditView{Issue: is, Target: target, BackURL: base, Action: base + "/edit?target=" + url.QueryEscape(target)}
	if target == "" || target == is.BodyFile {
		v.IsBody = true
		v.Target = ""
		v.Action = base + "/edit"
		v.Title = is.Title
		v.Body = is.Body
		return v, nil
	}
	for _, ev := range is.Events {
		if ev.File == target && !ev.Deleted && (ev.Type == store.EventComment || ev.Type == store.EventClosed || ev.Type == store.EventReopened || ev.Type == store.EventReview || ev.Type == store.EventReviewComment) {
			v.Body = ev.Body
			return v, nil
		}
	}
	return v, opErr(404, "ppoi.error.comment_not_found")
}

func (s *Server) renderIssueEdit(w http.ResponseWriter, status int, repo store.Repo, v issueEditView, errMsg string) {
	tab := "issues"
	if v.Issue.IsPull {
		tab = "pulls"
	}
	p := s.repoPage(s.tr("edit"), repo, tab)
	p.Error = errMsg
	p.Data = v
	s.render(w, status, "issue_edit", p)
}

func (s *Server) handleIssueEdit(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	v, err := s.issueEditView(st, repo, is, r.URL.Query().Get("target"))
	if err == nil {
		err = s.requireAuthor(is, v.Target)
	}
	if err != nil {
		s.renderIssueError(w, st, repo, is, err)
		return
	}
	s.renderIssueEdit(w, http.StatusOK, repo, v, "")
}

func (s *Server) handleIssueEditSave(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	v, err := s.issueEditView(st, repo, is, r.URL.Query().Get("target"))
	if err == nil {
		// タイトルの変更も含めて、書いた本人だけ
		err = s.requireAuthor(is, v.Target)
	}
	if err != nil {
		s.renderIssueError(w, st, repo, is, err)
		return
	}
	v.Body = normalizeNewlines(r.FormValue("body"))
	if v.IsBody {
		v.Title = strings.TrimSpace(r.FormValue("title"))
		if v.Title == "" {
			s.renderIssueEdit(w, http.StatusUnprocessableEntity, repo, v, s.tr("ppoi.error.title_required"))
			return
		}
	}
	// 編集画面で貼った・選んだ添付も、新規と同じく送信時に保存して印を置き換える
	v.Body, err = s.saveAttachments(st, repo, is.Number, r, v.Body)
	if err != nil {
		s.renderIssueEdit(w, http.StatusUnprocessableEntity, repo, v, s.opMessage(err))
		return
	}
	// 本文を先に書く。題名を先に書くと、本文の書き込みに失敗したとき題名だけ変わった状態で画面に戻る
	if v.Body != bodyOf(is, v) {
		if err := s.editBody(st, repo, is, v.Target, v.Body); err != nil {
			s.renderIssueEdit(w, opStatus(err), repo, v, s.opMessage(err))
			return
		}
	}
	if v.IsBody && v.Title != is.Title {
		if err := s.setTitle(st, repo, is, v.Title); err != nil {
			s.renderIssueEdit(w, opStatus(err), repo, v, s.opMessage(err))
			return
		}
	}
	s.redirectIssue(w, r, st, repo, is)
}

// bodyOf は編集画面の対象の今の本文を返す。
func bodyOf(is store.Issue, v issueEditView) string {
	if v.IsBody {
		return is.Body
	}
	for _, ev := range is.Events {
		if ev.File == v.Target {
			return ev.Body
		}
	}
	return ""
}

func (s *Server) handleIssueDelete(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	if err := s.deleteComment(st, repo, is, r.URL.Query().Get("target")); err != nil {
		s.renderIssueError(w, st, repo, is, err)
		return
	}
	s.redirectIssue(w, r, st, repo, is)
}

// handleMarkdownPreview は入力欄のプレビュー用に Markdown を描画して返す。
func (s *Server) handleMarkdownPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(s.renderMarkdown(normalizeNewlines(r.FormValue("text")))))
}

// renderIssueError は Issue か PR の詳細画面にエラーを出す。
func (s *Server) renderIssueError(w http.ResponseWriter, st *store.Store, repo store.Repo, is store.Issue, err error) {
	status := opStatus(err)
	if status == 500 {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if is.IsPull {
		s.renderPull(w, status, st, repo, is, "conversation", s.opMessage(err))
		return
	}
	p, perr := s.issuePage(st, repo, is)
	if perr != nil {
		s.renderError(w, http.StatusInternalServerError, perr.Error())
		return
	}
	p.Error = s.opMessage(err)
	s.render(w, status, "issue", p)
}

func (s *Server) handleIssueLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	r.ParseForm()
	var ids []int
	for _, v := range r.Form["labels"] {
		if id, err := strconv.Atoi(v); err == nil {
			ids = append(ids, id)
		}
	}
	if err := s.setLabels(st, repo, is, ids); err != nil {
		s.renderIssueError(w, st, repo, is, err)
		return
	}
	s.redirectIssue(w, r, st, repo, is)
}

func (s *Server) handleIssueAssignees(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	r.ParseForm()
	if err := s.setAssignees(st, repo, is, r.Form["assignees"]); err != nil {
		s.renderIssueError(w, st, repo, is, err)
		return
	}
	s.redirectIssue(w, r, st, repo, is)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- ラベル管理 ----

type labelsView struct {
	Labels  []store.Label
	EditID  int         // 編集中のラベルのID（?edit=ID）
	Form    store.Label // 新規または編集中の入力値
	ShowNew bool
	BaseURL string
}

func (s *Server) labelsPage(st *store.Store, repo store.Repo, v labelsView) (page, error) {
	labels, err := st.ListLabels(repo.Name)
	if err != nil {
		return page{}, err
	}
	v.Labels = labels
	v.BaseURL = s.repoURL(st, repo) + "/labels"
	p := s.repoPage(s.tr("repo.labels"), repo, "issues")
	p.Data = v
	return p, nil
}

func (s *Server) handleLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	v := labelsView{Form: store.Label{Color: "0075ca"}}
	if id, err := strconv.Atoi(r.URL.Query().Get("edit")); err == nil {
		if l, err := st.GetLabel(repo.Name, id); err == nil {
			v.EditID = id
			v.Form = l
		}
	}
	v.ShowNew = r.URL.Query().Get("new") == "1"
	p, err := s.labelsPage(st, repo, v)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, http.StatusOK, "labels", p)
}

func labelFromForm(r *http.Request) store.Label {
	return store.Label{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Color:       store.NormalizeLabelColor(r.FormValue("color")),
		Description: strings.TrimSpace(r.FormValue("description")),
	}
}

func (s *Server) handleLabelCreate(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	l := labelFromForm(r)
	if _, err := st.CreateLabel(repo.Name, l); err != nil {
		p, perr := s.labelsPage(st, repo, labelsView{Form: l, ShowNew: true})
		if perr != nil {
			s.renderError(w, http.StatusInternalServerError, perr.Error())
			return
		}
		p.Error = err.Error()
		s.render(w, http.StatusUnprocessableEntity, "labels", p)
		return
	}
	http.Redirect(w, r, s.repoURL(st, repo)+"/labels", http.StatusSeeOther)
}

func (s *Server) handleLabelUpdate(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.label_not_found"))
		return
	}
	l := labelFromForm(r)
	l.ID = id
	if err := st.UpdateLabel(repo.Name, l); err != nil {
		if errors.Is(err, store.ErrLabelNotFound) {
			s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.label_not_found"))
			return
		}
		p, perr := s.labelsPage(st, repo, labelsView{Form: l, EditID: id})
		if perr != nil {
			s.renderError(w, http.StatusInternalServerError, perr.Error())
			return
		}
		p.Error = err.Error()
		s.render(w, http.StatusUnprocessableEntity, "labels", p)
		return
	}
	http.Redirect(w, r, s.repoURL(st, repo)+"/labels", http.StatusSeeOther)
}

func (s *Server) handleLabelDelete(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err == nil {
		err = st.DeleteLabel(repo.Name, id)
	}
	if err != nil && !errors.Is(err, store.ErrLabelNotFound) {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, s.repoURL(st, repo)+"/labels", http.StatusSeeOther)
}

// normalizeNewlines はフォームから来る CRLF を LF に揃え、末尾の空白を落とす。
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimRight(s, " \t\n")
}

// CurrentLogin はWindowsのログオン名（%USERNAME%）を返す。取れなければ "unknown"。
func CurrentLogin() string {
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}
