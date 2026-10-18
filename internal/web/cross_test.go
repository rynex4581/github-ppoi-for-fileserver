package web

import (
	"testing"

	"github-ppoi-for-fileserver/internal/store"
)

func TestIssueMatches(t *testing.T) {
	is := store.Issue{Title: "README に起動手順を書く", Body: "本文 Body", Events: []store.Event{
		{Type: store.EventComment, Body: "コメントの中の言葉"},
		{Type: store.EventReviewComment, Body: "行コメント"},
		{Type: store.EventComment, Body: "消したコメント", Deleted: true},
		{Type: store.EventTitle, Title: "古い題名", Body: ""},
	}}
	cases := []struct {
		q    string
		want bool
	}{
		{"", true},
		{"起動手順", true},
		{"body", true},
		{"中の言葉", true},
		{"行コメ", true},
		{"消した", false},
		{"無い言葉", false},
	}
	for _, c := range cases {
		if got := issueMatches(is, c.q); got != c.want {
			t.Errorf("%q: %v のはずが %v", c.q, c.want, got)
		}
	}
}
