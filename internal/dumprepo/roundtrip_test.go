package dumprepo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// テストで使う名前はすべて架空のもの。

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test User", "-c", "user.email=test@example.com", "-c", "safe.directory=*", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sampleRoot は Issue、PR（マージ済みと open）、レビュー、リリースの入ったデータルートを作る。
func sampleRoot(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Create(root, store.Config{Owner: "demo", OwnerName: "デモ"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("tool", "説明文"); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "repos", "tool.git")
	work := filepath.Join(t.TempDir(), "work")
	git(t, root, "clone", "--quiet", bare, work)
	write(t, work, "a.txt", "1\n2\n3\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "first")
	git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	git(t, work, "checkout", "--quiet", "-b", "feature")
	write(t, work, "a.txt", "1\n2\n3\n4\n")
	git(t, work, "commit", "--quiet", "-am", "add line")
	git(t, work, "push", "--quiet", "origin", "feature")
	featureSHA := git(t, work, "rev-parse", "HEAD")
	git(t, work, "checkout", "--quiet", "main")
	git(t, work, "merge", "--quiet", "--no-ff", "-m", "Merge pull request #2 from feature", "feature")
	git(t, work, "push", "--quiet", "origin", "main")
	mergeSHA := git(t, work, "rev-parse", "HEAD")
	git(t, work, "tag", "v1.0")
	git(t, work, "push", "--quiet", "origin", "v1.0")
	git(t, work, "checkout", "--quiet", "-b", "topic")
	write(t, work, "b.txt", "b\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "topic work")
	git(t, work, "push", "--quiet", "origin", "topic")
	topicSHA := git(t, work, "rev-parse", "HEAD")

	bug, _ := st.CreateLabel("tool", store.Label{Name: "bug", Color: "d73a4a", Description: "不具合"})
	t0 := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	// Issue #1：ラベル、担当、コメント、タイトル変更、クローズ
	if err := st.CreateIssueAt("tool", 1); err != nil {
		t.Fatal(err)
	}
	st.WriteCreated("tool", 1, store.Event{User: "yamada", Title: "最初の題", Body: "本文", Time: t0}, nil, nil)
	st.AppendEvent("tool", 1, store.Event{Type: store.EventLabels, User: "yamada", Labels: []int{bug.ID}, Time: t0})
	st.AppendEvent("tool", 1, store.Event{Type: store.EventAssigned, User: "yamada", Assignees: []string{"suzuki"}, Time: t0})
	st.AppendEvent("tool", 1, store.Event{Type: store.EventComment, User: "suzuki", Body: "コメント1", Time: t0.Add(time.Hour)})
	st.AppendEvent("tool", 1, store.Event{Type: store.EventTitle, User: "yamada", Title: "直した題", Time: t0.Add(2 * time.Hour)})
	st.AppendEvent("tool", 1, store.Event{Type: store.EventClosed, User: "suzuki", Body: "対応済み", Time: t0.Add(3 * time.Hour)})
	// PR #2：マージ済み
	if err := st.CreateIssueAt("tool", 2); err != nil {
		t.Fatal(err)
	}
	st.WriteCreated("tool", 2, store.Event{User: "yamada", Title: "行を足す", Body: "", IsPull: true, Head: "feature", Base: "main", HeadSHA: featureSHA, Time: t0}, nil, nil)
	st.AppendEvent("tool", 2, store.Event{Type: store.EventReview, User: "suzuki", State: store.ReviewApproved, CommitSHA: featureSHA, Body: "LGTM", Time: t0.Add(time.Hour)})
	st.AppendEvent("tool", 2, store.Event{Type: store.EventMerged, User: "suzuki", MergeSHA: mergeSHA, HeadSHA: featureSHA, Time: t0.Add(2 * time.Hour)})
	// PR #3：open、行コメント付きの変更要請
	if err := st.CreateIssueAt("tool", 3); err != nil {
		t.Fatal(err)
	}
	st.WriteCreated("tool", 3, store.Event{User: "yamada", Title: "topic", Body: "作業中", IsPull: true, Head: "topic", Base: "main", HeadSHA: topicSHA, Time: t0}, nil, nil)
	st.AppendEvent("tool", 3, store.Event{Type: store.EventReviewComment, User: "suzuki", Body: "ここ直して", Path: "b.txt", Line: 1, Side: "new", DiffHunk: "@@ -0,0 +1 @@\n+b", CommitSHA: topicSHA, Time: t0.Add(time.Hour)})
	st.AppendEvent("tool", 3, store.Event{Type: store.EventReview, User: "suzuki", State: store.ReviewChangesRequested, CommitSHA: topicSHA, Body: "要修正", Time: t0.Add(time.Hour + time.Minute)})
	st.AppendEvent("tool", 3, store.Event{Type: store.EventComment, User: "yamada", Body: "消すコメント", Time: t0.Add(2 * time.Hour)})
	is3, _ := st.GetIssue("tool", 3)
	for _, ev := range is3.Events {
		if ev.Body == "消すコメント" {
			st.DeleteComment("tool", 3, "yamada", ev.File)
		}
	}
	// リリース
	tagSHA, err := gitcmd.Repo{Dir: bare}.ResolveCommit("refs/tags/v1.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateReleaseAt("tool", "yamada", "v1.0", tagSHA, "版 1.0", "リリースノート", false, t0.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveReleaseAttachment("tool", "v1.0", "tool.zip", []byte("zip-bytes")); err != nil {
		t.Fatal(err)
	}
	return st, root
}

func TestExportImportRoundTrip(t *testing.T) {
	st, _ := sampleRoot(t)
	out := filepath.Join(t.TempDir(), "dump")
	var log bytes.Buffer
	dir, err := Export(st, "tool", out, &log)
	if err != nil {
		t.Fatalf("export: %v\n%s", err, log.String())
	}
	if dir != filepath.Join(out, "demo", "tool") {
		t.Fatalf("出力先が違う: %s", dir)
	}
	for _, name := range []string{"repo.yml", "topic.yml", "label.yml", "issue.yml", "pull_request.yml", "release.yml", "comments/1.yml", "reviews/2.yml", "reviews/3.yml", "git/HEAD", "release_assets/v1.0"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s が無い", name)
		}
	}
	// YAML の中身を確かめる
	var issues []map[string]any
	data, _ := os.ReadFile(filepath.Join(dir, "issue.yml"))
	if err := yaml.Unmarshal(data, &issues); err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0]["number"] != 1 || issues[0]["title"] != "直した題" || issues[0]["state"] != "closed" || issues[0]["poster_name"] != "yamada" {
		t.Fatalf("issue.yml が違う: %v", issues)
	}
	for k := range issues[0] {
		switch k {
		case "number", "poster_id", "poster_name", "poster_email", "title", "content", "ref", "milestone", "state", "is_locked", "created", "updated", "closed", "labels", "reactions", "assignees":
		default:
			t.Errorf("issue.yml にスキーマに無い項目 %q がある", k)
		}
	}
	var comments []ymlComment
	data, _ = os.ReadFile(filepath.Join(dir, "comments", "1.yml"))
	yaml.Unmarshal(data, &comments)
	types := []string{}
	for _, c := range comments {
		types = append(types, c.CommentType)
	}
	if strings.Join(types, ",") != "comment,change_title,comment,close" {
		t.Errorf("コメントの種類の並びが違う: %v", types)
	}
	var pulls []ymlPullRequest
	data, _ = os.ReadFile(filepath.Join(dir, "pull_request.yml"))
	yaml.Unmarshal(data, &pulls)
	if len(pulls) != 2 || !pulls[0].Merged || pulls[0].MergeCommitSHA == "" || pulls[0].Head.SHA == "" || pulls[0].Base.SHA == "" || pulls[1].State != "open" {
		t.Fatalf("pull_request.yml が違う: %+v", pulls)
	}
	if ref := git(t, filepath.Join(dir, "git"), "rev-parse", "refs/pull/2/head"); ref != pulls[0].Head.SHA {
		t.Errorf("refs/pull/2/head が head の SHA と違う: %s", ref)
	}
	var reviews []ymlReview
	data, _ = os.ReadFile(filepath.Join(dir, "reviews", "3.yml"))
	yaml.Unmarshal(data, &reviews)
	if len(reviews) != 1 || reviews[0].State != "CHANGES_REQUESTED" || len(reviews[0].Comments) != 1 || reviews[0].Comments[0].Line != 1 || reviews[0].Comments[0].TreePath != "b.txt" {
		t.Fatalf("reviews/3.yml が違う: %+v", reviews)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "comments", "3.yml"))
	if strings.Contains(string(data), "消すコメント") {
		t.Error("削除したコメントが出ている")
	}

	// 別のデータルートに取り込んで、同じものが読めること
	st2, err := store.Create(t.TempDir(), store.Config{Owner: "other"})
	if err != nil {
		t.Fatal(err)
	}
	log.Reset()
	res, err := Import(st2, out, "", &log)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, log.String())
	}
	if res.Issues != 1 || res.Pulls != 2 || res.Labels != 1 || res.Comments != 2 || res.Reviews != 2 || res.Releases != 1 || res.Assets != 1 {
		t.Fatalf("取り込んだ件数が違う: %+v\n%s", res, log.String())
	}
	is1, err := st2.GetIssue("tool", 1)
	if err != nil {
		t.Fatal(err)
	}
	if is1.Title != "直した題" || is1.State != "closed" || is1.User != "yamada" || len(is1.Labels) != 1 || len(is1.Assignees) != 1 || is1.Comments != 2 || !is1.CreatedAt.Equal(time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("Issue #1 が違う: %+v", is1)
	}
	pr2, _ := st2.GetIssue("tool", 2)
	if !pr2.IsPull || !pr2.Merged || pr2.MergeSHA != pulls[0].MergeCommitSHA || pr2.Head != "feature" || pr2.Base != "main" || len(pr2.Reviews) != 1 {
		t.Fatalf("PR #2 が違う: %+v", pr2)
	}
	pr3, _ := st2.GetIssue("tool", 3)
	if pr3.State != "open" || pr3.Comments != 2 || len(pr3.Reviews) != 1 || pr3.Reviews[0].State != store.ReviewChangesRequested {
		t.Fatalf("PR #3 が違う: %+v", pr3)
	}
	g2 := gitcmd.Repo{Dir: filepath.Join(st2.Root(), "repos", "tool.git")}
	if _, err := g2.ResolveCommit("refs/heads/topic"); err != nil {
		t.Error("取り込んだ git に topic ブランチが無い")
	}
	// 新19：refs/pull/* は bare に入れない（GitHub への書き出しで拒まれる）
	if refs := git(t, g2.Dir, "for-each-ref", "refs/pull"); strings.TrimSpace(refs) != "" {
		t.Errorf("取り込んだ bare に refs/pull が入っている: %s", refs)
	}
	rel, err := st2.GetRelease("tool", "v1.0")
	if err != nil || rel.Title != "版 1.0" || rel.User != "yamada" {
		t.Fatalf("リリースが違う: %+v %v", rel, err)
	}
	assets, _ := st2.ListReleaseAttachments("tool", "v1.0")
	if len(assets) != 1 || !strings.HasSuffix(assets[0].Name, "_tool.zip") {
		t.Fatalf("リリースの添付が違う: %+v", assets)
	}
	// 同じ名前にもう一度は取り込めない
	if _, err := Import(st2, out, "", &log); err == nil {
		t.Error("同じ名前のリポジトリに二重に取り込めてしまう")
	}

	// 新27：途中で失敗したら作りかけの repos と meta を消し、直して再実行できる
	broken := filepath.Join(t.TempDir(), "broken")
	if err := os.CopyFS(broken, os.DirFS(out)); err != nil {
		t.Fatal(err)
	}
	brokenDir, _ := FindDumpDir(broken)
	badComments := filepath.Join(brokenDir, "comments", "1.yml")
	good, _ := os.ReadFile(badComments)
	os.WriteFile(badComments, []byte("- {broken: ["), 0o644)
	st3, err := store.Create(t.TempDir(), store.Config{Owner: "third"})
	if err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if _, err := Import(st3, broken, "", &log); err == nil {
		t.Fatal("壊れた comments で import が通ってしまう")
	}
	if _, err := os.Stat(filepath.Join(st3.Root(), "repos", "tool.git")); !os.IsNotExist(err) {
		t.Errorf("失敗した import の bare が残っている: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(st3.IssuesDir("tool"))); !os.IsNotExist(err) {
		t.Errorf("失敗した import の meta が残っている: %v", err)
	}
	if !strings.Contains(log.String(), "元に戻した") {
		t.Errorf("後始末の表示が無い: %s", log.String())
	}
	os.WriteFile(badComments, good, 0o644)
	if _, err := Import(st3, broken, "", &log); err != nil {
		t.Fatalf("直した後の再実行が通らない: %v", err)
	}
}
