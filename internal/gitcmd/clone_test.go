package gitcmd

import (
	"errors"
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
