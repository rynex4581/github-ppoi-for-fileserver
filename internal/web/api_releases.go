package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github-ppoi-for-fileserver/internal/store"
)

// Gitea の /repos/{owner}/{repo}/releases に合わせた API（設計メモ 3.18）。
// 下書き（draft）は持たないので常に false。tarball_url と zipball_url は出さない。

type apiRelease struct {
	ID          int64           `json:"id"`
	TagName     string          `json:"tag_name"`
	Target      string          `json:"target_commitish"`
	Name        string          `json:"name"`
	Body        string          `json:"body"`
	URL         string          `json:"url"`
	HTMLURL     string          `json:"html_url"`
	TarballURL  string          `json:"tarball_url"`
	ZipballURL  string          `json:"zipball_url"`
	UploadURL   string          `json:"upload_url"`
	Draft       bool            `json:"draft"`
	Prerelease  bool            `json:"prerelease"`
	CreatedAt   string          `json:"created_at"`
	PublishedAt string          `json:"published_at"`
	Author      apiUser         `json:"author"`
	Assets      []apiAttachment `json:"assets"`
}

type releaseOption struct {
	TagName    *string `json:"tag_name"`
	Target     *string `json:"target_commitish"`
	Name       *string `json:"name"`
	Body       *string `json:"body"`
	Draft      *bool   `json:"draft"`
	Prerelease *bool   `json:"prerelease"`
}

func releaseID(owner, repo, tag string) int64 {
	return stableID("release", owner, repo, tag)
}

func (s *Server) toReleaseAsset(st *store.Store, repo store.Repo, tag string, a store.Attachment) apiAttachment {
	owner := st.Config().Owner
	return apiAttachment{
		ID: stableID("release_asset", owner, repo.Name, tag, a.Name), Name: a.Name, Size: a.Size,
		CreatedAt: apiTime(a.ModTime), UUID: a.Name,
		DownloadURL: s.releaseDownloadURL(st, repo, tag, a.Name),
	}
}

func (s *Server) toRelease(st *store.Store, repo store.Repo, rel store.Release) apiRelease {
	owner := st.Config().Owner
	id := releaseID(owner, repo.Name, rel.Tag)
	assets, _ := st.ListReleaseAttachments(repo.Name, rel.Tag)
	out := apiRelease{
		ID: id, TagName: rel.Tag, Target: rel.TagSHA, Name: rel.Title, Body: rel.Body,
		URL:        s.apiURL("repos", owner, repo.Name, "releases", strconv.FormatInt(id, 10)),
		HTMLURL:    fmt.Sprintf("%s%s/tag/%s", s.apiBase(), s.releasesURL(st, repo), rel.Tag),
		UploadURL:  s.apiURL("repos", owner, repo.Name, "releases", strconv.FormatInt(id, 10), "assets"),
		Prerelease: rel.Prerelease, CreatedAt: apiTime(rel.CreatedAt), PublishedAt: apiTime(rel.CreatedAt),
		Author: s.toUser(rel.User), Assets: []apiAttachment{},
	}
	for _, a := range assets {
		out.Assets = append(out.Assets, s.toReleaseAsset(st, repo, rel.Tag, a))
	}
	return out
}

func (s *Server) apiReleases(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rels, err := st.ListReleases(repo.Name)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	q := r.URL.Query()
	out := []apiRelease{}
	for _, rel := range rels {
		if v := q.Get("pre-release"); v != "" {
			if want, err := strconv.ParseBool(v); err == nil && want != rel.Prerelease {
				continue
			}
		}
		if v := q.Get("draft"); v != "" {
			if want, err := strconv.ParseBool(v); err == nil && want {
				continue // 下書きは無い
			}
		}
		out = append(out, s.toRelease(st, repo, rel))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) apiCreateRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	var in releaseOption
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	if in.TagName == nil || strings.TrimSpace(*in.TagName) == "" {
		s.apiError(w, http.StatusUnprocessableEntity, "tag_name is required")
		return
	}
	ri := releaseInput{Tag: *in.TagName}
	if in.Target != nil {
		ri.Target = *in.Target
	}
	if in.Name != nil {
		ri.Title = *in.Name
	}
	if strings.TrimSpace(ri.Title) == "" {
		ri.Title = ri.Tag // tea は name を省略できる。Gitea もタグ名を題にする
	}
	if in.Body != nil {
		ri.Body = *in.Body
	}
	if in.Prerelease != nil {
		ri.Prerelease = *in.Prerelease
	}
	rel, err := s.createRelease(st, repo, ri)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toRelease(st, repo, rel))
}

// apiReleaseSub は releases/tags/{tag} と releases/{id}/assets[/{aid}] を振り分ける。
func (s *Server) apiReleaseSub(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	id, rest := r.PathValue("id"), r.PathValue("rest")
	if id == "tags" {
		r.SetPathValue("tag", rest)
		switch r.Method {
		case http.MethodGet:
			s.apiGetReleaseByTag(w, r, st, repo)
		case http.MethodDelete:
			s.apiDeleteReleaseByTag(w, r, st, repo)
		default:
			s.apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if rest == "assets" {
		switch r.Method {
		case http.MethodGet:
			s.apiReleaseAssets(w, r, st, repo)
		case http.MethodPost:
			s.apiCreateReleaseAsset(w, r, st, repo)
		default:
			s.apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if aid, ok := strings.CutPrefix(rest, "assets/"); ok && !strings.Contains(aid, "/") {
		r.SetPathValue("aid", aid)
		switch r.Method {
		case http.MethodGet:
			s.apiGetReleaseAsset(w, r, st, repo)
		case http.MethodDelete:
			s.apiDeleteReleaseAsset(w, r, st, repo)
		default:
			s.apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	s.apiError(w, http.StatusNotFound, "not implemented: "+r.Method+" "+r.URL.Path)
}

// findRelease は id か tag でリリースを引く。
func (s *Server) findRelease(st *store.Store, repo store.Repo, idOrTag string, byTag bool) (store.Release, bool) {
	if byTag {
		rel, err := st.GetRelease(repo.Name, idOrTag)
		return rel, err == nil
	}
	id, err := strconv.ParseInt(idOrTag, 10, 64)
	if err != nil {
		return store.Release{}, false
	}
	rels, _ := st.ListReleases(repo.Name)
	for _, rel := range rels {
		if releaseID(st.Config().Owner, repo.Name, rel.Tag) == id {
			return rel, true
		}
	}
	return store.Release{}, false
}

func (s *Server) apiGetRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toRelease(st, repo, rel))
}

func (s *Server) apiGetReleaseByTag(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("tag"), true)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toRelease(st, repo, rel))
}

func (s *Server) apiLatestRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rels, _ := st.ListReleases(repo.Name)
	for _, rel := range rels {
		if !rel.Prerelease {
			writeJSON(w, http.StatusOK, s.toRelease(st, repo, rel))
			return
		}
	}
	s.apiError(w, http.StatusNotFound, "release not found")
}

func (s *Server) apiEditRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	var in releaseOption
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	title, body, pre := rel.Title, rel.Body, rel.Prerelease
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		title = strings.TrimSpace(*in.Name)
	}
	if in.Body != nil {
		body = normalizeNewlines(*in.Body)
	}
	if in.Prerelease != nil {
		pre = *in.Prerelease
	}
	if err := st.EditRelease(repo.Name, s.login, rel.Tag, title, body, pre); err != nil {
		s.apiFail(w, err)
		return
	}
	rel, _ = st.GetRelease(repo.Name, rel.Tag)
	writeJSON(w, http.StatusOK, s.toRelease(st, repo, rel))
}

func (s *Server) apiDeleteRelease(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	if err := st.DeleteRelease(repo.Name, s.login, rel.Tag); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiDeleteReleaseByTag(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("tag"), true)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	if err := st.DeleteRelease(repo.Name, s.login, rel.Tag); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiReleaseAssets(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	assets, _ := st.ListReleaseAttachments(repo.Name, rel.Tag)
	out := []apiAttachment{}
	for _, a := range assets {
		out = append(out, s.toReleaseAsset(st, repo, rel.Tag, a))
	}
	writeJSON(w, http.StatusOK, out)
}

// apiCreateReleaseAsset は multipart の attachment を受けて保存する。?name= があればその名前にする。
func (s *Server) apiCreateReleaseAsset(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.apiError(w, http.StatusRequestEntityTooLarge, s.tr("ppoi.attach.too_large", st.AttachmentMaxBytes()/(1024*1024)))
			return
		}
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	f, fh, err := r.FormFile("attachment")
	if err != nil {
		s.apiError(w, http.StatusUnprocessableEntity, "attachment is required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, st.AttachmentMaxBytes()+1))
	if err != nil {
		s.apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := fh.Filename
	if q := r.URL.Query().Get("name"); q != "" {
		name = q
	}
	saved, err := st.SaveReleaseAttachment(repo.Name, rel.Tag, name, data)
	if errors.Is(err, store.ErrAttachmentTooLarge) {
		s.apiError(w, http.StatusRequestEntityTooLarge, s.tr("ppoi.attach.too_large", st.AttachmentMaxBytes()/(1024*1024)))
		return
	}
	if err != nil {
		s.apiFail(w, err)
		return
	}
	assets, _ := st.ListReleaseAttachments(repo.Name, rel.Tag)
	for _, a := range assets {
		if a.Name == saved {
			writeJSON(w, http.StatusCreated, s.toReleaseAsset(st, repo, rel.Tag, a))
			return
		}
	}
	s.apiError(w, http.StatusInternalServerError, "asset was written but could not be read back")
}

func (s *Server) findReleaseAsset(st *store.Store, repo store.Repo, rel store.Release, idText string) (store.Attachment, bool) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return store.Attachment{}, false
	}
	assets, _ := st.ListReleaseAttachments(repo.Name, rel.Tag)
	for _, a := range assets {
		if stableID("release_asset", st.Config().Owner, repo.Name, rel.Tag, a.Name) == id {
			return a, true
		}
	}
	return store.Attachment{}, false
}

func (s *Server) apiGetReleaseAsset(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	a, ok := s.findReleaseAsset(st, repo, rel, r.PathValue("aid"))
	if !ok {
		s.apiError(w, http.StatusNotFound, "asset not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toReleaseAsset(st, repo, rel.Tag, a))
}

func (s *Server) apiDeleteReleaseAsset(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	rel, ok := s.findRelease(st, repo, r.PathValue("id"), false)
	if !ok {
		s.apiError(w, http.StatusNotFound, "release not found")
		return
	}
	a, ok := s.findReleaseAsset(st, repo, rel, r.PathValue("aid"))
	if !ok {
		s.apiError(w, http.StatusNotFound, "asset not found")
		return
	}
	if err := st.RemoveReleaseAttachment(repo.Name, rel.Tag, a.Name); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiTag は Gitea の Tag。
type apiTag struct {
	Name       string        `json:"name"`
	Message    string        `json:"message"`
	ID         string        `json:"id"`
	Commit     apiCommitLink `json:"commit"`
	ZipballURL string        `json:"zipball_url"`
	TarballURL string        `json:"tarball_url"`
}

func (s *Server) apiTags(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	tags, err := s.gitRepo(st, repo).Tags()
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiTag{}
	for _, tg := range tags {
		out = append(out, apiTag{Name: tg.Name, ID: tg.SHA, Commit: apiCommitLink{SHA: tg.SHA, URL: s.apiURL("repos", st.Config().Owner, repo.Name, "git", "commits", tg.SHA)}})
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}
