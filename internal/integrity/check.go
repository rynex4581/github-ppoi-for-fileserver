// Package integrity はデータルートの整合性チェック（設計メモ 3.17 の check）。
// バックアップからの復元のあとに、イベントの JSON が読めるか、本文が参照する添付があるか、
// イベントが持つコミットがベアリポジトリにあるか、リリースのイベントと配布物の置き場があるか、
// git fsck が通るかを一括で確かめて一覧にする。
package integrity

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github-ppoi-for-fileserver/internal/gitcmd"
	"github-ppoi-for-fileserver/internal/store"
)

// Problem は見つかった問題1件。
type Problem struct {
	Repo    string
	Number  int    // Issue 番号。リポジトリ全体の問題なら 0
	File    string // イベントのファイル名。無ければ空
	Message string
}

func (p Problem) String() string {
	var b strings.Builder
	b.WriteString(p.Repo)
	if p.Number > 0 {
		fmt.Fprintf(&b, " #%d", p.Number)
	}
	if p.File != "" {
		b.WriteString(" " + p.File)
	}
	b.WriteString(": " + p.Message)
	return b.String()
}

// 本文中の添付の参照。/attachments/<owner>/<リポジトリ名>/<番号>/<ファイル名>
var attachmentRef = regexp.MustCompile(`/attachments/([^/\s)]+)/([^/\s)]+)/(\d+)/([^\s)"']+)`)

// Check はデータルートを調べ、問題を出力して件数を返す。repo が空なら全リポジトリ。
func Check(root, repo string, out io.Writer) (int, error) {
	st, err := store.Open(root)
	if err != nil {
		return 0, err
	}
	repos, err := st.ListRepos()
	if err != nil {
		return 0, err
	}
	var problems []Problem
	checked := 0
	for _, r := range repos {
		if repo != "" && r.Name != repo {
			continue
		}
		checked++
		problems = append(problems, checkRepo(st, r.Name)...)
	}
	if repo != "" && checked == 0 {
		return 0, fmt.Errorf("リポジトリ %s が無い", repo)
	}
	for _, p := range problems {
		fmt.Fprintln(out, p.String())
	}
	fmt.Fprintf(out, "リポジトリ %d 件を調べ、問題 %d 件\n", checked, len(problems))
	return len(problems), nil
}

// checkRepo は1リポジトリ分を調べる。
func checkRepo(st *store.Store, repo string) []Problem {
	var problems []Problem
	add := func(number int, file, msg string) {
		problems = append(problems, Problem{Repo: repo, Number: number, File: file, Message: msg})
	}
	g := gitcmd.Repo{Dir: filepath.Join(st.Root(), "repos", repo+".git")}
	commitSeen := map[string]bool{}
	commitOK := func(sha string) bool {
		if ok, seen := commitSeen[sha]; seen {
			return ok
		}
		_, err := g.ResolveCommit(sha)
		commitSeen[sha] = err == nil
		return err == nil
	}

	entries, err := os.ReadDir(st.IssuesDir(repo))
	if err != nil && !os.IsNotExist(err) {
		add(0, "", "issues フォルダを読めない: "+err.Error())
	}
	for _, e := range entries {
		number, err := strconv.Atoi(e.Name())
		if !e.IsDir() || err != nil || number <= 0 {
			continue
		}
		evDir := st.EventsDir(repo, number)
		files, err := os.ReadDir(evDir)
		if os.IsNotExist(err) {
			continue // 予約しただけの空の番号。許容する
		}
		if err != nil {
			add(number, "", "events フォルダを読めない: "+err.Error())
			continue
		}
		names := make([]string, 0, len(files))
		for _, f := range files {
			if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
				names = append(names, f.Name())
			}
		}
		sort.Strings(names)
		hasCreated := false
		known := map[string]bool{}
		var evs []store.Event
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(evDir, name))
			if err != nil {
				add(number, name, "読めない: "+err.Error())
				continue
			}
			var ev store.Event
			if err := json.Unmarshal(data, &ev); err != nil {
				add(number, name, "JSON として読めない: "+err.Error())
				continue
			}
			if ev.Type == "" || ev.User == "" {
				add(number, name, "type か user が無い")
			}
			if ev.Type == store.EventCreated {
				hasCreated = true
			}
			ev.File = name
			known[name] = true
			evs = append(evs, ev)
		}
		if len(names) > 0 && !hasCreated {
			add(number, "", "created イベントが無い")
		}
		for _, ev := range evs {
			// 編集・削除の対象が存在するか
			if ev.Target != "" && !known[ev.Target] {
				add(number, ev.File, "対象のイベント "+ev.Target+" が無い")
			}
			// 本文が参照する添付が存在するか
			for _, m := range attachmentRef.FindAllStringSubmatch(ev.Body, -1) {
				n, _ := strconv.Atoi(m[3])
				if m[2] != repo || n != number {
					continue // 他の Issue やリポジトリへの参照はここでは見ない
				}
				if _, err := st.AttachmentPath(repo, number, m[4]); err != nil {
					add(number, ev.File, "添付 "+m[4]+" が無い")
				}
			}
			// イベントが持つコミットがベアにあるか
			for label, sha := range map[string]string{"head_sha": ev.HeadSHA, "commit_sha": ev.CommitSHA, "merge_sha": ev.MergeSHA, "revert_sha": ev.RevertSHA} {
				if sha == "" {
					continue
				}
				if !commitOK(sha) {
					msg := label + " " + sha + " がベアリポジトリに無い"
					if label == "head_sha" || label == "commit_sha" {
						msg += "（force push で消えた可能性もある）"
					}
					add(number, ev.File, msg)
				}
			}
		}
	}

	// リリース（3.18）。タグごとに events の JSON、created の commit_sha、attachments\ の有無を見る（新21）
	rels, err := os.ReadDir(st.ReleasesDir(repo))
	if err != nil && !os.IsNotExist(err) {
		add(0, "", "releases フォルダを読めない: "+err.Error())
	}
	for _, e := range rels {
		if !e.IsDir() {
			continue
		}
		tag := e.Name()
		label := "リリース " + tag
		evDir := st.ReleaseEventsDir(repo, tag)
		files, err := os.ReadDir(evDir)
		if err != nil {
			add(0, label, "events フォルダを読めない: "+err.Error())
			continue
		}
		names := make([]string, 0, len(files))
		for _, f := range files {
			if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
				names = append(names, f.Name())
			}
		}
		sort.Strings(names)
		hasCreated, alive := false, false
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(evDir, name))
			if err != nil {
				add(0, label+" "+name, "読めない: "+err.Error())
				continue
			}
			var ev store.Event
			if err := json.Unmarshal(data, &ev); err != nil {
				add(0, label+" "+name, "JSON として読めない: "+err.Error())
				continue
			}
			if ev.Type == "" || ev.User == "" {
				add(0, label+" "+name, "type か user が無い")
			}
			switch ev.Type {
			case store.EventCreated:
				hasCreated, alive = true, true
				if ev.CommitSHA != "" && !commitOK(ev.CommitSHA) {
					add(0, label+" "+name, "commit_sha "+ev.CommitSHA+" がベアリポジトリに無い")
				}
			case store.EventDeleted:
				alive = false
			}
		}
		if len(names) > 0 && !hasCreated {
			add(0, label, "created イベントが無い")
		}
		if alive {
			if _, err := os.Stat(st.ReleaseAttachmentsDir(repo, tag)); err != nil {
				add(0, label, "attachments フォルダが無い")
			}
		}
	}

	if _, err := os.Stat(g.Dir); err != nil {
		add(0, "", "ベアリポジトリが無い: "+g.Dir)
		return problems
	}
	if out, err := g.Fsck(); err != nil {
		add(0, "", "git fsck: "+strings.TrimSpace(out+" "+err.Error()))
	}
	return problems
}
