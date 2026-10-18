package store

import (
	"errors"
	"testing"
)

func TestReleases(t *testing.T) {
	st, err := Create(t.TempDir(), Config{Owner: "testowner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRelease("r", "testuser1", "v1/0", "", "x", "", false); !errors.Is(err, ErrBadTagName) {
		t.Fatalf("フォルダ名に使えないタグが通る: %v", err)
	}
	rel, err := st.CreateRelease("r", "testuser1", "v1.0", "0123456789abcdef0123456789abcdef01234567", "最初の版", "本文", false)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v1.0" || rel.Title != "最初の版" || rel.User != "testuser1" || rel.TagSHA == "" {
		t.Fatalf("作ったリリースが違う: %+v", rel)
	}
	if _, err := st.CreateRelease("r", "testuser1", "v1.0", "", "二重", "", false); !errors.Is(err, ErrReleaseExists) {
		t.Fatalf("同じタグで二重に作れる: %v", err)
	}
	if err := st.EditRelease("r", "testuser2", "v1.0", "直した版", "本文2", true); err != nil {
		t.Fatal(err)
	}
	rel, _ = st.GetRelease("r", "v1.0")
	if rel.Title != "直した版" || !rel.Prerelease || rel.EditedBy != "testuser2" || rel.User != "testuser1" {
		t.Fatalf("編集が反映されていない: %+v", rel)
	}
	name, err := st.SaveReleaseAttachment("r", "v1.0", "tool.zip", []byte("zip"))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListReleaseAttachments("r", "v1.0")
	if len(list) != 1 || list[0].Name != name || list[0].Size != 3 {
		t.Fatalf("添付の一覧が違う: %+v", list)
	}
	if _, err := st.ReleaseAttachmentPath("r", "v1.0", name); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRelease("r", "testuser1", "v0.9", "", "古い版", "", true); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListReleases("r")
	if len(all) != 2 || all[0].Tag != "v0.9" {
		t.Fatalf("一覧が新しい順でない: %+v", all)
	}
	if err := st.DeleteRelease("r", "testuser1", "v1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRelease("r", "v1.0"); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("削除したのに読める: %v", err)
	}
	all, _ = st.ListReleases("r")
	if len(all) != 1 {
		t.Fatalf("削除後の一覧が %d 件", len(all))
	}
	// 削除後に同じタグで作り直せる
	if _, err := st.CreateRelease("r", "testuser1", "v1.0", "", "作り直し", "", false); err != nil {
		t.Fatal(err)
	}
	rel, _ = st.GetRelease("r", "v1.0")
	if rel.Title != "作り直し" || rel.EditedBy != "" {
		t.Fatalf("作り直しが違う: %+v", rel)
	}
}
