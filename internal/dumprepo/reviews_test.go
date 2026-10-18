package dumprepo

import (
	"testing"
	"time"

	"github-ppoi-for-fileserver/internal/store"
)

// 新20：alice の行コメントの後に bob が承認しても、alice の行コメントは alice の COMMENTED のレビューになり、
// bob の承認には付かない。alice 自身のレビューが続けば、そちらに付く。
func TestExportReviewsByPoster(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	is := store.Issue{Number: 7, Events: []store.Event{
		{Type: store.EventReviewComment, User: "alice", Body: "alice line", Path: "a.txt", Line: 1, CommitSHA: "c1", Time: t0},
		{Type: store.EventReview, User: "bob", State: store.ReviewApproved, Body: "LGTM", CommitSHA: "c1", Time: t0.Add(time.Minute)},
		{Type: store.EventReviewComment, User: "carol", Body: "carol line", Path: "a.txt", Line: 2, CommitSHA: "c1", Time: t0.Add(2 * time.Minute)},
		{Type: store.EventReview, User: "carol", State: store.ReviewChangesRequested, Body: "fix", CommitSHA: "c1", Time: t0.Add(3 * time.Minute)},
		{Type: store.EventReviewComment, User: "dave", Body: "dave line", Path: "a.txt", Line: 3, CommitSHA: "c1", Time: t0.Add(4 * time.Minute)},
	}}
	got := exportReviews(is)
	want := []struct {
		user, state string
		comments    int
	}{{"alice", "COMMENTED", 1}, {"bob", "APPROVED", 0}, {"carol", "CHANGES_REQUESTED", 1}, {"dave", "COMMENTED", 1}}
	if len(got) != len(want) {
		t.Fatalf("レビューの数が %d（%+v）", len(got), got)
	}
	for i, w := range want {
		r := got[i]
		if r.ReviewerName != w.user || r.State != w.state || len(r.Comments) != w.comments || r.ID != int64(i+1) || r.IssueIndex != 7 {
			t.Errorf("%d 番目: %s %s comments=%d id=%d", i, r.ReviewerName, r.State, len(r.Comments), r.ID)
		}
		for _, c := range r.Comments {
			if c.Content != w.user+" line" {
				t.Errorf("%s のレビューに %q が付いている", w.user, c.Content)
			}
		}
	}
}
