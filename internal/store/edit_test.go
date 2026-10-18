package store

import (
	"errors"
	"testing"
)

// 本文とコメントの編集、コメントの削除。元のイベントは残り、表示用の結果だけが変わる。
func TestEditAndDelete(t *testing.T) {
	root := t.TempDir()
	st, err := Create(root, Config{Owner: "testowner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	is, err := st.CreateIssue("r", "testuser1", "題名", "最初の本文", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent("r", is.Number, Event{Type: EventComment, User: "testuser2", Body: "コメント1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent("r", is.Number, Event{Type: EventComment, User: "testuser2", Body: "コメント2"}); err != nil {
		t.Fatal(err)
	}
	is, _ = st.GetIssue("r", is.Number)
	if is.BodyFile == "" || is.Comments != 2 {
		t.Fatalf("前提が違う: BodyFile=%q Comments=%d", is.BodyFile, is.Comments)
	}
	var c1, c2 string
	for _, ev := range is.Events {
		if ev.Type == EventComment {
			if c1 == "" {
				c1 = ev.File
			} else {
				c2 = ev.File
			}
		}
	}

	// 本文を2回編集する
	if err := st.EditBody("r", is.Number, "testuser1", is.BodyFile, "2番目の本文"); err != nil {
		t.Fatal(err)
	}
	if err := st.EditBody("r", is.Number, "testuser3", is.BodyFile, "3番目の本文"); err != nil {
		t.Fatal(err)
	}
	// コメント1を編集し、コメント2を消す
	if err := st.EditBody("r", is.Number, "testuser2", c1, "コメント1（直した）"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteComment("r", is.Number, "testuser2", c2); err != nil {
		t.Fatal(err)
	}

	is, _ = st.GetIssue("r", is.Number)
	if is.Body != "3番目の本文" || is.BodyEditedBy != "testuser3" {
		t.Fatalf("本文の編集が反映されていない: %q by %q", is.Body, is.BodyEditedBy)
	}
	if len(is.BodyHistory) != 2 || is.BodyHistory[0].Body != "最初の本文" || is.BodyHistory[0].User != "testuser1" || is.BodyHistory[1].Body != "2番目の本文" || is.BodyHistory[1].User != "testuser1" {
		t.Fatalf("本文の履歴が違う: %+v", is.BodyHistory)
	}
	if is.Comments != 1 {
		t.Fatalf("削除後のコメント数が %d", is.Comments)
	}
	for _, ev := range is.Events {
		switch ev.File {
		case c1:
			if ev.Body != "コメント1（直した）" || len(ev.History) != 1 || ev.History[0].Body != "コメント1" || ev.Deleted {
				t.Fatalf("コメント1の編集が違う: %+v", ev)
			}
		case c2:
			if !ev.Deleted {
				t.Fatal("コメント2が削除になっていない")
			}
		}
	}

	// 本文は消せない。無い対象は ErrEventNotFound
	if err := st.DeleteComment("r", is.Number, "testuser1", is.BodyFile); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("本文の削除が通った: %v", err)
	}
	if err := st.EditBody("r", is.Number, "testuser1", "nothing.json", "x"); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("無い対象の編集が通った: %v", err)
	}
	// 元のイベントは全部残っている（created 1、comment 2、edited 3、deleted 1）
	raw, _ := st.ReadEvents("r", is.Number)
	if len(raw) != 7 {
		t.Fatalf("イベントの数が %d", len(raw))
	}
}
