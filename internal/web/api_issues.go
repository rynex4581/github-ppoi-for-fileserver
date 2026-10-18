package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/store"
)

// ---- labels ----

func (s *Server) apiLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	labels, err := st.ListLabels(repo.Name)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiLabel{}
	for _, l := range labels {
		out = append(out, s.toLabel(st, repo, l))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

type labelInput struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

func (s *Server) apiCreateLabel(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	var in labelInput
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	l, err := st.CreateLabel(repo.Name, store.Label{Name: in.Name, Color: in.Color, Description: in.Description})
	if err != nil {
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.toLabel(st, repo, l))
}

func labelID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	return id, err == nil
}

func (s *Server) apiGetLabel(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	id, ok := labelID(r)
	l, err := st.GetLabel(repo.Name, id)
	if !ok || err != nil {
		s.apiError(w, http.StatusNotFound, "label not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toLabel(st, repo, l))
}

func (s *Server) apiEditLabel(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	id, ok := labelID(r)
	l, err := st.GetLabel(repo.Name, id)
	if !ok || err != nil {
		s.apiError(w, http.StatusNotFound, "label not found")
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Color       *string `json:"color"`
		Description *string `json:"description"`
	}
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	if in.Name != nil {
		l.Name = *in.Name
	}
	if in.Color != nil {
		l.Color = *in.Color
	}
	if in.Description != nil {
		l.Description = *in.Description
	}
	if err := st.UpdateLabel(repo.Name, l); err != nil {
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	l, _ = st.GetLabel(repo.Name, id)
	writeJSON(w, http.StatusOK, s.toLabel(st, repo, l))
}

func (s *Server) apiDeleteLabel(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	id, ok := labelID(r)
	if !ok {
		s.apiError(w, http.StatusNotFound, "label not found")
		return
	}
	if err := st.DeleteLabel(repo.Name, id); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- issues ----

// filterIssues は state と type で絞り、更新日時の新しい順に並べる。
func (s *Server) filterIssues(r *http.Request, all []store.Issue, isPull bool) []store.Issue {
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		state = "open"
	}
	typ := q.Get("type") // issues / pulls / 空（issues 用）
	labelsQ := strings.Split(q.Get("labels"), ",")
	assignee := q.Get("assigned_by")
	created := q.Get("created_by")
	search := strings.ToLower(q.Get("q"))
	var out []store.Issue
	for _, is := range all {
		if typ == "" {
			if is.IsPull != isPull {
				continue
			}
		} else if (typ == "pulls") != is.IsPull {
			continue
		}
		if state != "all" && is.State != state {
			continue
		}
		if created != "" && is.User != created {
			continue
		}
		if assignee != "" && !is.HasAssignee(assignee) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(is.Title), search) && !strings.Contains(strings.ToLower(is.Body), search) {
			continue
		}
		if q.Get("labels") != "" {
			// Gitea はラベル名の列挙。名前で絞る
			labels, _ := s.labelsOf(r, is)
			ok := true
			for _, want := range labelsQ {
				want = strings.TrimSpace(want)
				if want == "" {
					continue
				}
				found := false
				for _, l := range labels {
					if strings.EqualFold(l.Name, want) {
						found = true
					}
				}
				if !found {
					ok = false
				}
			}
			if !ok {
				continue
			}
		}
		out = append(out, is)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// labelsOf は絞り込み用にラベルを引く。リクエストから owner と repo を取る。
func (s *Server) labelsOf(r *http.Request, is store.Issue) ([]store.Label, error) {
	st := s.storeByOwner(r.PathValue("owner"))
	if st == nil {
		return nil, store.ErrRepoNotFound
	}
	all, err := st.ListLabels(r.PathValue("repo"))
	if err != nil {
		return nil, err
	}
	return resolveLabels(is.Labels, labelsByID(all)), nil
}

func (s *Server) apiIssues(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	all, err := st.ListIssues(repo.Name)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiIssue{}
	for _, is := range s.filterIssues(r, all, false) {
		out = append(out, s.toIssue(st, repo, is))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

type issueInput struct {
	Title     *string  `json:"title"`
	Body      *string  `json:"body"`
	State     *string  `json:"state"`
	Labels    []int64  `json:"labels"`
	Assignees []string `json:"assignees"`
	Assignee  *string  `json:"assignee"`
}

func toInts(ids []int64) []int {
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		out = append(out, int(id))
	}
	return out
}

func (in issueInput) assigneeList() []string {
	logins := append([]string(nil), in.Assignees...)
	if in.Assignee != nil && *in.Assignee != "" && !containsString(logins, *in.Assignee) {
		logins = append(logins, *in.Assignee)
	}
	return logins
}

func (s *Server) apiCreateIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	var in issueInput
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	title, body := "", ""
	if in.Title != nil {
		title = *in.Title
	}
	if in.Body != nil {
		body = *in.Body
	}
	labels, _ := st.ListLabels(repo.Name)
	ids := parseLabelIDs(intsToStrings(toInts(in.Labels)), labels)
	users, _ := st.ListUsers()
	assignees := parseAssignees(in.assigneeList(), users)
	is, err := s.createIssue(st, repo, title, body, ids, assignees)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toIssue(st, repo, is))
}

func intsToStrings(ids []int) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strconv.Itoa(id))
	}
	return out
}

func (s *Server) apiGetIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	writeJSON(w, http.StatusOK, s.toIssue(st, repo, is))
}

// apiEditIssue は title、state、assignees を変える。body の変更は持たない（本文を書き換えるイベントが無い）。
func (s *Server) apiEditIssue(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	var in issueInput
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	if in.Body != nil && normalizeNewlines(*in.Body) != is.Body {
		if err := s.editBody(st, repo, is, is.BodyFile, *in.Body); err != nil {
			s.apiFail(w, err)
			return
		}
	}
	// tea は state だけ変えるときも title を空で送ってくる。空は変更なしとして扱う
	if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
		if err := s.setTitle(st, repo, is, *in.Title); err != nil {
			s.apiFail(w, err)
			return
		}
	}
	if in.Assignees != nil || in.Assignee != nil {
		if err := s.setAssignees(st, repo, is, in.assigneeList()); err != nil {
			s.apiFail(w, err)
			return
		}
	}
	if in.Labels != nil {
		if err := s.setLabels(st, repo, is, toInts(in.Labels)); err != nil {
			s.apiFail(w, err)
			return
		}
	}
	if in.State != nil {
		is, _ = st.GetIssue(repo.Name, is.Number)
		if err := s.setState(st, repo, is, *in.State, ""); err != nil {
			s.apiFail(w, err)
			return
		}
	}
	is, _ = st.GetIssue(repo.Name, is.Number)
	if is.IsPull {
		writeJSON(w, http.StatusCreated, s.toPull(st, repo, is))
		return
	}
	writeJSON(w, http.StatusCreated, s.toIssue(st, repo, is))
}

// ---- comments ----

// eventID はイベントのファイル名から安定した ID を作る。
func eventID(ev store.Event) int64 {
	return stableID("event", ev.File)
}

// updatedTime はイベントの更新時刻。編集されていればその時刻、なければ書いた時刻。
func updatedTime(ev store.Event) time.Time {
	if !ev.EditedAt.IsZero() {
		return ev.EditedAt
	}
	return ev.Time
}

func (s *Server) toComment(st *store.Store, repo store.Repo, is store.Issue, ev store.Event) apiComment {
	owner := st.Config().Owner
	kind := "issues"
	if is.IsPull {
		kind = "pulls"
	}
	html := fmt.Sprintf("%s/%s/%s/%s/%d", s.apiBase(), owner, repo.Name, kind, is.Number)
	out := apiComment{
		ID: eventID(ev), HTMLURL: html, IssueURL: s.apiURL("repos", owner, repo.Name, "issues", strconv.Itoa(is.Number)),
		User: s.toUser(ev.User), Body: ev.Body, Assets: []apiAttachment{},
		CreatedAt: apiTime(ev.Time), UpdatedAt: apiTime(updatedTime(ev)),
	}
	if is.IsPull {
		out.PullURL = s.apiURL("repos", owner, repo.Name, "pulls", strconv.Itoa(is.Number))
	}
	return out
}

// commentEvents は本文を持つイベント（コメント、本文付きのクローズ・再オープン、本文付きのレビュー）を返す。
func commentEvents(is store.Issue) []store.Event {
	var out []store.Event
	for _, ev := range is.Events {
		if ev.Deleted {
			continue
		}
		switch ev.Type {
		case store.EventComment:
			out = append(out, ev)
		case store.EventClosed, store.EventReopened, store.EventReview:
			if ev.Body != "" {
				out = append(out, ev)
			}
		}
	}
	return out
}

func (s *Server) apiComments(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	out := []apiComment{}
	for _, ev := range commentEvents(is) {
		out = append(out, s.toComment(st, repo, is, ev))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) apiCreateComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	var in struct {
		Body string `json:"body"`
	}
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	if err := s.addComment(st, repo, is, in.Body); err != nil {
		s.apiFail(w, err)
		return
	}
	is, _ = st.GetIssue(repo.Name, is.Number)
	evs := commentEvents(is)
	if len(evs) == 0 {
		s.apiError(w, http.StatusInternalServerError, "comment was written but could not be read back")
		return
	}
	writeJSON(w, http.StatusCreated, s.toComment(st, repo, is, evs[len(evs)-1]))
}

// findComment はコメントの ID から Issue とイベントを探す。
func (s *Server) findComment(st *store.Store, repo store.Repo, id int64) (store.Issue, store.Event, bool) {
	issues, err := st.ListIssues(repo.Name)
	if err != nil {
		return store.Issue{}, store.Event{}, false
	}
	for _, is := range issues {
		for _, ev := range commentEvents(is) {
			if eventID(ev) == id {
				return is, ev, true
			}
		}
	}
	return store.Issue{}, store.Event{}, false
}

// apiGetComment は Gitea の GET /issues/comments/{id}。
func (s *Server) apiGetComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, id int64) {
	is, ev, found := s.findComment(st, repo, id)
	if !found {
		s.apiError(w, http.StatusNotFound, "comment not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toComment(st, repo, is, ev))
}

// apiEditComment は Gitea の PATCH /issues/comments/{id}。body を書き換える。
func (s *Server) apiEditComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, id int64) {
	is, ev, found := s.findComment(st, repo, id)
	if !found {
		s.apiError(w, http.StatusNotFound, "comment not found")
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	if err := s.editBody(st, repo, is, ev.File, in.Body); err != nil {
		s.apiFail(w, err)
		return
	}
	is, _ = st.GetIssue(repo.Name, is.Number)
	for _, cur := range commentEvents(is) {
		if cur.File == ev.File {
			writeJSON(w, http.StatusOK, s.toComment(st, repo, is, cur))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiDeleteComment は Gitea の DELETE /issues/comments/{id}。
func (s *Server) apiDeleteComment(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, id int64) {
	is, ev, found := s.findComment(st, repo, id)
	if !found {
		s.apiError(w, http.StatusNotFound, "comment not found")
		return
	}
	if err := s.deleteComment(st, repo, is, ev.File); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- issue labels ----

func (s *Server) apiIssueLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	writeJSON(w, http.StatusOK, s.toLabels(st, repo, is.Labels))
}

func (s *Server) readLabelIDs(r *http.Request) ([]int, error) {
	var in struct {
		Labels []int64 `json:"labels"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return toInts(in.Labels), nil
}

func (s *Server) apiIssueReplaceLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	ids, err := s.readLabelIDs(r)
	if err != nil {
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.setLabels(st, repo, is, ids); err != nil {
		s.apiFail(w, err)
		return
	}
	is, _ = st.GetIssue(repo.Name, is.Number)
	writeJSON(w, http.StatusOK, s.toLabels(st, repo, is.Labels))
}

func (s *Server) apiIssueAddLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	ids, err := s.readLabelIDs(r)
	if err != nil {
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.setLabels(st, repo, is, append(append([]int(nil), is.Labels...), ids...)); err != nil {
		s.apiFail(w, err)
		return
	}
	is, _ = st.GetIssue(repo.Name, is.Number)
	writeJSON(w, http.StatusOK, s.toLabels(st, repo, is.Labels))
}

func (s *Server) apiIssueClearLabels(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	if err := s.setLabels(st, repo, is, nil); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiIssueRemoveLabel(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	id, ok := labelID(r)
	if !ok {
		s.apiError(w, http.StatusNotFound, "label not found")
		return
	}
	var next []int
	for _, x := range is.Labels {
		if x != id {
			next = append(next, x)
		}
	}
	if err := s.setLabels(st, repo, is, next); err != nil {
		s.apiFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- assets（添付ファイル）----

func (s *Server) toAttachment(st *store.Store, repo store.Repo, is store.Issue, a store.Attachment) apiAttachment {
	owner := st.Config().Owner
	return apiAttachment{
		ID: stableID("attachment", owner, repo.Name, strconv.Itoa(is.Number), a.Name), Name: a.Name, Size: a.Size,
		CreatedAt: apiTime(a.ModTime), UUID: a.Name,
		DownloadURL: fmt.Sprintf("%s/attachments/%s/%s/%d/%s", s.apiBase(), url.PathEscape(owner), url.PathEscape(repo.Name), is.Number, url.PathEscape(a.Name)),
	}
}

func (s *Server) apiAssets(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	list, err := st.ListAttachments(repo.Name, is.Number)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiAttachment{}
	for _, a := range list {
		out = append(out, s.toAttachment(st, repo, is, a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiGetAsset(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	list, err := st.ListAttachments(repo.Name, is.Number)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	id := r.PathValue("id")
	for _, a := range list {
		at := s.toAttachment(st, repo, is, a)
		if a.Name == id || strconv.FormatInt(at.ID, 10) == id {
			writeJSON(w, http.StatusOK, at)
			return
		}
	}
	s.apiError(w, http.StatusNotFound, "attachment not found")
}

// apiCreateAsset は multipart の attachment（Gitea と同じ項目名）を保存する。?name= があればその名前にする。
func (s *Server) apiCreateAsset(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.apiError(w, http.StatusRequestEntityTooLarge, err.Error())
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
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	name := fh.Filename
	if q := r.URL.Query().Get("name"); q != "" {
		name = q
	}
	saved, err := st.SaveAttachment(repo.Name, is.Number, name, data)
	if err != nil {
		s.apiError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	list, _ := st.ListAttachments(repo.Name, is.Number)
	for _, a := range list {
		if a.Name == saved {
			writeJSON(w, http.StatusCreated, s.toAttachment(st, repo, is, a))
			return
		}
	}
	s.apiError(w, http.StatusInternalServerError, "saved but not found")
}
