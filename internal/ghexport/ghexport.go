// Package ghexport はデータルートのリポジトリを GitHub に書き出す（設計メモ 3.16 の export-github）。
//
// ネットが要るので家で実行する前提。GitHub 側には空のリポジトリを先に作っておく。
// git は push --mirror、Issue と PR は GitHub の REST API で作る。作成者は API を叩いた本人になるため、
// 本文の先頭に元の投稿者と日時を入れる。番号を揃えるため、欠番は空の Issue を作ってクローズして埋める。
// マージ済みの PR は GitHub で PR として作れないため、Issue として作り「元は PR #N、マージ済み」と記す。
// open の PR は head のブランチが push 済みなら PR として作る。
package ghexport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// Options は export-github の入力。
type Options struct {
	Owner     string // GitHub 側の owner
	Repo      string // GitHub 側のリポジトリ名
	Token     string // Personal Access Token（repo 権限）
	APIBase   string // 既定は https://api.github.com。テストでは模したサーバ
	GitRemote string // 既定は https://github.com/<owner>/<repo>.git。テストではローカルのパス
	Wait      time.Duration
	Log       io.Writer
	Resume    bool // 途中で止まった続きから。GitHub 側の最大番号の次から書き出す（新23）
}

// Result は作った件数。
type Result struct {
	Issues, Pulls, Placeholders, Comments, Labels, Releases, Assets int
	Skipped                                                         []string
}

type client struct {
	base  string
	token string
	http  *http.Client
	wait  time.Duration
	log   io.Writer
}

// 制限に掛かったときの再試行の上限と、ヘッダに待ち時間が無いときの待ち。
const (
	rateLimitRetries = 8
	rateLimitDefault = 60 * time.Second
	rateLimitMaxWait = time.Hour
)

// retryWait は 403／429 の応答から待つ時間を決める。Retry-After、無ければ x-ratelimit-reset、
// それも無ければ本文が制限の話なら既定の 60 秒。制限と関係ない 403（権限など）なら 0。
func retryWait(resp *http.Response, body string) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec >= 0 {
			return min(time.Duration(sec)*time.Second+time.Second, rateLimitMaxWait)
		}
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if epoch, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			d := time.Until(time.Unix(epoch, 0)) + time.Second
			if d < time.Second {
				d = time.Second
			}
			return min(d, rateLimitMaxWait)
		}
	}
	lower := strings.ToLower(body)
	if resp.StatusCode == 429 || strings.Contains(lower, "rate limit") || strings.Contains(lower, "secondary") || strings.Contains(lower, "abuse") {
		return rateLimitDefault
	}
	return 0
}

// errStatus は GitHub が 2xx 以外を返したときのエラー。
type errStatus struct {
	Status int
	Body   string
}

func (e *errStatus) Error() string {
	return fmt.Sprintf("GitHub API が %d を返した: %s", e.Status, e.Body)
}

// do は API を 1 回呼ぶ。GitHub の制限（403／429）に掛かったら、ヘッダの指示どおり待って再試行する（新23）。
func (c *client) do(method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = data
	}
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, c.base+path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if (resp.StatusCode == 403 || resp.StatusCode == 429) && attempt < rateLimitRetries {
			if wait := retryWait(resp, string(data)); wait > 0 {
				fmt.Fprintf(c.log, "GitHub の制限に掛かった（%d）。%s 待って再試行する\n", resp.StatusCode, wait.Round(time.Second))
				time.Sleep(wait)
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &errStatus{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

// post は書き込みの API。GitHub の二次的な制限に掛からないよう、呼ぶたびに少し待つ。
func (c *client) post(path string, in, out any) error {
	if c.wait > 0 {
		time.Sleep(c.wait)
	}
	return c.do(http.MethodPost, path, in, out)
}

func (c *client) patch(path string, in, out any) error {
	if c.wait > 0 {
		time.Sleep(c.wait)
	}
	return c.do(http.MethodPatch, path, in, out)
}

// upload はリリースの添付を upload_url に送る。
func (c *client) upload(uploadURL, name string, data []byte) error {
	if c.wait > 0 {
		time.Sleep(c.wait)
	}
	// upload_url は "https://uploads.github.com/.../assets{?name,label}" の形
	if i := strings.Index(uploadURL, "{"); i >= 0 {
		uploadURL = uploadURL[:i]
	}
	req, err := http.NewRequest(http.MethodPost, uploadURL+"?name="+url.QueryEscape(name), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &errStatus{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return nil
}

func stamp(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04")
}

// header は本文の先頭に入れる「元の投稿者と日時」。
func header(user string, t time.Time) string {
	return fmt.Sprintf("> 元の投稿者: %s（%s）\n\n", user, stamp(t))
}

// Export はリポジトリを GitHub に書き出す。
func Export(st *store.Store, repoName string, opts Options) (Result, error) {
	var res Result
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.APIBase == "" {
		opts.APIBase = "https://api.github.com"
	}
	if opts.GitRemote == "" {
		opts.GitRemote = fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", url.PathEscape(opts.Token), opts.Owner, opts.Repo)
	}
	if opts.Owner == "" || opts.Repo == "" || opts.Token == "" {
		return res, errors.New("GitHub 側の owner、リポジトリ名、トークンが要る")
	}
	if _, err := st.GetRepo(repoName); err != nil {
		return res, err
	}
	c := &client{base: strings.TrimRight(opts.APIBase, "/"), token: opts.Token, http: &http.Client{Timeout: 60 * time.Second}, wait: opts.Wait, log: opts.Log}
	ghRepo := "/repos/" + opts.Owner + "/" + opts.Repo

	// 先に GitHub 側を確かめる：リポジトリがあり、Issue も PR も無いこと（番号を揃えるため）。
	// -resume なら、GitHub 側にある最大番号の次から続ける
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(http.MethodGet, ghRepo, nil, &info); err != nil {
		return res, fmt.Errorf("GitHub のリポジトリ %s/%s を読めない: %w", opts.Owner, opts.Repo, err)
	}
	var existing []struct {
		Number int `json:"number"`
	}
	if err := c.do(http.MethodGet, ghRepo+"/issues?state=all&per_page=1&sort=created&direction=desc", nil, &existing); err != nil {
		return res, err
	}
	start := 1
	if len(existing) > 0 {
		if !opts.Resume {
			return res, fmt.Errorf("GitHub のリポジトリ %s/%s には既に Issue か PR がある。番号が揃わないので空のリポジトリに書き出す（途中で止まった続きなら -resume）", opts.Owner, opts.Repo)
		}
		for _, e := range existing {
			if e.Number >= start {
				start = e.Number + 1
			}
		}
		fmt.Fprintf(opts.Log, "GitHub 側に #%d まであるので、#%d から続ける\n", start-1, start)
	}

	// git
	bare := filepath.Join(st.Root(), "repos", repoName+".git")
	g := gitcmd.Repo{Dir: bare}
	// refs/pull/* は GitHub が拒むので、ブランチとタグだけを push する（新19）
	if err := gitcmd.PushBranchesAndTags(bare, opts.GitRemote); err != nil {
		return res, fmt.Errorf("git の push に失敗: %w", trimToken(err, opts.Token))
	}
	fmt.Fprintln(opts.Log, "git: ブランチとタグを push")
	branches := map[string]bool{}
	if bs, err := g.Branches(); err == nil {
		for _, b := range bs {
			branches[b.Name] = true
		}
	}

	// ラベル
	labels, _ := st.ListLabels(repoName)
	labelName := map[int]string{}
	for _, l := range labels {
		labelName[l.ID] = l.Name
		err := c.post(ghRepo+"/labels", map[string]any{"name": l.Name, "color": strings.TrimPrefix(l.Color, "#"), "description": l.Description}, nil)
		var es *errStatus
		if errors.As(err, &es) && es.Status == 422 {
			continue // 既にある（GitHub の既定のラベルなど）
		}
		if err != nil {
			return res, fmt.Errorf("ラベル %s: %w", l.Name, err)
		}
		res.Labels++
	}
	namesOf := func(ids []int) []string {
		out := []string{}
		for _, id := range ids {
			if n, ok := labelName[id]; ok {
				out = append(out, n)
			}
		}
		return out
	}

	// Issue と PR を番号順に。欠番は埋める
	issues, err := st.ListIssues(repoName)
	if err != nil {
		return res, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	byNumber := map[int]store.Issue{}
	maxNumber := 0
	for _, is := range issues {
		byNumber[is.Number] = is
		if is.Number > maxNumber {
			maxNumber = is.Number
		}
	}
	var created struct {
		Number int `json:"number"`
	}
	for n := start; n <= maxNumber; n++ {
		is, ok := byNumber[n]
		if !ok {
			// 欠番：空の Issue を作って閉じる
			if err := c.post(ghRepo+"/issues", map[string]any{"title": fmt.Sprintf("（欠番 #%d）", n), "body": "元のリポジトリで使われていない番号。番号を揃えるための空の Issue。"}, &created); err != nil {
				return res, err
			}
			if created.Number != n {
				return res, fmt.Errorf("GitHub 側の番号が揃わない: #%d を作ったつもりが #%d になった", n, created.Number)
			}
			if err := c.patch(fmt.Sprintf("%s/issues/%d", ghRepo, n), map[string]any{"state": "closed", "state_reason": "not_planned"}, nil); err != nil {
				return res, err
			}
			res.Placeholders++
			continue
		}
		body := header(is.User, is.CreatedAt) + is.Body
		asPull := false
		if is.IsPull {
			if !is.Merged && branches[is.Head] && branches[is.Base] && is.Head != is.Base {
				asPull = true
			}
			note := fmt.Sprintf("> 元は Pull Request #%d（%s → %s）", is.Number, is.Head, is.Base)
			if is.Merged {
				note += fmt.Sprintf("。マージ済み（%s が %s、マージコミット %s）", is.MergedBy, stamp(is.MergedAt), short(is.MergeSHA))
				if is.RevertSHA != "" {
					note += fmt.Sprintf("、Revert 済み（%s）", short(is.RevertSHA))
				}
			} else if is.State == "closed" {
				note += "。マージせずにクローズ"
			}
			body = note + "\n" + body
		}
		if asPull {
			if err := c.post(ghRepo+"/pulls", map[string]any{"title": is.Title, "head": is.Head, "base": is.Base, "body": body}, &created); err != nil {
				return res, fmt.Errorf("PR #%d: %w", n, err)
			}
			res.Pulls++
		} else {
			title := is.Title
			if is.IsPull {
				title = fmt.Sprintf("[PR #%d] %s", is.Number, is.Title)
			}
			if err := c.post(ghRepo+"/issues", map[string]any{"title": title, "body": body, "labels": namesOf(is.Labels)}, &created); err != nil {
				return res, fmt.Errorf("Issue #%d: %w", n, err)
			}
			res.Issues++
		}
		if created.Number != n {
			return res, fmt.Errorf("GitHub 側の番号が揃わない: #%d を作ったつもりが #%d になった", n, created.Number)
		}
		if asPull && len(is.Labels) > 0 {
			c.patch(fmt.Sprintf("%s/issues/%d", ghRepo, n), map[string]any{"labels": namesOf(is.Labels)}, nil)
		}
		// コメント、レビュー、行コメント、クローズ・再オープン、タイトル変更を時系列に
		title := ""
		for _, ev := range is.Events {
			if ev.Deleted {
				continue
			}
			text := ""
			switch ev.Type {
			case store.EventCreated:
				title = ev.Title
				continue
			case store.EventComment:
				text = ev.Body
			case store.EventClosed:
				text = "クローズした"
				if ev.Body != "" {
					text = ev.Body + "\n\n（クローズした）"
				}
			case store.EventReopened:
				text = "再オープンした"
				if ev.Body != "" {
					text = ev.Body + "\n\n（再オープンした）"
				}
			case store.EventTitle:
				text = fmt.Sprintf("タイトルを「%s」から「%s」に変えた", title, ev.Title)
				title = ev.Title
			case store.EventReview:
				label := map[string]string{store.ReviewApproved: "承認", store.ReviewChangesRequested: "変更を要請", store.ReviewComment: "レビューコメント"}[ev.State]
				text = fmt.Sprintf("**%s**（%s）\n\n%s", label, short(ev.CommitSHA), ev.Body)
			case store.EventReviewComment:
				text = fmt.Sprintf("行コメント `%s` の %d 行目（%s、%s）\n\n```diff\n%s\n```\n\n%s", ev.Path, ev.Line, ev.Side, short(ev.CommitSHA), ev.DiffHunk, ev.Body)
			case store.EventMerged:
				text = fmt.Sprintf("マージした（マージコミット %s）", short(ev.MergeSHA))
			case store.EventReverted:
				text = fmt.Sprintf("マージを取り消した（%s）", short(ev.RevertSHA))
			default:
				continue
			}
			if err := c.post(fmt.Sprintf("%s/issues/%d/comments", ghRepo, n), map[string]any{"body": header(ev.User, ev.Time) + text}, nil); err != nil {
				return res, fmt.Errorf("#%d のコメント: %w", n, err)
			}
			res.Comments++
		}
		if is.State == "closed" {
			reason := "completed"
			if is.IsPull && !is.Merged {
				reason = "not_planned"
			}
			in := map[string]any{"state": "closed"}
			if !asPull {
				in["state_reason"] = reason
			}
			if err := c.patch(fmt.Sprintf("%s/issues/%d", ghRepo, n), in, nil); err != nil {
				return res, fmt.Errorf("#%d のクローズ: %w", n, err)
			}
		}
		fmt.Fprintf(opts.Log, "#%d %s\n", n, is.Title)
	}

	// リリース
	rels, _ := st.ListReleases(repoName)
	sort.Slice(rels, func(i, j int) bool { return rels[i].CreatedAt.Before(rels[j].CreatedAt) })
	for _, rel := range rels {
		var out struct {
			ID        int64  `json:"id"`
			UploadURL string `json:"upload_url"`
		}
		if opts.Resume {
			// 前回の実行で作ってあれば飛ばす
			if err := c.do(http.MethodGet, ghRepo+"/releases/tags/"+url.PathEscape(rel.Tag), nil, nil); err == nil {
				fmt.Fprintf(opts.Log, "release %s は既にある\n", rel.Tag)
				continue
			}
		}
		body := header(rel.User, rel.CreatedAt) + rel.Body
		if err := c.post(ghRepo+"/releases", map[string]any{"tag_name": rel.Tag, "name": rel.Title, "body": body, "prerelease": rel.Prerelease, "draft": false}, &out); err != nil {
			return res, fmt.Errorf("リリース %s: %w", rel.Tag, err)
		}
		res.Releases++
		assets, _ := st.ListReleaseAttachments(repoName, rel.Tag)
		for _, a := range assets {
			p, err := st.ReleaseAttachmentPath(repoName, rel.Tag, a.Name)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return res, err
			}
			if err := c.upload(out.UploadURL, store.OriginalAttachmentName(a.Name), data); err != nil {
				return res, fmt.Errorf("リリース %s の添付 %s: %w", rel.Tag, a.Name, err)
			}
			res.Assets++
		}
		fmt.Fprintf(opts.Log, "release %s\n", rel.Tag)
	}
	fmt.Fprintf(opts.Log, "Issue %d、PR %d、欠番 %d、コメント %d、ラベル %d、リリース %d（添付 %d）\n", res.Issues, res.Pulls, res.Placeholders, res.Comments, res.Labels, res.Releases, res.Assets)
	return res, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// trimToken はエラー文に入ったトークンを消す（git の URL に入れているため）。
func trimToken(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "***"))
}
