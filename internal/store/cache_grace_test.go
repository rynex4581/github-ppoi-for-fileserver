package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 自分が書いた直後のイベントは、フォルダの一覧にまだ出てこなくても捨てない（SMB の一覧の遅れ）。
func TestCacheKeepsOwnWriteUntilListed(t *testing.T) {
	root := t.TempDir()
	st, err := Create(root, Config{Owner: "testowner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	is, err := st.CreateIssue("r", "testuser1", "題名", "本文", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 自分が書いたコメントがまだ一覧に無い状態を作る：書いた後にファイルを別の場所へ退避する
	if err := st.AppendEvent("r", is.Number, Event{Type: EventComment, User: "testuser1", Body: "c"}); err != nil {
		t.Fatal(err)
	}
	dir := st.EventsDir("r", is.Number)
	entries, _ := os.ReadDir(dir)
	var commentFile string
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if len(data) > 0 && string(data) != "" && containsType(data, EventComment) {
			commentFile = e.Name()
		}
	}
	if commentFile == "" {
		t.Fatal("コメントのファイルが見つからない")
	}
	hidden := filepath.Join(t.TempDir(), commentFile)
	if err := os.Rename(filepath.Join(dir, commentFile), hidden); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetIssue("r", is.Number)
	if got.Comments != 1 {
		t.Fatalf("書いた直後のコメントが消えた: Comments=%d", got.Comments)
	}

	// 猶予が過ぎれば一覧に無いものは捨てる
	st.cache.mu.Lock()
	rc := st.repoCacheLocked("r")
	rc.recent[commentFile] = time.Now().Add(-2 * ownWriteGrace)
	st.cache.mu.Unlock()
	got, _ = st.GetIssue("r", is.Number)
	if got.Comments != 0 {
		t.Fatalf("猶予を過ぎても残っている: Comments=%d", got.Comments)
	}

	// 戻せばまた読める
	os.Rename(hidden, filepath.Join(dir, commentFile))
	got, _ = st.GetIssue("r", is.Number)
	if got.Comments != 1 {
		t.Fatalf("戻したのに読めない: Comments=%d", got.Comments)
	}
}

func containsType(data []byte, typ string) bool {
	return len(data) > 0 && string(data) != "" && (indexOf(string(data), `"type": "`+typ+`"`) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
