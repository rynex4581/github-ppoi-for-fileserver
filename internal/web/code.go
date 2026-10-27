package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// 画面に出すファイルの大きさの上限。超えたら Raw へのリンクだけ出す。
const maxBlobBytes = 1024 * 1024

// maxRawBytes は raw で返すファイルの上限。これより大きいものは返さない。
const maxRawBytes = 64 * 1024 * 1024

// 履歴の1ページの件数。
const commitsPerPage = 30

// gitRepo はベアリポジトリを返す。Dir は git に渡す形（UNC のデータルートなら仮想パス）で、
// clone コマンドと push 手順の表示にもそのまま使う。
func (s *Server) gitRepo(st *store.Store, repo store.Repo) gitcmd.Repo {
	return gitcmd.Repo{Dir: st.GitPath(filepath.Join(st.Root(), "repos", repo.Name+".git"))}
}

// escapePath はパスの各セグメントをURL用にエスケープする。
func escapePath(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	for i, x := range parts {
		parts[i] = url.PathEscape(x)
	}
	return strings.Join(parts, "/")
}

// cleanTreePath は URL から来たパスを正規化し、上位への参照や絶対パスを拒む。
func cleanTreePath(p string) (string, bool) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", true
	}
	c := path.Clean(p)
	if c == "." || strings.HasPrefix(c, "../") || c == ".." || strings.HasPrefix(c, "/") || strings.Contains(c, "\\") {
		return "", false
	}
	return c, true
}

// splitRefPath は "ブランチ名/パス" を分ける。ブランチ名に / が入ることがあるため、
// ブランチ一覧との最長一致で決め、どのブランチにも当たらなければ最初のセグメントを ref とする。
func splitRefPath(rest string, branches []gitcmd.Branch) (ref, p string) {
	rest = strings.Trim(rest, "/")
	best := ""
	for _, b := range branches {
		if rest == b.Name || strings.HasPrefix(rest, b.Name+"/") {
			if len(b.Name) > len(best) {
				best = b.Name
			}
		}
	}
	if best != "" {
		return best, strings.Trim(strings.TrimPrefix(rest, best), "/")
	}
	ref, p, _ = strings.Cut(rest, "/")
	return ref, p
}

type crumb struct {
	Name string
	URL  string
}

// refContext は Code 系の画面に共通の、ブランチと場所の情報。
type refContext struct {
	Base        string // /owner/repo
	Ref         string
	RefIsBranch bool
	Path        string
	Branches    []gitcmd.Branch
	BranchItems []filterItem
	Crumbs      []crumb
	CommitCount int
}

func (c refContext) treeURL(ref, p string) string {
	u := c.Base + "/tree/" + escapePath(ref)
	if p != "" {
		u += "/" + escapePath(p)
	}
	return u
}

func (c refContext) blobURL(p string) string {
	return c.Base + "/blob/" + escapePath(c.Ref) + "/" + escapePath(p)
}

func (c refContext) rawURL(p string) string {
	return c.Base + "/raw/" + escapePath(c.Ref) + "/" + escapePath(p)
}

func (c refContext) commitsURL(ref, p string) string {
	u := c.Base + "/commits/" + escapePath(ref)
	if p != "" {
		u += "/" + escapePath(p)
	}
	return u
}

// TreeHere は今のブランチとパスのツリーのURL。
func (c refContext) TreeHere() string { return c.treeURL(c.Ref, c.Path) }

// CommitsHere は今のブランチとパスの履歴のURL。
func (c refContext) CommitsHere() string { return c.commitsURL(c.Ref, c.Path) }

// RootURL はブランチのルートのURL。
func (c refContext) RootURL() string { return c.treeURL(c.Ref, "") }

// buildRefContext は rest（"ref/path"）を解釈し、ブランチ一覧と各種URLを揃える。
// kind は切替メニューのリンク先の種類（tree / commits）。
func (s *Server) buildRefContext(g gitcmd.Repo, base, defaultBranch, rest, kind string) (refContext, error) {
	branches, err := g.Branches()
	if err != nil {
		return refContext{}, err
	}
	c := refContext{Base: base, Branches: branches}
	if rest == "" {
		c.Ref = defaultBranch
	} else {
		c.Ref, c.Path = splitRefPath(rest, branches)
	}
	p, ok := cleanTreePath(c.Path)
	if !ok {
		return refContext{}, gitcmd.ErrNotFound
	}
	c.Path = p
	for _, b := range branches {
		if b.Name == c.Ref {
			c.RefIsBranch = true
		}
	}
	if !c.RefIsBranch {
		if _, err := g.ResolveCommit(c.Ref); err != nil {
			return refContext{}, gitcmd.ErrNotFound
		}
	}
	for _, b := range branches {
		u := c.treeURL(b.Name, c.Path)
		if kind == "commits" {
			u = c.commitsURL(b.Name, c.Path)
		}
		c.BranchItems = append(c.BranchItems, filterItem{Text: b.Name, URL: u, Selected: b.Name == c.Ref})
	}
	if c.Path != "" {
		parts := strings.Split(c.Path, "/")
		for i := range parts {
			c.Crumbs = append(c.Crumbs, crumb{Name: parts[i], URL: c.treeURL(c.Ref, strings.Join(parts[:i+1], "/"))})
		}
	}
	c.CommitCount, _ = g.CountCommits(c.Ref)
	return c, nil
}

// cloneCommand は手元に clone するための PowerShell 1行を返す。
// push 時の所有者チェック対策（remote.origin.receivepack）を含める。共有フォルダの深いパスで
// push 中の一時ファイルの名前が 260 文字を超えないよう、受け側に core.longpaths も付ける。
func cloneCommand(g gitcmd.Repo, name string) string {
	return fmt.Sprintf(`git clone "%s"; git -C "%s" config remote.origin.receivepack "git -c safe.directory=* -c core.longpaths=true receive-pack"`, g.Dir, name)
}

// pushExistingCommands は既存のリポジトリを履歴ごと移す手順を返す。
func pushExistingCommands(g gitcmd.Repo) string {
	return fmt.Sprintf("git remote add origin \"%s\"\ngit config remote.origin.receivepack \"git -c safe.directory=* -c core.longpaths=true receive-pack\"\ngit push --mirror origin", g.Dir)
}

// ---- ツリー ----

type entryRow struct {
	gitcmd.Entry
	URL string
}

type codeView struct {
	refContext
	Entries   []entryRow
	ParentURL string
	Latest    *gitcmd.Commit
	LatestURL string
	Empty     bool
	BarePath  string
	CloneCmd  string
	PushCmds  string

	TagCount     int
	ReleaseCount int
	TagsURL      string
	ReleasesURL  string
}

func (s *Server) renderCodeError(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, gitcmd.ErrNotFound) {
		s.renderError(w, http.StatusNotFound, msg)
		return
	}
	s.renderError(w, http.StatusInternalServerError, err.Error())
}

func (s *Server) handleCode(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	base := s.repoURL(st, repo)
	rest := r.PathValue("rest")

	branches, err := g.Branches()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(branches) == 0 {
		// コミットが1つも無い
		v := codeView{refContext: refContext{Base: base}, Empty: true, BarePath: g.Dir, CloneCmd: cloneCommand(g, repo.Name), PushCmds: pushExistingCommands(g)}
		s.fillTagCounts(st, repo, &v)
		p := s.repoPage(s.tr("repo.code"), repo, "code")
		p.Data = v
		s.render(w, http.StatusOK, "code", p)
		return
	}

	c, err := s.buildRefContext(g, base, s.defaultBranch(g, repo), rest, "tree")
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.ref_not_found"))
		return
	}
	if c.Path != "" {
		typ, err := g.ObjectType(c.Ref, c.Path)
		if err != nil {
			s.renderCodeError(w, err, s.tr("ppoi.code.path_not_found"))
			return
		}
		if typ == "blob" {
			http.Redirect(w, r, c.blobURL(c.Path), http.StatusSeeOther)
			return
		}
	}
	entries, err := g.Tree(c.Ref, c.Path)
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.path_not_found"))
		return
	}
	v := codeView{refContext: c, CloneCmd: cloneCommand(g, repo.Name), BarePath: g.Dir}
	for _, e := range entries {
		u := c.blobURL(e.Path)
		if e.IsDir() {
			u = c.treeURL(c.Ref, e.Path)
		}
		v.Entries = append(v.Entries, entryRow{Entry: e, URL: u})
	}
	if c.Path != "" {
		v.ParentURL = c.treeURL(c.Ref, path.Dir(c.Path))
		if path.Dir(c.Path) == "." {
			v.ParentURL = c.treeURL(c.Ref, "")
		}
	}
	if latest, err := g.Log(c.Ref, c.Path, 0, 1); err == nil && len(latest) > 0 {
		v.Latest = &latest[0]
		v.LatestURL = base + "/commit/" + latest[0].SHA
	}
	title := s.tr("repo.code")
	if c.Path != "" {
		title = c.Path
	}
	s.fillTagCounts(st, repo, &v)
	p := s.repoPage(title, repo, "code")
	p.Data = v
	s.render(w, http.StatusOK, "code", p)
}

// fillTagCounts は Code 画面の見出しに出すタグ数とリリース数、それぞれの URL を入れる。
func (s *Server) fillTagCounts(st *store.Store, repo store.Repo, v *codeView) {
	if tags, err := s.gitRepo(st, repo).Tags(); err == nil {
		v.TagCount = len(tags)
	}
	if rels, err := st.ListReleases(repo.Name); err == nil {
		v.ReleaseCount = len(rels)
	}
	v.TagsURL = s.repoURL(st, repo) + "/tags"
	v.ReleasesURL = s.releasesURL(st, repo)
}

// defaultBranch は repo.json の既定ブランチが実在すればそれを、無ければベアリポジトリの HEAD を使う。
func (s *Server) defaultBranch(g gitcmd.Repo, repo store.Repo) string {
	if _, err := g.ResolveCommit("refs/heads/" + repo.DefaultBranch); err == nil {
		return repo.DefaultBranch
	}
	if head, err := g.HeadBranch(); err == nil {
		if _, err := g.ResolveCommit("refs/heads/" + head); err == nil {
			return head
		}
	}
	if branches, err := g.Branches(); err == nil && len(branches) > 0 {
		return branches[0].Name
	}
	return repo.DefaultBranch
}

// ---- ファイル表示 ----

type blobView struct {
	refContext
	Name       string
	Size       int64
	Lines      []string
	IsBinary   bool
	TooLarge   bool
	IsImage    bool
	IsMarkdown bool
	ShowSource bool
	Rendered   template.HTML
	RawURL     string
	HistoryURL string
	SourceURL  string
	RenderURL  string
}

func isImagePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".bmp":
		return true
	}
	return false
}

func isMarkdownPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

func looksBinary(data []byte) bool {
	// UTF-16 のテキストは NUL を含むので、BOM があればテキストとして扱う
	if len(data) >= 2 && ((data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF)) {
		return false
	}
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	c, err := s.buildRefContext(g, s.repoURL(st, repo), s.defaultBranch(g, repo), r.PathValue("rest"), "tree")
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.ref_not_found"))
		return
	}
	if c.Path == "" {
		http.Redirect(w, r, c.RootURL(), http.StatusSeeOther)
		return
	}
	typ, err := g.ObjectType(c.Ref, c.Path)
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.path_not_found"))
		return
	}
	if typ != "blob" {
		http.Redirect(w, r, c.TreeHere(), http.StatusSeeOther)
		return
	}
	v := blobView{
		refContext: c,
		Name:       path.Base(c.Path),
		RawURL:     c.rawURL(c.Path),
		HistoryURL: c.CommitsHere(),
		SourceURL:  c.blobURL(c.Path) + "?plain=1",
		RenderURL:  c.blobURL(c.Path),
		IsImage:    isImagePath(c.Path),
		IsMarkdown: isMarkdownPath(c.Path),
		ShowSource: r.URL.Query().Get("plain") == "1",
	}
	// パンくずの最後はファイル名なので、ツリーのリンクから外す
	if n := len(v.Crumbs); n > 0 {
		v.Crumbs = v.Crumbs[:n-1]
	}
	v.Size, err = g.BlobSize(c.Ref, c.Path)
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.path_not_found"))
		return
	}
	switch {
	case v.IsImage:
	case v.Size > maxBlobBytes:
		v.TooLarge = true
	default:
		data, err := g.Blob(c.Ref, c.Path)
		if err != nil {
			s.renderCodeError(w, err, s.tr("ppoi.code.path_not_found"))
			return
		}
		if looksBinary(data) {
			v.IsBinary = true
			break
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		if v.IsMarkdown && !v.ShowSource {
			v.Rendered = s.renderMarkdown(text)
		} else {
			text = strings.TrimSuffix(text, "\n")
			if text != "" {
				v.Lines = strings.Split(text, "\n")
			}
		}
	}
	p := s.repoPage(c.Path, repo, "code")
	p.Data = v
	s.render(w, http.StatusOK, "blob", p)
}

// handleRaw はファイルをそのまま返す。画像は画像として、文字は text/plain として、それ以外はダウンロードにする。
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	c, err := s.buildRefContext(g, s.repoURL(st, repo), s.defaultBranch(g, repo), r.PathValue("rest"), "tree")
	if err != nil || c.Path == "" {
		http.NotFound(w, r)
		return
	}
	if size, err := g.BlobSize(c.Ref, c.Path); err == nil && size > maxRawBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := g.Blob(c.Ref, c.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctype := "text/plain; charset=utf-8"
	switch strings.ToLower(path.Ext(c.Path)) {
	case ".png":
		ctype = "image/png"
	case ".jpg", ".jpeg":
		ctype = "image/jpeg"
	case ".gif":
		ctype = "image/gif"
	case ".svg":
		ctype = "image/svg+xml"
	case ".webp":
		ctype = "image/webp"
	case ".ico":
		ctype = "image/x-icon"
	case ".bmp":
		ctype = "image/bmp"
	default:
		if looksBinary(data) {
			ctype = "application/octet-stream"
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(path.Base(c.Path)))
		}
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Write(data)
}

// ---- コミット履歴 ----

type commitRow struct {
	gitcmd.Commit
	URL string
}

type commitsView struct {
	refContext
	Commits  []commitRow
	Page     int
	NewerURL string
	OlderURL string
}

func (s *Server) handleCommits(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	base := s.repoURL(st, repo)
	c, err := s.buildRefContext(g, base, s.defaultBranch(g, repo), r.PathValue("rest"), "commits")
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.ref_not_found"))
		return
	}
	pageNo, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNo < 1 {
		pageNo = 1
	}
	commits, err := g.Log(c.Ref, c.Path, (pageNo-1)*commitsPerPage, commitsPerPage+1)
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.ref_not_found"))
		return
	}
	v := commitsView{refContext: c, Page: pageNo}
	if len(commits) > commitsPerPage {
		commits = commits[:commitsPerPage]
		v.OlderURL = fmt.Sprintf("%s?page=%d", c.CommitsHere(), pageNo+1)
	}
	if pageNo > 1 {
		v.NewerURL = fmt.Sprintf("%s?page=%d", c.CommitsHere(), pageNo-1)
	}
	for _, cm := range commits {
		v.Commits = append(v.Commits, commitRow{Commit: cm, URL: base + "/commit/" + cm.SHA})
	}
	p := s.repoPage(s.tr("repo.commits"), repo, "code")
	p.Data = v
	s.render(w, http.StatusOK, "commits", p)
}

// ---- コミットの差分 ----

type commitView struct {
	Base      string
	Commit    gitcmd.Commit
	Parents   []crumb
	TreeURL   string
	Files     []gitcmd.DiffFile
	Stat      gitcmd.DiffStat
	StatsHTML template.HTML
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	g := s.gitRepo(st, repo)
	base := s.repoURL(st, repo)
	sha, err := g.ResolveCommit(r.PathValue("sha"))
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.ref_not_found"))
		return
	}
	cm, err := g.GetCommit(sha)
	if err != nil {
		s.renderCodeError(w, err, s.tr("ppoi.code.ref_not_found"))
		return
	}
	raw, err := g.CommitDiff(cm)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v := commitView{Base: base, Commit: cm, TreeURL: base + "/tree/" + cm.SHA}
	for _, p := range cm.Parents {
		v.Parents = append(v.Parents, crumb{Name: p[:7], URL: base + "/commit/" + p})
	}
	v.Files = gitcmd.ParseDiff(raw)
	v.Stat = gitcmd.Stat(v.Files)
	v.StatsHTML = s.trh("repo.diff.stats_desc", v.Stat.Files, v.Stat.Additions, v.Stat.Deletions)
	p := s.repoPage(cm.Subject+" · "+cm.ShortSHA(), repo, "code")
	p.Data = v
	s.render(w, http.StatusOK, "commit", p)
}
