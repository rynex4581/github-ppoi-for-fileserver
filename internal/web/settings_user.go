package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github-ppoi-for-fileserver/internal/i18n"
	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// rootRow は自分の設定画面に出すデータルート1件。
type rootRow struct {
	Path      string
	Owner     string
	OwnerName string
	Error     string // 開けなかったときの理由
}

type settingsForm struct {
	Roots       []rootRow
	Name        string // 表示名
	Lang        string
	Langs       []string
	ConfigPath  string
	Saved       bool
	NewPath     string
	NewOwner    string
	NewOwnerNm  string
	ShowAddForm bool
}

func (s *Server) settingsData() settingsForm {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := settingsForm{Lang: s.loc.Lang(), Langs: i18n.Langs, Name: s.displayNameLocked(s.login)}
	f.ConfigPath, _ = localconfig.Path()
	for _, root := range s.cfg.DataRoots {
		row := rootRow{Path: root}
		if st := s.storeByRootLocked(root); st != nil {
			row.Owner = st.Config().Owner
			row.OwnerName = st.Config().OwnerName
		} else {
			row.Error = s.rootErrs[root]
		}
		f.Roots = append(f.Roots, row)
	}
	if f.Name == s.login {
		f.Name = ""
	}
	return f
}

func (s *Server) renderSettings(w http.ResponseWriter, status int, f settingsForm, errMsg string) {
	p := s.newPage(s.tr("ppoi.settings.title"))
	p.Error = errMsg
	p.Data = f
	s.render(w, status, "settings", p)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	f := s.settingsData()
	f.Saved = r.URL.Query().Get("saved") == "1"
	f.ShowAddForm = len(f.Roots) == 0 || r.URL.Query().Get("add") == "1"
	s.renderSettings(w, http.StatusOK, f, "")
}

// handleSettingsSave は表示名と言語を保存する。表示名は登録されている全データルートに書く。
func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	lang := r.FormValue("lang")
	if !i18n.IsSupported(lang) {
		lang = localconfig.DefaultLang
	}
	loc, err := i18n.Load(lang)
	if err != nil {
		s.renderSettings(w, http.StatusInternalServerError, s.settingsData(), err.Error())
		return
	}
	var failed []string
	for _, st := range s.allStores() {
		if err := st.SetUserName(s.login, name); err != nil {
			failed = append(failed, st.Config().Owner+": "+err.Error())
		}
	}

	s.mu.Lock()
	s.cfg.Lang = lang
	s.loc = loc
	cfg := s.cfg
	s.mu.Unlock()

	if err := localconfig.Save(cfg); err != nil {
		s.renderSettings(w, http.StatusInternalServerError, s.settingsData(), s.tr("ppoi.error.save_failed", err.Error()))
		return
	}
	if len(failed) > 0 {
		s.renderSettings(w, http.StatusInternalServerError, s.settingsData(), s.tr("ppoi.settings.name_write_failed", strings.Join(failed, " / ")))
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// handleRootAdd はデータルートを登録する。
// 指定のフォルダに config.json があればそのまま開き、無ければ owner 名を必須として初期化する。
func (s *Server) handleRootAdd(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.FormValue("path"))
	owner := strings.TrimSpace(r.FormValue("owner"))
	ownerName := strings.TrimSpace(r.FormValue("owner_name"))

	fail := func(status int, msg string) {
		f := s.settingsData()
		f.ShowAddForm = true
		f.NewPath, f.NewOwner, f.NewOwnerNm = root, owner, ownerName
		s.renderSettings(w, status, f, msg)
	}
	if root == "" {
		fail(http.StatusUnprocessableEntity, s.tr("ppoi.error.data_root_required"))
		return
	}
	root = filepath.Clean(root)
	s.mu.RLock()
	already := s.cfg.HasRoot(root)
	s.mu.RUnlock()
	if already {
		fail(http.StatusUnprocessableEntity, s.tr("ppoi.settings.root_already"))
		return
	}

	var st *store.Store
	var err error
	if _, statErr := os.Stat(filepath.Join(root, "config.json")); statErr == nil {
		st, err = s.openStore(root)
	} else {
		if owner == "" {
			fail(http.StatusUnprocessableEntity, s.tr("ppoi.settings.owner_required"))
			return
		}
		// 初期化してしまう前に、同じ owner 名が無いか確かめる
		if other := s.storeByOwner(owner); other != nil {
			fail(http.StatusUnprocessableEntity, s.tr("ppoi.settings.duplicate_owner", owner, other.Root()))
			return
		}
		st, err = store.Create(root, store.Config{Owner: owner, OwnerName: ownerName})
		if err == nil {
			st, err = s.openStore(root)
		}
	}
	if err != nil {
		fail(http.StatusUnprocessableEntity, err.Error())
		return
	}
	if other := s.storeByOwner(st.Config().Owner); other != nil {
		fail(http.StatusUnprocessableEntity, s.tr("ppoi.settings.duplicate_owner", st.Config().Owner, other.Root()))
		return
	}

	s.mu.Lock()
	s.cfg.DataRoots = append(s.cfg.DataRoots, root)
	s.stores = append(s.stores, st)
	cfg := s.cfg
	s.mu.Unlock()

	if err := localconfig.Save(cfg); err != nil {
		fail(http.StatusInternalServerError, s.tr("ppoi.error.save_failed", err.Error()))
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// handleRootRemove はデータルートの登録を外す。フォルダの中身には触らない。
func (s *Server) handleRootRemove(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.FormValue("path"))

	s.mu.Lock()
	var roots []string
	for _, x := range s.cfg.DataRoots {
		if x != root {
			roots = append(roots, x)
		}
	}
	s.cfg.DataRoots = roots
	var stores []*store.Store
	for _, st := range s.stores {
		if st.Root() != root {
			stores = append(stores, st)
		}
	}
	s.stores = stores
	delete(s.rootErrs, root)
	cfg := s.cfg
	s.mu.Unlock()

	if err := localconfig.Save(cfg); err != nil {
		s.renderSettings(w, http.StatusInternalServerError, s.settingsData(), s.tr("ppoi.error.save_failed", err.Error()))
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
