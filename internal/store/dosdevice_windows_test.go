//go:build windows

package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github-ppoi-for-fileserver/internal/gitcmd"
)

// 実際に DOS デバイス名を定義し、リポジトリの作成、clone、push、読み取りが仮想パスで通ることを確かめる。
// 定義先はローカルの一時フォルダ（UNC の共有はテストでは用意できない）。名前は本物と重ならない
// テスト用のものにし、終わったら削除する。
func TestDOSDeviceWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	saved := dosDevicePrefix
	dosDevicePrefix = fmt.Sprintf("github-ppoi-test%d-", os.Getpid())
	t.Cleanup(func() { dosDevicePrefix = saved })
	name := dosDevicePrefix + "team-a"

	s := newTestStore(t)
	t.Cleanup(func() {
		for i := 0; i < 16; i++ {
			if sysRemoveDOSDevice(name) != nil {
				break
			}
		}
	})
	target := strings.TrimRight(filepath.Clean(s.Root()), `\`)
	if err := s.defineDOSDevice(target); err != nil {
		t.Fatal(err)
	}
	targets, err := sysQueryDOSDevice(name)
	if err != nil || len(targets) != 1 || !strings.EqualFold(targets[0], `\??\`+target) {
		t.Fatalf("定義先が違う: %v %v", targets, err)
	}

	// 定義が残った状態でもう一度開くと、その定義をそのまま使用し、積み重ならない（新37）
	again, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := again.defineDOSDevice(target); err != nil {
		t.Fatal(err)
	}
	if targets, _ := sysQueryDOSDevice(name); len(targets) != 1 {
		t.Errorf("定義が積み重なっている: %v", targets)
	}

	if _, err := s.CreateRepo("sample", ""); err != nil {
		t.Fatal(err)
	}
	bare := s.GitPath(filepath.Join(s.Root(), "repos", "sample.git"))
	if bare != `\\.\`+name+`\repos\sample.git` {
		t.Fatalf("仮想パスが違う: %s", bare)
	}
	work := filepath.Join(t.TempDir(), "work")
	git := func(dir string, args ...string) {
		t.Helper()
		full := append([]string{"-c", "user.name=Test User", "-c", "user.email=test@example.com", "-c", "safe.directory=*", "-C", dir}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(t.TempDir(), "clone", "--quiet", bare, work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", ".")
	git(work, "commit", "--quiet", "-m", "最初のコミット")
	git(work, "-c", "remote.origin.receivepack=git -c safe.directory=* receive-pack", "push", "--quiet", "origin", "HEAD:refs/heads/main")

	branches, err := gitcmd.Repo{Dir: bare}.Branches()
	if err != nil || len(branches) != 1 || branches[0].Name != "main" {
		t.Fatalf("仮想パスでブランチを読めない: %+v %v", branches, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "repos", "sample.git", "HEAD")); err != nil {
		t.Errorf("実体のフォルダにベアリポジトリが無い: %v", err)
	}
}
