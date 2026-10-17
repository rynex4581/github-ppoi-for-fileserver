package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// ---- トップ ----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	st := s.requireStore(w, r)
	if st == nil {
		return
	}
	repos, err := st.ListRepos()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := s.newPage("Repositories")
	p.Data = map[string]any{"Repos": repos}
	s.render(w, http.StatusOK, "index", p)
}

// ---- 新規リポジトリ ----

type repoForm struct {
	Name        string
	Description string
}

func (s *Server) handleRepoNew(w http.ResponseWriter, r *http.Request) {
	if s.requireStore(w, r) == nil {
		return
	}
	p := s.newPage("Create a new repository")
	p.Data = repoForm{}
	s.render(w, http.StatusOK, "repo_new", p)
}

func (s *Server) handleRepoCreate(w http.ResponseWriter, r *http.Request) {
	st := s.requireStore(w, r)
	if st == nil {
		return
	}
	form := repoForm{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
	}
	repo, err := st.CreateRepo(form.Name, form.Description)
	if err != nil {
		p := s.newPage("Create a new repository")
		p.Error = err.Error()
		p.Data = form
		s.render(w, http.StatusUnprocessableEntity, "repo_new", p)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/%s/issues", st.Config().Owner, repo.Name), http.StatusSeeOther)
}

func (s *Server) handleRepoHome(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	// Code画面ができるまでは Issues に送る
	http.Redirect(w, r, fmt.Sprintf("/%s/%s/issues", st.Config().Owner, repo.Name), http.StatusSeeOther)
}

// ---- 設定 ----

type settingsForm struct {
	DataRoot   string
	ConfigPath string
	OpenError  string
	Saved      bool
}

func (s *Server) settingsData() settingsForm {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := settingsForm{DataRoot: s.cfg.DataRoot}
	if s.openErr != nil {
		f.OpenError = s.openErr.Error()
	}
	f.ConfigPath, _ = localconfig.Path()
	return f
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	p := s.newPage("Settings")
	f := s.settingsData()
	f.Saved = r.URL.Query().Get("saved") == "1"
	p.Data = f
	s.render(w, http.StatusOK, "settings", p)
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.FormValue("data_root"))
	p := s.newPage("Settings")
	f := s.settingsData()
	f.DataRoot = root
	f.OpenError = ""

	if root == "" {
		p.Error = "データルートのパスを入れる"
		p.Data = f
		s.render(w, http.StatusUnprocessableEntity, "settings", p)
		return
	}
	st, err := store.Open(root)
	if err != nil {
		p.Error = err.Error()
		p.Data = f
		s.render(w, http.StatusUnprocessableEntity, "settings", p)
		return
	}

	s.mu.Lock()
	s.cfg.DataRoot = root
	s.st = st
	s.openErr = nil
	cfg := s.cfg
	s.mu.Unlock()

	if err := localconfig.Save(cfg); err != nil {
		p.Error = "設定の保存に失敗: " + err.Error()
		p.Data = f
		s.render(w, http.StatusInternalServerError, "settings", p)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// ---- Issues ----

type issuesView struct {
	State       string
	Issues      []store.Issue
	OpenCount   int
	ClosedCount int
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	all, err := st.ListIssues(repo.Name)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v := issuesView{State: r.URL.Query().Get("state")}
	if v.State != "closed" {
		v.State = "open"
	}
	for _, is := range all {
		if is.State == "open" {
			v.OpenCount++
		} else {
			v.ClosedCount++
		}
		if is.State == v.State {
			v.Issues = append(v.Issues, is)
		}
	}
	p := s.repoPage("Issues", repo, "issues")
	p.Data = v
	s.render(w, http.StatusOK, "issues", p)
}

type issueForm struct {
	Title string
	Body  string
}

func (s *Server) handleIssueNew(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	p := s.repoPage("New Issue", repo, "issues")
	p.Data = issueForm{}
	s.render(w, http.StatusOK, "issue_new", p)
}

func (s *Server) handleIssueCreate(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	form := issueForm{
		Title: strings.TrimSpace(r.FormValue("title")),
		Body:  normalizeNewlines(r.FormValue("body")),
	}
	if form.Title == "" {
		p := s.repoPage("New Issue", repo, "issues")
		p.Error = "タイトルを入れる"
		p.Data = form
		s.render(w, http.StatusUnprocessableEntity, "issue_new", p)
		return
	}
	is, err := st.CreateIssue(repo.Name, s.login, form.Title, form.Body)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/%s/issues/%d", st.Config().Owner, repo.Name, is.Number), http.StatusSeeOther)
}

// timelineItem はIssue詳細のタイムラインに出す1項目。
type timelineItem struct {
	Kind  string // comment / closed / reopened / title
	User  string
	Time  time.Time
	Body  string
	Title string
}

type issueView struct {
	Issue    store.Issue
	Timeline []timelineItem
}

func buildTimeline(is store.Issue) []timelineItem {
	var items []timelineItem
	seenCreated := false
	for _, ev := range is.Events {
		switch ev.Type {
		case store.EventCreated:
			if !seenCreated {
				seenCreated = true
				continue
			}
		case store.EventComment:
			items = append(items, timelineItem{Kind: "comment", User: ev.User, Time: ev.Time, Body: ev.Body})
		case store.EventClosed, store.EventReopened:
			if ev.Body != "" {
				items = append(items, timelineItem{Kind: "comment", User: ev.User, Time: ev.Time, Body: ev.Body})
			}
			items = append(items, timelineItem{Kind: ev.Type, User: ev.User, Time: ev.Time})
		case store.EventTitle:
			items = append(items, timelineItem{Kind: "title", User: ev.User, Time: ev.Time, Title: ev.Title})
		}
	}
	return items
}

func (s *Server) loadIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) (store.Issue, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number <= 0 {
		s.renderError(w, http.StatusNotFound, "Issueが無い")
		return store.Issue{}, false
	}
	is, err := st.GetIssue(repo.Name, number)
	if errors.Is(err, store.ErrIssueNotFound) {
		s.renderError(w, http.StatusNotFound, "Issueが無い")
		return store.Issue{}, false
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return store.Issue{}, false
	}
	return is, true
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	p := s.repoPage(fmt.Sprintf("%s · Issue #%d", is.Title, is.Number), repo, "issues")
	p.Data = issueView{Issue: is, Timeline: buildTimeline(is)}
	s.render(w, http.StatusOK, "issue", p)
}

func (s *Server) handleIssueComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	is, ok := s.loadIssue(w, r, st, repo)
	if !ok {
		return
	}
	body := normalizeNewlines(r.FormValue("body"))
	action := r.FormValue("action")

	var ev store.Event
	switch action {
	case "close":
		if is.State == "closed" {
			break
		}
		ev = store.Event{Type: store.EventClosed, User: s.login, Body: body}
	case "reopen":
		if is.State == "open" {
			break
		}
		ev = store.Event{Type: store.EventReopened, User: s.login, Body: body}
	default:
		if strings.TrimSpace(body) == "" {
			p := s.repoPage(fmt.Sprintf("%s · Issue #%d", is.Title, is.Number), repo, "issues")
			p.Error = "コメントの本文を入れる"
			p.Data = issueView{Issue: is, Timeline: buildTimeline(is)}
			s.render(w, http.StatusUnprocessableEntity, "issue", p)
			return
		}
		ev = store.Event{Type: store.EventComment, User: s.login, Body: body}
	}
	if ev.Type != "" {
		if err := st.AppendEvent(repo.Name, is.Number, ev); err != nil {
			s.renderError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/%s/issues/%d", st.Config().Owner, repo.Name, is.Number), http.StatusSeeOther)
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
