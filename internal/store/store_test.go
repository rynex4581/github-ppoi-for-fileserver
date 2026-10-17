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
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenCreatesConfigAndDirs(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if s.Config().Owner == "" || s.Config().DefaultBranch == "" {
		t.Fatalf("config の既定値が入っていない: %+v", s.Config())
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
