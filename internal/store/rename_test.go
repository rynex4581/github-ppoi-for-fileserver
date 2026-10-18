package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// renameNoReplace は既存のファイルを置き換えない。
func TestRenameNoReplaceKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.json")
	to := filepath.Join(dir, "to.json")
	os.WriteFile(from, []byte("new"), 0o644)
	os.WriteFile(to, []byte("old"), 0o644)

	err := renameNoReplace(from, to)
	if err == nil {
		t.Fatal("既存のファイルがあるのに rename が通った")
	}
	if !errors.Is(err, fs.ErrExist) && !strings.Contains(err.Error(), "exist") {
		t.Logf("エラーの種類: %v", err)
	}
	data, _ := os.ReadFile(to)
	if string(data) != "old" {
		t.Fatalf("既存のファイルが置き換えられた: %q", data)
	}
	if _, err := os.Stat(from); err != nil {
		t.Fatalf("元のファイルが消えた: %v", err)
	}

	// 既存が無ければ通る
	os.Remove(to)
	if err := renameNoReplace(from, to); err != nil {
		t.Fatalf("rename に失敗: %v", err)
	}
	data, _ = os.ReadFile(to)
	if string(data) != "new" {
		t.Fatalf("中身が違う: %q", data)
	}
}

// writeFileAtomic は一時名を一意にし、書き終えると古い .tmp を掃除する。
func TestWriteFileAtomicCleansStaleTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "labels.json")
	stale := path + ".old-pc-1-7.tmp"
	os.WriteFile(stale, []byte("x"), 0o644)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stale, old, old)
	fresh := path + ".old-pc-1-8.tmp"
	os.WriteFile(fresh, []byte("x"), 0o644)

	if err := writeFileAtomic(path, []byte("a"), true); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("b"), true); err != nil {
		t.Fatalf("2回目の上書きに失敗: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("1時間以上前の .tmp が残っている")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("新しい .tmp まで消された")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "b" {
		t.Fatalf("中身が違う: %q", data)
	}

	// 置き換え無しの書き込みは既存があれば失敗し、一時名は残さない
	if err := writeFileAtomic(path, []byte("c"), false); err == nil {
		t.Fatal("既存があるのに置き換え無しの書き込みが通った")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") && e.Name() != filepath.Base(fresh) {
			t.Fatalf("一時ファイルが残った: %s", e.Name())
		}
	}
}
