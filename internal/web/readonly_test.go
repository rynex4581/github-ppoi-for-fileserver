package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// 読み取り専用（format が exe より新しい）のデータルートでは、git に触る操作も画面と API の両方で
// 止まり、bare が動かないことを確かめる。
func TestReadOnlyBlocksGitWrites(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	st, err := store.Create(root, store.Config{Owner: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "repos", "r.git")
	work := filepath.Join(t.TempDir(), "work")
	tgit(t, root, "clone", "--quiet", bare, work)
	twrite(t, work, "a.txt", "a\n")
	tgit(t, work, "add", ".")
	tgit(t, work, "commit", "--quiet", "-m", "first")
	tgit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	tgit(t, work, "checkout", "--quiet", "-b", "feature")
	twrite(t, work, "a.txt", "a\nb\n")
	tgit(t, work, "commit", "--quiet", "-am", "second")
	tgit(t, work, "push", "--quiet", "origin", "feature")
	g := gitcmd.Repo{Dir: bare}
	headSHA := sha(t, g, "refs/heads/feature")
	mainBefore := sha(t, g, "refs/heads/main")
	if _, err := st.CreatePull("r", "yamada", "t", "b", "feature", "main", headSHA, nil, nil); err != nil {
		t.Fatal(err)
	}
	st.FlushCache()

	// format を exe の知らない値にして開き直す
	cfgPath := filepath.Join(root, "config.json")
	var cfg map[string]any
	data, _ := os.ReadFile(cfgPath)
	json.Unmarshal(data, &cfg)
	cfg["format"] = 99
	data, _ = json.Marshal(cfg)
	os.WriteFile(cfgPath, data, 0o644)

	srv, err := New(localconfig.Config{DataRoots: []string{root}, Lang: "ja-JP"}, "yamada", 9999)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	do := func(method, path, ctype, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("Authorization", "token x")
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost:9999"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	w := do("GET", "/demo/r/pulls/1", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "/pulls/1/merge") {
		t.Fatalf("読み取り専用なのにマージの form が出ている: %d", w.Code)
	}
	w = do("POST", "/demo/r/pulls/1/merge", "application/x-www-form-urlencoded", "message=m")
	if w.Code != 403 {
		t.Fatalf("画面のマージが %d", w.Code)
	}
	w = do("POST", "/api/v1/repos/demo/r/pulls/1/merge", "application/json", `{"Do":"merge"}`)
	if w.Code != 403 {
		t.Fatalf("API のマージが %d: %s", w.Code, w.Body.String())
	}
	w = do("GET", "/demo/r/settings", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `type="submit"`) {
		t.Fatalf("読み取り専用なのに Settings の送信ボタンが出ている: %d", w.Code)
	}
	w = do("POST", "/demo/r/settings", "application/x-www-form-urlencoded", "default_branch=main")
	if w.Code != 403 {
		t.Fatalf("Settings の保存が %d", w.Code)
	}
	w = do("POST", "/demo/r/settings/maintenance", "application/x-www-form-urlencoded", "")
	if w.Code != 403 {
		t.Fatalf("メンテナンスが %d", w.Code)
	}
	if sha(t, g, "refs/heads/main") != mainBefore {
		t.Fatal("bare の main が動いている")
	}
	// 新14：PUT labels の Content-Type 違いは 415
	w = do("PUT", "/api/v1/repos/demo/r/issues/1/labels", "text/plain", `{"labels":[1]}`)
	if w.Code != 415 {
		t.Fatalf("labels の Content-Type 違いが %d", w.Code)
	}
}
