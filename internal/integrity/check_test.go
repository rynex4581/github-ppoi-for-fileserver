package integrity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

func TestCheck(t *testing.T) {
	root := t.TempDir()
	st, err := store.Create(root, store.Config{Owner: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "repos", "r.git")
	work := filepath.Join(t.TempDir(), "work")
	git(t, root, "clone", "--quiet", bare, work)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "--quiet", "-m", "first")
	git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	sha := git(t, work, "rev-parse", "HEAD")

	// 正常な Issue：添付付きの本文と、実在するコミットを持つ PR
	is, err := st.CreateIssue("r", "yamada", "題名", "本文", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	name, err := st.SaveAttachment("r", is.Number, "shot.png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent("r", is.Number, store.Event{Type: store.EventComment, User: "suzuki", Body: "![x](/attachments/demo/r/1/" + name + ")"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePull("r", "yamada", "PR", "", "main", "main", sha, nil, nil); err != nil {
		t.Fatal(err)
	}
	// 予約しただけの空の番号は問題にしない
	if _, err := st.ReserveIssueNumbers("r", 5); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	n, err := Check(root, "", &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("問題が無いはず:\n%s", out.String())
	}

	// 壊す：読めない JSON、無い添付への参照、無いコミット、無い対象
	evDir := st.EventsDir("r", is.Number)
	os.WriteFile(filepath.Join(evDir, "20990101T000000000Z_x_1_001.json"), []byte("{broken"), 0o644)
	st.AppendEvent("r", is.Number, store.Event{Type: store.EventComment, User: "suzuki", Body: "![x](/attachments/demo/r/1/20000101T000000000Z_none.png)"})
	st.AppendEvent("r", 2, store.Event{Type: store.EventReview, User: "suzuki", State: "approved", CommitSHA: "0123456789abcdef0123456789abcdef01234567"})
	st.AppendEvent("r", is.Number, store.Event{Type: store.EventEdited, User: "yamada", Body: "x", Target: "nothing.json"})

	out.Reset()
	n, err = Check(root, "r", &out)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"JSON として読めない", "添付 20000101T000000000Z_none.png が無い", "commit_sha 0123456789abcdef0123456789abcdef01234567 がベアリポジトリに無い", "対象のイベント nothing.json が無い"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q が報告されていない:\n%s", want, text)
		}
	}
	if n != 4 {
		t.Errorf("問題は 4 件のはずが %d 件:\n%s", n, text)
	}

	if _, err := Check(root, "nothing", &out); err == nil {
		t.Error("無いリポジトリを指定してもエラーにならない")
	}
}
