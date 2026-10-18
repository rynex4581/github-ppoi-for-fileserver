package gitcmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRangeAndConflict(t *testing.T) {
	r, _ := newTestRepo(t)
	commits, err := r.RangeLog("main", "feature/x", 10)
	if err != nil || len(commits) != 1 || commits[0].Subject != "手順を追加" {
		t.Fatalf("base..head の履歴が違う: %+v %v", commits, err)
	}
	raw, err := r.RangeDiff("main", "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	files := ParseDiff(raw)
	if len(files) != 1 || files[0].NewPath != "docs/手順.md" {
		t.Fatalf("base...head の差分が違う: %+v", files)
	}
	conflict, err := r.HasConflict("main", "feature/x")
	if err != nil || conflict {
		t.Errorf("コンフリクトしないはずが: %v %v", conflict, err)
	}
	if !r.IsAncestor("main", "feature/x") {
		t.Error("main は feature/x の祖先のはず")
	}
	if r.IsAncestor("feature/x", "main") {
		t.Error("feature/x は main の祖先ではないはず")
	}
	if r.IsAncestor("0123456789abcdef0123456789abcdef01234567", "main") {
		t.Error("無いコミットが祖先扱いになっている")
	}
	mb, err := r.MergeBase("main", "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	mainSHA, _ := r.ResolveCommit("main")
	if mb != mainSHA {
		t.Errorf("共通の祖先が違う: %s != %s", mb, mainSHA)
	}
}

func TestCloneMergeAndRevert(t *testing.T) {
	r, work := newTestRepo(t)
	c := Clone{Dir: filepath.Join(t.TempDir(), "clones", "sample"), Remote: r.Dir, UserName: "testuser1", UserEmail: "testuser1@example.invalid"}

	mergeSHA, err := c.Merge("main", "feature/x", "Merge pull request #2 from feature/x\n\n手順を追加")
	if err != nil {
		t.Fatal(err)
	}
	cm, err := r.GetCommit(mergeSHA)
	if err != nil {
		t.Fatal(err)
	}
	if len(cm.Parents) != 2 || cm.Subject != "Merge pull request #2 from feature/x" || cm.Author != "testuser1" {
		t.Fatalf("マージコミットが違う: %+v", cm)
	}
	mainSHA, _ := r.ResolveCommit("main")
	if mainSHA != mergeSHA {
		t.Errorf("ベアリポジトリの main が進んでいない")
	}
	if _, err := r.Blob("main", "docs/手順.md"); err != nil {
		t.Errorf("マージ後に head のファイルが main に無い: %v", err)
	}

	// 2回目は同じ内容なので差分なし。マージは通る（空のマージ）が、ここでは head を消す動きを確かめる
	if err := c.DeleteRemoteBranch("feature/x"); err != nil {
		t.Fatal(err)
	}
	branches, _ := r.Branches()
	if len(branches) != 1 || branches[0].Name != "main" {
		t.Errorf("リモートのブランチが消えていない: %+v", branches)
	}

	revertSHA, err := c.Revert("main", mergeSHA, "Revert \"Merge pull request #2 from feature/x\"\n\nThis reverts commit "+mergeSHA+".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Blob("main", "docs/手順.md"); err == nil {
		t.Error("Revert 後も head のファイルが残っている")
	}
	mainSHA, _ = r.ResolveCommit("main")
	if mainSHA != revertSHA {
		t.Errorf("Revert が push されていない")
	}

	// コンフリクト：main と別ブランチで同じ行を変える
	git(t, work, "checkout", "--quiet", "main")
	git(t, work, "pull", "--quiet", "origin", "main")
	write(t, work, "README.md", "# sample\n\nmain側の変更\n")
	git(t, work, "commit", "--quiet", "-am", "main 側")
	git(t, work, "push", "--quiet", "origin", "main")
	git(t, work, "checkout", "--quiet", "-b", "feature/y", "HEAD~1")
	write(t, work, "README.md", "# sample\n\nfeature側の変更\n")
	git(t, work, "commit", "--quiet", "-am", "feature 側")
	git(t, work, "push", "--quiet", "origin", "feature/y")

	conflict, err := r.HasConflict("main", "feature/y")
	if err != nil || !conflict {
		t.Errorf("コンフリクトの判定が違う: %v %v", conflict, err)
	}
	_, err = c.Merge("main", "feature/y", "merge")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("コンフリクトが ErrConflict にならない: %v", err)
	}
	out := git(t, c.Dir, "status", "--porcelain")
	if strings.TrimSpace(out) != "" {
		t.Errorf("コンフリクト後に作業ツリーが汚れている:\n%s", out)
	}
	before, _ := r.ResolveCommit("main")
	if before == "" {
		t.Fatal("main が消えている")
	}
}

func TestCloneMergeRetriesAfterRemoteMoved(t *testing.T) {
	// fetch と push の間に他の端末が push した体で、clone の origin/main を古いままにして Merge を呼ぶ。
	// Merge は push が弾かれたら取り込み直してやり直すので、最終的に通る。
	r, work := newTestRepo(t)
	c := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: r.Dir, UserName: "u", UserEmail: "u@example.invalid"}
	if err := c.sync("main"); err != nil {
		t.Fatal(err)
	}
	// 他の端末が main を進める
	git(t, work, "checkout", "--quiet", "main")
	write(t, work, "other.txt", "x\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "他の端末")
	git(t, work, "push", "--quiet", "origin", "main")

	sha, err := c.Merge("main", "feature/x", "merge")
	if err != nil {
		t.Fatal(err)
	}
	cm, _ := r.GetCommit(sha)
	if len(cm.Parents) != 2 {
		t.Errorf("マージコミットになっていない: %+v", cm)
	}
	if _, err := r.Blob("main", "other.txt"); err != nil {
		t.Error("他の端末のコミットが失われている")
	}
}

// 本ツールの git 呼び出しでは、ベアリポジトリと専用 clone に置かれた hooks が動かないことを確かめる。
// -c core.hooksPath= はローカルの push では receive-pack に渡らないので、--receive-pack で付けた分も含めて見る。
func TestHooksDoNotRunForTool(t *testing.T) {
	r, work := newTestRepo(t)
	hook := "#!/bin/sh\necho HOOK-RAN >&2\nexit 1\n"
	write(t, r.Dir, "hooks/pre-receive", hook)

	// まず、この環境で hooks が実際に動くことを確かめる（動かない環境では確かめようがない）
	plain := exec.Command("git", "-c", "safe.directory=*", "-C", work, "push", "--quiet", "origin", "feature/x:refs/heads/probe")
	if out, err := plain.CombinedOutput(); err == nil || !strings.Contains(string(out), "HOOK-RAN") {
		t.Skipf("この環境では pre-receive が動かない: %v\n%s", err, out)
	}

	c := Clone{Dir: filepath.Join(t.TempDir(), "clones", "sample"), Remote: r.Dir, UserName: "testuser1", UserEmail: "testuser1@example.invalid"}
	if err := c.Ensure(); err != nil {
		t.Fatal(err)
	}
	// 専用 clone 側の hooks（コミット時に動くもの）も動かないこと
	write(t, c.Dir, ".git/hooks/pre-commit", hook)
	write(t, c.Dir, ".git/hooks/commit-msg", hook)
	write(t, c.Dir, ".git/hooks/pre-push", hook)

	mergeSHA, err := c.Merge("main", "feature/x", "Merge pull request #2 from feature/x")
	if err != nil {
		t.Fatalf("hooks があるとマージが通らない: %v", err)
	}
	if sha, _ := r.ResolveCommit("main"); sha != mergeSHA {
		t.Fatalf("push が bare に届いていない: main=%s merge=%s", sha, mergeSHA)
	}
	if err := c.DeleteRemoteBranch("feature/x"); err != nil {
		t.Fatalf("hooks があるとブランチの削除が通らない: %v", err)
	}

	// 運2：core.hooksPath を空文字にすると git は「ルート直下」を見る。bare のあるドライブ（Linux なら /）の
	// 直下に pre-receive を置けるなら置いて、それでも動かないことを確かめる。置けない環境ではこの部分は飛ばす
	rootHook := filepath.Join(filepath.VolumeName(r.Dir)+string(filepath.Separator), "pre-receive")
	if _, err := os.Stat(rootHook); err == nil {
		t.Fatalf("%s が既にある。消してから実行すること", rootHook)
	}
	if err := os.WriteFile(rootHook, []byte(hook), 0o755); err != nil {
		t.Logf("ルート直下に pre-receive を置けないので、その確認は飛ばす: %v", err)
	} else {
		t.Cleanup(func() { os.Remove(rootHook) })
		write(t, work, "root.txt", "root\n")
		git(t, work, "add", ".")
		git(t, work, "commit", "--quiet", "-m", "root")
		// bare には hooks/pre-receive が残っているので、ブランチの用意は本ツールと同じ指定で push する
		if _, _, err := runGit(work, nil, "push", "--quiet", noHooksReceivePack(), "origin", "HEAD:refs/heads/feature/root"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Merge("main", "feature/root", "Merge pull request #3 from feature/root"); err != nil {
			t.Fatalf("ルート直下の pre-receive が動いている: %v", err)
		}
	}

	// 本ツールが渡す core.hooksPath を receive-pack が本当に見ていることを、逆向きに確かめる。
	// noHooksDir を差し替えてそこに pre-receive を置くと、push はその hook で止まるはず
	write(t, work, "b.txt", "b\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "b")
	if _, _, err := runGit(work, nil, "push", "--quiet", noHooksReceivePack(), "origin", "HEAD:refs/heads/feature/y"); err != nil {
		t.Fatal(err)
	}
	saved := noHooksDir
	noHooksDir = filepath.Join(t.TempDir(), "hooks with space")
	t.Cleanup(func() { noHooksDir = saved })
	write(t, noHooksDir, "pre-receive", hook)
	_, err = c.Merge("main", "feature/y", "Merge pull request #4 from feature/y")
	if !errors.Is(err, ErrPushRejected) {
		t.Fatalf("receive-pack が本ツールの core.hooksPath を見ていない: %v", err)
	}
}

func TestTagsAndCreateTag(t *testing.T) {
	r, work := newTestRepo(t)
	git(t, work, "tag", "-a", "v0.1", "-m", "最初のタグ", "main~1")
	git(t, work, "push", "--quiet", "origin", "v0.1")
	c := Clone{Dir: filepath.Join(t.TempDir(), "clones", "sample"), Remote: r.Dir, UserName: "testuser1", UserEmail: "testuser1@example.invalid"}
	sha, err := c.CreateTag("v0.2", "main")
	if err != nil {
		t.Fatal(err)
	}
	mainSHA, _ := r.ResolveCommit("main")
	if sha != mainSHA {
		t.Fatalf("タグの指す先が違う: %s != %s", sha, mainSHA)
	}
	tags, err := r.Tags()
	if err != nil {
		t.Fatal(err)
	}
	// 並びは作成日時の新しい順（注釈付きタグはタグの日時、軽量タグはコミットの日時）なので、名前で引いて確かめる
	byName := map[string]Tag{}
	for _, tg := range tags {
		byName[tg.Name] = tg
	}
	if len(tags) != 2 || byName["v0.2"].SHA != mainSHA || byName["v0.1"].SHA == "" || byName["v0.1"].Time.IsZero() {
		t.Fatalf("タグの一覧が違う: %+v", tags)
	}
	if _, err := c.CreateTag("v0.2", "main"); !errors.Is(err, ErrTagExists) {
		t.Fatalf("同名のタグが作れてしまう: %v", err)
	}
	if _, err := c.CreateTag("v0.3", "no-such-branch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("無いブランチへのタグが %v", err)
	}
}
