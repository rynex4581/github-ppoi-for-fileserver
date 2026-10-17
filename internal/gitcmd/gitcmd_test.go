package gitcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// テストで使う名前はすべて架空のもの。

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test User", "-c", "user.email=test@example.com", "-c", "safe.directory=*", "-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestRepo はベアリポジトリと、そこへ push 済みの作業コピーを作る。
func newTestRepo(t *testing.T) (Repo, string) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "sample.git")
	work := filepath.Join(root, "work")
	git(t, root, "init", "--bare", "--quiet", "--initial-branch=main", bare)
	git(t, root, "clone", "--quiet", bare, work)
	write(t, work, "README.md", "# sample\n\n説明\n")
	write(t, work, "src/main.go", "package main\n\nfunc main() {}\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "最初のコミット")
	write(t, work, "src/main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n")
	write(t, work, "data/bin.dat", "\x00\x01\x02binary")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "出力を追加\n\n本文の説明。")
	git(t, work, "push", "--quiet", "origin", "main")
	git(t, work, "checkout", "--quiet", "-b", "feature/x")
	write(t, work, "docs/手順.md", "手順\n")
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "手順を追加")
	git(t, work, "push", "--quiet", "origin", "feature/x")
	return Repo{Dir: bare}, work
}

func TestBranchesAndTree(t *testing.T) {
	r, _ := newTestRepo(t)
	branches, err := r.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || branches[0].Name != "feature/x" || branches[1].Name != "main" {
		t.Fatalf("ブランチ一覧が違う: %+v", branches)
	}
	head, err := r.HeadBranch()
	if err != nil || head != "main" {
		t.Fatalf("HEAD が違う: %q %v", head, err)
	}
	if _, err := r.ResolveCommit("nothing"); err != ErrNotFound {
		t.Errorf("無い ref が ErrNotFound にならない: %v", err)
	}

	entries, err := r.Tree("main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name != "data" || entries[1].Name != "src" || entries[2].Name != "README.md" {
		t.Fatalf("ルートのツリーが違う（フォルダが先、名前順）: %+v", entries)
	}
	if entries[2].Size == 0 || entries[2].Type != "blob" {
		t.Errorf("blob の大きさが入っていない: %+v", entries[2])
	}
	sub, err := r.Tree("main", "src")
	if err != nil || len(sub) != 1 || sub[0].Path != "src/main.go" {
		t.Fatalf("サブフォルダのツリーが違う: %+v %v", sub, err)
	}
	docs, err := r.Tree("feature/x", "docs")
	if err != nil || len(docs) != 1 || docs[0].Name != "手順.md" {
		t.Fatalf("日本語名のファイルが崩れている: %+v %v", docs, err)
	}
	if _, err := r.Tree("main", "docs"); err != ErrNotFound {
		t.Errorf("main に無いフォルダが ErrNotFound にならない: %v", err)
	}
}

func TestBlobAndObjectType(t *testing.T) {
	r, _ := newTestRepo(t)
	typ, err := r.ObjectType("main", "src")
	if err != nil || typ != "tree" {
		t.Fatalf("フォルダの種類が違う: %q %v", typ, err)
	}
	typ, _ = r.ObjectType("main", "README.md")
	if typ != "blob" {
		t.Fatalf("ファイルの種類が違う: %q", typ)
	}
	data, err := r.Blob("main", "README.md")
	if err != nil || string(data) != "# sample\n\n説明\n" {
		t.Fatalf("ファイルの中身が違う: %q %v", data, err)
	}
	size, err := r.BlobSize("main", "README.md")
	if err != nil || size != int64(len(data)) {
		t.Fatalf("大きさが違う: %d %v", size, err)
	}
	if _, err := r.Blob("main", "nothing.txt"); err != ErrNotFound {
		t.Errorf("無いファイルが ErrNotFound にならない: %v", err)
	}
}

func TestLogAndCommitDiff(t *testing.T) {
	r, _ := newTestRepo(t)
	commits, err := r.Log("main", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Subject != "出力を追加" || commits[1].Subject != "最初のコミット" {
		t.Fatalf("履歴が違う: %+v", commits)
	}
	if commits[0].Body != "本文の説明。" || commits[0].Author != "Test User" || commits[0].Time.IsZero() {
		t.Errorf("コミットの項目が違う: %+v", commits[0])
	}
	if len(commits[0].Parents) != 1 || commits[0].Parents[0] != commits[1].SHA {
		t.Errorf("親が違う: %+v", commits[0])
	}
	if len(commits[1].Parents) != 0 {
		t.Errorf("最初のコミットに親がある: %+v", commits[1])
	}
	n, err := r.CountCommits("feature/x")
	if err != nil || n != 3 {
		t.Errorf("コミット数が違う: %d %v", n, err)
	}
	only, _ := r.Log("main", "README.md", 0, 10)
	if len(only) != 1 {
		t.Errorf("パスで絞った履歴が違う: %+v", only)
	}
	page, _ := r.Log("feature/x", "", 1, 1)
	if len(page) != 1 || page[0].Subject != "出力を追加" {
		t.Errorf("skip と limit が効いていない: %+v", page)
	}

	c, err := r.GetCommit(commits[0].SHA)
	if err != nil || c.Subject != "出力を追加" {
		t.Fatalf("GetCommit が違う: %+v %v", c, err)
	}
	raw, err := r.CommitDiff(c)
	if err != nil {
		t.Fatal(err)
	}
	files := ParseDiff(raw)
	if len(files) != 2 {
		t.Fatalf("差分のファイル数が違う: %d\n%s", len(files), raw)
	}
	if files[0].NewPath != "data/bin.dat" || files[0].Status != "added" || !files[0].IsBinary {
		t.Errorf("バイナリの追加が取れていない: %+v", files[0])
	}
	if files[1].NewPath != "src/main.go" || files[1].Status != "modified" || files[1].Additions != 3 || files[1].Deletions != 1 {
		t.Errorf("変更の集計が違う: %+v", files[1])
	}
	if len(files[1].Hunks) != 1 || files[1].Hunks[0].Lines[0].Kind != "context" || files[1].Hunks[0].Lines[0].OldNum != 1 {
		t.Errorf("塊の行番号が違う: %+v", files[1].Hunks)
	}

	root, _ := r.GetCommit(commits[1].SHA)
	raw, err = r.CommitDiff(root)
	if err != nil {
		t.Fatal(err)
	}
	files = ParseDiff(raw)
	if len(files) != 2 || files[0].Status != "added" || files[1].Status != "added" {
		t.Errorf("最初のコミットの差分が違う: %+v", files)
	}
	st := Stat(files)
	if st.Files != 2 || st.Additions != 6 || st.Deletions != 0 {
		t.Errorf("集計が違う: %+v", st)
	}
}

func TestParseDiffRename(t *testing.T) {
	raw := "diff --git a/old name.txt b/new name.txt\nsimilarity index 100%\nrename from old name.txt\nrename to new name.txt\n"
	files := ParseDiff(raw)
	if len(files) != 1 || files[0].Status != "renamed" || files[0].OldPath != "old name.txt" || files[0].NewPath != "new name.txt" {
		t.Fatalf("改名の解析が違う: %+v", files)
	}
	if files[0].Path() != "old name.txt → new name.txt" {
		t.Errorf("表示用パスが違う: %q", files[0].Path())
	}
}
