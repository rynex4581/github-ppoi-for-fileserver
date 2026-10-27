package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// テストで使う名前はすべて架空のもの。

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Create(t.TempDir(), Config{Owner: "team-a", OwnerName: "試験チーム"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateAndOpen(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root); err == nil {
		t.Fatal("config.json の無いフォルダが開けてしまう")
	}
	if _, err := Create(root, Config{Owner: "日本語"}); err == nil {
		t.Fatal("ASCII でない owner 名が通った")
	}
	if _, err := Create(root, Config{Owner: ""}); err == nil {
		t.Fatal("空の owner 名が通った")
	}
	if _, err := Create(root, Config{Owner: "team."}); err == nil {
		t.Fatal("末尾が . の owner 名で新しく作れた")
	}
	s, err := Create(root, Config{Owner: "team-a", OwnerName: "試験チーム"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Config().Owner != "team-a" || s.Config().DisplayOwner() != "試験チーム" || s.Config().DefaultBranch != "main" {
		t.Fatalf("config の内容が違う: %+v", s.Config())
	}
	if _, err := Create(root, Config{Owner: "team-b"}); err != ErrConfigExists {
		t.Errorf("二重の初期化が ErrConfigExists にならない: %v", err)
	}
	again, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if again.Config().Owner != "team-a" {
		t.Errorf("開き直した config が違う: %+v", again.Config())
	}
	if (Config{Owner: "x"}).DisplayOwner() != "x" {
		t.Error("表示名が無いときに owner 名で代用していない")
	}
	for _, p := range []string{"config.json", "repos", filepath.Join("meta", "users")} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s が無い: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "config.json.tmp")); err == nil {
		t.Error("一時ファイルが残っている")
	}
}

func TestOpenRejectsMissingRoot(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nothing")); err == nil {
		t.Fatal("存在しないデータルートでエラーにならない")
	}
	if _, err := Create(filepath.Join(t.TempDir(), "nothing"), Config{Owner: "x"}); err == nil {
		t.Fatal("存在しないフォルダを初期化できてしまう")
	}
}

func TestSetUserName(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetUserName("testuser1", " 試験 太郎 "); err != nil {
		t.Fatal(err)
	}
	if got := s.DisplayName("testuser1"); got != "試験 太郎" {
		t.Errorf("表示名が違う: %q", got)
	}
	if err := s.SetUserName("testuser1", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.DisplayName("testuser1"); got != "testuser1" {
		t.Errorf("空にした後はログオン名に戻るはず: %q", got)
	}
}

func TestCreateAndListRepos(t *testing.T) {
	s := newTestStore(t)
	r, err := s.CreateRepo("sample-tool", "動作確認用")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "sample-tool" || r.Description != "動作確認用" || r.DefaultBranch != "main" {
		t.Fatalf("作成結果が違う: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "repos", "sample-tool.git", "HEAD")); err != nil {
		t.Errorf("ベアリポジトリができていない: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "meta", "sample-tool", "repo.json")); err != nil {
		t.Errorf("repo.json ができていない: %v", err)
	}

	if _, err := s.CreateRepo("sample-tool", ""); err != ErrRepoExists {
		t.Errorf("同名の作成が ErrRepoExists にならない: %v", err)
	}

	if _, err := s.CreateRepo("another", ""); err != nil {
		t.Fatal(err)
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].Name != "another" || repos[1].Name != "sample-tool" {
		t.Fatalf("一覧が違う: %+v", repos)
	}

	got, err := s.GetRepo("sample-tool")
	if err != nil || got.Description != "動作確認用" {
		t.Fatalf("GetRepo の結果が違う: %+v, %v", got, err)
	}
	if _, err := s.GetRepo("nothing"); err != ErrRepoNotFound {
		t.Errorf("無いリポジトリが ErrRepoNotFound にならない: %v", err)
	}
}

func TestValidateRepoName(t *testing.T) {
	ok := []string{"a", "sample-tool", "Tool_1.2", "x" + strings.Repeat("y", 99)}
	ng := []string{"", "-a", ".a", "a b", "a/b", "a\\b", "a..b", "a.git", "x" + strings.Repeat("y", 100)}
	for _, n := range ok {
		if err := ValidateRepoName(n); err != nil {
			t.Errorf("%q が拒否された: %v", n, err)
		}
	}
	for _, n := range ng {
		if err := ValidateRepoName(n); err == nil {
			t.Errorf("%q が通った", n)
		}
	}
}

func TestIssueNumberingAndEvents(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("sample-tool", ""); err != nil {
		t.Fatal(err)
	}

	first, err := s.CreateIssue("sample-tool", "testuser1", "最初のIssue", "本文1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateIssue("sample-tool", "testuser2", "次のIssue", "", []int{1, 2}, []string{"testuser2"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Number != 1 || second.Number != 2 {
		t.Fatalf("番号が連番でない: %d, %d", first.Number, second.Number)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "meta", "sample-tool", "issues", "000002", "events")); err != nil {
		t.Errorf("6桁ゼロ埋めのフォルダができていない: %v", err)
	}

	// 他の端末が先に番号を取っていた体で、フォルダだけ作っておく
	if err := os.Mkdir(filepath.Join(s.Root(), "meta", "sample-tool", "issues", "000003"), 0o755); err != nil {
		t.Fatal(err)
	}
	third, err := s.CreateIssue("sample-tool", "testuser1", "飛ばされる番号の次", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.Number != 4 {
		t.Fatalf("既に存在する番号を避けていない: %d", third.Number)
	}
	if len(second.Labels) != 2 || !second.HasLabel(2) || !second.HasAssignee("testuser2") {
		t.Errorf("作成時のラベルと担当者が入っていない: %+v", second)
	}
	if err := s.AppendEvent("sample-tool", 2, Event{Type: EventLabels, User: "testuser1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent("sample-tool", 2, Event{Type: EventAssigned, User: "testuser1", Assignees: []string{"testuser1", "testuser2"}}); err != nil {
		t.Fatal(err)
	}
	second, _ = s.GetIssue("sample-tool", 2)
	if len(second.Labels) != 0 || len(second.Assignees) != 2 {
		t.Errorf("ラベル全解除と担当者変更が反映されていない: %+v", second)
	}

	// イベントのファイル名の形式
	entries, err := os.ReadDir(filepath.Join(s.Root(), "meta", "sample-tool", "issues", "000001", "events"))
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^\d{8}T\d{9}Z_[A-Za-z0-9_-]+_\d{3,}\.json$`)
	for _, e := range entries {
		if !pattern.MatchString(e.Name()) {
			t.Errorf("イベントのファイル名の形式が違う: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("created イベントが1件でない: %d", len(entries))
	}

	// コメント、クローズ、再オープンを積んで組み立てる
	for _, ev := range []Event{
		{Type: EventComment, User: "testuser2", Body: "コメント1"},
		{Type: EventClosed, User: "testuser1"},
		{Type: EventReopened, User: "testuser1", Body: "やはり開ける"},
		{Type: EventTitle, User: "testuser1", Title: "改題"},
	} {
		if err := s.AppendEvent("sample-tool", 1, ev); err != nil {
			t.Fatal(err)
		}
	}
	is, err := s.GetIssue("sample-tool", 1)
	if err != nil {
		t.Fatal(err)
	}
	if is.Title != "改題" || is.Body != "本文1" || is.User != "testuser1" || is.State != "open" {
		t.Errorf("組み立て結果が違う: %+v", is)
	}
	if is.Comments != 2 {
		t.Errorf("コメント数が違う: %d", is.Comments)
	}
	if len(is.Events) != 5 {
		t.Errorf("イベント数が違う: %d", len(is.Events))
	}
	if !is.ClosedAt.IsZero() {
		t.Error("再オープン後に ClosedAt が残っている")
	}

	if err := s.AppendEvent("sample-tool", 1, Event{Type: EventClosed, User: "testuser2", Body: "完了"}); err != nil {
		t.Fatal(err)
	}
	is, _ = s.GetIssue("sample-tool", 1)
	if is.State != "closed" || is.ClosedAt.IsZero() || is.Comments != 3 {
		t.Errorf("クローズ後の状態が違う: %+v", is)
	}

	// 一覧は番号の降順。created の無い残骸（000003）は出ない
	issues, err := s.ListIssues("sample-tool")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 || issues[0].Number != 4 || issues[1].Number != 2 || issues[2].Number != 1 {
		t.Fatalf("一覧が違う: %+v", issues)
	}

	if err := s.AppendEvent("sample-tool", 99, Event{Type: EventComment}); err != ErrIssueNotFound {
		t.Errorf("無いIssueへの追記が ErrIssueNotFound にならない: %v", err)
	}
	if _, err := s.GetIssue("sample-tool", 3); err != ErrIssueNotFound {
		t.Errorf("残骸のフォルダが ErrIssueNotFound にならない: %v", err)
	}
	if _, err := s.CreateIssue("nothing", "testuser1", "x", "", nil, nil); err != ErrRepoNotFound {
		t.Errorf("無いリポジトリへの作成が ErrRepoNotFound にならない: %v", err)
	}

	// 一時ファイルが残っていないこと
	filepath.WalkDir(s.Root(), func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(d.Name(), ".tmp") {
			t.Errorf("一時ファイルが残っている: %s", path)
		}
		return nil
	})
}

func TestDisplayName(t *testing.T) {
	s := newTestStore(t)
	if got := s.DisplayName("testuser1"); got != "testuser1" {
		t.Errorf("未登録の表示名が違う: %s", got)
	}
	path := filepath.Join(s.Root(), "meta", "users", "testuser1.json")
	if err := os.WriteFile(path, []byte(`{"name": "試験 太郎"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.DisplayName("testuser1"); got != "試験 太郎" {
		t.Errorf("登録済みの表示名が違う: %s", got)
	}
	if got := s.DisplayName("../x"); got != "../x" {
		t.Errorf("不正な名前でファイルを読みに行っている: %s", got)
	}
}

func TestLabels(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("sample-tool", ""); err != nil {
		t.Fatal(err)
	}
	labels, err := s.ListLabels("sample-tool")
	if err != nil || len(labels) != 0 {
		t.Fatalf("初期状態が空でない: %v %v", labels, err)
	}
	bug, err := s.CreateLabel("sample-tool", Label{Name: "bug", Color: "#D73A4A", Description: "不具合"})
	if err != nil {
		t.Fatal(err)
	}
	if bug.ID != 1 || bug.Color != "d73a4a" {
		t.Errorf("作成結果が違う: %+v", bug)
	}
	if _, err := s.CreateLabel("sample-tool", Label{Name: "Bug", Color: "000000"}); err == nil {
		t.Error("同名（大文字小文字違い）が通った")
	}
	if _, err := s.CreateLabel("sample-tool", Label{Name: "x", Color: "red"}); err == nil {
		t.Error("不正な色が通った")
	}
	doc, err := s.CreateLabel("sample-tool", Label{Name: "documentation", Color: "0075ca"})
	if err != nil || doc.ID != 2 {
		t.Fatalf("2つ目のIDが違う: %+v %v", doc, err)
	}
	if err := s.UpdateLabel("sample-tool", Label{ID: 1, Name: "bug", Color: "ff0000", Description: "直す"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetLabel("sample-tool", 1)
	if got.Color != "ff0000" || got.Description != "直す" {
		t.Errorf("更新が反映されていない: %+v", got)
	}
	if err := s.DeleteLabel("sample-tool", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLabel("sample-tool", 1); err != ErrLabelNotFound {
		t.Errorf("削除後に残っている: %v", err)
	}
	labels, _ = s.ListLabels("sample-tool")
	if len(labels) != 1 || labels[0].ID != 2 {
		t.Errorf("削除後の一覧が違う: %+v", labels)
	}
	if _, err := s.CreateLabel("sample-tool", Label{Name: "bug", Color: "d73a4a"}); err != nil {
		t.Fatal(err)
	}
	labels, _ = s.ListLabels("sample-tool")
	if labels[1].ID != 3 {
		t.Errorf("IDは最大＋1で振る: %+v", labels)
	}
}

func TestUsers(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureUser("testuser1"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureUser("testuser1"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureUser("../x"); err == nil {
		t.Error("不正なログオン名が通った")
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "meta", "users", "testuser2.json"), []byte(`{"name": "試験 次郎"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Login != "testuser1" || users[1].Name != "試験 次郎" {
		t.Errorf("利用者一覧が違う: %+v", users)
	}
}

func TestPullRequestEvents(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("sample-tool", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIssue("sample-tool", "testuser1", "先にIssue", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	pr, err := s.CreatePull("sample-tool", "testuser1", "機能追加", "本文", "feature/x", "main", "abc1234", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 2 || !pr.IsPull || pr.Head != "feature/x" || pr.Base != "main" || pr.HeadSHA != "abc1234" {
		t.Fatalf("PR の作成結果が違う: %+v", pr)
	}
	for _, ev := range []Event{
		{Type: EventReview, User: "testuser2", State: ReviewChangesRequested, Body: "直して", CommitSHA: "abc1234"},
		{Type: EventReviewComment, User: "testuser2", Body: "ここ", CommitSHA: "abc1234", Path: "src/main.go", Line: 3, Side: "new", DiffHunk: "@@ -1,3 +1,3 @@"},
		{Type: EventReview, User: "testuser2", State: ReviewApproved, CommitSHA: "def5678"},
		{Type: EventReview, User: "testuser3", State: ReviewComment, Body: "見た"},
	} {
		if err := s.AppendEvent("sample-tool", 2, ev); err != nil {
			t.Fatal(err)
		}
	}
	pr, _ = s.GetIssue("sample-tool", 2)
	if len(pr.Reviews) != 1 || pr.Reviews[0].User != "testuser2" || pr.Reviews[0].State != ReviewApproved || !pr.Approved() {
		t.Errorf("承認状態の集計が違う: %+v", pr.Reviews)
	}
	if pr.Comments != 3 {
		t.Errorf("コメント数が違う（本文ありのレビュー2 + 行コメント1）: %d", pr.Comments)
	}
	if pr.State != "open" || pr.Merged {
		t.Errorf("マージ前の状態が違う: %+v", pr)
	}
	if err := s.AppendEvent("sample-tool", 2, Event{Type: EventMerged, User: "testuser1", MergeSHA: "m1", HeadSHA: "def5678"}); err != nil {
		t.Fatal(err)
	}
	pr, _ = s.GetIssue("sample-tool", 2)
	if !pr.Merged || pr.State != "closed" || pr.MergeSHA != "m1" || pr.MergedBy != "testuser1" || pr.MergedHeadSHA != "def5678" || pr.ClosedAt.IsZero() {
		t.Errorf("マージ後の状態が違う: %+v", pr)
	}
	if err := s.AppendEvent("sample-tool", 2, Event{Type: EventReverted, User: "testuser3", MergeSHA: "m1", RevertSHA: "r1"}); err != nil {
		t.Fatal(err)
	}
	pr, _ = s.GetIssue("sample-tool", 2)
	if pr.RevertSHA != "r1" || pr.RevertedBy != "testuser3" {
		t.Errorf("Revert の状態が違う: %+v", pr)
	}
	// Issue 側には PR の項目が付かない
	is, _ := s.GetIssue("sample-tool", 1)
	if is.IsPull || is.Head != "" {
		t.Errorf("Issue に PR の項目が付いている: %+v", is)
	}
}

func TestAttachments(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateRepo("sample-tool", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAttachment("sample-tool", 1, "a.png", []byte("x")); err != ErrIssueNotFound {
		t.Errorf("無い Issue への添付が ErrIssueNotFound にならない: %v", err)
	}
	number, err := s.AllocateIssue("sample-tool")
	if err != nil {
		t.Fatal(err)
	}
	name, err := s.SaveAttachment("sample-tool", number, "C:\\temp\\画面 キャプチャ.PNG", []byte("png data"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, ".png") || strings.Contains(name, "\\") || strings.Contains(name, " ") {
		t.Errorf("保存名の形式が違う: %s", name)
	}
	name2, err := s.SaveAttachment("sample-tool", number, "", []byte("y"))
	if err != nil || !strings.Contains(name2, "_file") {
		t.Errorf("名前の無いファイルの保存名が違う: %s %v", name2, err)
	}
	p, err := s.AttachmentPath("sample-tool", number, name)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "png data" {
		t.Errorf("中身が違う: %q", data)
	}
	if _, err := s.AttachmentPath("sample-tool", number, "../"+name); err != ErrAttachmentNotFound {
		t.Error("上位フォルダへの参照が通った")
	}
	if _, err := s.AttachmentPath("sample-tool", number, "nothing.png"); err != ErrAttachmentNotFound {
		t.Error("無いファイルが ErrAttachmentNotFound にならない")
	}
	big := make([]byte, s.AttachmentMaxBytes()+1)
	if _, err := s.SaveAttachment("sample-tool", number, "big.bin", big); err != ErrAttachmentTooLarge {
		t.Errorf("上限超えが ErrAttachmentTooLarge にならない: %v", err)
	}
	is, err := s.WriteCreated("sample-tool", number, Event{User: "testuser1", Title: "添付あり", Body: "![image](/attachments/x)"}, nil, nil)
	if err != nil || is.Number != number || is.Title != "添付あり" {
		t.Fatalf("WriteCreated の結果が違う: %+v %v", is, err)
	}
}

func TestEventCache(t *testing.T) {
	s := newTestStore(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	s.SetCacheDir(cacheDir)
	if _, err := s.CreateRepo("sample-tool", ""); err != nil {
		t.Fatal(err)
	}
	is, err := s.CreateIssue("sample-tool", "testuser1", "キャッシュ", "本文", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent("sample-tool", is.Number, Event{Type: EventComment, User: "testuser1", Body: "1"}); err != nil {
		t.Fatal(err)
	}
	if n, e := s.CacheStats("sample-tool"); n != 1 || e != 2 {
		t.Errorf("書いたイベントがキャッシュに入っていない: %d issues %d events", n, e)
	}

	// 別の端末が書いた体で、ファイルを直接置く
	dir := filepath.Join(s.Root(), "meta", "sample-tool", "issues", "000001", "events")
	other := `{"type":"comment","user":"testuser2","time":"2030-01-01T00:00:00Z","body":"他の端末から"}`
	if err := os.WriteFile(filepath.Join(dir, "20300101T000000000Z_other-pc_001.json"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIssue("sample-tool", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Comments != 2 || len(got.Events) != 3 {
		t.Errorf("他の端末のイベントを読み足していない: %+v", got)
	}
	if _, e := s.CacheStats("sample-tool"); e != 3 {
		t.Errorf("読み足したイベントがキャッシュに入っていない: %d", e)
	}

	// 一時ファイルは拾わない
	if err := os.WriteFile(filepath.Join(dir, "20300101T000000001Z_other-pc_002.json.tmp"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetIssue("sample-tool", 1)
	if len(got.Events) != 3 {
		t.Errorf("一時ファイルを拾っている: %d", len(got.Events))
	}

	// ディスクに書いて、新しい Store で読み直す。ファイルを読まずに済むことは、フォルダから json を消して確かめる
	s.FlushCache()
	if _, err := os.Stat(filepath.Join(cacheDir, "sample-tool.json")); err != nil {
		t.Fatalf("キャッシュのファイルが無い: %v", err)
	}
	s2, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	s2.SetCacheDir(cacheDir)
	// 中身を壊して、読み直していないことを確かめる（ファイル名は残す）
	if err := os.WriteFile(filepath.Join(dir, "20300101T000000000Z_other-pc_001.json"), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = s2.GetIssue("sample-tool", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Comments != 2 {
		t.Errorf("キャッシュから復元できていない: %+v", got)
	}

	// ファイルが消えたらキャッシュからも消える
	if err := os.Remove(filepath.Join(dir, "20300101T000000000Z_other-pc_001.json")); err != nil {
		t.Fatal(err)
	}
	got, _ = s2.GetIssue("sample-tool", 1)
	if got.Comments != 1 {
		t.Errorf("消えたファイルがキャッシュに残っている: %+v", got)
	}

	// Issue のフォルダごと消えたら ErrIssueNotFound
	if err := os.RemoveAll(filepath.Join(s.Root(), "meta", "sample-tool", "issues", "000001")); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.GetIssue("sample-tool", 1); err != ErrIssueNotFound {
		t.Errorf("消えた Issue が ErrIssueNotFound にならない: %v", err)
	}
	if n, _ := s2.CacheStats("sample-tool"); n != 0 {
		t.Errorf("消えた Issue がキャッシュに残っている: %d", n)
	}
}
