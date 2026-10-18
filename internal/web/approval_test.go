package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github-ppoi-for-fileserver/internal/gitcmd"
)

// テストで使う名前はすべて架空のもの。

func tgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test User", "-c", "user.email=test@example.com", "-c", "safe.directory=*", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func twrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bare と作業コピーを作り、main に2コミット、feature に1コミットを置く。
func approvalRepo(t *testing.T) (gitcmd.Repo, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "sample.git")
	work := filepath.Join(root, "work")
	tgit(t, root, "init", "--bare", "--quiet", "--initial-branch=main", bare)
	tgit(t, root, "clone", "--quiet", bare, work)
	twrite(t, work, "a.txt", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	twrite(t, work, "b.txt", "b\n")
	tgit(t, work, "add", ".")
	tgit(t, work, "commit", "--quiet", "-m", "最初")
	tgit(t, work, "push", "--quiet", "origin", "main")
	tgit(t, work, "checkout", "--quiet", "-b", "feature")
	twrite(t, work, "a.txt", "1\n2\n3\n4\n5-changed\n6\n7\n8\n9\n10\n")
	tgit(t, work, "commit", "--quiet", "-am", "5行目を直す")
	tgit(t, work, "push", "--quiet", "origin", "feature")
	return gitcmd.Repo{Dir: bare}, work
}

func sha(t *testing.T, g gitcmd.Repo, ref string) string {
	t.Helper()
	s, err := g.ResolveCommit(ref)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApprovalStillValid(t *testing.T) {
	g, work := approvalRepo(t)
	approved := sha(t, g, "refs/heads/feature")

	check := func(name string, want bool) {
		t.Helper()
		got, err := approvalStillValid(g, "main", approved, sha(t, g, "refs/heads/feature"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: 承認が有効=%v のはずが %v", name, want, got)
		}
	}

	check("同じコミット", true)

	// base を取り込んだだけ（差分は変わらない）
	tgit(t, work, "checkout", "--quiet", "main")
	twrite(t, work, "b.txt", "b\nbase side\n")
	tgit(t, work, "commit", "--quiet", "-am", "base 側の変更")
	tgit(t, work, "push", "--quiet", "origin", "main")
	tgit(t, work, "checkout", "--quiet", "feature")
	tgit(t, work, "merge", "--quiet", "--no-edit", "main")
	tgit(t, work, "push", "--quiet", "origin", "feature")
	check("base を取り込んだだけ", true)

	// コミットメッセージだけの amend（force push）。差分は同じ
	tgit(t, work, "commit", "--quiet", "--amend", "-m", "メッセージだけ変更")
	tgit(t, work, "push", "--quiet", "--force", "origin", "feature")
	check("メッセージだけの amend", true)

	// 中身が変わる push
	twrite(t, work, "a.txt", "1\n2\n3\n4\n5-changed-again\n6\n7\n8\n9\n10\n")
	tgit(t, work, "commit", "--quiet", "-am", "さらに直す")
	tgit(t, work, "push", "--quiet", "origin", "feature")
	check("中身が変わる push", false)

	// 承認したコミットが無い（力づくで消えた）ときはエラー
	if _, err := approvalStillValid(g, "main", "0123456789abcdef0123456789abcdef01234567", sha(t, g, "refs/heads/feature")); err == nil {
		t.Error("無いコミットでエラーにならない")
	}
}

func TestApprovalValidMemo(t *testing.T) {
	g, _ := approvalRepo(t)
	s := &Server{}
	head := sha(t, g, "refs/heads/feature")
	if !s.approvalValid(g, "main", head, head) {
		t.Fatal("同じコミットなのに無効")
	}
	if len(s.approvalMemo) != 0 {
		t.Fatal("同じコミットの判定は覚えなくてよい")
	}
	// 2つの SHA が違う判定は覚える
	first := sha(t, g, "refs/heads/main")
	if s.approvalValid(g, "main", first, head) {
		t.Fatal("main のコミットを承認したことにしても差分は違うはず")
	}
	key := approvalKey{sha(t, g, "refs/heads/main"), first, head}
	if ok, hit := s.approvalMemo[key]; !hit || ok {
		t.Fatalf("判定が覚えられていない: hit=%v ok=%v", hit, ok)
	}
	// 無いコミットの判定（git が失敗）は覚えない
	s.approvalValid(g, "main", "0123456789abcdef0123456789abcdef01234567", head)
	if len(s.approvalMemo) != 1 {
		t.Fatalf("git が失敗した判定まで覚えている: %d", len(s.approvalMemo))
	}
}

func TestStripIndexLines(t *testing.T) {
	in := "diff --git a/x b/x\nindex 1111111..2222222 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	want := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	if got := stripIndexLines(in); got != want {
		t.Fatalf("index 行が除けていない:\n%q", got)
	}
	// 本文の中の「index 」で始まる行は diff では常に +/- か空白が頭に付くので巻き込まれない
	body := "@@ -1 +1 @@\n-index of things\n+index of other things\n"
	if got := stripIndexLines(body); got != body {
		t.Fatalf("本文の行まで除いている:\n%q", got)
	}
	if got := stripIndexLines(""); got != "" {
		t.Fatalf("空が空にならない: %q", got)
	}
}
