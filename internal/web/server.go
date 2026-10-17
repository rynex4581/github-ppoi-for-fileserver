// Package web はWeb画面を提供する。
// HTMLはサーバ側で html/template で組み、SPAにはしない。
// CSSはPrimer、アイコンはOcticonsをexeに埋め込み、外部への参照は持たない。
package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
	"github-ppoi-for-fileserver/internal/version"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server はWeb画面の状態を持つ。
type Server struct {
	mu      sync.RWMutex
	cfg     localconfig.Config
	st      *store.Store // データルート未設定、または開けなかったときは nil
	openErr error        // データルートを開けなかったときの理由

	login string
	pages map[string]*template.Template
	icons map[string]template.HTML
	md    goldmark.Markdown
	mux   *http.ServeMux
}

// New はサーバを作る。データルートが設定済みなら開く。開けなくても起動はし、設定画面で知らせる。
func New(cfg localconfig.Config, login string) (*Server, error) {
	s := &Server{cfg: cfg, login: login}
	s.md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(goldmarkhtml.WithHardWraps()),
	)
	if err := s.loadIcons(); err != nil {
		return nil, err
	}
	if err := s.loadTemplates(); err != nil {
		return nil, err
	}
	if cfg.DataRoot != "" {
		s.st, s.openErr = store.Open(cfg.DataRoot)
		if s.openErr != nil {
			log.Printf("データルートを開けない: %v", s.openErr)
		}
	}
	s.routes()
	return s, nil
}

// Handler はHTTPハンドラを返す。
// /static/ 配下は埋め込みファイルをそのまま返し、それ以外を画面のルーティングに回す。
// GitHubと同じ /{owner}/{repo}/ 形のURLと /static/ をServeMuxに同居させると
// パターンが衝突するため、ここで先に分ける。
func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(staticFS, "static")
	staticHandler := http.StripPrefix("/static/", http.FileServerFS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			staticHandler.ServeHTTP(w, r)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) store() *store.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st
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

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"icon": func(name string) template.HTML {
			if svg, ok := s.icons[name]; ok {
				return svg
			}
			return template.HTML("")
		},
		"markdown": s.renderMarkdown,
		"datetime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"name": func(login string) string {
			if st := s.store(); st != nil {
				return st.DisplayName(login)
			}
			return login
		},
	}
}

func (s *Server) loadTemplates() error {
	s.pages = map[string]*template.Template{}
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name == "layout.html" {
			continue
		}
		t, err := template.New("layout.html").Funcs(s.funcs()).ParseFS(templateFS, "templates/layout.html", "templates/"+name)
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
	DataRoot  string
	Owner     string
	Repo      *store.Repo
	Tab       string
	Error     string
	Data      any
}

func (s *Server) newPage(title string) page {
	p := page{Title: title, Login: s.login, LoginName: s.login, Version: version.Display}
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

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /new", s.handleRepoNew)
	mux.HandleFunc("POST /new", s.handleRepoCreate)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)

	mux.HandleFunc("GET /{owner}/{repo}", s.withRepo(s.handleRepoHome))
	mux.HandleFunc("GET /{owner}/{repo}/issues", s.withRepo(s.handleIssues))
	mux.HandleFunc("GET /{owner}/{repo}/issues/new", s.withRepo(s.handleIssueNew))
	mux.HandleFunc("POST /{owner}/{repo}/issues/new", s.withRepo(s.handleIssueCreate))
	mux.HandleFunc("GET /{owner}/{repo}/issues/{number}", s.withRepo(s.handleIssue))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/comments", s.withRepo(s.handleIssueComment))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, http.StatusNotFound, "ページが無い")
	})
	s.mux = mux
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
			s.renderError(w, http.StatusNotFound, "リポジトリが無い")
			return
		}
		repo, err := st.GetRepo(r.PathValue("repo"))
		if errors.Is(err, store.ErrRepoNotFound) {
			s.renderError(w, http.StatusNotFound, "リポジトリが無い")
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
