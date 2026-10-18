package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github-ppoi-for-fileserver/internal/store"
)

// crossRow は横断一覧の1行。
type crossRow struct {
	store.Issue
	Owner    string
	RepoName string
	URL      string
	Labels   []store.Label
}

type crossView struct {
	IsPull      bool
	Type        string // all / assigned / created
	State       string
	Query       string // 検索語。空なら絞り込まない
	Rows        []crossRow
	OpenCount   int
	ClosedCount int
	BaseURL     string
}

func (v crossView) url(typ, state string) string {
	u := fmt.Sprintf("%s?type=%s&state=%s", v.BaseURL, typ, state)
	if v.Query != "" {
		u += "&q=" + url.QueryEscape(v.Query)
	}
	return u
}

// issueMatches は検索語がタイトル、本文、コメント（レビューと行コメントを含む）のどれかに
// 含まれるかを返す。大文字小文字は区別しない。削除したコメントは見ない。
func issueMatches(is store.Issue, q string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	if strings.Contains(strings.ToLower(is.Title), q) || strings.Contains(strings.ToLower(is.Body), q) {
		return true
	}
	for _, ev := range is.Events {
		if ev.Deleted || ev.Body == "" {
			continue
		}
		switch ev.Type {
		case store.EventComment, store.EventReview, store.EventReviewComment:
			if strings.Contains(strings.ToLower(ev.Body), q) {
				return true
			}
		}
	}
	return false
}

// URLFor はテンプレート用。種類と状態を差し替えたURLを返す。
func (v crossView) URLFor(typ, state string) string {
	if typ == "" {
		typ = v.Type
	}
	if state == "" {
		state = v.State
	}
	return v.url(typ, state)
}

func (s *Server) handleCrossIssues(w http.ResponseWriter, r *http.Request) {
	s.listCross(w, r, false)
}

func (s *Server) handleCrossPulls(w http.ResponseWriter, r *http.Request) {
	s.listCross(w, r, true)
}

// listCross は全データルートをまたいだ Issue または Pull Request の一覧を出す。
func (s *Server) listCross(w http.ResponseWriter, r *http.Request, isPull bool) {
	if !s.requireStores(w, r) {
		return
	}
	v := crossView{IsPull: isPull, Type: r.URL.Query().Get("type"), State: r.URL.Query().Get("state"), Query: strings.TrimSpace(r.URL.Query().Get("q")), BaseURL: "/issues"}
	if isPull {
		v.BaseURL = "/pulls"
	}
	if v.Type != "assigned" && v.Type != "created" {
		v.Type = "all"
	}
	if v.State != "closed" {
		v.State = "open"
	}
	for _, st := range s.allStores() {
		repos, err := st.ListRepos()
		if err != nil {
			continue
		}
		owner := st.Config().Owner
		for _, repo := range repos {
			issues, err := st.ListIssues(repo.Name)
			if err != nil {
				continue
			}
			labels, _ := st.ListLabels(repo.Name)
			byID := labelsByID(labels)
			for _, is := range issues {
				if is.IsPull != isPull {
					continue
				}
				switch v.Type {
				case "assigned":
					if !is.HasAssignee(s.login) {
						continue
					}
				case "created":
					if !strings.EqualFold(is.User, s.login) {
						continue
					}
				}
				if !issueMatches(is, v.Query) {
					continue
				}
				if is.State == "open" {
					v.OpenCount++
				} else {
					v.ClosedCount++
				}
				if is.State != v.State {
					continue
				}
				kind := "issues"
				if isPull {
					kind = "pulls"
				}
				v.Rows = append(v.Rows, crossRow{
					Issue: is, Owner: owner, RepoName: repo.Name,
					URL:    fmt.Sprintf("/%s/%s/%s/%d", owner, repo.Name, kind, is.Number),
					Labels: resolveLabels(is.Labels, byID),
				})
			}
		}
	}
	sort.SliceStable(v.Rows, func(i, j int) bool { return v.Rows[i].UpdatedAt.After(v.Rows[j].UpdatedAt) })

	title := s.tr("repo.issues")
	if isPull {
		title = s.tr("repo.pulls")
	}
	p := s.newPage(title)
	p.Tab = v.BaseURL
	p.Data = v
	s.render(w, http.StatusOK, "cross", p)
}
