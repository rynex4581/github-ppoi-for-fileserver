package web

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// 低9：画面を開いた後に他の人がラベル（担当者）を変えていたら、古い画面からの適用は 409 で弾き、
// 先の変更が消えないことを確かめる。フォームの hidden（seen）に開いたときの最後の変更イベント名を
// 持たせ、保存時に今のものと比べる。
func TestStaleLabelsRejected(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	st, err := store.Create(root, store.Config{Owner: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRepo("r", ""); err != nil {
		t.Fatal(err)
	}
	bug, err := st.CreateLabel("r", store.Label{Name: "bug", Color: "d73a4a"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := st.CreateLabel("r", store.Label{Name: "doc", Color: "0075ca"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateIssue("r", "yamada", "t", "b", nil, nil); err != nil {
		t.Fatal(err)
	}
	srv, err := New(localconfig.Config{DataRoots: []string{root}, Lang: "ja-JP"}, "yamada", 9999)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost:9999"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost:9999"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	issue := func() store.Issue {
		st.FlushCache()
		is, err := st.GetIssue("r", 1)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	labelsOf := func(is store.Issue) string {
		var parts []string
		for _, id := range is.Labels {
			parts = append(parts, map[int]string{bug.ID: "bug", doc.ID: "doc"}[id])
		}
		return strings.Join(parts, ",")
	}

	// 画面の hidden は、ラベルの変更イベントが無いので空
	w := get("/demo/r/issues/1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="seen" value=""`) {
		t.Fatalf("詳細画面: %d", w.Code)
	}

	// A と B が同じ画面を開いた（seen は空）。B が bug を付ける
	w = post("/demo/r/issues/1/labels", "seen=&labels="+strconv.Itoa(bug.ID))
	if w.Code != 303 {
		t.Fatalf("B の適用が %d: %s", w.Code, w.Body.String())
	}
	if got := labelsOf(issue()); got != "bug" {
		t.Fatalf("B の後のラベルが %q", got)
	}

	// A は古い画面から doc だけを付けて適用。409 で弾かれ、B の bug は残る
	w = post("/demo/r/issues/1/labels", "seen=&labels="+strconv.Itoa(doc.ID))
	if w.Code != 409 {
		t.Fatalf("古い画面からの適用が %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "画面を読み直して") {
		t.Fatal("409 の画面に案内が無い")
	}
	if got := labelsOf(issue()); got != "bug" {
		t.Fatalf("弾いたのにラベルが %q", got)
	}

	// A が画面を読み直すと hidden に今のイベント名が入り、それを付けて送れば通る
	seen := lastEventFile(issue(), store.EventLabels)
	if seen == "" {
		t.Fatal("ラベル変更のイベント名が取れない")
	}
	w = get("/demo/r/issues/1")
	if !strings.Contains(w.Body.String(), `name="seen" value="`+seen+`"`) {
		t.Fatal("読み直した画面の hidden が今のイベント名になっていない")
	}
	w = post("/demo/r/issues/1/labels", "seen="+seen+"&labels="+strconv.Itoa(bug.ID)+"&labels="+strconv.Itoa(doc.ID))
	if w.Code != 303 {
		t.Fatalf("読み直した後の適用が %d: %s", w.Code, w.Body.String())
	}
	if got := labelsOf(issue()); got != "bug,doc" {
		t.Fatalf("読み直した後のラベルが %q", got)
	}

	// 担当者も同じ仕組み。変更イベントを 1 つ書いた後、空の seen は 409
	if err := st.AppendEvent("r", 1, store.Event{Type: store.EventAssigned, User: "suzuki", Assignees: []string{"suzuki"}}); err != nil {
		t.Fatal(err)
	}
	w = post("/demo/r/issues/1/assignees", "seen=&assignees=suzuki")
	if w.Code != 409 {
		t.Fatalf("担当者の古い画面からの適用が %d", w.Code)
	}
}
