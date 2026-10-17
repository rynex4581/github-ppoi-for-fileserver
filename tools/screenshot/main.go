// screenshot は README に載せる画面の画像を撮る。
//
// 一時フォルダに架空のデータルート（リポジトリ2つ、Issue 数件、Pull Request 1件）を作り、
// 本体と同じサーバを localhost に上げ、端末の Chromium 系ブラウザ（Chrome、Iron、Edge）で
// 主要な画面を撮って docs/images に PNG で置く。リポジトリ直下で次のように実行する。
//
//	go run ./tools/screenshot
//
// 撮影中は LOCALAPPDATA を一時フォルダに向けるので、端末側の設定やキャッシュには触らない。
// 終わると一時フォルダごと消す。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
	"github-ppoi-for-fileserver/internal/web"
)

// 撮影に使う架空の名前。実在の人や職場のものは使わない。
const (
	owner     = "demo"
	ownerName = "デモチーム"
	login     = "yamada" // 撮影時にログオンしていることにする利用者
	repoTool  = "kintai-tool"
	repoOps   = "server-scripts"
)

// shot は撮る画面1つ。
type shot struct {
	File   string
	Path   string
	Height int64
}

func main() {
	out := flag.String("out", "docs/images", "PNG を置くフォルダ")
	browser := flag.String("browser", "", "使うブラウザの exe（空なら Chrome、Iron、Edge の順に探す）")
	flag.Parse()

	if err := run(*out, *browser); err != nil {
		log.Fatal(err)
	}
}

func run(out, browser string) error {
	if browser == "" {
		var err error
		if browser, err = findBrowser(); err != nil {
			return err
		}
	}
	log.Printf("ブラウザ: %s", browser)

	tmp, err := os.MkdirTemp("", "ppoi-screenshot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// 端末側の設定、キャッシュ、専用 clone を一時フォルダに向ける
	local := filepath.Join(tmp, "localappdata")
	if err := os.MkdirAll(local, 0o755); err != nil {
		return err
	}
	os.Setenv("LOCALAPPDATA", local)

	root := filepath.Join(tmp, "data-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	numbers, err := buildData(root, filepath.Join(tmp, "work"))
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := localconfig.Config{DataRoots: []string{root}, Lang: localconfig.DefaultLang}
	srv, err := web.New(cfg, login, port)
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: srv.Handler()}
	go hs.Serve(ln)
	defer hs.Shutdown(context.Background())

	shots := []shot{
		{File: "top.png", Path: "/", Height: 720},
		{File: "issue.png", Path: fmt.Sprintf("/%s/%s/issues/%d", owner, repoTool, numbers.issue), Height: 960},
		{File: "pull-files.png", Path: fmt.Sprintf("/%s/%s/pulls/%d/files", owner, repoTool, numbers.pull), Height: 900},
		{File: "cross-issues.png", Path: "/issues", Height: 720},
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	return capture(browser, fmt.Sprintf("http://localhost:%d", port), out, shots)
}

// findBrowser は端末にある Chromium 系のブラウザを探す。
func findBrowser() (string, error) {
	pf := os.Getenv("ProgramFiles")
	pf86 := os.Getenv("ProgramFiles(x86)")
	local := os.Getenv("LOCALAPPDATA")
	candidates := []string{
		filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(pf, `SRWare Iron (64-Bit)\chrome.exe`),
		filepath.Join(pf86, `SRWare Iron\chrome.exe`),
		filepath.Join(local, `Chromium\Application\chrome.exe`),
		filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("Chromium 系のブラウザが見つからない。-browser で exe を指定する")
}

// issueNumbers は撮影に使う Issue と Pull Request の番号。
type issueNumbers struct {
	issue int
	pull  int
}

// buildData は架空のデータルートを組み立てる。
func buildData(root, work string) (issueNumbers, error) {
	var n issueNumbers
	st, err := store.Create(root, store.Config{Owner: owner, OwnerName: ownerName})
	if err != nil {
		return n, err
	}
	for login, name := range map[string]string{"yamada": "山田", "suzuki": "鈴木", "tanaka": "田中"} {
		if err := st.SetUserName(login, name); err != nil {
			return n, err
		}
	}
	for _, r := range []struct{ name, desc string }{
		{repoTool, "勤怠データの集計ツール"},
		{repoOps, "サーバ運用のスクリプト集"},
	} {
		if _, err := st.CreateRepo(r.name, r.desc); err != nil {
			return n, err
		}
	}
	labels := map[string]map[string]int{}
	for _, repo := range []string{repoTool, repoOps} {
		labels[repo] = map[string]int{}
		for _, l := range []store.Label{
			{Name: "bug", Color: "d73a4a", Description: "動作がおかしい"},
			{Name: "enhancement", Color: "a2eeef", Description: "機能の追加や改善"},
			{Name: "documentation", Color: "0075ca", Description: "ドキュメントの修正"},
		} {
			created, err := st.CreateLabel(repo, l)
			if err != nil {
				return n, err
			}
			labels[repo][l.Name] = created.ID
		}
	}

	bare := filepath.Join(root, "repos", repoTool+".git")
	if err := buildCommits(filepath.Join(work, repoTool), bare); err != nil {
		return n, err
	}
	if err := buildOpsCommits(filepath.Join(work, repoOps), filepath.Join(root, "repos", repoOps+".git")); err != nil {
		return n, err
	}

	now := time.Now()
	at := func(days, hours int) time.Time { return now.Add(-time.Duration(days*24+hours) * time.Hour) }
	lt := labels[repoTool]

	// Issue 1：バグ報告。コメントのやり取りがある
	is, err := createIssue(st, repoTool, store.Event{User: "suzuki", Time: at(6, 3),
		Title: "月末の集計で合計が勤怠システムの値と合わない",
		Body: "10月分を集計したところ、一部のメンバーの合計が勤怠システムの月次画面の値と 2 時間ほどずれる。\n\n" +
			"再現手順\n\n1. `python collect.py 202510.csv` を実行する\n2. 勤怠システムの月次画面の合計と比べる\n\n" +
			"30分単位で切り捨てているところが原因ではないかと思う。"},
		[]int{lt["bug"]}, []string{"yamada"})
	if err != nil {
		return n, err
	}
	n.issue = is.Number
	if err := st.AppendEvent(repoTool, is.Number, store.Event{Type: store.EventComment, User: "yamada", Time: at(6, 1),
		Body: "`round_minutes` で 30 分単位に切り捨てている。勤怠システム側は分単位で持っているので、ここで差が出ている。"}); err != nil {
		return n, err
	}
	if err := st.AppendEvent(repoTool, is.Number, store.Event{Type: store.EventComment, User: "tanaka", Time: at(5, 20),
		Body: "先月分も同じずれが出ていた。直すなら過去分の再集計も要る。"}); err != nil {
		return n, err
	}
	if err := st.AppendEvent(repoTool, is.Number, store.Event{Type: store.EventComment, User: "yamada", Time: at(5, 2),
		Body: "分単位に直す Pull Request を出す。再集計は直ってから別の Issue で扱う。"}); err != nil {
		return n, err
	}

	// Issue 2〜4
	if _, err := createIssue(st, repoTool, store.Event{User: "tanaka", Time: at(5, 0),
		Title: "CSV の文字コードを UTF-8 にも対応する",
		Body:  "勤怠システムの新しい版は UTF-8 で出力する。今は cp932 固定なので、そのままでは読めない。"},
		[]int{lt["enhancement"]}, nil); err != nil {
		return n, err
	}
	closed, err := createIssue(st, repoTool, store.Event{User: "yamada", Time: at(4, 6),
		Title: "集計結果に部署名を出す",
		Body:  "部署ごとに集計表を分けるため、出力に部署名の列を足す。"},
		[]int{lt["enhancement"]}, []string{"tanaka"})
	if err != nil {
		return n, err
	}
	if err := st.AppendEvent(repoTool, closed.Number, store.Event{Type: store.EventClosed, User: "tanaka", Time: at(3, 1),
		Body: "部署名の列を足した。"}); err != nil {
		return n, err
	}
	if _, err := createIssue(st, repoTool, store.Event{User: "suzuki", Time: at(2, 7),
		Title: "README の実行手順が古い",
		Body:  "引数の説明が前の版のままになっている。"},
		[]int{lt["documentation"]}, nil); err != nil {
		return n, err
	}

	// Pull Request：Issue 1 を直すもの。行コメントとレビューが付いている
	g := gitcmd.Repo{Dir: bare}
	headSHA, err := g.ResolveCommit("refs/heads/fix-rounding")
	if err != nil {
		return n, err
	}
	pr, err := createIssue(st, repoTool, store.Event{User: "yamada", Time: at(1, 8),
		Title:  "端数処理を分単位に直す",
		Body:   fmt.Sprintf("Closes #%d\n\n30分単位の切り捨てをやめ、分単位で四捨五入する。", is.Number),
		IsPull: true, Head: "fix-rounding", Base: "main", HeadSHA: headSHA},
		[]int{lt["bug"]}, []string{"yamada"})
	if err != nil {
		return n, err
	}
	n.pull = pr.Number
	path, line, hunk, err := changedLine(g, "main", "fix-rounding", "collect.py", "round(")
	if err != nil {
		return n, err
	}
	if err := st.AppendEvent(repoTool, pr.Number, store.Event{Type: store.EventReviewComment, User: "suzuki", Time: at(1, 2),
		Body: "四捨五入でよいか。勤怠システム側は切り捨てだった気がする。", CommitSHA: headSHA,
		Path: path, Line: line, Side: "new", DiffHunk: hunk}); err != nil {
		return n, err
	}
	if err := st.AppendEvent(repoTool, pr.Number, store.Event{Type: store.EventComment, User: "yamada", Time: at(0, 20),
		Body: "勤怠システムの仕様書を確認した。あちらも分単位の四捨五入だったので、このままにする。"}); err != nil {
		return n, err
	}
	if err := st.AppendEvent(repoTool, pr.Number, store.Event{Type: store.EventReview, User: "suzuki", Time: at(0, 3),
		State: store.ReviewApproved, Body: "確認した。問題ない。", CommitSHA: headSHA}); err != nil {
		return n, err
	}

	// もう1つのリポジトリの Issue
	lo := labels[repoOps]
	if _, err := createIssue(st, repoOps, store.Event{User: "tanaka", Time: at(3, 5),
		Title: "バックアップのログをローテーションする",
		Body:  "ログが1ファイルに追記され続けていて、開くのに時間がかかる。日ごとに分ける。"},
		[]int{lo["enhancement"]}, []string{"suzuki"}); err != nil {
		return n, err
	}
	if _, err := createIssue(st, repoOps, store.Event{User: "yamada", Time: at(0, 9),
		Title: "ディスク使用量の通知しきい値を見直す",
		Body:  "80% で通知が出るが、ログの肥大化で毎日出るようになった。しきい値か対象を見直す。"},
		nil, []string{"yamada"}); err != nil {
		return n, err
	}
	st.FlushCache()
	return n, nil
}

// createIssue は時刻を指定して Issue（または Pull Request）を作る。
// ラベルと担当者のイベントにも作成直後の時刻を入れ、更新日時が撮影時刻にならないようにする。
func createIssue(st *store.Store, repo string, ev store.Event, labels []int, assignees []string) (store.Issue, error) {
	number, err := st.AllocateIssue(repo)
	if err != nil {
		return store.Issue{}, err
	}
	if _, err := st.WriteCreated(repo, number, ev, nil, nil); err != nil {
		return store.Issue{}, err
	}
	t := ev.Time.Add(time.Minute)
	if len(labels) > 0 {
		if err := st.AppendEvent(repo, number, store.Event{Type: store.EventLabels, User: ev.User, Time: t, Labels: labels}); err != nil {
			return store.Issue{}, err
		}
	}
	if len(assignees) > 0 {
		if err := st.AppendEvent(repo, number, store.Event{Type: store.EventAssigned, User: ev.User, Time: t, Assignees: assignees}); err != nil {
			return store.Issue{}, err
		}
	}
	return st.GetIssue(repo, number)
}

// changedLine は base...head の差分から、path の追加行のうち needle を含む最初の行を探し、
// そのパス、新しい側の行番号、前後の差分の断片を返す。
func changedLine(g gitcmd.Repo, base, head, path, needle string) (string, int, string, error) {
	raw, err := g.RangeDiff(base, head)
	if err != nil {
		return "", 0, "", err
	}
	for _, f := range gitcmd.ParseDiff(raw) {
		if f.NewPath != path {
			continue
		}
		for _, h := range f.Hunks {
			for i, l := range h.Lines {
				if l.Kind != "add" || !strings.Contains(l.Text, needle) {
					continue
				}
				lo := i - 3
				if lo < 0 {
					lo = 0
				}
				var b strings.Builder
				b.WriteString(h.Header + "\n")
				for _, x := range h.Lines[lo : i+1] {
					switch x.Kind {
					case "add":
						b.WriteString("+")
					case "del":
						b.WriteString("-")
					default:
						b.WriteString(" ")
					}
					b.WriteString(x.Text + "\n")
				}
				return path, l.NewNum, b.String(), nil
			}
		}
	}
	return "", 0, "", fmt.Errorf("差分に %s の %q を含む追加行が無い", path, needle)
}

const collectV1 = `import csv
import sys


def round_minutes(minutes):
    # 30分単位で切り捨てる
    return (minutes // 30) * 30


def main(path):
    total = 0
    with open(path, encoding="cp932") as f:
        for row in csv.DictReader(f):
            total += round_minutes(int(row["minutes"]))
    print(total)


if __name__ == "__main__":
    main(sys.argv[1])
`

const collectV2 = `import csv
import sys


def round_minutes(minutes):
    # 分単位で四捨五入する。勤怠システムの月次集計と同じ扱い
    return int(round(minutes))


def main(path):
    total = 0
    with open(path, encoding="cp932") as f:
        for row in csv.DictReader(f):
            total += round_minutes(int(row["minutes"]))
    print(total)


if __name__ == "__main__":
    main(sys.argv[1])
`

const readmeV1 = "# kintai-tool\n\n勤怠システムから出力した CSV を読み、月ごとの合計を出す。\n"

const readmeV2 = "# kintai-tool\n\n勤怠システムから出力した CSV を読み、月ごとの合計を出す。\n\n" +
	"## 実行\n\n```\npython collect.py <CSVファイル>\n```\n"

// buildCommits は集計ツールのリポジトリに main と fix-rounding のコミットを作って bare に push する。
func buildCommits(work, bare string) error {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	if err := git(work, nil, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	if err := writeFiles(work, map[string]string{"README.md": readmeV1, "collect.py": collectV1}); err != nil {
		return err
	}
	if err := commit(work, "yamada", "山田", -9, "集計ツールの初版"); err != nil {
		return err
	}
	if err := writeFiles(work, map[string]string{"README.md": readmeV2}); err != nil {
		return err
	}
	if err := commit(work, "suzuki", "鈴木", -7, "README に実行手順を書く"); err != nil {
		return err
	}
	if err := git(work, nil, "remote", "add", "origin", bare); err != nil {
		return err
	}
	if err := git(work, nil, "push", "-q", "origin", "main"); err != nil {
		return err
	}
	if err := git(work, nil, "checkout", "-q", "-b", "fix-rounding"); err != nil {
		return err
	}
	if err := writeFiles(work, map[string]string{"collect.py": collectV2}); err != nil {
		return err
	}
	if err := commit(work, "yamada", "山田", -1, "端数処理を30分単位の切り捨てから分単位の四捨五入に直す"); err != nil {
		return err
	}
	return git(work, nil, "push", "-q", "origin", "fix-rounding")
}

// buildOpsCommits はスクリプト集のリポジトリにコミットを1つ作って bare に push する。
func buildOpsCommits(work, bare string) error {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	if err := git(work, nil, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	files := map[string]string{
		"README.md": "# server-scripts\n\nサーバ運用で使う PowerShell のスクリプト集。\n",
		"backup.ps1": "# 共有フォルダを日次でバックアップする\nparam([string]$Source, [string]$Dest)\n" +
			"robocopy $Source $Dest /MIR /R:2 /W:5 /LOG+:backup.log\n",
	}
	if err := writeFiles(work, files); err != nil {
		return err
	}
	if err := commit(work, "tanaka", "田中", -12, "バックアップのスクリプトを置く"); err != nil {
		return err
	}
	if err := git(work, nil, "remote", "add", "origin", bare); err != nil {
		return err
	}
	return git(work, nil, "push", "-q", "origin", "main")
}

func writeFiles(dir string, files map[string]string) error {
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// commit は作者と日付を指定してコミットする。メッセージは文字コードの問題を避けてファイルで渡す。
func commit(dir, login, name string, days int, message string) error {
	msg, err := os.CreateTemp("", "ppoi-msg-")
	if err != nil {
		return err
	}
	msg.WriteString(message + "\n")
	msg.Close()
	defer os.Remove(msg.Name())
	date := time.Now().AddDate(0, 0, days).Format(time.RFC3339)
	env := []string{
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + login + "@example.invalid", "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + login + "@example.invalid", "GIT_COMMITTER_DATE=" + date,
	}
	if err := git(dir, env, "add", "-A"); err != nil {
		return err
	}
	return git(dir, env, "commit", "-q", "-F", msg.Name())
}

func git(dir string, env []string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// capture はブラウザで各画面を開いて PNG に保存する。
func capture(browser, base, out string, shots []shot) error {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.WindowSize(1280, 800),
	)
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	ctx, cancel := chromedp.NewContext(actx)
	defer cancel()

	// 最初の呼び出しでブラウザが起動する。ここにはタイムアウトを付けない（付けるとブラウザごと止まる）
	if err := chromedp.Do(ctx, chromedp.Navigate("about:blank")); err != nil {
		return fmt.Errorf("ブラウザを起動できない: %w", err)
	}

	for _, s := range shots {
		sctx, cancelS := context.WithTimeout(ctx, 30*time.Second)
		buf, err := captureOne(sctx, base+s.Path, s.Height)
		cancelS()
		if err != nil {
			return fmt.Errorf("%s の撮影に失敗: %w", s.Path, err)
		}
		dst := filepath.Join(out, s.File)
		if err := os.WriteFile(dst, buf, 0o644); err != nil {
			return err
		}
		log.Printf("%s ← %s", dst, s.Path)
	}
	return nil
}

// captureOne は1画面を開いて、表示が落ち着いてから撮る。
func captureOne(ctx context.Context, url string, height int64) ([]byte, error) {
	err := chromedp.Do(ctx,
		chromedp.EmulateViewport(1280, height, chromedp.EmulateScale(2)),
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(300*time.Millisecond),
	)
	if err != nil {
		return nil, err
	}
	return chromedp.Run(ctx, chromedp.CaptureScreenshot())
}
