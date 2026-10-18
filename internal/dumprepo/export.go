package dumprepo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// Export はデータルートのリポジトリを dump-repo 形式で outRoot/<owner>/<リポジトリ名>/ に書き出す。
// 編集履歴は最新の本文だけ、削除済みのコメントは出さない。書いたフォルダのパスを返す。
func Export(st *store.Store, repoName, outRoot string, log io.Writer) (string, error) {
	repo, err := st.GetRepo(repoName)
	if err != nil {
		return "", err
	}
	owner := st.Config().Owner
	dir := filepath.Join(outRoot, owner, repoName)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("出力先 %s が空でない", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	bare := filepath.Join(st.Root(), "repos", repoName+".git")
	g := gitcmd.Repo{Dir: bare}

	// git：--mirror の clone
	gitDir := filepath.Join(dir, "git")
	if err := gitcmd.CloneMirror(bare, gitDir); err != nil {
		return "", fmt.Errorf("git の clone に失敗: %w", err)
	}
	fmt.Fprintln(log, "git: clone --mirror")

	// repo.yml（Gitea の dumper と同じ map。restore が読む default_branch も入れる）
	repoYML := map[string]any{
		"name": repoName, "owner": owner, "description": repo.Description, "website": "",
		"clone_addr": "", "original_url": "", "is_private": true, "service_type": serviceTypePlainGit,
		"default_branch": repo.DefaultBranch,
		"wiki":           false, "issues": true, "milestones": false, "labels": true, "releases": true,
		"comments": true, "pulls": true, "assets": true,
	}
	if err := writeYAML(filepath.Join(dir, "repo.yml"), repoYML); err != nil {
		return "", err
	}
	if err := writeYAML(filepath.Join(dir, "topic.yml"), map[string]any{"topics": []string{}}); err != nil {
		return "", err
	}

	// ラベル
	labels, _ := st.ListLabels(repoName)
	byID := map[int]store.Label{}
	var ymlLabels []ymlLabel
	for _, l := range labels {
		byID[l.ID] = l
		ymlLabels = append(ymlLabels, toYMLLabel(l))
	}
	if err := writeYAML(filepath.Join(dir, "label.yml"), nonNil(ymlLabels)); err != nil {
		return "", err
	}
	fmt.Fprintf(log, "label.yml: %d 件\n", len(ymlLabels))

	// Issue と PR
	issues, err := st.ListIssues(repoName)
	if err != nil {
		return "", err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	var ymlIssues []ymlIssue
	var ymlPulls []ymlPullRequest
	comments := 0
	reviews := 0
	for _, is := range issues {
		labelsOf := func(ids []int) []ymlLabel {
			var out []ymlLabel
			for _, id := range ids {
				if l, ok := byID[id]; ok {
					out = append(out, toYMLLabel(l))
				}
			}
			return nonNil(out)
		}
		var closed *time.Time
		if is.State == "closed" && !is.ClosedAt.IsZero() {
			t := is.ClosedAt.UTC()
			closed = &t
		}
		if is.IsPull {
			pr := ymlPullRequest{
				Number: int64(is.Number), Title: is.Title, PosterName: is.User, Content: is.Body, State: is.State,
				Created: is.CreatedAt.UTC(), Updated: is.UpdatedAt.UTC(), Closed: closed, Labels: labelsOf(is.Labels),
				Merged: is.Merged, MergeCommitSHA: is.MergeSHA, Assignees: nonNil(is.Assignees),
				Head:      ymlPRBranch{Ref: is.Head, RepoName: repoName, OwnerName: owner},
				Base:      ymlPRBranch{Ref: is.Base, RepoName: repoName, OwnerName: owner},
				Reactions: []any{},
				// Gitea の restore は PR のコメントとレビューのファイルを foreignindex で引く（Issue は番号に
				// 戻るが PR は戻らない）ので、番号を入れておく
				ForeignIndex: int64(is.Number),
			}
			if is.Merged && !is.MergedAt.IsZero() {
				t := is.MergedAt.UTC()
				pr.MergedTime = &t
			}
			// head の SHA：マージ済みならマージした時点、それ以外は今のブランチ、無ければ作成時
			headSHA := is.HeadSHA
			if is.Merged && is.MergedHeadSHA != "" {
				headSHA = is.MergedHeadSHA
			} else if sha, err := g.ResolveCommit("refs/heads/" + is.Head); err == nil {
				headSHA = sha
			}
			pr.Head.SHA = headSHA
			if sha, err := g.ResolveCommit("refs/heads/" + is.Base); err == nil {
				if mb, err := g.MergeBase(sha, headSHA); err == nil {
					pr.Base.SHA = mb
				}
			}
			// Gitea の dumper と同じく refs/pull/<番号>/head を置く（ブランチが消えていても head が引ける）
			if headSHA != "" {
				gitcmd.Repo{Dir: gitDir}.UpdateRef(fmt.Sprintf("refs/pull/%d/head", is.Number), headSHA)
			}
			ymlPulls = append(ymlPulls, pr)
			rs := exportReviews(is)
			if len(rs) > 0 {
				if err := writeYAMLInDir(filepath.Join(dir, "reviews"), fmt.Sprintf("%d.yml", is.Number), rs); err != nil {
					return "", err
				}
				reviews += len(rs)
			}
		} else {
			ymlIssues = append(ymlIssues, ymlIssue{
				Number: int64(is.Number), PosterName: is.User, Title: is.Title, Content: is.Body, State: is.State,
				Created: is.CreatedAt.UTC(), Updated: is.UpdatedAt.UTC(), Closed: closed, Labels: labelsOf(is.Labels),
				Reactions: []any{}, Assignees: nonNil(is.Assignees),
			})
		}
		cs := exportComments(is)
		if len(cs) > 0 {
			if err := writeYAMLInDir(filepath.Join(dir, "comments"), fmt.Sprintf("%d.yml", is.Number), cs); err != nil {
				return "", err
			}
			comments += len(cs)
		}
	}
	if err := writeYAML(filepath.Join(dir, "issue.yml"), nonNil(ymlIssues)); err != nil {
		return "", err
	}
	if err := writeYAML(filepath.Join(dir, "pull_request.yml"), nonNil(ymlPulls)); err != nil {
		return "", err
	}
	fmt.Fprintf(log, "issue.yml: %d 件、pull_request.yml: %d 件、comments: %d 件、reviews: %d 件\n", len(ymlIssues), len(ymlPulls), comments, reviews)

	// リリース
	rels, _ := st.ListReleases(repoName)
	var ymlRels []ymlRelease
	assetCount := 0
	for _, rel := range rels {
		y := ymlRelease{
			TagName: rel.Tag, TargetCommitish: rel.TagSHA, Name: rel.Title, Body: rel.Body, Prerelease: rel.Prerelease,
			PublisherName: rel.User, Created: rel.CreatedAt.UTC(), Published: rel.CreatedAt.UTC(), Assets: []ymlReleaseAsset{},
		}
		assets, _ := st.ListReleaseAttachments(repoName, rel.Tag)
		for _, a := range assets {
			src, err := st.ReleaseAttachmentPath(repoName, rel.Tag, a.Name)
			if err != nil {
				continue
			}
			rel := "release_assets/" + rel.Tag + "/" + a.Name
			dst := filepath.Join(dir, filepath.FromSlash(rel))
			if err := copyFile(src, dst); err != nil {
				return "", err
			}
			size := int(a.Size)
			zero := 0
			y.Assets = append(y.Assets, ymlReleaseAsset{Name: a.Name, Size: &size, DownloadCount: &zero, Created: a.ModTime.UTC(), Updated: a.ModTime.UTC(), DownloadURL: &rel})
			assetCount++
		}
		ymlRels = append(ymlRels, y)
	}
	if err := writeYAML(filepath.Join(dir, "release.yml"), nonNil(ymlRels)); err != nil {
		return "", err
	}
	fmt.Fprintf(log, "release.yml: %d 件、添付 %d 件\n", len(ymlRels), assetCount)
	return dir, nil
}

func toYMLLabel(l store.Label) ymlLabel {
	color := l.Color
	if color != "" && !strings.HasPrefix(color, "#") {
		color = "#" + color
	}
	return ymlLabel{Name: l.Name, Color: color, Description: l.Description}
}

// exportComments はコメント、クローズ、再オープン、タイトル変更を Gitea のコメントにする。
// 削除済みは出さず、編集済みは最新の本文だけを出す。
func exportComments(is store.Issue) []ymlComment {
	var out []ymlComment
	title := ""
	for _, ev := range is.Events {
		c := ymlComment{IssueIndex: int64(is.Number), Index: int64(len(out) + 1), PosterName: ev.User, Created: ev.Time.UTC(), Updated: ev.Time.UTC(), Reactions: []any{}}
		if !ev.EditedAt.IsZero() {
			c.Updated = ev.EditedAt.UTC()
		}
		switch ev.Type {
		case store.EventCreated:
			title = ev.Title
			continue
		case store.EventComment:
			if ev.Deleted {
				continue
			}
			c.CommentType, c.Content = "comment", ev.Body
		case store.EventClosed:
			c.CommentType = "close"
			if ev.Body != "" && !ev.Deleted {
				// 本文付きのクローズは、コメントとクローズの2つに分ける（Gitea は close の本文を捨てる）
				out = append(out, ymlComment{IssueIndex: c.IssueIndex, Index: c.Index, CommentType: "comment", PosterName: ev.User, Created: c.Created, Updated: c.Updated, Content: ev.Body, Reactions: []any{}})
				c.Index++
			}
		case store.EventReopened:
			c.CommentType = "reopen"
			if ev.Body != "" && !ev.Deleted {
				out = append(out, ymlComment{IssueIndex: c.IssueIndex, Index: c.Index, CommentType: "comment", PosterName: ev.User, Created: c.Created, Updated: c.Updated, Content: ev.Body, Reactions: []any{}})
				c.Index++
			}
		case store.EventTitle:
			c.CommentType = "change_title"
			c.Meta = map[string]any{"OldTitle": title, "NewTitle": ev.Title}
			title = ev.Title
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

// exportReviews はレビューと行コメントを Gitea の Review にする。行コメントは、その後に来た同じ人の
// レビューに付ける。レビューが続かない行コメントは COMMENTED のレビューとして出す。
func exportReviews(is store.Issue) []ymlReview {
	var out []ymlReview
	var pending []ymlReviewComment
	pendingUser, pendingTime, pendingCommit := "", time.Time{}, ""
	flush := func(r ymlReview) {
		r.Comments = append(nonNil(pending), r.Comments...)
		r.ID = int64(len(out) + 1)
		r.IssueIndex = int64(is.Number)
		out = append(out, r)
		pending = nil
	}
	for _, ev := range is.Events {
		if ev.Deleted {
			continue
		}
		switch ev.Type {
		case store.EventReviewComment:
			line := ev.Line
			if ev.Side == "old" {
				line = -line
			}
			if len(pending) == 0 {
				pendingUser, pendingTime, pendingCommit = ev.User, ev.Time, ev.CommitSHA
			}
			pending = append(pending, ymlReviewComment{
				ID: int64(len(pending) + 1), Content: ev.Body, TreePath: ev.Path, DiffHunk: ev.DiffHunk, Line: line,
				CommitID: ev.CommitSHA, Reactions: []any{}, CreatedAt: ev.Time.UTC(), UpdatedAt: ev.Time.UTC(),
			})
		case store.EventReview:
			state := "COMMENTED"
			switch ev.State {
			case store.ReviewApproved:
				state = "APPROVED"
			case store.ReviewChangesRequested:
				state = "CHANGES_REQUESTED"
			}
			flush(ymlReview{ReviewerName: ev.User, CommitID: ev.CommitSHA, Content: ev.Body, CreatedAt: ev.Time.UTC(), State: state})
		}
	}
	if len(pending) > 0 {
		flush(ymlReview{ReviewerName: pendingUser, CommitID: pendingCommit, CreatedAt: pendingTime.UTC(), State: "COMMENTED"})
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func writeYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeYAMLInDir(dir, name string, v any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeYAML(filepath.Join(dir, name), v)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
