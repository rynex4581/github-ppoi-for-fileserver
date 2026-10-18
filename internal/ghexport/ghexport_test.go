package ghexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github-ppoi-for-fileserver/internal/store"
)

// テストで使う名前はすべて架空のもの。GitHub の API を模した小さなサーバで確かめる。

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test User", "-c", "user.email=test@example.com", "-c", "safe.directory=*", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakeGitHub は GitHub の REST API のうち export-github が使う分だけを模す。
type fakeGitHub struct {
	mu        sync.Mutex
	issues    []map[string]any // 番号順。PR も含む
	comments  map[int][]string
	labels    []string
	releases  []map[string]any
	assets    map[int64][]string
	patches   map[int][]map[string]any
	authOK    bool
	limitOnce bool // 真なら、次のコメント作成を 403（Retry-After: 1）で一度だけ弾く（新23）
	limited   int  // 弾いた回数
}

func (f *fakeGitHub) handler(t *testing.T, uploadBase *string) http.Handler {
	mux := http.NewServeMux()
	read := func(r *http.Request) map[string]any {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	check := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer test-token-0123" {
			w.WriteHeader(401)
			return false
		}
		f.authOK = true
		return true
	}
	mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	})
	mux.HandleFunc("GET /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.issues)
	})
	mux.HandleFunc("POST /repos/o/r/labels", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		m := read(r)
		name, _ := m["name"].(string)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, l := range f.labels {
			if l == name {
				w.WriteHeader(422)
				w.Write([]byte(`{"message":"Validation Failed"}`))
				return
			}
		}
		f.labels = append(f.labels, name)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(m)
	})
	create := func(w http.ResponseWriter, r *http.Request, isPull bool) {
		if !check(w, r) {
			return
		}
		m := read(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		m["number"] = len(f.issues) + 1
		m["is_pull"] = isPull
		f.issues = append(f.issues, m)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(m)
	}
	mux.HandleFunc("POST /repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) { create(w, r, false) })
	mux.HandleFunc("POST /repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) { create(w, r, true) })
	mux.HandleFunc("PATCH /repos/o/r/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		var n int
		fmt.Sscanf(r.PathValue("n"), "%d", &n)
		m := read(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		if n < 1 || n > len(f.issues) {
			w.WriteHeader(404)
			return
		}
		f.patches[n] = append(f.patches[n], m)
		for k, v := range m {
			f.issues[n-1][k] = v
		}
		json.NewEncoder(w).Encode(f.issues[n-1])
	})
	mux.HandleFunc("POST /repos/o/r/issues/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		var n int
		fmt.Sscanf(r.PathValue("n"), "%d", &n)
		m := read(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.limitOnce {
			f.limitOnce = false
			f.limited++
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(403)
			w.Write([]byte(`{"message":"You have exceeded a secondary rate limit."}`))
			return
		}
		if n < 1 || n > len(f.issues) {
			w.WriteHeader(404)
			return
		}
		f.comments[n] = append(f.comments[n], m["body"].(string))
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("GET /repos/o/r/releases/tags/{tag}", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, rel := range f.releases {
			if rel["tag_name"] == r.PathValue("tag") {
				json.NewEncoder(w).Encode(rel)
				return
			}
		}
		w.WriteHeader(404)
	})
	mux.HandleFunc("POST /repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		m := read(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		id := int64(len(f.releases) + 1)
		m["id"] = id
		m["upload_url"] = fmt.Sprintf("%s/uploads/releases/%d/assets{?name,label}", *uploadBase, id)
		f.releases = append(f.releases, m)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("POST /uploads/releases/{id}/assets", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		var id int64
		fmt.Sscanf(r.PathValue("id"), "%d", &id)
		data, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.assets[id] = append(f.assets[id], r.URL.Query().Get("name")+":"+fmt.Sprint(len(data)))
		w.WriteHeader(201)
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("想定外の呼び出し: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	})
	return mux
}

func TestExportToFakeGitHub(t *testing.T) {
	root := t.TempDir()
	st, err := store.Create(root, store.Config{Owner: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("tool", ""); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "repos", "tool.git")
	work := filepath.Join(t.TempDir(), "work")
	git(t, root, "clone", "--quiet", bare, work)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "first")
	git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	git(t, work, "checkout", "--quiet", "-b", "topic")
	os.WriteFile(filepath.Join(work, "b.txt"), []byte("b\n"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "topic")
	git(t, work, "push", "--quiet", "origin", "topic")
	topicSHA := git(t, work, "rev-parse", "HEAD")
	git(t, work, "tag", "v1.0", "main")
	git(t, work, "push", "--quiet", "origin", "v1.0")
	mainSHA := git(t, work, "rev-parse", "main")

	bug, _ := st.CreateLabel("tool", store.Label{Name: "bug", Color: "d73a4a"})
	t0 := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	// #1 Issue（ラベル、コメント、クローズ）、#2 欠番、#3 マージ済み PR、#4 open PR
	st.CreateIssueAt("tool", 1)
	st.WriteCreated("tool", 1, store.Event{User: "yamada", Title: "不具合", Body: "本文", Time: t0}, nil, nil)
	st.AppendEvent("tool", 1, store.Event{Type: store.EventLabels, User: "yamada", Labels: []int{bug.ID}, Time: t0})
	st.AppendEvent("tool", 1, store.Event{Type: store.EventComment, User: "suzuki", Body: "直します", Time: t0.Add(time.Hour)})
	st.AppendEvent("tool", 1, store.Event{Type: store.EventClosed, User: "suzuki", Time: t0.Add(2 * time.Hour)})
	st.CreateIssueAt("tool", 3)
	st.WriteCreated("tool", 3, store.Event{User: "yamada", Title: "済んだ PR", Body: "", IsPull: true, Head: "gone", Base: "main", HeadSHA: mainSHA, Time: t0}, nil, nil)
	st.AppendEvent("tool", 3, store.Event{Type: store.EventReview, User: "suzuki", State: store.ReviewApproved, CommitSHA: mainSHA, Body: "LGTM", Time: t0.Add(time.Hour)})
	st.AppendEvent("tool", 3, store.Event{Type: store.EventMerged, User: "suzuki", MergeSHA: mainSHA, HeadSHA: mainSHA, Time: t0.Add(2 * time.Hour)})
	st.CreateIssueAt("tool", 4)
	st.WriteCreated("tool", 4, store.Event{User: "yamada", Title: "作業中", Body: "", IsPull: true, Head: "topic", Base: "main", HeadSHA: topicSHA, Time: t0}, nil, nil)
	st.AppendEvent("tool", 4, store.Event{Type: store.EventReviewComment, User: "suzuki", Body: "ここ", Path: "b.txt", Line: 1, Side: "new", DiffHunk: "@@ -0,0 +1 @@\n+b", CommitSHA: topicSHA, Time: t0.Add(time.Hour)})
	if _, err := st.CreateReleaseAt("tool", "yamada", "v1.0", mainSHA, "版 1.0", "ノート", false, t0); err != nil {
		t.Fatal(err)
	}
	st.SaveReleaseAttachment("tool", "v1.0", "tool.zip", []byte("zip-bytes"))

	fake := &fakeGitHub{comments: map[int][]string{}, assets: map[int64][]string{}, patches: map[int][]map[string]any{}, limitOnce: true}
	var base string
	srv := httptest.NewServer(fake.handler(t, &base))
	defer srv.Close()
	base = srv.URL
	remote := filepath.Join(t.TempDir(), "github.git")
	git(t, t.TempDir(), "init", "--quiet", "--bare", remote)

	var log bytes.Buffer
	res, err := Export(st, "tool", Options{Owner: "o", Repo: "r", Token: "test-token-0123", APIBase: srv.URL, GitRemote: remote, Log: &log})
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.Issues != 2 || res.Pulls != 1 || res.Placeholders != 1 || res.Labels != 1 || res.Releases != 1 || res.Assets != 1 {
		t.Fatalf("件数が違う: %+v\n%s", res, log.String())
	}
	// 新23：一度 403 を受けても、Retry-After のぶん待って再試行し、コメントは落ちない
	if fake.limited != 1 || !strings.Contains(log.String(), "待って再試行する") {
		t.Errorf("制限の再試行が働いていない（弾いた回数 %d）:\n%s", fake.limited, log.String())
	}
	if len(fake.issues) != 4 {
		t.Fatalf("GitHub 側の Issue が %d 件", len(fake.issues))
	}
	if fake.issues[0]["title"] != "不具合" || !strings.HasPrefix(fake.issues[0]["body"].(string), "> 元の投稿者: yamada（") || fake.issues[0]["state"] != "closed" {
		t.Errorf("#1 が違う: %v", fake.issues[0])
	}
	if ls, _ := fake.issues[0]["labels"].([]any); len(ls) != 1 || ls[0] != "bug" {
		t.Errorf("#1 のラベルが違う: %v", fake.issues[0]["labels"])
	}
	if !strings.HasPrefix(fake.issues[1]["title"].(string), "（欠番 #2）") || fake.issues[1]["state"] != "closed" {
		t.Errorf("欠番 #2 が違う: %v", fake.issues[1])
	}
	if fake.issues[2]["is_pull"] != false || fake.issues[2]["title"] != "[PR #3] 済んだ PR" || !strings.Contains(fake.issues[2]["body"].(string), "マージ済み") || fake.issues[2]["state"] != "closed" {
		t.Errorf("マージ済み PR #3 が違う: %v", fake.issues[2])
	}
	if fake.issues[3]["is_pull"] != true || fake.issues[3]["head"] != "topic" || fake.issues[3]["base"] != "main" {
		t.Errorf("open PR #4 が違う: %v", fake.issues[3])
	}
	if len(fake.comments[1]) != 2 || !strings.Contains(fake.comments[1][0], "直します") || !strings.Contains(fake.comments[1][1], "クローズした") {
		t.Errorf("#1 のコメントが違う: %v", fake.comments[1])
	}
	if len(fake.comments[3]) != 2 || !strings.Contains(fake.comments[3][0], "承認") || !strings.Contains(fake.comments[3][1], "マージした") {
		t.Errorf("#3 のコメントが違う: %v", fake.comments[3])
	}
	if len(fake.comments[4]) != 1 || !strings.Contains(fake.comments[4][0], "b.txt") || !strings.Contains(fake.comments[4][0], "```diff") {
		t.Errorf("#4 の行コメントが違う: %v", fake.comments[4])
	}
	if len(fake.releases) != 1 || fake.releases[0]["tag_name"] != "v1.0" || len(fake.assets[1]) != 1 || fake.assets[1][0] != "tool.zip:9" {
		t.Errorf("リリースが違う: %v %v", fake.releases, fake.assets)
	}
	if ref := git(t, remote, "rev-parse", "refs/heads/topic"); ref != topicSHA {
		t.Errorf("git が push されていない: %s", ref)
	}
	if git(t, remote, "tag", "-l") != "v1.0" {
		t.Error("タグが push されていない")
	}

	// Issue が既にあるリポジトリには書き出さない
	if _, err := Export(st, "tool", Options{Owner: "o", Repo: "r", Token: "test-token-0123", APIBase: srv.URL, GitRemote: remote}); err == nil || !strings.Contains(err.Error(), "既に Issue か PR がある") {
		t.Errorf("既に Issue のあるリポジトリに書き出せてしまう: %v", err)
	}
}

// 新23：GitHub 側に途中まで出来ているとき、-resume 無しは拒み、-resume なら最大番号の次から続ける。
// 作成済みのリリースは飛ばす。
func TestExportResume(t *testing.T) {
	root := t.TempDir()
	st, err := store.Create(root, store.Config{Owner: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("tool", ""); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "repos", "tool.git")
	work := filepath.Join(t.TempDir(), "work")
	git(t, root, "clone", "--quiet", bare, work)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "first")
	git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	mainSHA := git(t, work, "rev-parse", "main")
	t0 := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	for n, title := range map[int]string{1: "一つ目", 2: "二つ目", 3: "三つ目"} {
		st.CreateIssueAt("tool", n)
		st.WriteCreated("tool", n, store.Event{User: "yamada", Title: title, Body: "", Time: t0}, nil, nil)
	}
	if _, err := st.CreateReleaseAt("tool", "yamada", "v1.0", mainSHA, "版 1.0", "", false, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateReleaseAt("tool", "yamada", "v1.1", mainSHA, "版 1.1", "", false, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// 前回の実行で #1 と v1.0 まで出来ていた、という状態の GitHub
	fake := &fakeGitHub{comments: map[int][]string{}, assets: map[int64][]string{}, patches: map[int][]map[string]any{}}
	fake.issues = []map[string]any{{"number": 1, "title": "一つ目", "is_pull": false}}
	fake.releases = []map[string]any{{"id": int64(1), "tag_name": "v1.0"}}
	var base string
	srv := httptest.NewServer(fake.handler(t, &base))
	defer srv.Close()
	base = srv.URL
	remote := filepath.Join(t.TempDir(), "github.git")
	git(t, t.TempDir(), "init", "--quiet", "--bare", remote)

	var log bytes.Buffer
	opts := Options{Owner: "o", Repo: "r", Token: "test-token-0123", APIBase: srv.URL, GitRemote: remote, Log: &log}
	if _, err := Export(st, "tool", opts); err == nil || !strings.Contains(err.Error(), "-resume") {
		t.Fatalf("途中まで出来ているのに -resume 無しで通ってしまう: %v", err)
	}
	opts.Resume = true
	res, err := Export(st, "tool", opts)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.Issues != 2 || res.Releases != 1 || len(fake.issues) != 3 || fake.issues[1]["title"] != "二つ目" || fake.issues[2]["title"] != "三つ目" {
		t.Fatalf("続きから書けていない: %+v issues=%v\n%s", res, fake.issues, log.String())
	}
	if len(fake.releases) != 2 || fake.releases[1]["tag_name"] != "v1.1" {
		t.Fatalf("作成済みのリリースを飛ばしていない: %v", fake.releases)
	}
	if !strings.Contains(log.String(), "#2 から続ける") {
		t.Errorf("続きの表示が無い:\n%s", log.String())
	}
}
