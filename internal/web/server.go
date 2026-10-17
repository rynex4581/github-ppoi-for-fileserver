// Package web はWeb画面を提供する。
// HTMLはサーバ側で html/template で組み、SPAにはしない。
// CSSはPrimer、アイコンはOcticonsをexeに埋め込み、外部への参照は持たない。
// 文言は Gitea の言語ファイルのキーで引く（internal/i18n）。
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"

	"github-ppoi-for-fileserver/internal/i18n"
	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
	"github-ppoi-for-fileserver/internal/version"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// giteaVersion は /api/v1/version で名乗る Gitea の版。言語ファイルの取り込み元と揃える。
const giteaVersion = "28.1.0"

// Server はWeb画面の状態を持つ。
type Server struct {
	mu      sync.RWMutex
	cfg     localconfig.Config
	st      *store.Store // データルート未設定、または開けなかったときは nil
	openErr error        // データルートを開けなかったときの理由
	loc     *i18n.Locale

	login string
	port  int
	pages map[string]*template.Template
	icons map[string]template.HTML
	md    goldmark.Markdown
	mux   *http.ServeMux
}

// New はサーバを作る。データルートが設定済みなら開く。開けなくても起動はし、設定画面で知らせる。
func New(cfg localconfig.Config, login string, port int) (*Server, error) {
	s := &Server{cfg: cfg, login: login, port: port}
	s.md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(goldmarkhtml.WithHardWraps()),
	)
	loc, err := i18n.Load(cfg.Lang)
	if err != nil {
		return nil, err
	}
	s.loc = loc
	if err := s.loadIcons(); err != nil {
		return nil, err
	}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	if cfg.DataRoot != "" {
		s.st, s.openErr = s.openStore(cfg.DataRoot)
		if s.openErr != nil {
			log.Printf("データルートを開けない: %v", s.openErr)
		}
	}
	s.routes()
	return s, nil
}

// openStore はデータルートを開き、自分の利用者ファイルが無ければ作る。
func (s *Server) openStore(root string) (*store.Store, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	if err := st.EnsureUser(s.login); err != nil {
		log.Printf("利用者ファイルを作れない: %v", err)
	}
	return st, nil
}

// Handler はHTTPハンドラを返す。
// Hostヘッダが localhost または 127.0.0.1 以外のリクエストは弾く（CSRF対策はひとまずこれのみ）。
// /static/ 配下は埋め込みファイルをそのまま返し、それ以外を画面のルーティングに回す。
// GitHubと同じ /{owner}/{repo}/ 形のURLと /static/ をServeMuxに同居させると
// パターンが衝突するため、ここで先に分ける。
func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(staticFS, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServerFS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/static/") {
			staticHandler.ServeHTTP(w, r)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// isLocalHost は Host ヘッダが localhost か 127.0.0.1（ポート付き可）かを返す。
func isLocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1"
}

func (s *Server) store() *store.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st
}

func (s *Server) locale() *i18n.Locale {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loc
}

func (s *Server) loadIcons() error {
	s.icons = map[string]template.HTML{}
	entries, err := fs.ReadDir(staticFS, "static/octicons")
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), "-16.svg")
		data, err := fs.ReadFile(staticFS, "static/octicons/"+e.Name())
		if err != nil {
			return err
		}
		svg := strings.Replace(string(data), "<svg ", fmt.Sprintf(`<svg class="octicon octicon-%s" aria-hidden="true" `, name), 1)
		s.icons[name] = template.HTML(svg)
	}
	return nil
}

// tr は文言を返す。引数は fmt.Sprintf で埋める。戻りは通常の文字列で、テンプレート側でエスケープされる。
func (s *Server) tr(key string, args ...any) string {
	return s.locale().Tr(key, args...)
}

// trh はHTMLを含む文言（Gitea のタイムライン用の文言など）を返す。
// 引数のうち template.HTML はそのまま、文字列はエスケープしてから埋める。数値はそのまま渡す。
func (s *Server) trh(key string, args ...any) template.HTML {
	safe := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case template.HTML:
			safe[i] = string(v)
		case string:
			safe[i] = html.EscapeString(v)
		default:
			safe[i] = a
		}
	}
	return template.HTML(s.locale().Tr(key, safe...))
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"icon": func(name string) template.HTML {
			if svg, ok := s.icons[name]; ok {
				return svg
			}
			return template.HTML("")
		},
		"tr":       s.tr,
		"trh":      s.trh,
		"markdown": s.renderMarkdown,
		"datetime": formatTime,
		"name": func(login string) string {
			if st := s.store(); st != nil {
				return st.DisplayName(login)
			}
			return login
		},
		"labelStyle": labelStyle,
		"chips":      labelChips,
		"inc":        func(i int) int { return i + 1 },
		"lineKey":    lineKey,
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// labelStyle はラベルの背景色と、明度から決めた文字色を返す。
func labelStyle(color string) template.CSS {
	fg := "#ffffff"
	if len(color) == 6 {
		var r, g, b int
		if _, err := fmt.Sscanf(color, "%02x%02x%02x", &r, &g, &b); err == nil {
			// 輝度が高ければ黒文字にする
			if (299*r+587*g+114*b)/1000 > 150 {
				fg = "#1f2328"
			}
		}
	}
	return template.CSS(fmt.Sprintf("background-color:#%s;color:%s", color, fg))
}

// labelChips はラベルのチップをHTMLで返す。タイムラインの文言に埋め込むために使う。
func labelChips(labels []store.Label) template.HTML {
	var b strings.Builder
	for i, l := range labels {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, `<span class="IssueLabel" style="%s" title="%s">%s</span>`,
			labelStyle(l.Color), html.EscapeString(l.Description), html.EscapeString(l.Name))
	}
	return template.HTML(b.String())
}

func (s *Server) loadTemplates() error {
	s.pages = map[string]*template.Template{}
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == "layout.html" || strings.HasPrefix(name, "_") {
			continue
		}
		// _ で始まるファイルは各画面から使う共通の部品
		t, err := template.New("layout.html").Funcs(s.funcs()).ParseFS(templateFS, "templates/layout.html", "templates/_*.html", "templates/"+name)
		if err != nil {
			return fmt.Errorf("テンプレート %s の読み込みに失敗: %w", name, err)
		}
		s.pages[strings.TrimSuffix(name, ".html")] = t
	}
	return nil
}

// renderMarkdown はMarkdownをHTMLにする。生のHTMLはgoldmarkの既定でエスケープされる。
func (s *Server) renderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := s.md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}

// page は全画面に共通のデータ。
type page struct {
	Title     string
	Login     string
	LoginName string
	Version   string
	Port      int
	Lang      string
	DataRoot  string
	Owner     string
	Repo      *store.Repo
	Tab       string
	Error     string
	Data      any
}

func (s *Server) newPage(title string) page {
	p := page{Title: title, Login: s.login, LoginName: s.login, Version: version.Display, Port: s.port, Lang: s.locale().Lang()}
	if st := s.store(); st != nil {
		p.LoginName = st.DisplayName(s.login)
		p.DataRoot = st.Root()
		p.Owner = st.Config().Owner
	}
	return p
}

func (s *Server) render(w http.ResponseWriter, status int, name string, p page) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "テンプレートが無い: "+name, http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", p); err != nil {
		log.Printf("テンプレート %s の描画に失敗: %v", name, err)
		http.Error(w, "画面の描画に失敗した", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (s *Server) renderError(w http.ResponseWriter, status int, msg string) {
	p := s.newPage(fmt.Sprintf("%d", status))
	p.Data = map[string]any{"Status": status, "Message": msg}
	s.render(w, status, "error", p)
}

func (s *Server) routes() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/version", s.handleAPIVersion)

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /new", s.handleRepoNew)
	mux.HandleFunc("POST /new", s.handleRepoCreate)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)

	mux.HandleFunc("GET /{owner}/{repo}", s.withRepo(s.handleCode))
	mux.HandleFunc("GET /{owner}/{repo}/tree/{rest...}", s.withRepo(s.handleCode))
	mux.HandleFunc("GET /{owner}/{repo}/blob/{rest...}", s.withRepo(s.handleBlob))
	mux.HandleFunc("GET /{owner}/{repo}/raw/{rest...}", s.withRepo(s.handleRaw))
	mux.HandleFunc("GET /{owner}/{repo}/commits/{rest...}", s.withRepo(s.handleCommits))
	mux.HandleFunc("GET /{owner}/{repo}/commit/{sha}", s.withRepo(s.handleCommit))
	mux.HandleFunc("GET /{owner}/{repo}/issues", s.withRepo(s.handleIssues))
	mux.HandleFunc("GET /{owner}/{repo}/issues/new", s.withRepo(s.handleIssueNew))
	mux.HandleFunc("POST /{owner}/{repo}/issues/new", s.withRepo(s.handleIssueCreate))
	mux.HandleFunc("GET /{owner}/{repo}/issues/{number}", s.withRepo(s.handleIssue))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/comments", s.withRepo(s.handleIssueComment))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/labels", s.withRepo(s.handleIssueLabels))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/assignees", s.withRepo(s.handleIssueAssignees))
	mux.HandleFunc("GET /{owner}/{repo}/pulls", s.withRepo(s.handlePulls))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/new", s.withRepo(s.handlePullNew))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/new", s.withRepo(s.handlePullCreate))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/{number}", s.withRepo(s.pullTab("conversation")))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/{number}/commits", s.withRepo(s.pullTab("commits")))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/{number}/files", s.withRepo(s.pullTab("files")))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/comments", s.withRepo(s.handleIssueComment))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/labels", s.withRepo(s.handleIssueLabels))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/assignees", s.withRepo(s.handleIssueAssignees))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/reviews", s.withRepo(s.handlePullReview))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/review_comments", s.withRepo(s.handlePullReviewComment))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/merge", s.withRepo(s.handlePullMerge))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/revert", s.withRepo(s.handlePullRevert))
	mux.HandleFunc("GET /{owner}/{repo}/settings", s.withRepo(s.handleRepoSettings))
	mux.HandleFunc("POST /{owner}/{repo}/settings", s.withRepo(s.handleRepoSettingsSave))
	mux.HandleFunc("GET /{owner}/{repo}/labels", s.withRepo(s.handleLabels))
	mux.HandleFunc("POST /{owner}/{repo}/labels", s.withRepo(s.handleLabelCreate))
	mux.HandleFunc("POST /{owner}/{repo}/labels/{id}", s.withRepo(s.handleLabelUpdate))
	mux.HandleFunc("POST /{owner}/{repo}/labels/{id}/delete", s.withRepo(s.handleLabelDelete))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.not_found"))
	})
	s.mux = mux
}

// handleAPIVersion は Gitea 互換の /api/v1/version。
// version には Gitea の版を名乗る値を返し、本ツールの版は github_ppoi_version に返す。
// 二重起動の検知にもこの応答を使う。
func (s *Server) handleAPIVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version":             giteaVersion,
		"github_ppoi_version": version.Number,
	})
}

// requireStore はデータルートが使える状態かを確かめ、使えなければ設定画面へ送る。
func (s *Server) requireStore(w http.ResponseWriter, r *http.Request) *store.Store {
	st := s.store()
	if st == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return nil
	}
	return st
}

type repoHandler func(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo)

// withRepo はURLの owner と repo を確かめてからハンドラに渡す。
func (s *Server) withRepo(h repoHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := s.requireStore(w, r)
		if st == nil {
			return
		}
		if r.PathValue("owner") != st.Config().Owner {
			s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.repo_not_found"))
			return
		}
		repo, err := st.GetRepo(r.PathValue("repo"))
		if errors.Is(err, store.ErrRepoNotFound) {
			s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.repo_not_found"))
			return
		}
		if err != nil {
			s.renderError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h(w, r, st, repo)
	}
}

func (s *Server) repoPage(title string, repo store.Repo, tab string) page {
	p := s.newPage(title + " · " + repo.Name)
	p.Repo = &repo
	p.Tab = tab
	return p
}

func (s *Server) repoURL(st *store.Store, repo store.Repo) string {
	return fmt.Sprintf("/%s/%s", st.Config().Owner, repo.Name)
}
