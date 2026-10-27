package dumprepo

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// Result はインポートで取り込んだ件数と読み飛ばしたもの。
type Result struct {
	Repo     string
	Labels   int
	Issues   int
	Pulls    int
	Comments int
	Reviews  int
	Releases int
	Assets   int
	Skipped  []string // 読み飛ばしたものの説明
}

// FindDumpDir は dir が dump-repo のフォルダ（repo.yml がある）か、その親（<owner>/<リポジトリ名> を含む）かを
// 判定し、repo.yml のあるフォルダを返す。
func FindDumpDir(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "repo.yml")); err == nil {
		return dir, nil
	}
	var found []string
	owners, _ := os.ReadDir(dir)
	for _, o := range owners {
		if !o.IsDir() {
			continue
		}
		repos, _ := os.ReadDir(filepath.Join(dir, o.Name()))
		for _, r := range repos {
			if r.IsDir() {
				if _, err := os.Stat(filepath.Join(dir, o.Name(), r.Name(), "repo.yml")); err == nil {
					found = append(found, filepath.Join(dir, o.Name(), r.Name()))
				}
			}
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s に repo.yml が無い（dump-repo のフォルダか、その親を指定する）", dir)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%s の下に dump-repo のフォルダが %d 個ある。1つを指定する", dir, len(found))
}

// Import は dump-repo 形式のフォルダをデータルートに取り込む。repoName が空なら repo.yml の name を使う。
// Issue と PR の番号は元のまま保つ。マイルストーンと wiki は読み飛ばす。
// 途中で失敗したら、作った repos\<名前>.git と meta\<名前>\ を消して元に戻す（新27。残すと再実行が
// 「同じ名前のリポジトリが既にある」で止まる）。
func Import(st *store.Store, dir, repoName string, log io.Writer) (Result, error) {
	created := false
	res, err := importRepo(st, dir, repoName, log, &created)
	if err != nil && created {
		bare := filepath.Join(st.Root(), "repos", res.Repo+".git")
		meta := filepath.Dir(st.IssuesDir(res.Repo))
		if e1, e2 := os.RemoveAll(bare), os.RemoveAll(meta); e1 != nil || e2 != nil {
			fmt.Fprintf(log, "作りかけのリポジトリを消せなかった。%s と %s を消してから再実行すること\n", bare, meta)
		} else {
			fmt.Fprintln(log, "作りかけのリポジトリを消して元に戻した")
		}
	}
	return res, err
}

func importRepo(st *store.Store, dir, repoName string, log io.Writer, created *bool) (Result, error) {
	var res Result
	dir, err := FindDumpDir(dir)
	if err != nil {
		return res, err
	}
	var repoYML map[string]any
	if err := readYAML(filepath.Join(dir, "repo.yml"), &repoYML); err != nil {
		return res, err
	}
	if repoName == "" {
		repoName, _ = repoYML["name"].(string)
	}
	if err := store.ValidateRepoName(repoName); err != nil {
		return res, fmt.Errorf("リポジトリ名 %q: %w", repoName, err)
	}
	res.Repo = repoName
	description, _ := repoYML["description"].(string)
	repo, err := st.CreateRepo(repoName, description)
	if err != nil {
		return res, err
	}
	*created = true
	fmt.Fprintf(log, "リポジトリ %s を作った\n", repoName)
	bare := st.GitPath(filepath.Join(st.Root(), "repos", repoName+".git"))
	g := gitcmd.Repo{Dir: bare}

	// git
	gitDir := filepath.Join(dir, "git")
	if _, err := os.Stat(gitDir); err == nil {
		// refs/pull/* は bare に入れない（PR の head は created イベントに持つ。新19）
		if err := gitcmd.PushBranchesAndTags(gitDir, bare); err != nil {
			return res, fmt.Errorf("git の取り込みに失敗: %w", err)
		}
		fmt.Fprintln(log, "git: ブランチとタグを取り込んだ")
	} else {
		res.Skipped = append(res.Skipped, "git フォルダが無いので、リポジトリは空のまま")
	}
	if def, _ := repoYML["default_branch"].(string); def != "" {
		if _, err := g.ResolveCommit("refs/heads/" + def); err == nil {
			repo.DefaultBranch = def
			g.SetHead(def)
			st.UpdateRepo(repo)
		}
	} else if branches, err := g.Branches(); err == nil && len(branches) > 0 {
		// default_branch が無い dump（Gitea 自身の dump-repo もそう）は、HEAD か main か最初のブランチ
		if _, err := g.ResolveCommit("refs/heads/" + repo.DefaultBranch); err != nil {
			repo.DefaultBranch = branches[0].Name
			for _, b := range branches {
				if b.Name == "main" || b.Name == "master" {
					repo.DefaultBranch = b.Name
				}
			}
			g.SetHead(repo.DefaultBranch)
			st.UpdateRepo(repo)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "wiki")); err == nil {
		res.Skipped = append(res.Skipped, "wiki は対応していないので読み飛ばした")
	}
	if _, err := os.Stat(filepath.Join(dir, "milestone.yml")); err == nil {
		res.Skipped = append(res.Skipped, "milestone.yml（マイルストーン）は対応していないので読み飛ばした")
	}

	// ラベル
	var labels []ymlLabel
	if err := readYAMLIfExists(filepath.Join(dir, "label.yml"), &labels); err != nil {
		return res, err
	}
	labelID := map[string]int{}
	for _, l := range labels {
		created, err := st.CreateLabel(repoName, store.Label{Name: l.Name, Color: strings.TrimPrefix(l.Color, "#"), Description: l.Description})
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("ラベル %q: %v", l.Name, err))
			continue
		}
		labelID[l.Name] = created.ID
		res.Labels++
	}
	labelIDs := func(ls []ymlLabel) []int {
		var ids []int
		for _, l := range ls {
			if id, ok := labelID[l.Name]; ok {
				ids = append(ids, id)
			}
		}
		return ids
	}
	user := func(name string) string {
		name = strings.TrimSpace(name)
		if name == "" {
			return "unknown"
		}
		return name
	}
	appendEvent := func(number int, ev store.Event) error {
		return st.AppendEvent(repoName, number, ev)
	}

	// Issue
	var issues []ymlIssue
	if err := readYAMLIfExists(filepath.Join(dir, "issue.yml"), &issues); err != nil {
		return res, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	for _, is := range issues {
		n := int(is.Number)
		if err := st.CreateIssueAt(repoName, n); err != nil {
			return res, fmt.Errorf("Issue #%d: %w", n, err)
		}
		if _, err := st.WriteCreated(repoName, n, store.Event{User: user(is.PosterName), Title: is.Title, Body: is.Content, Time: is.Created}, nil, nil); err != nil {
			return res, err
		}
		if ids := labelIDs(is.Labels); len(ids) > 0 {
			appendEvent(n, store.Event{Type: store.EventLabels, User: user(is.PosterName), Labels: ids, Time: is.Created})
		}
		if len(is.Assignees) > 0 {
			appendEvent(n, store.Event{Type: store.EventAssigned, User: user(is.PosterName), Assignees: is.Assignees, Time: is.Created})
		}
		closedByComment, c, err := importComments(st, repoName, n, dir, &res)
		if err != nil {
			return res, err
		}
		res.Comments += c
		if is.State == "closed" && !closedByComment {
			t := is.Updated
			if is.Closed != nil {
				t = *is.Closed
			}
			appendEvent(n, store.Event{Type: store.EventClosed, User: user(is.PosterName), Time: t})
		}
		res.Issues++
	}

	// Pull Request
	var pulls []ymlPullRequest
	if err := readYAMLIfExists(filepath.Join(dir, "pull_request.yml"), &pulls); err != nil {
		return res, err
	}
	sort.Slice(pulls, func(i, j int) bool { return pulls[i].Number < pulls[j].Number })
	for _, pr := range pulls {
		n := int(pr.Number)
		if err := st.CreateIssueAt(repoName, n); err != nil {
			return res, fmt.Errorf("PR #%d: %w", n, err)
		}
		headSHA := pr.Head.SHA
		if headSHA == "" {
			// dump の git にある refs/pull/<番号>/head から引く（bare には入れていない）
			if sha, err := (gitcmd.Repo{Dir: gitDir}).ResolveCommit(fmt.Sprintf("refs/pull/%d/head", n)); err == nil {
				headSHA = sha
			}
		}
		ev := store.Event{User: user(pr.PosterName), Title: pr.Title, Body: pr.Content, IsPull: true, Head: pr.Head.Ref, Base: pr.Base.Ref, HeadSHA: headSHA, Time: pr.Created}
		if _, err := st.WriteCreated(repoName, n, ev, nil, nil); err != nil {
			return res, err
		}
		if ids := labelIDs(pr.Labels); len(ids) > 0 {
			appendEvent(n, store.Event{Type: store.EventLabels, User: user(pr.PosterName), Labels: ids, Time: pr.Created})
		}
		if len(pr.Assignees) > 0 {
			appendEvent(n, store.Event{Type: store.EventAssigned, User: user(pr.PosterName), Assignees: pr.Assignees, Time: pr.Created})
		}
		closedByComment, c, err := importComments(st, repoName, n, dir, &res)
		if err != nil {
			return res, err
		}
		res.Comments += c
		r, err := importReviews(st, repoName, n, dir, headSHA, &res)
		if err != nil {
			return res, err
		}
		res.Reviews += r
		if pr.Merged {
			t := pr.Updated
			if pr.MergedTime != nil {
				t = *pr.MergedTime
			}
			// マージした人は dump に無いので、PR の作成者を入れる
			appendEvent(n, store.Event{Type: store.EventMerged, User: user(pr.PosterName), MergeSHA: pr.MergeCommitSHA, HeadSHA: headSHA, Time: t})
		} else if pr.State == "closed" && !closedByComment {
			t := pr.Updated
			if pr.Closed != nil {
				t = *pr.Closed
			}
			appendEvent(n, store.Event{Type: store.EventClosed, User: user(pr.PosterName), Time: t})
		}
		res.Pulls++
	}

	// リリース
	var rels []ymlRelease
	if err := readYAMLIfExists(filepath.Join(dir, "release.yml"), &rels); err != nil {
		return res, err
	}
	tagSHA := map[string]string{}
	if tags, err := g.Tags(); err == nil {
		for _, t := range tags {
			tagSHA[t.Name] = t.SHA
		}
	}
	for _, rel := range rels {
		if rel.Draft {
			res.Skipped = append(res.Skipped, fmt.Sprintf("リリース %q は下書きなので読み飛ばした", rel.Name))
			continue
		}
		if store.ValidateTagName(rel.TagName) != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("リリース %q: タグ名 %q はフォルダ名に使えないので読み飛ばした", rel.Name, rel.TagName))
			continue
		}
		sha := tagSHA[rel.TagName]
		if sha == "" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("リリース %q: タグ %s が git に無いので読み飛ばした", rel.Name, rel.TagName))
			continue
		}
		title := rel.Name
		if strings.TrimSpace(title) == "" {
			title = rel.TagName
		}
		if _, err := st.CreateReleaseAt(repoName, user(rel.PublisherName), rel.TagName, sha, title, rel.Body, rel.Prerelease, rel.Created); err != nil {
			return res, fmt.Errorf("リリース %s: %w", rel.TagName, err)
		}
		res.Releases++
		for _, a := range rel.Assets {
			if a.DownloadURL == nil || strings.Contains(*a.DownloadURL, "://") {
				res.Skipped = append(res.Skipped, fmt.Sprintf("リリース %s の添付 %q は dump の中に無い（URL のまま）ので読み飛ばした", rel.TagName, a.Name))
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(*a.DownloadURL)))
			if err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("リリース %s の添付 %q を読めない: %v", rel.TagName, a.Name, err))
				continue
			}
			if _, err := st.SaveReleaseAttachment(repoName, rel.TagName, a.Name, data); err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("リリース %s の添付 %q を保存できない: %v", rel.TagName, a.Name, err))
				continue
			}
			res.Assets++
		}
	}

	fmt.Fprintf(log, "ラベル %d、Issue %d、PR %d、コメント %d、レビュー %d、リリース %d（添付 %d）\n", res.Labels, res.Issues, res.Pulls, res.Comments, res.Reviews, res.Releases, res.Assets)
	for _, s := range res.Skipped {
		fmt.Fprintln(log, "読み飛ばし:", s)
	}
	return res, nil
}

// importComments は comments/<番号>.yml を読んでイベントにする。クローズのコメントがあれば真を返す。
func importComments(st *store.Store, repoName string, number int, dir string, res *Result) (closed bool, count int, err error) {
	var comments []ymlComment
	if err := readYAMLIfExists(filepath.Join(dir, "comments", fmt.Sprintf("%d.yml", number)), &comments); err != nil {
		return false, 0, err
	}
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].Created.Before(comments[j].Created) })
	for _, c := range comments {
		u := strings.TrimSpace(c.PosterName)
		if u == "" {
			u = "unknown"
		}
		ev := store.Event{User: u, Time: c.Created}
		switch c.CommentType {
		case "", "comment":
			ev.Type, ev.Body = store.EventComment, c.Content
		case "close":
			ev.Type = store.EventClosed
			closed = true
		case "reopen":
			ev.Type = store.EventReopened
			closed = false
		case "change_title":
			ev.Type = store.EventTitle
			ev.Title = fmt.Sprint(c.Meta["NewTitle"])
			if ev.Title == "" || ev.Title == "<nil>" {
				continue
			}
		case "merge_pull":
			continue // merged は PR 側で書く
		default:
			res.Skipped = append(res.Skipped, fmt.Sprintf("#%d のコメント種別 %q は対応していないので読み飛ばした", number, c.CommentType))
			continue
		}
		if err := st.AppendEvent(repoName, number, ev); err != nil {
			return closed, count, err
		}
		if ev.Type == store.EventComment {
			count++
		}
	}
	return closed, count, nil
}

// importReviews は reviews/<番号>.yml を読んで review_comment と review のイベントにする。
func importReviews(st *store.Store, repoName string, number int, dir, headSHA string, res *Result) (int, error) {
	var reviews []ymlReview
	if err := readYAMLIfExists(filepath.Join(dir, "reviews", fmt.Sprintf("%d.yml", number)), &reviews); err != nil {
		return 0, err
	}
	sort.SliceStable(reviews, func(i, j int) bool { return reviews[i].CreatedAt.Before(reviews[j].CreatedAt) })
	count := 0
	for _, r := range reviews {
		u := strings.TrimSpace(r.ReviewerName)
		if u == "" {
			u = "unknown"
		}
		state := ""
		switch r.State {
		case "APPROVED":
			state = store.ReviewApproved
		case "CHANGES_REQUESTED":
			state = store.ReviewChangesRequested
		case "COMMENTED", "PENDING":
			state = store.ReviewComment
		default:
			res.Skipped = append(res.Skipped, fmt.Sprintf("PR #%d のレビュー状態 %q は対応していないので読み飛ばした", number, r.State))
			continue
		}
		commit := r.CommitID
		if commit == "" {
			commit = headSHA
		}
		for _, c := range r.Comments {
			side, line := "new", c.Line
			if line < 0 {
				side, line = "old", -line
			}
			cc := c.CommitID
			if cc == "" {
				cc = commit
			}
			t := c.CreatedAt
			if t.IsZero() {
				t = r.CreatedAt
			}
			ev := store.Event{Type: store.EventReviewComment, User: u, Body: c.Content, Path: c.TreePath, Line: line, Side: side, DiffHunk: c.DiffHunk, CommitSHA: cc, Time: t}
			if err := st.AppendEvent(repoName, number, ev); err != nil {
				return count, err
			}
		}
		if state == store.ReviewComment && strings.TrimSpace(r.Content) == "" && len(r.Comments) > 0 {
			// 行コメントだけのレビューは、本文の無い review を置かない（画面の流儀に合わせる）
			count++
			continue
		}
		ev := store.Event{Type: store.EventReview, User: u, State: state, Body: r.Content, CommitSHA: commit, Time: r.CreatedAt}
		if err := st.AppendEvent(repoName, number, ev); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func readYAML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s を読めない: %w", filepath.Base(path), err)
	}
	return nil
}

func readYAMLIfExists(path string, v any) error {
	err := readYAML(path, v)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
