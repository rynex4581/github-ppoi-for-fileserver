package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 新しく作ったデータルートには format が入り、exe が知らない format は読み取り専用で開く。
func TestFormatReadOnly(t *testing.T) {
	root := t.TempDir()
	st, err := Create(root, Config{Owner: "testowner"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Config().Format != CurrentFormat || st.ReadOnly() {
		t.Fatalf("作成直後の format=%d readOnly=%v", st.Config().Format, st.ReadOnly())
	}

	// format を未来の値に書き換えて開き直す
	path := filepath.Join(root, "config.json")
	data, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(`{"owner":"testowner","format":99}`), 0o644)
	ro, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !ro.ReadOnly() {
		t.Fatal("format 99 なのに読み取り専用になっていない")
	}
	if _, err := ro.CreateRepo("r", ""); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("読み取り専用で CreateRepo が通った: %v", err)
	}
	if err := ro.SetUserName("testuser1", "x"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("読み取り専用で SetUserName が通った: %v", err)
	}
	if err := ro.EnsureUser("testuser1"); err != nil {
		t.Fatalf("読み取り専用の EnsureUser はエラーにしない: %v", err)
	}

	// format の無い古い config.json は 1 として読み書きできる
	os.WriteFile(path, []byte(`{"owner":"testowner"}`), 0o644)
	old, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if old.Config().Format != 1 || old.ReadOnly() {
		t.Fatalf("format 無しの config.json: format=%d readOnly=%v", old.Config().Format, old.ReadOnly())
	}
	os.WriteFile(path, data, 0o644)
}

// 予約した番号の次から採番が続き、予約だけの番号は一覧に出ないことを確かめる。
func TestReserveIssueNumbers(t *testing.T) {
	root := t.TempDir()
	st, err := Create(root, Config{Owner: "testowner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateIssue("r", "testuser1", "1", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	created, err := st.ReserveIssueNumbers("r", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 4 || created[0] != 2 || created[3] != 5 {
		t.Fatalf("2〜5 を作るはずが %v", created)
	}
	again, err := st.ReserveIssueNumbers("r", 5)
	if err != nil || len(again) != 0 {
		t.Fatalf("2回目は何も作らないはず: %v %v", again, err)
	}
	is, err := st.CreateIssue("r", "testuser1", "6", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if is.Number != 6 {
		t.Fatalf("次は 6 番のはずが %d", is.Number)
	}
	list, _ := st.ListIssues("r")
	if len(list) != 2 {
		t.Fatalf("一覧は 2 件のはずが %d 件", len(list))
	}
}
