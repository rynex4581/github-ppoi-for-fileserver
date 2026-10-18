package web

import (
	"net/http"
	"regexp"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// repoSettingsView はリポジトリの Settings 画面のデータ。
type repoSettingsView struct {
	Repo     store.Repo
	Branches []gitcmd.Branch
	Saved    bool
	BaseURL  string // /{owner}/{repo}/settings
}

var branchNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

func (s *Server) renderRepoSettings(w http.ResponseWriter, status int, st *store.Store, repo store.Repo, saved bool, errMsg string) {
	branches, err := s.gitRepo(st, repo).Branches()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := s.repoPage(s.tr("repo.settings"), repo, "settings")
	p.Error = errMsg
	p.Data = repoSettingsView{Repo: repo, Branches: branches, Saved: saved, BaseURL: s.repoURL(st, repo) + "/settings"}
	s.render(w, status, "repo_settings", p)
}

func (s *Server) handleRepoSettings(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	s.renderRepoSettings(w, http.StatusOK, st, repo, r.URL.Query().Get("saved") == "1", "")
}

// handleRepoMaintenance はベアリポジトリの gc を実行する。
func (s *Server) handleRepoMaintenance(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	if st.ReadOnly() {
		s.renderRepoSettings(w, http.StatusForbidden, st, repo, false, s.tr("ppoi.readonly.write"))
		return
	}
	if err := s.gitRepo(st, repo).Maintenance(); err != nil {
		s.renderRepoSettings(w, http.StatusInternalServerError, st, repo, false, err.Error())
		return
	}
	http.Redirect(w, r, s.repoURL(st, repo)+"/settings?saved=1", http.StatusSeeOther)
}

// handleRepoSettingsSave は repo.json を書き換え、ベアリポジトリ側の設定も合わせる。
// force push 禁止は receive.denyNonFastForwards、既定ブランチは HEAD に反映する。
func (s *Server) handleRepoSettingsSave(w http.ResponseWriter, r *http.Request, st *store.Store, repo store.Repo) {
	// 読み取り専用なら bare の設定（denyNonFastForwards、HEAD）にも触らない
	if st.ReadOnly() {
		s.renderRepoSettings(w, http.StatusForbidden, st, repo, false, s.tr("ppoi.readonly.write"))
		return
	}
	next := repo
	next.Description = strings.TrimSpace(r.FormValue("description"))
	next.DefaultBranch = strings.TrimSpace(r.FormValue("default_branch"))
	next.RequireApproval = r.FormValue("require_approval") == "1"
	next.DismissStaleApproval = r.FormValue("dismiss_stale_approval") == "1"
	next.DenyForcePush = r.FormValue("deny_force_push") == "1"

	if next.DefaultBranch == "" || !branchNamePattern.MatchString(next.DefaultBranch) || strings.Contains(next.DefaultBranch, "..") {
		s.renderRepoSettings(w, http.StatusUnprocessableEntity, st, next, false, s.tr("ppoi.settings.invalid_branch"))
		return
	}

	g := s.gitRepo(st, repo)
	if next.DenyForcePush != repo.DenyForcePush {
		if err := g.SetDenyNonFastForwards(next.DenyForcePush); err != nil {
			s.renderRepoSettings(w, http.StatusInternalServerError, st, next, false, err.Error())
			return
		}
	}
	if next.DefaultBranch != repo.DefaultBranch {
		// ブランチが1つでもあるリポジトリでは、存在するブランチしか既定にしない（clone 直後の checkout が失敗する）
		if branches, err := g.Branches(); err == nil && len(branches) > 0 && !branchExists(branches, next.DefaultBranch) {
			s.renderRepoSettings(w, http.StatusUnprocessableEntity, st, next, false, s.tr("ppoi.code.ref_not_found"))
			return
		}
		if err := g.SetHead(next.DefaultBranch); err != nil {
			s.renderRepoSettings(w, http.StatusInternalServerError, st, next, false, err.Error())
			return
		}
	}
	if err := st.UpdateRepo(next); err != nil {
		s.renderRepoSettings(w, http.StatusInternalServerError, st, next, false, err.Error())
		return
	}
	http.Redirect(w, r, s.repoURL(st, repo)+"/settings?saved=1", http.StatusSeeOther)
}
