package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// リリースの画面（設計メモ 3.18）。GitHub の Releases を真似る。
// 一覧（/releases）、詳細（/releases/tag/<タグ>）、新規（/releases/new）、編集（/releases/edit/<タグ>）、
// 削除、添付のダウンロード、タグ一覧（/tags）。

type releaseRow struct {
	store.Release
	Assets    []store.Attachment
	Latest    bool // プレリリースでない中で一番新しい
	URL       string
	EditURL   string
	DeleteURL string
	CommitURL string
	Download  string // 添付のダウンロード URL の前半（/<owner>/<repo>/releases/download/<タグ>/）
}

type releasesView struct {
	Rows     []releaseRow
	Single   bool // 詳細（1件だけ）
	ListURL  string
	NewURL   string
	TagsURL  string
	TagCount int
}

type tagRow struct {
	gitcmd.Tag
	CommitURL  string
	ReleaseURL string // リリースがあればその URL
	NewURL     string // 無ければ、このタグでリリースを作る URL
}

type tagsView struct {
	Rows        []tagRow
	ReleasesURL string
}

type releaseForm struct {
	Edit       bool
	Action     string
	BackURL    string
	Tag        string
	Target     string
	Title      string
	Body       string
	Prerelease bool
	Branches   []gitcmd.Branch
	FreeTags   []string // リリースの無いタグ（新規のときの候補）
	Assets     []store.Attachment
	Download   string
}

func (s *Server) releasesURL(st *store.Store, repo store.Repo) string {
	return s.repoURL(st, repo) + "/releases"
}

func (s *Server) releaseRows(st *store.Store, repo store.Repo, rels []store.Release) []releaseRow {
	base := s.releasesURL(st, repo)
	rows := make([]releaseRow, 0, len(rels))
	latestDone := false
	for _, rel := range rels {
		assets, _ := st.ListReleaseAttachments(repo.Name, rel.Tag)
		row := releaseRow{
			Release: rel, Assets: assets,
			URL:       base + "/tag/" + url.PathEscape(rel.Tag),
			EditURL:   base + "/edit/" + url.PathEscape(rel.Tag),
			DeleteURL: base + "/delete/" + url.PathEscape(rel.Tag),
			Download:  base + "/download/" + url.PathEscape(rel.Tag) + "/",
		}
		if rel.TagSHA != "" {
			row.CommitURL = s.repoURL(st, repo) + "/commit/" + rel.TagSHA
		}
		if !rel.Prerelease && !latestDone {
			row.Latest = true
			latestDone = true
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) renderReleases(w http.ResponseWriter, status int, st *store.Store, repo store.Repo, v releasesView, errMsg string) {
	v.ListURL = s.releasesURL(st, repo)
	v.NewURL = v.ListURL + "/new"
	v.TagsURL = s.repoURL(st, repo) + "/tags"
	if tags, err := s.gitRepo(st, repo).Tags(); err == nil {
		v.TagCount = len(tags)
	}
	p := s.repoPage(s.tr("repo.release.releases"), repo, "code")
	p.Error = errMsg
	p.Data = v
	s.render(w, status, "releases", p)
}

func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rels, err := st.ListReleases(repo.Name)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.renderReleases(w, http.StatusOK, st, repo, releasesView{Rows: s.releaseRows(st, repo, rels)}, "")
}

// loadRelease はパスのタグからリリースを読む。無ければ 404 を出して偽。
func (s *Server) loadRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) (store.Release, bool) {
	rel, err := st.GetRelease(repo.Name, r.PathValue("tag"))
	if errors.Is(err, store.ErrReleaseNotFound) {
		s.renderError(w, http.StatusNotFound, s.tr("ppoi.release.not_found"))
		return store.Release{}, false
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return store.Release{}, false
	}
	return rel, true
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.loadRelease(w, r, st, repo)
	if !ok {
		return
	}
	// 「最新」の印は一覧の中での位置で決まるので、一覧を引いてから1件に絞る
	rels, _ := st.ListReleases(repo.Name)
	var rows []releaseRow
	for _, row := range s.releaseRows(st, repo, rels) {
		if row.Tag == rel.Tag {
			rows = append(rows, row)
		}
	}
	s.renderReleases(w, http.StatusOK, st, repo, releasesView{Rows: rows, Single: true}, "")
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	tags, err := s.gitRepo(st, repo).Tags()
	if err != nil {
		s.renderCodeError(w, err, err.Error())
		return
	}
	rels, _ := st.ListReleases(repo.Name)
	has := map[string]bool{}
	for _, rel := range rels {
		has[rel.Tag] = true
	}
	base := s.releasesURL(st, repo)
	v := tagsView{ReleasesURL: base}
	for _, tg := range tags {
		row := tagRow{Tag: tg, CommitURL: s.repoURL(st, repo) + "/commit/" + tg.SHA}
		if has[tg.Name] {
			row.ReleaseURL = base + "/tag/" + url.PathEscape(tg.Name)
		} else if store.ValidateTagName(tg.Name) == nil {
			row.NewURL = base + "/new?tag=" + url.QueryEscape(tg.Name)
		}
		v.Rows = append(v.Rows, row)
	}
	p := s.repoPage(s.tr("repo.release.tags"), repo, "code")
	p.Data = v
	s.render(w, http.StatusOK, "tags", p)
}

func (s *Server) renderReleaseForm(w http.ResponseWriter, status int, st *store.Store, repo store.Repo, f releaseForm, errMsg string) {
	g := s.gitRepo(st, repo)
	f.Branches, _ = g.Branches()
	if !f.Edit {
		rels, _ := st.ListReleases(repo.Name)
		has := map[string]bool{}
		for _, rel := range rels {
			has[rel.Tag] = true
		}
		if tags, err := g.Tags(); err == nil {
			for _, tg := range tags {
				if !has[tg.Name] && store.ValidateTagName(tg.Name) == nil {
					f.FreeTags = append(f.FreeTags, tg.Name)
				}
			}
		}
	}
	f.BackURL = s.releasesURL(st, repo)
	title := s.tr("repo.release.new_release")
	if f.Edit {
		title = s.tr("repo.release.edit_release")
	}
	p := s.repoPage(title, repo, "code")
	p.Error = errMsg
	p.Data = f
	s.render(w, status, "release_form", p)
}

func (s *Server) handleReleaseNew(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	f := releaseForm{Action: s.releasesURL(st, repo) + "/new", Tag: strings.TrimSpace(r.URL.Query().Get("tag")), Target: repo.DefaultBranch}
	s.renderReleaseForm(w, http.StatusOK, st, repo, f, "")
}

// releaseInput はフォームと API に共通の入力。
type releaseInput struct {
	Tag, Target, Title, Body string
	Prerelease               bool
}

// createRelease はタグが無ければ作ってから、リリースを作る。画面と API の両方がここを通る。
func (s *Server) createRelease(st *store.Store, repo store.Repo, in releaseInput) (store.Release, error) {
	if st.ReadOnly() {
		return store.Release{}, store.ErrReadOnly
	}
	in.Tag = strings.TrimSpace(in.Tag)
	in.Title = strings.TrimSpace(in.Title)
	if err := store.ValidateTagName(in.Tag); err != nil {
		return store.Release{}, opErr(422, "repo.release.tag_name_invalid")
	}
	if in.Title == "" {
		return store.Release{}, opErr(422, "repo.release.title_empty")
	}
	if _, err := st.GetRelease(repo.Name, in.Tag); err == nil {
		return store.Release{}, opErr(422, "repo.release.tag_name_already_exist")
	}
	unlock, err := s.locks.lock(st, repo)
	if err != nil {
		return store.Release{}, err
	}
	defer unlock()
	g := s.gitRepo(st, repo)
	sha := ""
	if tags, err := g.Tags(); err == nil {
		for _, tg := range tags {
			if tg.Name == in.Tag {
				sha = tg.SHA
			}
		}
	}
	if sha == "" {
		target := strings.TrimSpace(in.Target)
		if target == "" {
			target = repo.DefaultBranch
		}
		c, err := s.cloneFor(st, repo)
		if err != nil {
			return store.Release{}, err
		}
		sha, err = c.CreateTag(in.Tag, target)
		if errors.Is(err, gitcmd.ErrNotFound) {
			return store.Release{}, opErr(422, "ppoi.code.ref_not_found")
		}
		if errors.Is(err, gitcmd.ErrTagExists) {
			// 直前に誰かが同じタグを push した。その先を使う
			sha, err = g.ResolveCommit("refs/tags/" + in.Tag)
		}
		if err != nil {
			return store.Release{}, err
		}
	}
	rel, err := st.CreateRelease(repo.Name, s.login, in.Tag, sha, in.Title, normalizeNewlines(in.Body), in.Prerelease)
	if errors.Is(err, store.ErrReleaseExists) {
		return store.Release{}, opErr(422, "repo.release.tag_name_already_exist")
	}
	return rel, err
}

// saveReleaseAssets はフォームの assets を保存する。
func (s *Server) saveReleaseAssets(st *store.Store, repo store.Repo, tag string, r *http.Request) error {
	if r.MultipartForm == nil {
		return nil
	}
	for _, fh := range r.MultipartForm.File["assets"] {
		if fh.Size > st.AttachmentMaxBytes() {
			return attachmentError{s.tr("ppoi.attach.too_large", st.AttachmentMaxBytes()/(1024*1024))}
		}
		f, err := fh.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, st.AttachmentMaxBytes()+1))
		f.Close()
		if err != nil {
			return err
		}
		if _, err := st.SaveReleaseAttachment(repo.Name, tag, fh.Filename, data); err != nil {
			if errors.Is(err, store.ErrAttachmentTooLarge) {
				return attachmentError{s.tr("ppoi.attach.too_large", st.AttachmentMaxBytes()/(1024*1024))}
			}
			return err
		}
	}
	return nil
}

func parseReleaseForm(r *http.Request) (releaseInput, error) {
	if err := r.ParseMultipartForm(multipartMemory); err != nil && !strings.Contains(err.Error(), "multipart") {
		return releaseInput{}, err
	}
	return releaseInput{
		Tag: r.FormValue("tag"), Target: r.FormValue("target"), Title: r.FormValue("title"), Body: r.FormValue("body"),
		Prerelease: r.FormValue("prerelease") == "1",
	}, nil
}

func (s *Server) handleReleaseCreate(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	in, err := parseReleaseForm(r)
	f := releaseForm{Action: s.releasesURL(st, repo) + "/new", Tag: in.Tag, Target: in.Target, Title: in.Title, Body: in.Body, Prerelease: in.Prerelease}
	if err != nil {
		s.renderReleaseForm(w, http.StatusUnprocessableEntity, st, repo, f, s.opMessage(err))
		return
	}
	rel, err := s.createRelease(st, repo, in)
	if err != nil {
		s.renderReleaseForm(w, opStatus(err), st, repo, f, s.opMessage(err))
		return
	}
	if err := s.saveReleaseAssets(st, repo, rel.Tag, r); err != nil {
		// リリース自体はできているので、編集画面に戻してやり直せるようにする
		f.Edit, f.Action = true, s.releasesURL(st, repo)+"/edit/"+url.PathEscape(rel.Tag)
		s.renderReleaseForm(w, http.StatusUnprocessableEntity, st, repo, f, s.opMessage(err))
		return
	}
	http.Redirect(w, r, s.releasesURL(st, repo)+"/tag/"+url.PathEscape(rel.Tag), http.StatusSeeOther)
}

func (s *Server) releaseEditForm(st *store.Store, repo store.Repo, rel store.Release) releaseForm {
	assets, _ := st.ListReleaseAttachments(repo.Name, rel.Tag)
	base := s.releasesURL(st, repo)
	return releaseForm{
		Edit: true, Action: base + "/edit/" + url.PathEscape(rel.Tag),
		Tag: rel.Tag, Title: rel.Title, Body: rel.Body, Prerelease: rel.Prerelease,
		Assets: assets, Download: base + "/download/" + url.PathEscape(rel.Tag) + "/",
	}
}

func (s *Server) handleReleaseEdit(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.loadRelease(w, r, st, repo)
	if !ok {
		return
	}
	s.renderReleaseForm(w, http.StatusOK, st, repo, s.releaseEditForm(st, repo, rel), "")
}

func (s *Server) handleReleaseEditSave(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.loadRelease(w, r, st, repo)
	if !ok {
		return
	}
	in, err := parseReleaseForm(r)
	f := s.releaseEditForm(st, repo, rel)
	if err != nil {
		s.renderReleaseForm(w, http.StatusUnprocessableEntity, st, repo, f, s.opMessage(err))
		return
	}
	f.Title, f.Body, f.Prerelease = in.Title, in.Body, in.Prerelease
	if strings.TrimSpace(in.Title) == "" {
		s.renderReleaseForm(w, http.StatusUnprocessableEntity, st, repo, f, s.tr("repo.release.title_empty"))
		return
	}
	if err := st.EditRelease(repo.Name, s.login, rel.Tag, strings.TrimSpace(in.Title), normalizeNewlines(in.Body), in.Prerelease); err != nil {
		s.renderReleaseForm(w, opStatus(err), st, repo, f, s.opMessage(err))
		return
	}
	for _, name := range r.Form["remove"] {
		if err := st.RemoveReleaseAttachment(repo.Name, rel.Tag, name); err != nil && !errors.Is(err, store.ErrAttachmentNotFound) {
			s.renderReleaseForm(w, opStatus(err), st, repo, f, s.opMessage(err))
			return
		}
	}
	if err := s.saveReleaseAssets(st, repo, rel.Tag, r); err != nil {
		s.renderReleaseForm(w, http.StatusUnprocessableEntity, st, repo, s.releaseEditForm(st, repo, rel), s.opMessage(err))
		return
	}
	http.Redirect(w, r, s.releasesURL(st, repo)+"/tag/"+url.PathEscape(rel.Tag), http.StatusSeeOther)
}

func (s *Server) handleReleaseDelete(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.loadRelease(w, r, st, repo)
	if !ok {
		return
	}
	if err := st.DeleteRelease(repo.Name, s.login, rel.Tag); err != nil {
		s.renderError(w, opStatus(err), s.opMessage(err))
		return
	}
	http.Redirect(w, r, s.releasesURL(st, repo), http.StatusSeeOther)
}

// handleReleaseDownload は /releases/download/<タグ>/<ファイル名> で添付をダウンロードさせる。
func (s *Server) handleReleaseDownload(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	p, err := st.ReleaseAttachmentPath(repo.Name, r.PathValue("tag"), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeFile(w, r, p)
}

// releaseDownloadURL は API 用の絶対 URL。
func (s *Server) releaseDownloadURL(st *store.Store, repo store.Repo, tag, name string) string {
	return fmt.Sprintf("%s%s/download/%s/%s", s.apiBase(), s.releasesURL(st, repo), url.PathEscape(tag), url.PathEscape(name))
}
