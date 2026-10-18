package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
	"github-ppoi-for-fileserver/internal/version"
)

// Gitea API v1 互換のエンドポイント。パスと JSON の項目名は Gitea に合わせる。
// 画面と同じ ops.go の関数を呼ぶので、動きは画面と一致する。
//
// 認証はローカルのみのため、Authorization ヘッダのトークンは空でなければ何でも受け付ける。
// /api/v1/version だけは認証無しで返す（二重起動の検知に使う）。

// 1ページの件数の既定と上限（Gitea と同じ）。
const (
	apiDefaultLimit = 30
	apiMaxLimit     = 50
)

// apiBase は html_url などに使うこの端末の URL。
func (s *Server) apiBase() string {
	return fmt.Sprintf("http://localhost:%d", s.port)
}

func (s *Server) apiURL(parts ...string) string {
	return s.apiBase() + "/api/v1/" + strings.Join(parts, "/")
}

// writeJSON は JSON を返す。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// apiError は Gitea と同じ {"message": ..., "url": ...} の形でエラーを返す。
func (s *Server) apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg, "url": "https://docs.gitea.com/api/"})
}

// apiFail は ops のエラーを HTTP の状態コードと文言にして返す。
func (s *Server) apiFail(w http.ResponseWriter, err error) {
	status := opStatus(err)
	if errors.Is(err, store.ErrIssueNotFound) || errors.Is(err, store.ErrRepoNotFound) || errors.Is(err, store.ErrLabelNotFound) || errors.Is(err, gitcmd.ErrNotFound) {
		status = http.StatusNotFound
	}
	s.apiError(w, status, s.opMessage(err))
}

// errNotJSON は JSON を受ける経路に application/json 以外のボディが来たときのエラー。
var errNotJSON = errors.New("Content-Type must be application/json")

// readJSON はリクエストの JSON を読む。ボディがあるのに Content-Type が application/json で
// なければ errNotJSON を返す（他サイトのフォームは text/plain や multipart でしか送れないため、
// これが CSRF の防ぎにもなる）。
func readJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return errNotJSON
	}
	return json.Unmarshal(data, v)
}

// apiReadJSON は readJSON を呼び、失敗したら応答を書いて false を返す。
// Content-Type の違いは 415、JSON の壊れは 422。
func (s *Server) apiReadJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	err := readJSON(r, v)
	switch {
	case err == nil:
		return true
	case errors.Is(err, errNotJSON):
		s.apiError(w, http.StatusUnsupportedMediaType, err.Error())
	default:
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
	}
	return false
}

// pageParams は page と limit を読む。
func pageParams(r *http.Request) (page, limit int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = apiDefaultLimit
	}
	if limit > apiMaxLimit {
		limit = apiMaxLimit
	}
	return page, limit
}

// paginate は一覧を切り出し、X-Total-Count を付ける。
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	page, limit := pageParams(r)
	w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))
	start := (page - 1) * limit
	if start >= len(items) {
		return []T{}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// stableID は文字列から安定した数値 ID を作る。Gitea の数値 ID の代わり。
func stableID(parts ...string) int64 {
	h := fnv.New32a()
	h.Write([]byte(strings.Join(parts, "\x00")))
	return int64(h.Sum32())
}

// ---- Gitea の JSON の型 ----

type apiUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	LoginName string `json:"login_name"`
	FullName  string `json:"full_name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	Active    bool   `json:"active"`
	Created   string `json:"created"`
}

type apiRepoOwner struct {
	ID       int64  `json:"id"`
	Login    string `json:"login"`
	FullName string `json:"full_name"`
	Username string `json:"username"`
}

type apiRepository struct {
	ID            int64        `json:"id"`
	Owner         apiRepoOwner `json:"owner"`
	Name          string       `json:"name"`
	FullName      string       `json:"full_name"`
	Description   string       `json:"description"`
	Empty         bool         `json:"empty"`
	Private       bool         `json:"private"`
	Fork          bool         `json:"fork"`
	Mirror        bool         `json:"mirror"`
	HTMLURL       string       `json:"html_url"`
	URL           string       `json:"url"`
	CloneURL      string       `json:"clone_url"`
	SSHURL        string       `json:"ssh_url"`
	DefaultBranch string       `json:"default_branch"`
	Archived      bool         `json:"archived"`
	HasIssues     bool         `json:"has_issues"`
	HasPulls      bool         `json:"has_pull_requests"`
	Permissions   apiPerms     `json:"permissions"`
	CreatedAt     string       `json:"created_at"`
	UpdatedAt     string       `json:"updated_at"`
}

type apiPerms struct {
	Admin bool `json:"admin"`
	Push  bool `json:"push"`
	Pull  bool `json:"pull"`
}

type apiLabel struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Exclusive   bool   `json:"exclusive"`
	URL         string `json:"url"`
}

type apiRepoRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Owner    string `json:"owner"`
	FullName string `json:"full_name"`
}

type apiPullRef struct {
	Merged   bool    `json:"merged"`
	MergedAt *string `json:"merged_at"`
	HTMLURL  string  `json:"html_url"`
	DiffURL  string  `json:"diff_url"`
	PatchURL string  `json:"patch_url"`
}

type apiIssue struct {
	ID        int64       `json:"id"`
	URL       string      `json:"url"`
	HTMLURL   string      `json:"html_url"`
	Number    int         `json:"number"`
	User      apiUser     `json:"user"`
	Title     string      `json:"title"`
	Body      string      `json:"body"`
	Ref       string      `json:"ref"`
	Labels    []apiLabel  `json:"labels"`
	Milestone *struct{}   `json:"milestone"`
	Assignee  *apiUser    `json:"assignee"`
	Assignees []apiUser   `json:"assignees"`
	State     string      `json:"state"`
	IsLocked  bool        `json:"is_locked"`
	Comments  int         `json:"comments"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	ClosedAt  *string     `json:"closed_at"`
	DueDate   *string     `json:"due_date"`
	Pull      *apiPullRef `json:"pull_request"`
	Repo      apiRepoRef  `json:"repository"`
}

type apiComment struct {
	ID        int64           `json:"id"`
	HTMLURL   string          `json:"html_url"`
	PullURL   string          `json:"pull_request_url"`
	IssueURL  string          `json:"issue_url"`
	User      apiUser         `json:"user"`
	Body      string          `json:"body"`
	Assets    []apiAttachment `json:"assets"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

type apiAttachment struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Downloads   int    `json:"download_count"`
	CreatedAt   string `json:"created_at"`
	UUID        string `json:"uuid"`
	DownloadURL string `json:"browser_download_url"`
}

type apiCommitUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type apiCommitMeta struct {
	URL       string        `json:"url"`
	Author    apiCommitUser `json:"author"`
	Committer apiCommitUser `json:"committer"`
	Message   string        `json:"message"`
}

type apiCommit struct {
	URL     string          `json:"url"`
	SHA     string          `json:"sha"`
	HTMLURL string          `json:"html_url"`
	Commit  apiCommitMeta   `json:"commit"`
	Parents []apiCommitLink `json:"parents"`
}

type apiCommitLink struct {
	URL string `json:"url"`
	SHA string `json:"sha"`
}

type apiBranch struct {
	Name      string          `json:"name"`
	Commit    apiBranchCommit `json:"commit"`
	Protected bool            `json:"protected"`
}

type apiBranchCommit struct {
	ID        string        `json:"id"`
	Message   string        `json:"message"`
	URL       string        `json:"url"`
	Author    apiCommitUser `json:"author"`
	Committer apiCommitUser `json:"committer"`
	Timestamp string        `json:"timestamp"`
}

type apiPRBranch struct {
	Label  string        `json:"label"`
	Ref    string        `json:"ref"`
	SHA    string        `json:"sha"`
	RepoID int64         `json:"repo_id"`
	Repo   apiRepository `json:"repo"`
}

type apiPull struct {
	ID             int64       `json:"id"`
	URL            string      `json:"url"`
	Number         int         `json:"number"`
	User           apiUser     `json:"user"`
	Title          string      `json:"title"`
	Body           string      `json:"body"`
	Labels         []apiLabel  `json:"labels"`
	Milestone      *struct{}   `json:"milestone"`
	Assignee       *apiUser    `json:"assignee"`
	Assignees      []apiUser   `json:"assignees"`
	State          string      `json:"state"`
	IsLocked       bool        `json:"is_locked"`
	Comments       int         `json:"comments"`
	HTMLURL        string      `json:"html_url"`
	DiffURL        string      `json:"diff_url"`
	PatchURL       string      `json:"patch_url"`
	Mergeable      bool        `json:"mergeable"`
	Merged         bool        `json:"merged"`
	MergedAt       *string     `json:"merged_at"`
	MergeCommitSHA *string     `json:"merge_commit_sha"`
	MergedBy       *apiUser    `json:"merged_by"`
	Base           apiPRBranch `json:"base"`
	Head           apiPRBranch `json:"head"`
	MergeBase      string      `json:"merge_base"`
	DueDate        *string     `json:"due_date"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
	ClosedAt       *string     `json:"closed_at"`
}

type apiReview struct {
	ID   int64   `json:"id"`
	User apiUser `json:"user"`
	Team *struct {
	} `json:"team"`
	State         string `json:"state"`
	Body          string `json:"body"`
	CommitID      string `json:"commit_id"`
	Stale         bool   `json:"stale"`
	Official      bool   `json:"official"`
	Dismissed     bool   `json:"dismissed"`
	PullURL       string `json:"pull_request_url"`
	CommentsCount int    `json:"comments_count"`
	SubmittedAt   string `json:"submitted_at"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	HTMLURL       string `json:"html_url"`
}

type apiReviewComment struct {
	ID         int64   `json:"id"`
	Body       string  `json:"body"`
	Path       string  `json:"path"`
	CommitID   string  `json:"commit_id"`
	OrigCommit string  `json:"original_commit_id"`
	DiffHunk   string  `json:"diff_hunk"`
	Position   int     `json:"position"`
	OrigPos    int     `json:"original_position"`
	User       apiUser `json:"user"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	HTMLURL    string  `json:"html_url"`
	PullURL    string  `json:"pull_request_url"`
	Side       string  `json:"side"` // 本ツール独自。new か old
	Line       int     `json:"line"` // 本ツール独自。行番号
	ReviewID   int64   `json:"review_id"`
}

type apiChangedFile struct {
	Filename    string `json:"filename"`
	PrevName    string `json:"previous_filename,omitempty"`
	Status      string `json:"status"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	Changes     int    `json:"changes"`
	HTMLURL     string `json:"html_url"`
	ContentsURL string `json:"contents_url"`
	RawURL      string `json:"raw_url"`
}

// ---- 変換 ----

func apiTime(t time.Time) string {
	if t.IsZero() {
		return time.Time{}.UTC().Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339)
}

func apiTimePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := apiTime(t)
	return &s
}

func (s *Server) toUser(login string) apiUser {
	name := s.displayName(login)
	return apiUser{
		ID: stableID("user", login), Login: login, LoginName: login, FullName: name, Username: login,
		Email: login + "@github-ppoi.invalid", HTMLURL: s.apiBase() + "/issues?type=created", Active: true,
		Created: apiTime(time.Time{}),
	}
}

func (s *Server) toRepo(st *store.Store, repo store.Repo) apiRepository {
	g := s.gitRepo(st, repo)
	branches, _ := g.Branches()
	owner := st.Config().Owner
	return apiRepository{
		ID:    stableID("repo", owner, repo.Name),
		Owner: apiRepoOwner{ID: stableID("owner", owner), Login: owner, FullName: st.Config().DisplayOwner(), Username: owner},
		Name:  repo.Name, FullName: owner + "/" + repo.Name, Description: repo.Description,
		Empty:    len(branches) == 0,
		HTMLURL:  s.apiBase() + "/" + owner + "/" + repo.Name,
		URL:      s.apiURL("repos", owner, repo.Name),
		CloneURL: g.Dir, DefaultBranch: s.defaultBranch(g, repo),
		HasIssues: true, HasPulls: true,
		Permissions: apiPerms{Admin: true, Push: true, Pull: true},
		CreatedAt:   apiTime(time.Time{}), UpdatedAt: apiTime(time.Time{}),
	}
}

func (s *Server) toLabel(st *store.Store, repo store.Repo, l store.Label) apiLabel {
	return apiLabel{ID: int64(l.ID), Name: l.Name, Color: l.Color, Description: l.Description, URL: s.apiURL("repos", st.Config().Owner, repo.Name, "labels", strconv.Itoa(l.ID))}
}

func (s *Server) toLabels(st *store.Store, repo store.Repo, ids []int) []apiLabel {
	all, _ := st.ListLabels(repo.Name)
	out := []apiLabel{}
	for _, l := range resolveLabels(ids, labelsByID(all)) {
		out = append(out, s.toLabel(st, repo, l))
	}
	return out
}

func (s *Server) toUsers(logins []string) []apiUser {
	out := []apiUser{}
	for _, l := range logins {
		out = append(out, s.toUser(l))
	}
	return out
}

func (s *Server) toIssue(st *store.Store, repo store.Repo, is store.Issue) apiIssue {
	owner := st.Config().Owner
	kind := "issues"
	if is.IsPull {
		kind = "pulls"
	}
	out := apiIssue{
		ID: int64(is.Number), Number: is.Number,
		URL:     s.apiURL("repos", owner, repo.Name, "issues", strconv.Itoa(is.Number)),
		HTMLURL: fmt.Sprintf("%s/%s/%s/%s/%d", s.apiBase(), owner, repo.Name, kind, is.Number),
		User:    s.toUser(is.User), Title: is.Title, Body: is.Body,
		Labels: s.toLabels(st, repo, is.Labels), Assignees: s.toUsers(is.Assignees),
		State: is.State, Comments: is.Comments,
		CreatedAt: apiTime(is.CreatedAt), UpdatedAt: apiTime(is.UpdatedAt), ClosedAt: apiTimePtr(is.ClosedAt),
		Repo: apiRepoRef{ID: stableID("repo", owner, repo.Name), Name: repo.Name, Owner: owner, FullName: owner + "/" + repo.Name},
	}
	if len(out.Assignees) > 0 {
		a := out.Assignees[0]
		out.Assignee = &a
	}
	if is.IsPull {
		out.Pull = &apiPullRef{Merged: is.Merged, MergedAt: apiTimePtr(is.MergedAt), HTMLURL: out.HTMLURL, DiffURL: out.HTMLURL + ".diff", PatchURL: out.HTMLURL + ".patch"}
	}
	return out
}

func (s *Server) toCommit(st *store.Store, repo store.Repo, c gitcmd.Commit) apiCommit {
	owner := st.Config().Owner
	msg := c.Subject
	if c.Body != "" {
		msg += "\n\n" + c.Body
	}
	au := apiCommitUser{Name: c.Author, Email: c.Email, Date: apiTime(c.Time)}
	out := apiCommit{
		URL: s.apiURL("repos", owner, repo.Name, "git", "commits", c.SHA), SHA: c.SHA,
		HTMLURL: fmt.Sprintf("%s/%s/%s/commit/%s", s.apiBase(), owner, repo.Name, c.SHA),
		Commit:  apiCommitMeta{URL: s.apiURL("repos", owner, repo.Name, "git", "commits", c.SHA), Author: au, Committer: au, Message: msg},
		Parents: []apiCommitLink{},
	}
	for _, p := range c.Parents {
		out.Parents = append(out.Parents, apiCommitLink{URL: s.apiURL("repos", owner, repo.Name, "git", "commits", p), SHA: p})
	}
	return out
}

func (s *Server) toChangedFiles(st *store.Store, repo store.Repo, ref string, files []gitcmd.DiffFile) []apiChangedFile {
	owner := st.Config().Owner
	out := []apiChangedFile{}
	for _, f := range files {
		name := f.NewPath
		if name == "" {
			name = f.OldPath
		}
		cf := apiChangedFile{Filename: name, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Changes: f.Additions + f.Deletions}
		if f.Status == "renamed" {
			cf.PrevName = f.OldPath
		}
		if f.Status != "deleted" {
			cf.HTMLURL = fmt.Sprintf("%s/%s/%s/blob/%s/%s", s.apiBase(), owner, repo.Name, url.PathEscape(ref), escapePath(name))
			cf.ContentsURL = s.apiURL("repos", owner, repo.Name, "contents", escapePath(name)) + "?ref=" + url.QueryEscape(ref)
			cf.RawURL = fmt.Sprintf("%s/%s/%s/raw/%s/%s", s.apiBase(), owner, repo.Name, url.PathEscape(ref), escapePath(name))
		}
		out = append(out, cf)
	}
	return out
}

// ---- ルーティング ----

// apiAuth はトークンの有無を確かめる。空でなければ何でも通す。
// 受けるのは Authorization ヘッダだけで、URL のクエリ（token= / access_token=）は見ない。
// クエリで受けると他サイトのフォームからでも認証が通ってしまうため。
func (s *Server) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		token := ""
		for _, prefix := range []string{"token ", "Token ", "Bearer ", "bearer "} {
			if strings.HasPrefix(auth, prefix) {
				token = strings.TrimSpace(strings.TrimPrefix(auth, prefix))
			}
		}
		if token == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="github-ppoi"`)
			s.apiError(w, http.StatusUnauthorized, "token required")
			return
		}
		next(w, r)
	}
}

// commentPathPattern は /issues/comments/{id} の形。ServeMux のパターンにすると
// /issues/{index}/... と衝突するので、Handler で先に振り分ける。
var commentPathPattern = regexp.MustCompile(`^/api/v1/repos/([^/]+)/([^/]+)/issues/comments/(\d+)$`)

// handleAPIComment は Gitea の /issues/comments/{id}（GET、PATCH、DELETE）を受ける。
func (s *Server) handleAPIComment(w http.ResponseWriter, r *http.Request, m []string) {
	s.apiAuth(func(w http.ResponseWriter, r *http.Request) {
		st := s.storeByOwner(m[1])
		if st == nil {
			s.apiError(w, http.StatusNotFound, "owner not found")
			return
		}
		repo, err := st.GetRepo(m[2])
		if err != nil {
			s.apiError(w, http.StatusNotFound, "repository not found")
			return
		}
		limitRequestBody(w, r, st)
		id, _ := strconv.ParseInt(m[3], 10, 64)
		switch r.Method {
		case http.MethodGet:
			s.apiGetComment(w, r, st, repo, id)
		case http.MethodPatch:
			s.apiEditComment(w, r, st, repo, id)
		case http.MethodDelete:
			s.apiDeleteComment(w, r, st, repo, id)
		default:
			s.apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})(w, r)
}

// apiRepoHandler は owner と repo を解決してから呼ぶハンドラ。
type apiRepoHandler func(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo)

func (s *Server) apiRepo(h apiRepoHandler) http.HandlerFunc {
	return s.apiAuth(func(w http.ResponseWriter, r *http.Request) {
		st := s.storeByOwner(r.PathValue("owner"))
		if st == nil {
			s.apiError(w, http.StatusNotFound, "owner not found")
			return
		}
		repo, err := st.GetRepo(r.PathValue("repo"))
		if err != nil {
			s.apiError(w, http.StatusNotFound, "repository not found")
			return
		}
		limitRequestBody(w, r, st)
		h(w, r, st, repo)
	})
}

// apiIssueHandler は owner、repo、index を解決してから呼ぶハンドラ。
type apiIssueHandler func(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue)

func (s *Server) apiIssue(mustPull bool, h apiIssueHandler) http.HandlerFunc {
	return s.apiRepo(func(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
		// /pulls/{index}.diff と .patch は番号の後ろに拡張子が付く。ServeMux のパターンでは書けないのでここで外す
		raw := strings.TrimSuffix(strings.TrimSuffix(r.PathValue("index"), ".diff"), ".patch")
		index, err := strconv.Atoi(raw)
		if err != nil {
			s.apiError(w, http.StatusNotFound, "issue not found")
			return
		}
		is, err := st.GetIssue(repo.Name, index)
		if err != nil {
			s.apiError(w, http.StatusNotFound, "issue not found")
			return
		}
		if mustPull && !is.IsPull {
			s.apiError(w, http.StatusNotFound, "pull request not found")
			return
		}
		h(w, r, st, repo, is)
	})
}

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/version", s.handleAPIVersion)
	mux.HandleFunc("GET /api/v1/user", s.apiAuth(s.apiCurrentUser))
	mux.HandleFunc("GET /api/v1/users/{username}", s.apiAuth(s.apiGetUser))
	mux.HandleFunc("GET /api/v1/user/repos", s.apiAuth(s.apiUserRepos))
	mux.HandleFunc("GET /api/v1/repos/search", s.apiAuth(s.apiSearchRepos))
	mux.HandleFunc("GET /api/v1/orgs/{org}/repos", s.apiAuth(s.apiOrgRepos))
	mux.HandleFunc("GET /api/v1/orgs/{org}", s.apiAuth(s.apiGetOrg))
	mux.HandleFunc("GET /api/v1/user/orgs", s.apiAuth(s.apiUserOrgs))
	// tea などのクライアントが必ず呼ぶが、本ツールには無い機能。空で返す
	mux.HandleFunc("GET /api/v1/orgs/{org}/labels", s.apiAuth(s.apiEmptyList))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/branch_protections", s.apiRepo(s.apiEmptyListRepo))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/milestones", s.apiRepo(s.apiEmptyListRepo))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/commits/{ref}/statuses", s.apiRepo(s.apiEmptyListRepo))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/commits/{ref}/status", s.apiRepo(s.apiEmptyStatus))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/reactions", s.apiIssue(false, s.apiEmptyListIssue))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/timeline", s.apiIssue(false, s.apiEmptyListIssue))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/times", s.apiIssue(false, s.apiEmptyListIssue))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews/{id}/comments/{cid}", s.apiIssue(true, s.apiEmptyListIssue))

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}", s.apiRepo(s.apiGetRepo))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/branches", s.apiRepo(s.apiBranches))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/branches/{branch...}", s.apiRepo(s.apiGetBranch))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/commits", s.apiRepo(s.apiCommits))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/git/commits/{sha}", s.apiRepo(s.apiGetCommit))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/contents", s.apiRepo(s.apiContents))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/contents/{path...}", s.apiRepo(s.apiContents))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/raw/{path...}", s.apiRepo(s.apiRaw))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/compare/{basehead...}", s.apiRepo(s.apiCompare))

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/labels", s.apiRepo(s.apiLabels))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/labels", s.apiRepo(s.apiCreateLabel))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/labels/{id}", s.apiRepo(s.apiGetLabel))
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/labels/{id}", s.apiRepo(s.apiEditLabel))
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/labels/{id}", s.apiRepo(s.apiDeleteLabel))

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues", s.apiRepo(s.apiIssues))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues", s.apiRepo(s.apiCreateIssue))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}", s.apiIssue(false, s.apiGetIssue))
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/issues/{index}", s.apiIssue(false, s.apiEditIssue))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/comments", s.apiIssue(false, s.apiComments))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{index}/comments", s.apiIssue(false, s.apiCreateComment))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/labels", s.apiIssue(false, s.apiIssueLabels))
	mux.HandleFunc("PUT /api/v1/repos/{owner}/{repo}/issues/{index}/labels", s.apiIssue(false, s.apiIssueReplaceLabels))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{index}/labels", s.apiIssue(false, s.apiIssueAddLabels))
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/issues/{index}/labels", s.apiIssue(false, s.apiIssueClearLabels))
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/issues/{index}/labels/{id}", s.apiIssue(false, s.apiIssueRemoveLabel))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/assets", s.apiIssue(false, s.apiAssets))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/issues/{index}/assets", s.apiIssue(false, s.apiCreateAsset))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/issues/{index}/assets/{id}", s.apiIssue(false, s.apiGetAsset))

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/releases", s.apiRepo(s.apiReleases))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/releases", s.apiRepo(s.apiCreateRelease))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/releases/latest", s.apiRepo(s.apiLatestRelease))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/releases/{id}", s.apiRepo(s.apiGetRelease))
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/releases/{id}", s.apiRepo(s.apiEditRelease))
	mux.HandleFunc("DELETE /api/v1/repos/{owner}/{repo}/releases/{id}", s.apiRepo(s.apiDeleteRelease))
	// releases/tags/{tag} と releases/{id}/assets は ServeMux のパターンが衝突するので、1つの入口で振り分ける
	mux.HandleFunc("/api/v1/repos/{owner}/{repo}/releases/{id}/{rest...}", s.apiRepo(s.apiReleaseSub))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/tags", s.apiRepo(s.apiTags))

	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls", s.apiRepo(s.apiPulls))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls", s.apiRepo(s.apiCreatePull))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}", s.apiIssue(true, s.apiGetPull))
	mux.HandleFunc("PATCH /api/v1/repos/{owner}/{repo}/pulls/{index}", s.apiIssue(true, s.apiEditPull))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/commits", s.apiIssue(true, s.apiPullCommits))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/files", s.apiIssue(true, s.apiPullFiles))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/merge", s.apiIssue(true, s.apiPullIsMerged))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{index}/merge", s.apiIssue(true, s.apiMergePull))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews", s.apiIssue(true, s.apiReviews))
	mux.HandleFunc("POST /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews", s.apiIssue(true, s.apiCreateReview))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews/{id}", s.apiIssue(true, s.apiGetReview))
	mux.HandleFunc("GET /api/v1/repos/{owner}/{repo}/pulls/{index}/reviews/{id}/comments", s.apiIssue(true, s.apiReviewComments))

	mux.HandleFunc("/api/v1/", s.apiAuth(func(w http.ResponseWriter, r *http.Request) {
		s.apiError(w, http.StatusNotFound, "not implemented: "+r.Method+" "+r.URL.Path)
	}))
}

// handleAPIVersion は Gitea 互換の /api/v1/version。
// version には Gitea の版を名乗る値を返し、本ツールの版は github_ppoi_version に返す。
// 二重起動の検知にもこの応答を使う。
func (s *Server) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":             giteaVersion,
		"github_ppoi_version": version.Number,
	})
}
