package web

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// ---- user ----

func (s *Server) apiCurrentUser(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.toUser(s.login))
}

func (s *Server) apiGetUser(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("username")
	for _, st := range s.allStores() {
		users, _ := st.ListUsers()
		for _, u := range users {
			if strings.EqualFold(u.Login, login) {
				writeJSON(w, http.StatusOK, s.toUser(login))
				return
			}
		}
	}
	if strings.EqualFold(login, s.login) {
		writeJSON(w, http.StatusOK, s.toUser(login))
		return
	}
	s.apiError(w, http.StatusNotFound, "user not found")
}

// ---- org（データルートを組織として見せる）----

type apiOrg struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Username    string `json:"username"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	AvatarURL   string `json:"avatar_url"`
}

func (s *Server) toOrg(st *store.Store) apiOrg {
	c := st.Config()
	return apiOrg{ID: stableID("owner", c.Owner), Name: c.Owner, Username: c.Owner, FullName: c.DisplayOwner(), Visibility: "private"}
}

func (s *Server) apiUserOrgs(w http.ResponseWriter, r *http.Request) {
	out := []apiOrg{}
	for _, st := range s.allStores() {
		out = append(out, s.toOrg(st))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) apiGetOrg(w http.ResponseWriter, r *http.Request) {
	st := s.storeByOwner(r.PathValue("org"))
	if st == nil {
		s.apiError(w, http.StatusNotFound, "organization not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toOrg(st))
}

func (s *Server) apiOrgRepos(w http.ResponseWriter, r *http.Request) {
	st := s.storeByOwner(r.PathValue("org"))
	if st == nil {
		s.apiError(w, http.StatusNotFound, "organization not found")
		return
	}
	repos, err := st.ListRepos()
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiRepository{}
	for _, repo := range repos {
		out = append(out, s.toRepo(st, repo))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

// ---- repos ----

// allRepos は全データルートのリポジトリを返す。
func (s *Server) allRepos() []apiRepository {
	out := []apiRepository{}
	for _, st := range s.allStores() {
		repos, err := st.ListRepos()
		if err != nil {
			continue
		}
		for _, repo := range repos {
			out = append(out, s.toRepo(st, repo))
		}
	}
	return out
}

func (s *Server) apiUserRepos(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, paginate(w, r, s.allRepos()))
}

func (s *Server) apiSearchRepos(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var hits []apiRepository
	for _, repo := range s.allRepos() {
		if q == "" || strings.Contains(strings.ToLower(repo.Name), q) || strings.Contains(strings.ToLower(repo.Description), q) {
			hits = append(hits, repo)
		}
	}
	if hits == nil {
		hits = []apiRepository{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": paginate(w, r, hits)})
}

func (s *Server) apiGetRepo(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	writeJSON(w, http.StatusOK, s.toRepo(st, repo))
}

// ---- branches / commits ----

func (s *Server) toBranch(st *store.Store, repo store.Repo, b gitcmd.Branch) apiBranch {
	g := s.gitRepo(st, repo)
	out := apiBranch{Name: b.Name, Commit: apiBranchCommit{ID: b.SHA, URL: s.apiURL("repos", st.Config().Owner, repo.Name, "git", "commits", b.SHA)}, Protected: repo.DenyForcePush}
	if c, err := g.GetCommit(b.SHA); err == nil {
		au := apiCommitUser{Name: c.Author, Email: c.Email, Date: apiTime(c.Time)}
		out.Commit.Message = c.Subject
		out.Commit.Author = au
		out.Commit.Committer = au
		out.Commit.Timestamp = apiTime(c.Time)
	}
	return out
}

func (s *Server) apiBranches(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	branches, err := s.gitRepo(st, repo).Branches()
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiBranch{}
	for _, b := range branches {
		out = append(out, s.toBranch(st, repo, b))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) apiGetBranch(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	name := r.PathValue("branch")
	branches, err := s.gitRepo(st, repo).Branches()
	if err != nil {
		s.apiFail(w, err)
		return
	}
	for _, b := range branches {
		if b.Name == name {
			writeJSON(w, http.StatusOK, s.toBranch(st, repo, b))
			return
		}
	}
	s.apiError(w, http.StatusNotFound, "branch not found")
}

func (s *Server) apiCommits(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	ref := r.URL.Query().Get("sha")
	if ref == "" {
		ref = s.defaultBranch(g, repo)
	}
	if _, err := g.ResolveCommit(ref); err != nil {
		s.apiError(w, http.StatusNotFound, "ref not found")
		return
	}
	page, limit := pageParams(r)
	commits, err := g.Log(ref, r.URL.Query().Get("path"), (page-1)*limit, limit)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	if total, err := g.CountCommitsPath(ref, r.URL.Query().Get("path")); err == nil {
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
	}
	out := []apiCommit{}
	for _, c := range commits {
		out = append(out, s.toCommit(st, repo, c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiGetCommit(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	sha := r.PathValue("sha")
	wantDiff := strings.HasSuffix(sha, ".diff") || strings.HasSuffix(sha, ".patch")
	sha = strings.TrimSuffix(strings.TrimSuffix(sha, ".diff"), ".patch")
	resolved, err := g.ResolveCommit(sha)
	if err != nil {
		s.apiError(w, http.StatusNotFound, "commit not found")
		return
	}
	c, err := g.GetCommit(resolved)
	if err != nil {
		s.apiError(w, http.StatusNotFound, "commit not found")
		return
	}
	if wantDiff {
		raw, err := g.CommitDiff(c)
		if err != nil {
			s.apiFail(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(raw))
		return
	}
	writeJSON(w, http.StatusOK, s.toCommit(st, repo, c))
}

// ---- contents / raw ----

type apiContents struct {
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	SHA         string  `json:"sha"`
	Type        string  `json:"type"`
	Size        int64   `json:"size"`
	Encoding    *string `json:"encoding"`
	Content     *string `json:"content"`
	HTMLURL     string  `json:"html_url"`
	DownloadURL *string `json:"download_url"`
	URL         string  `json:"url"`
}

func (s *Server) contentsOf(st *store.Store, repo store.Repo, ref string, e gitcmd.Entry, withContent bool) apiContents {
	owner := st.Config().Owner
	g := s.gitRepo(st, repo)
	typ := "file"
	if e.IsDir() {
		typ = "dir"
	} else if e.Type == "commit" {
		typ = "submodule"
	}
	out := apiContents{
		Name: e.Name, Path: e.Path, SHA: e.SHA, Type: typ, Size: e.Size,
		HTMLURL: s.apiBase() + "/" + owner + "/" + repo.Name + "/blob/" + url.PathEscape(ref) + "/" + escapePath(e.Path),
		URL:     s.apiURL("repos", owner, repo.Name, "contents", escapePath(e.Path)) + "?ref=" + url.QueryEscape(ref),
	}
	if typ == "dir" {
		out.HTMLURL = s.apiBase() + "/" + owner + "/" + repo.Name + "/tree/" + url.PathEscape(ref) + "/" + escapePath(e.Path)
	}
	if typ == "file" {
		raw := s.apiBase() + "/" + owner + "/" + repo.Name + "/raw/" + url.PathEscape(ref) + "/" + escapePath(e.Path)
		out.DownloadURL = &raw
		if withContent && e.Size <= maxBlobBytes {
			if data, err := g.Blob(ref, e.Path); err == nil {
				enc := "base64"
				content := base64.StdEncoding.EncodeToString(data)
				out.Encoding = &enc
				out.Content = &content
			}
		}
	}
	return out
}

func (s *Server) apiContents(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = s.defaultBranch(g, repo)
	}
	if _, err := g.ResolveCommit(ref); err != nil {
		s.apiError(w, http.StatusNotFound, "ref not found")
		return
	}
	p, ok := cleanTreePath(r.PathValue("path"))
	if !ok {
		s.apiError(w, http.StatusNotFound, "path not found")
		return
	}
	if p == "" {
		entries, err := g.Tree(ref, "")
		if err != nil {
			s.apiError(w, http.StatusNotFound, "path not found")
			return
		}
		out := []apiContents{}
		for _, e := range entries {
			out = append(out, s.contentsOf(st, repo, ref, e, false))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	typ, err := g.ObjectType(ref, p)
	if err != nil {
		s.apiError(w, http.StatusNotFound, "path not found")
		return
	}
	if typ == "tree" {
		entries, err := g.Tree(ref, p)
		if err != nil {
			s.apiError(w, http.StatusNotFound, "path not found")
			return
		}
		out := []apiContents{}
		for _, e := range entries {
			out = append(out, s.contentsOf(st, repo, ref, e, false))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// ファイル1つ。親フォルダの一覧から大きさと SHA を取る
	parent, name := path.Split(p)
	parent = strings.TrimSuffix(parent, "/")
	entries, err := g.Tree(ref, parent)
	if err != nil {
		s.apiError(w, http.StatusNotFound, "path not found")
		return
	}
	for _, e := range entries {
		if e.Name == name {
			writeJSON(w, http.StatusOK, s.contentsOf(st, repo, ref, e, true))
			return
		}
	}
	s.apiError(w, http.StatusNotFound, "path not found")
}

func (s *Server) apiRaw(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = s.defaultBranch(g, repo)
	}
	if _, err := g.ResolveCommit(ref); err != nil {
		s.apiError(w, http.StatusNotFound, "ref not found")
		return
	}
	p, ok := cleanTreePath(r.PathValue("path"))
	if !ok || p == "" {
		s.apiError(w, http.StatusNotFound, "path not found")
		return
	}
	data, err := g.Blob(ref, p)
	if err != nil {
		s.apiError(w, http.StatusNotFound, "path not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if looksBinary(data) {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

// ---- compare ----

func (s *Server) apiCompare(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	base, head, ok := strings.Cut(r.PathValue("basehead"), "...")
	if !ok {
		s.apiError(w, http.StatusUnprocessableEntity, "use base...head")
		return
	}
	if _, err := g.ResolveCommit(base); err != nil {
		s.apiError(w, http.StatusNotFound, "base not found")
		return
	}
	if _, err := g.ResolveCommit(head); err != nil {
		s.apiError(w, http.StatusNotFound, "head not found")
		return
	}
	commits, err := g.RangeLog(base, head, 250)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	raw, err := g.RangeDiff(base, head)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiCommit{}
	for _, c := range commits {
		out = append(out, s.toCommit(st, repo, c))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_commits": len(commits),
		"commits":       out,
		"files":         s.toChangedFiles(st, repo, head, gitcmd.ParseDiff(raw)),
	})
}

// ---- 本ツールに無い機能を空で返す（クライアントが落ちないようにする）----

func (s *Server) apiEmptyList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Total-Count", "0")
	writeJSON(w, http.StatusOK, []struct{}{})
}

func (s *Server) apiEmptyListRepo(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	s.apiEmptyList(w, r)
}

func (s *Server) apiEmptyListIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	s.apiEmptyList(w, r)
}

// apiEmptyStatus はコミットの CI 状態。CI は無いので statuses を空で返す。
func (s *Server) apiEmptyStatus(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	writeJSON(w, http.StatusOK, map[string]any{"state": "", "statuses": []struct{}{}, "sha": r.PathValue("ref"), "total_count": 0, "repository": s.toRepo(st, repo)})
}
