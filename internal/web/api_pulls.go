package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

func (s *Server) toPull(st *store.Store, repo store.Repo, is store.Issue) apiPull {
	owner := st.Config().Owner
	g := s.gitRepo(st, repo)
	html := fmt.Sprintf("%s/%s/%s/pulls/%d", s.apiBase(), owner, repo.Name, is.Number)
	apiRepo := s.toRepo(st, repo)
	baseSHA, _ := g.ResolveCommit("refs/heads/" + is.Base)
	headSHA, _ := g.ResolveCommit("refs/heads/" + is.Head)
	if headSHA == "" {
		headSHA = is.MergedHeadSHA
		if headSHA == "" {
			headSHA = is.HeadSHA
		}
	}
	mergeBase := ""
	if baseSHA != "" && headSHA != "" {
		mergeBase, _ = g.MergeBase(baseSHA, headSHA)
	}
	out := apiPull{
		ID: int64(is.Number), Number: is.Number,
		URL:  s.apiURL("repos", owner, repo.Name, "pulls", strconv.Itoa(is.Number)),
		User: s.toUser(is.User), Title: is.Title, Body: is.Body,
		Labels: s.toLabels(st, repo, is.Labels), Assignees: s.toUsers(is.Assignees),
		State: is.State, Comments: is.Comments,
		HTMLURL: html, DiffURL: html + ".diff", PatchURL: html + ".patch",
		Merged: is.Merged, MergedAt: apiTimePtr(is.MergedAt),
		Base:      apiPRBranch{Label: is.Base, Ref: is.Base, SHA: baseSHA, RepoID: apiRepo.ID, Repo: apiRepo},
		Head:      apiPRBranch{Label: is.Head, Ref: is.Head, SHA: headSHA, RepoID: apiRepo.ID, Repo: apiRepo},
		MergeBase: mergeBase,
		CreatedAt: apiTime(is.CreatedAt), UpdatedAt: apiTime(is.UpdatedAt), ClosedAt: apiTimePtr(is.ClosedAt),
	}
	if len(out.Assignees) > 0 {
		a := out.Assignees[0]
		out.Assignee = &a
	}
	if is.Merged {
		sha := is.MergeSHA
		out.MergeCommitSHA = &sha
		u := s.toUser(is.MergedBy)
		out.MergedBy = &u
	}
	if is.State == "open" && headSHA != "" && baseSHA != "" {
		conflict, _ := g.HasConflict(baseSHA, headSHA)
		out.Mergeable = !conflict && !g.IsAncestor(headSHA, baseSHA)
	}
	return out
}

func (s *Server) apiPulls(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	all, err := st.ListIssues(repo.Name)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	out := []apiPull{}
	for _, is := range s.filterIssues(r, all, true) {
		out = append(out, s.toPull(st, repo, is))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

type pullInput struct {
	issueInput
	Head string `json:"head"`
	Base string `json:"base"`
}

func (s *Server) apiCreatePull(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	var in pullInput
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
	// Gitea は head を "owner:branch" の形でも受けるので、コロンの前を落とす
	if i := strings.Index(in.Head, ":"); i >= 0 {
		in.Head = in.Head[i+1:]
	}
	labels, _ := st.ListLabels(repo.Name)
	ids := parseLabelIDs(intsToStrings(toInts(in.Labels)), labels)
	users, _ := st.ListUsers()
	assignees := parseAssignees(in.assigneeList(), users)
	pr, err := s.createPull(st, repo, title, body, in.Head, in.Base, ids, assignees)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toPull(st, repo, pr))
}

func (s *Server) apiGetPull(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	if idx := r.PathValue("index"); strings.HasSuffix(idx, ".diff") || strings.HasSuffix(idx, ".patch") {
		s.apiPullDiff(w, r, st, repo, is)
		return
	}
	writeJSON(w, http.StatusOK, s.toPull(st, repo, is))
}

func (s *Server) apiEditPull(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	s.apiEditIssue(w, r, st, repo, is)
}

// pullRange は差分と履歴の範囲（from..to）を返す。マージ済みならマージ時点の範囲。
func (s *Server) pullRange(st *store.Store, repo store.Repo, is store.Issue) (from, to string, ok bool) {
	g := s.gitRepo(st, repo)
	if is.Merged && is.MergeSHA != "" {
		return is.MergeSHA + "^1", is.MergedHeadSHA, true
	}
	if sha, err := g.ResolveCommit("refs/heads/" + is.Head); err == nil {
		return "refs/heads/" + is.Base, sha, true
	}
	if _, err := g.ResolveCommit(is.HeadSHA); err == nil {
		return "refs/heads/" + is.Base, is.HeadSHA, true
	}
	return "", "", false
}

func (s *Server) apiPullDiff(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	from, to, ok := s.pullRange(st, repo, is)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !ok {
		return
	}
	raw, err := s.gitRepo(st, repo).RangeDiff(from, to)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	w.Write([]byte(raw))
}

func (s *Server) apiPullCommits(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	out := []apiCommit{}
	if from, to, ok := s.pullRange(st, repo, is); ok {
		commits, err := s.gitRepo(st, repo).RangeLog(from, to, 250)
		if err != nil {
			s.apiFail(w, err)
			return
		}
		for _, c := range commits {
			out = append(out, s.toCommit(st, repo, c))
		}
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) apiPullFiles(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	out := []apiChangedFile{}
	if from, to, ok := s.pullRange(st, repo, is); ok {
		raw, err := s.gitRepo(st, repo).RangeDiff(from, to)
		if err != nil {
			s.apiFail(w, err)
			return
		}
		out = s.toChangedFiles(st, repo, to, gitcmd.ParseDiff(raw))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) apiPullIsMerged(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	if is.Merged {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.apiError(w, http.StatusNotFound, "pull request has not been merged")
}

// apiMergePull は Gitea の POST /pulls/{index}/merge。Do は merge だけを受け付ける。
func (s *Server) apiMergePull(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	var in struct {
		Do           string `json:"Do"`
		MergeMessage string `json:"MergeMessageField"`
		MergeTitle   string `json:"MergeTitleField"`
	}
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	if in.Do != "merge" {
		s.apiError(w, http.StatusUnprocessableEntity, "Do=merge is required (only --no-ff merge commit is supported)")
		return
	}
	message := in.MergeMessage
	if in.MergeTitle != "" {
		message = in.MergeTitle
		if in.MergeMessage != "" {
			message += "\n\n" + in.MergeMessage
		}
	}
	warn, err := s.mergePull(st, repo, is, message)
	if err != nil {
		s.apiFail(w, err)
		return
	}
	if warn != "" {
		w.Header().Set("X-Github-Ppoi-Warning", warn)
	}
	w.WriteHeader(http.StatusOK)
}

// ---- reviews ----

func reviewState(state string) string {
	switch state {
	case store.ReviewApproved:
		return "APPROVED"
	case store.ReviewChangesRequested:
		return "REQUEST_CHANGES"
	}
	return "COMMENT"
}

func (s *Server) toReview(st *store.Store, repo store.Repo, is store.Issue, ev store.Event) apiReview {
	owner := st.Config().Owner
	pullURL := s.apiURL("repos", owner, repo.Name, "pulls", strconv.Itoa(is.Number))
	return apiReview{
		ID: eventID(ev), User: s.toUser(ev.User), State: reviewState(ev.State), Body: ev.Body, CommitID: ev.CommitSHA,
		PullURL: pullURL, SubmittedAt: apiTime(ev.Time), CreatedAt: apiTime(ev.Time), UpdatedAt: apiTime(ev.Time),
		HTMLURL: fmt.Sprintf("%s/%s/%s/pulls/%d", s.apiBase(), owner, repo.Name, is.Number),
	}
}

func (s *Server) toReviewComment(st *store.Store, repo store.Repo, is store.Issue, ev store.Event, reviewID int64) apiReviewComment {
	owner := st.Config().Owner
	out := apiReviewComment{
		ID: eventID(ev), Body: ev.Body, Path: ev.Path, CommitID: ev.CommitSHA, OrigCommit: ev.CommitSHA, DiffHunk: ev.DiffHunk,
		User: s.toUser(ev.User), CreatedAt: apiTime(ev.Time), UpdatedAt: apiTime(ev.Time),
		HTMLURL: fmt.Sprintf("%s/%s/%s/pulls/%d/files", s.apiBase(), owner, repo.Name, is.Number),
		PullURL: s.apiURL("repos", owner, repo.Name, "pulls", strconv.Itoa(is.Number)),
		Side:    ev.Side, Line: ev.Line, ReviewID: reviewID,
	}
	if ev.Side == "old" {
		out.OrigPos = ev.Line
	} else {
		out.Position = ev.Line
	}
	return out
}

func reviewEvents(is store.Issue) []store.Event {
	var out []store.Event
	for _, ev := range is.Events {
		if ev.Type == store.EventReview {
			out = append(out, ev)
		}
	}
	return out
}

func (s *Server) apiReviews(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	out := []apiReview{}
	for _, ev := range reviewEvents(is) {
		out = append(out, s.toReview(st, repo, is, ev))
	}
	writeJSON(w, http.StatusOK, paginate(w, r, out))
}

func (s *Server) findReview(is store.Issue, id string) (store.Event, bool) {
	for _, ev := range reviewEvents(is) {
		if strconv.FormatInt(eventID(ev), 10) == id {
			return ev, true
		}
	}
	return store.Event{}, false
}

func (s *Server) apiGetReview(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	ev, ok := s.findReview(is, r.PathValue("id"))
	if !ok {
		s.apiError(w, http.StatusNotFound, "review not found")
		return
	}
	writeJSON(w, http.StatusOK, s.toReview(st, repo, is, ev))
}

// apiReviewComments はレビューに属する行コメントを返す。
// 行コメントはレビューに紐付けて保存していないため、そのレビューの直前（前のレビュー以降）に付いた行コメントを返す。
func (s *Server) apiReviewComments(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	target, ok := s.findReview(is, r.PathValue("id"))
	if !ok {
		s.apiError(w, http.StatusNotFound, "review not found")
		return
	}
	out := []apiReviewComment{}
	var pending []store.Event
	for _, ev := range is.Events {
		switch ev.Type {
		case store.EventReviewComment:
			pending = append(pending, ev)
		case store.EventReview:
			if ev.File == target.File {
				for _, c := range pending {
					out = append(out, s.toReviewComment(st, repo, is, c, eventID(target)))
				}
			}
			pending = nil
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiCreateReview は Gitea の POST /pulls/{index}/reviews。
// comments の各項目を行コメントとして保存し、続けて event（APPROVED / REQUEST_CHANGES / COMMENT）をレビューとして保存する。
func (s *Server) apiCreateReview(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo, is store.Issue) {
	var in struct {
		Body     string `json:"body"`
		Event    string `json:"event"`
		CommitID string `json:"commit_id"`
		Comments []struct {
			Path   string `json:"path"`
			Body   string `json:"body"`
			OldPos int    `json:"old_position"`
			NewPos int    `json:"new_position"`
		} `json:"comments"`
	}
	if !s.apiReadJSON(w, r, &in) {
		return
	}
	for _, c := range in.Comments {
		side, line := "new", c.NewPos
		if c.NewPos == 0 && c.OldPos > 0 {
			side, line = "old", c.OldPos
		}
		if err := s.addLineComment(st, repo, is, c.Path, side, line, c.Body); err != nil {
			s.apiFail(w, err)
			return
		}
	}
	state := store.ReviewComment
	switch strings.ToUpper(in.Event) {
	case "APPROVED":
		state = store.ReviewApproved
	case "REQUEST_CHANGES":
		state = store.ReviewChangesRequested
	}
	body := in.Body
	if state == store.ReviewComment && strings.TrimSpace(body) == "" && len(in.Comments) > 0 {
		// 行コメントだけのレビュー。本文の無いコメントのレビューは保存しない
		is, _ = st.GetIssue(repo.Name, is.Number)
		writeJSON(w, http.StatusOK, apiReview{User: s.toUser(s.login), State: "COMMENT", PullURL: s.apiURL("repos", st.Config().Owner, repo.Name, "pulls", strconv.Itoa(is.Number)), CommentsCount: len(in.Comments)})
		return
	}
	if err := s.addReview(st, repo, is, state, body); err != nil {
		s.apiFail(w, err)
		return
	}
	is, _ = st.GetIssue(repo.Name, is.Number)
	evs := reviewEvents(is)
	out := s.toReview(st, repo, is, evs[len(evs)-1])
	out.CommentsCount = len(in.Comments)
	writeJSON(w, http.StatusOK, out)
}
