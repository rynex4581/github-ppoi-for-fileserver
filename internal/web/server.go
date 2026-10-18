// Package web はWeb画面を提供する。
// HTMLはサーバ側で html/template で組み、SPAにはしない。
// CSSはPrimer、アイコンはOcticonsをexeに埋め込み、外部への参照は持たない。
// 文言は Gitea の言語ファイルのキーで引く（internal/i18n）。
package web

import (
	"regexp"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"path/filepath"
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
	mu       sync.RWMutex
	cfg      localconfig.Config
	stores   []*store.Store    // 開けたデータルート。1つが owner 1つに対応する
	rootErrs map[string]string // 開けなかったデータルートのパス → 理由
	loc      *i18n.Locale

	login  string
	port   int
	locks  repoLocks     // マージと Revert がツール専用 clone を取り合わないための排他
	newer  *newerVersion // bin\github-ppoi.exe の版が自分より新しいときに入る
	pages  map[string]*template.Template
	icons  map[string]template.HTML
	md     goldmark.Markdown
	mux    *http.ServeMux
	apiMux *http.ServeMux // /api/v1/ 配下。画面の /{owner}/{repo}/... と衝突するので別にする
}

// New はサーバを作る。登録されているデータルートをすべて開く。
// 開けないものがあっても起動はし、設定画面で知らせる。同じ owner 名が2つあればエラーで止める。
func New(cfg localconfig.Config, login string, port int) (*Server, error) {
	s := &Server{cfg: cfg, login: login, port: port, rootErrs: map[string]string{}}
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
	owners := map[string]string{}
	for _, root := range cfg.DataRoots {
		st, err := s.openStore(root)
		if err != nil {
			log.Printf("データルートを開けない: %v", err)
			s.rootErrs[root] = err.Error()
			continue
		}
		owner := st.Config().Owner
		if other, dup := owners[strings.ToLower(owner)]; dup {
			return nil, fmt.Errorf("owner 名 %q のデータルートが2つ登録されている: %s と %s。端末側の config.json を直す", owner, other, root)
		}
		owners[strings.ToLower(owner)] = root
		s.stores = append(s.stores, st)
	}
	s.newer = s.findNewerVersion()
	s.routes()
	return s, nil
}

// newerVersion は配布用 exe の版が自分より新しいときの情報。
type newerVersion struct {
	Version string // "0.11A" の形
	Path    string // その exe のパス
}

// findNewerVersion は各データルートの bin\github-ppoi.exe の VERSIONINFO を読み、
// 自分より新しいものがあれば一番新しいものを返す。読めないものは無視する。
func (s *Server) findNewerVersion() *newerVersion {
	var best *newerVersion
	bestParsed := version.Current
	for _, st := range s.stores {
		path := filepath.Join(st.Root(), "bin", "github-ppoi.exe")
		major, minor, patch, _, err := version.ReadFileVersion(path)
		if err != nil {
			continue
		}
		p := version.FromNumeric(major, minor, patch)
		if version.Compare(p, bestParsed) > 0 {
			bestParsed = p
			best = &newerVersion{Version: p.String(), Path: path}
		}
	}
	if best != nil {
		log.Printf("新しい版がある: Ver %s %s", best.Version, best.Path)
	}
	return best
}

// Close は終了前に呼ぶ。各データルートのキャッシュをディスクに書き出す。
func (s *Server) Close() {
	for _, st := range s.allStores() {
		st.FlushCache()
	}
}

// allStores は開けているデータルートを登録順で返す。
func (s *Server) allStores() []*store.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*store.Store(nil), s.stores...)
}

// storeByOwner は owner 名からデータルートを引く。無ければ nil。
func (s *Server) storeByOwner(owner string) *store.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, st := range s.stores {
		if strings.EqualFold(st.Config().Owner, owner) {
			return st
		}
	}
	return nil
}

// storeByRootLocked はパスからデータルートを引く。呼ぶ側でロックを取る。
func (s *Server) storeByRootLocked(root string) *store.Store {
	for _, st := range s.stores {
		if st.Root() == root {
			return st
		}
	}
	return nil
}

// displayName はログオン名の表示名を返す。全データルートを順に見て、登録が見つかった最初のものを使う。
func (s *Server) displayName(login string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.displayNameLocked(login)
}

func (s *Server) displayNameLocked(login string) string {
	for _, st := range s.stores {
		if name := st.DisplayName(login); name != login {
			return name
		}
	}
	return login
}

// openStore はデータルートを開き、自分の利用者ファイルが無ければ作る。
func (s *Server) openStore(root string) (*store.Store, error) {
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	if dir, err := localconfig.Dir(); err == nil {
		st.SetCacheDir(filepath.Join(dir, "cache", st.Config().Owner))
	}
	if err := st.EnsureUser(s.login); err != nil {
		log.Printf("利用者ファイルを作れない: %v", err)
	}
	return st, nil
}

// Handler はHTTPハンドラを返す。
// Hostヘッダが localhost または 127.0.0.1 以外のリクエストは弾き、別サイトのページからの書き込み
// （Sec-Fetch-Site が cross-site / same-site、または Origin が Host と違うもの）は
// http.CrossOriginProtection で拒否する。GET / HEAD / OPTIONS と、ブラウザ以外からの
// リクエスト（Sec-Fetch-Site も Origin も無い）は通す。画面と API の両方に掛ける。
// /static/ 配下は埋め込みファイルをそのまま返し、それ以外を画面のルーティングに回す。
// GitHubと同じ /{owner}/{repo}/ 形のURLと /static/ をServeMuxに同居させると
// パターンが衝突するため、ここで先に分ける。
func (s *Server) Handler() http.Handler {
	static, _ := fs.Sub(staticFS, "static")
	staticHandler := serveStatic(static)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			staticHandler.ServeHTTP(w, r)
			return
		}
		// 添付も /{owner}/{repo}/... の形と衝突するので、ここで分ける
		if strings.HasPrefix(r.URL.Path, "/attachments/") {
			s.handleAttachment(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if m := commentPathPattern.FindStringSubmatch(r.URL.Path); m != nil {
				s.handleAPIComment(w, r, m)
				return
			}
			s.apiMux.ServeHTTP(w, r)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
	protect := http.NewCrossOriginProtection()
	protect.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
	}))
	protected := protect.Handler(inner)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

// staticTypes は /static/ で返すファイルの Content-Type。拡張子ごとに明示し、端末のレジストリに
// 左右されないようにする（.js が text/plain になる端末があると app.js が拒まれる）。
var staticTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".map":   "application/json",
}

// serveStatic は埋め込みの静的ファイルを返す。フォルダの一覧は返さない。
func serveStatic(static fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		if name == "" || strings.HasSuffix(name, "/") || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		f, err := static.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		ct, ok := staticTypes[strings.ToLower(path.Ext(name))]
		if !ok {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if rs, ok := f.(io.ReadSeeker); ok {
			http.ServeContent(w, r, name, info.ModTime(), rs)
			return
		}
		io.Copy(w, f)
	})
}

// isLocalHost は Host ヘッダが localhost か 127.0.0.1（ポート付き可）かを返す。
func isLocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1"
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
		"tr":         s.tr,
		"trh":        s.trh,
		"markdown":   s.renderMarkdown,
		"datetime":   formatTime,
		"name":       s.displayName,
		"labelStyle": labelStyle,
		"chips":      labelChips,
		"inc":        func(i int) int { return i + 1 },
		"short":      shortSHA,
		"sameUser":   strings.EqualFold,
		"lineKey":    lineKey,
	}
}

// shortSHA はコミットの SHA を7文字に縮める。短ければそのまま返す（slice と違って落ちない）。
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// labelStyle はラベルの背景色と、明度から決めた文字色を返す。
// labelColorPattern はラベルの色として受け付ける形（6桁の16進）。labels.json を手で編集して
// 別の文字列が入っていても、style にそのまま埋め込まない。
var labelColorPattern = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

func labelStyle(color string) template.CSS {
	if !labelColorPattern.MatchString(color) {
		color = "ededed"
	}
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
	Owner     string
	Newer     *newerVersion
	ReadOnly  []string // 読み取り専用で開いているデータルートの知らせ（表示する文のまま）
	MaxAttach int64    // 添付ファイルの上限（バイト）。リポジトリの画面で入る
	Repo      *store.Repo
	Tab       string
	Error     string
	Data      any
}

func (s *Server) newPage(title string) page {
	p := page{Title: title, Login: s.login, LoginName: s.displayName(s.login), Version: version.Display, Port: s.port, Lang: s.locale().Lang(), Newer: s.newer}
	for _, st := range s.allStores() {
		if st.ReadOnly() {
			p.ReadOnly = append(p.ReadOnly, s.tr("ppoi.readonly.banner", st.Config().DisplayOwner(), st.Config().Format, store.CurrentFormat))
		}
	}
	return p
}

// htmlCSP は画面の HTML に付ける Content-Security-Policy。スクリプトは /static/app.js だけで、
// インラインのスクリプトは使わない。style はラベルの色をインラインで付けるので許す。
// 画像は Markdown に外部の URL が書かれることがあるので制限しない。
const htmlCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: http: https:; frame-ancestors 'none'; form-action 'self'; base-uri 'self'; object-src 'none'"

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
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", htmlCSP)
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

	s.apiMux = http.NewServeMux()
	s.apiRoutes(s.apiMux)

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /new", s.handleRepoNew)
	mux.HandleFunc("POST /new", s.handleRepoCreate)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("POST /settings/roots", s.handleRootAdd)
	mux.HandleFunc("POST /settings/roots/remove", s.handleRootRemove)
	mux.HandleFunc("GET /licenses", s.handleLicenses)
	mux.HandleFunc("POST /markdown", s.handleMarkdownPreview)
	mux.HandleFunc("GET /issues", s.handleCrossIssues)
	mux.HandleFunc("GET /pulls", s.handleCrossPulls)

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
	mux.HandleFunc("GET /{owner}/{repo}/issues/{number}/edit", s.withRepo(s.handleIssueEdit))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/edit", s.withRepo(s.handleIssueEditSave))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/delete", s.withRepo(s.handleIssueDelete))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/comments", s.withRepo(s.handleIssueComment))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/labels", s.withRepo(s.handleIssueLabels))
	mux.HandleFunc("POST /{owner}/{repo}/issues/{number}/assignees", s.withRepo(s.handleIssueAssignees))
	mux.HandleFunc("GET /{owner}/{repo}/pulls", s.withRepo(s.handlePulls))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/new", s.withRepo(s.handlePullNew))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/new", s.withRepo(s.handlePullCreate))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/{number}", s.withRepo(s.pullTab("conversation")))
	mux.HandleFunc("GET /{owner}/{repo}/pulls/{number}/edit", s.withRepo(s.handleIssueEdit))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/edit", s.withRepo(s.handleIssueEditSave))
	mux.HandleFunc("POST /{owner}/{repo}/pulls/{number}/delete", s.withRepo(s.handleIssueDelete))
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
	mux.HandleFunc("POST /{owner}/{repo}/settings/maintenance", s.withRepo(s.handleRepoMaintenance))
	mux.HandleFunc("GET /{owner}/{repo}/labels", s.withRepo(s.handleLabels))
	mux.HandleFunc("POST /{owner}/{repo}/labels", s.withRepo(s.handleLabelCreate))
	mux.HandleFunc("POST /{owner}/{repo}/labels/{id}", s.withRepo(s.handleLabelUpdate))
	mux.HandleFunc("POST /{owner}/{repo}/labels/{id}/delete", s.withRepo(s.handleLabelDelete))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, http.StatusNotFound, s.tr("ppoi.error.not_found"))
	})
	s.mux = mux
}

// maxAttachmentsPerPost は1回の送信で受け付ける添付ファイルの数の目安。ボディの上限の計算に使う。
const maxAttachmentsPerPost = 10

// limitRequestBody は書き込み系のリクエストのボディに上限を掛ける。
// 添付の上限はファイルごとに別に見るが、ここで掛けておかないと上限を超えるボディを
// 最後まで読んでから弾くことになる。超えたときの読み込みは *http.MaxBytesError になる。
func limitRequestBody(w http.ResponseWriter, r *http.Request, st *store.Store) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, st.AttachmentMaxBytes()*maxAttachmentsPerPost+1<<20)
}

// requireStores はデータルートが1つ以上使える状態かを確かめ、無ければ設定画面へ送る。
func (s *Server) requireStores(w http.ResponseWriter, r *http.Request) bool {
	if len(s.allStores()) == 0 {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return false
	}
	return true
}

type repoHandler func(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo)

// withRepo はURLの owner と repo を確かめてからハンドラに渡す。
func (s *Server) withRepo(h repoHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireStores(w, r) {
			return
		}
		st := s.storeByOwner(r.PathValue("owner"))
		if st == nil {
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
		limitRequestBody(w, r, st)
		h(w, r, st, repo)
	}
}

func (s *Server) repoPage(title string, repo store.Repo, tab string) page {
	p := s.newPage(title + " · " + repo.Name)
	p.Repo = &repo
	p.Owner = repo.Owner
	p.Tab = tab
	if st := s.storeByOwner(repo.Owner); st != nil {
		p.MaxAttach = st.AttachmentMaxBytes()
	}
	return p
}

func (s *Server) repoURL(st *store.Store, repo store.Repo) string {
	return fmt.Sprintf("/%s/%s", st.Config().Owner, repo.Name)
}
