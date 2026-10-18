package dumprepo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// 本物の Gitea に restore-repo して取り込めることを確かめる。
// 環境変数 GITEA_BIN に Gitea の実行ファイル（Linux 用。参照している版は v28.1.0）のパスを入れたときだけ動く。
// Gitea を一時フォルダに SQLite で立ち上げ、管理者を作り、export の出力を restore-repo --validation で
// 取り込んで、API で件数を確かめる。
func TestRestoreIntoGitea(t *testing.T) {
	bin := os.Getenv("GITEA_BIN")
	if bin == "" {
		t.Skip("GITEA_BIN が無い")
	}
	st, _ := sampleRoot(t)
	out := filepath.Join(t.TempDir(), "dump")
	var log bytes.Buffer
	dir, err := Export(st, "tool", out, &log)
	if err != nil {
		t.Fatalf("export: %v\n%s", err, log.String())
	}

	work := t.TempDir()
	port := freePort(t)
	appINI := fmt.Sprintf(`APP_NAME = test
RUN_MODE = prod
WORK_PATH = %[1]s

[server]
HTTP_PORT = %[2]d
ROOT_URL = http://127.0.0.1:%[2]d/
DISABLE_SSH = true
OFFLINE_MODE = true

[database]
DB_TYPE = sqlite3
PATH = %[1]s/gitea.db

[repository]
ROOT = %[1]s/repos

[security]
INSTALL_LOCK = true
INTERNAL_TOKEN = %[3]s

[log]
MODE = file
LEVEL = Warn
ROOT_PATH = %[1]s/log

[service]
DISABLE_REGISTRATION = true
`, work, port, "dGVzdC1pbnRlcm5hbC10b2tlbi1mb3ItcmVzdG9yZS10ZXN0LW9ubHktMDEyMzQ1Njc4OWFiY2RlZg")
	custom := filepath.Join(work, "custom", "conf")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "app.ini"), []byte(appINI), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GITEA_WORK_DIR="+work, "GITEA_CUSTOM="+filepath.Join(work, "custom"))
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Dir = work
		outb, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("gitea %v: %v\n%s", args, err, outb)
		}
		return string(outb)
	}
	run("migrate")
	run("admin", "user", "create", "--admin", "--username", "admin", "--password", "password-for-test-only", "--email", "admin@example.invalid", "--must-change-password=false")

	web := exec.Command(bin, "web")
	web.Env = env
	web.Dir = work
	var webOut bytes.Buffer
	web.Stdout, web.Stderr = &webOut, &webOut
	if err := web.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		web.Process.Kill()
		web.Wait()
	}()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(60 * time.Second)
	for {
		if resp, err := http.Get(base + "/api/v1/version"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Gitea が上がらない:\n%s", webOut.String())
		}
		time.Sleep(500 * time.Millisecond)
	}

	outb := run("restore-repo", "--repo_dir", dir, "--owner_name", "admin", "--repo_name", "tool", "--validation")
	t.Logf("restore-repo: %s", outb)

	get := func(path string, v any) {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"/api/v1"+path, nil)
		req.SetBasicAuth("admin", "password-for-test-only")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
		}
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("GET %s: %v\n%s", path, err, body)
		}
	}
	var issues, pulls, labels, releases, tags, comments, reviews []map[string]any
	get("/repos/admin/tool/issues?state=all&type=issues", &issues)
	get("/repos/admin/tool/pulls?state=all", &pulls)
	get("/repos/admin/tool/labels", &labels)
	get("/repos/admin/tool/releases", &releases)
	get("/repos/admin/tool/tags", &tags)
	get("/repos/admin/tool/issues/1/comments", &comments)
	get("/repos/admin/tool/pulls/3/reviews", &reviews)
	if len(issues) != 1 || issues[0]["title"] != "直した題" || issues[0]["state"] != "closed" {
		t.Errorf("Gitea 側の Issue が違う: %v", issues)
	}
	if len(pulls) != 2 {
		t.Fatalf("Gitea 側の PR が %d 件", len(pulls))
	}
	merged := 0
	for _, p := range pulls {
		if p["merged"] == true {
			merged++
		}
	}
	if merged != 1 {
		t.Errorf("マージ済みの PR が %d 件", merged)
	}
	if len(labels) != 1 || labels[0]["name"] != "bug" {
		t.Errorf("Gitea 側のラベルが違う: %v", labels)
	}
	if len(releases) != 1 || releases[0]["tag_name"] != "v1.0" {
		t.Errorf("Gitea 側のリリースが違う: %v", releases)
	} else if assets, _ := releases[0]["assets"].([]any); len(assets) != 1 {
		t.Errorf("リリースの添付が %d 件", len(assets))
	}
	if len(tags) != 1 {
		t.Errorf("タグが %d 件", len(tags))
	}
	if len(comments) != 2 {
		t.Errorf("Issue #1 のコメントが %d 件（本文付きクローズをコメントに分けるので 2 件のはず）: %v", len(comments), comments)
	}
	if len(reviews) != 1 || reviews[0]["state"] != "REQUEST_CHANGES" {
		t.Errorf("PR #3 のレビューが違う: %v", reviews)
	} else if id, ok := reviews[0]["id"].(float64); ok {
		var rc []map[string]any
		get("/repos/admin/tool/pulls/3/reviews/"+strconv.Itoa(int(id))+"/comments", &rc)
		if len(rc) != 1 || rc[0]["path"] != "b.txt" {
			t.Errorf("行コメントが違う: %v", rc)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
