// smbtest は SMB 上での同時更新の検証用。本体と同じ採番と書き込みのコード（internal/store）を使う。
//
// 2台の端末から同じデータルートに向けて同時に回し、終わったら -check で突き合わせる。
//
//	smbtest.exe -root \\server\share\smbtest -n 1000
//	smbtest.exe -root \\server\share\smbtest -check
//
// 確かめること（設計メモ 3.14）
//  1. 2台が同時に同じ番号を取ろうとしても片方だけが成功するか（番号の重複が無いか）
//  2. 一時名で書いて rename したファイルが、もう1台から書きかけで見えないか。
//     events フォルダを直接走査し、.json ごとに読み込みと JSON の解釈を試す。失敗した名前は次の周で
//     もう一度試し、「失敗→成功」の件数を数える。これが書きかけを読んだ回数になる
//  3. 片方が作ったファイルがもう片方から何秒で見えるか
//
// あわせて、os.Rename が既存のファイルを置き換えるか、置き換え無しの rename が共有上で効くか、
// 自分の書いたファイルの更新時刻（サーバの時計）と自分の時計のずれも記録する。
//
// 合格基準：2台で各1000回の採番で番号の重複がゼロ、書きかけの JSON を読んだ回数がゼロ、
// 見えるまでの遅れが30秒以内。-check がまとめて判定する。
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github-ppoi-for-fileserver/internal/store"
)

const testRepo = "smbtest"

func main() {
	root := flag.String("root", "", "データルート（共有フォルダ）のパス。無ければ初期化する")
	n := flag.Int("n", 1000, "作成する Issue の数")
	check := flag.Bool("check", false, "全端末のログと共有フォルダの中身を突き合わせる")
	name := flag.String("name", "", "端末名（既定はコンピュータ名）")
	flag.Parse()
	if *root == "" {
		fmt.Fprintln(os.Stderr, "-root を指定する")
		os.Exit(2)
	}
	host := *name
	if host == "" {
		host, _ = os.Hostname()
	}
	st, err := openOrCreate(*root)
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		if err := runCheck(st, *root); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runLoop(st, *root, host, *n); err != nil {
		log.Fatal(err)
	}
}

// openOrCreate はデータルートを開き、無ければ初期化する。
// 2台が同時に始まると、片方が config.json を書いている最中にもう片方が読んで失敗することが
// あるので、少し待って数回やり直す。
func openOrCreate(root string) (st *store.Store, err error) {
	for try := 0; try < 10; try++ {
		if try > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		if _, statErr := os.Stat(filepath.Join(root, "config.json")); statErr == nil {
			if st, err = store.Open(root); err == nil {
				return st, nil
			}
			continue
		}
		if err = os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		st, err = store.Create(root, store.Config{Owner: "smbtest", OwnerName: "SMB 検証"})
		if err == nil {
			return st, nil
		}
		if !errors.Is(err, store.ErrConfigExists) && !errors.Is(err, store.ErrConfigRace) {
			continue
		}
	}
	return nil, fmt.Errorf("データルートを開けない: %w", err)
}

// logRecord はこの端末が取った番号の記録。1行1件で <root>\smbtest-logs\<端末名>.log に書く。
type logRecord struct {
	Number int       `json:"number"`
	Host   string    `json:"host"`
	Time   time.Time `json:"time"`
}

// summary は1台分の結果。<root>\smbtest-logs\<端末名>.summary.json に書き、-check が読む。
type summary struct {
	Host           string  `json:"host"`
	Requested      int     `json:"requested"`
	Created        int     `json:"created"`
	AllocErrors    int     `json:"alloc_errors"`
	ElapsedSec     float64 `json:"elapsed_sec"`
	BrokenRecover  int     `json:"broken_then_ok"`   // JSON として読めず、後の周で読めた回数（書きかけを読んだ回数）
	BrokenNever    int     `json:"broken_never_ok"`  // 終了まで読めなかったファイル数
	TmpSeen        int     `json:"tmp_seen"`         // 他の端末の events 内で .tmp が見えた延べ回数
	DelayCount     int     `json:"delay_count"`      // 他の端末の Issue を見つけた件数
	DelayAvgSec    float64 `json:"delay_avg_sec"`    // 見えるまでの遅れ（相手の時計との差を含む）
	DelayMaxSec    float64 `json:"delay_max_sec"`    //
	SkewAvgSec     float64 `json:"skew_avg_sec"`     // 自分のファイルの更新時刻（サーバの時計）− 自分の時計
	RenameReplaces bool    `json:"rename_replaces"`  // os.Rename が既存を置き換えたか
	NoReplaceWorks bool    `json:"no_replace_works"` // 置き換え無しの rename が既存があるときに失敗したか
}

func logDir(root string) string            { return filepath.Join(root, "smbtest-logs") }
func logPath(root, host string) string     { return filepath.Join(logDir(root), host+".log") }
func summaryPath(root, host string) string { return filepath.Join(logDir(root), host+".summary.json") }

// watch は他の端末の Issue 1件の読み取り状況。
type watch struct {
	created bool            // created イベントが読めた
	ok      map[string]bool // 読めたファイル名
	failed  map[string]int  // 読めなかった回数
}

func runLoop(st *store.Store, root, host string, n int) error {
	if _, err := st.GetRepo(testRepo); err != nil {
		if _, err := st.CreateRepo(testRepo, "SMB 検証用"); err != nil && !errors.Is(err, store.ErrRepoExists) {
			return fmt.Errorf("検証用リポジトリを作れない（git が要る）: %w", err)
		}
	}
	if err := os.MkdirAll(logDir(root), 0o755); err != nil {
		return err
	}
	sum := summary{Host: host, Requested: n}
	sum.RenameReplaces, sum.NoReplaceWorks = renameBehavior(logDir(root), host)
	fmt.Printf("os.Rename は既存を置き換える: %v、置き換え無しの rename は既存があると失敗する: %v\n", sum.RenameReplaces, sum.NoReplaceWorks)

	lf, err := os.Create(logPath(root, host))
	if err != nil {
		return err
	}
	defer lf.Close()
	w := bufio.NewWriter(lf)
	defer w.Flush()

	own := map[int]bool{}
	others := map[int]*watch{}
	var delays []time.Duration
	var skews []time.Duration
	start := time.Now()
	fmt.Printf("%s: %d 件を作成する（%s）\n", host, n, root)

	for i := 1; i <= n; i++ {
		number, err := st.AllocateIssue(testRepo)
		if err != nil {
			sum.AllocErrors++
			fmt.Printf("採番に失敗: %v\n", err)
			continue
		}
		ev := store.Event{User: host, Title: fmt.Sprintf("%s %d", host, i), Body: "smbtest"}
		is, err := st.WriteCreated(testRepo, number, ev, nil, nil)
		if err != nil {
			return fmt.Errorf("書き込みに失敗 #%d: %w", number, err)
		}
		sum.Created++
		own[number] = true
		rec := logRecord{Number: number, Host: host, Time: time.Now().UTC()}
		data, _ := json.Marshal(rec)
		w.Write(data)
		w.WriteByte('\n')
		if len(is.Events) > 0 {
			if info, err := os.Stat(filepath.Join(st.EventsDir(testRepo, number), is.Events[0].File)); err == nil {
				skews = append(skews, info.ModTime().Sub(is.Events[0].Time))
			}
		}

		// 他の端末の Issue を events フォルダから直接読み、見えるまでの遅れと読めないファイルを記録する
		scanOthers(st, own, others, &sum, &delays)
		if i%100 == 0 {
			fmt.Printf("%s: %d 件（%.0f 秒）\n", host, i, time.Since(start).Seconds())
		}
	}
	w.Flush()
	// 最後にもう一周して、読めないまま残ったものを数える
	scanOthers(st, own, others, &sum, &delays)
	for _, wt := range others {
		for name, c := range wt.failed {
			if c > 0 && !wt.ok[name] {
				sum.BrokenNever++
			}
		}
	}

	sum.ElapsedSec = time.Since(start).Seconds()
	if len(delays) > 0 {
		sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
		var total time.Duration
		for _, d := range delays {
			total += d
		}
		sum.DelayCount = len(delays)
		sum.DelayAvgSec = (total / time.Duration(len(delays))).Seconds()
		sum.DelayMaxSec = delays[len(delays)-1].Seconds()
	}
	if len(skews) > 0 {
		var total time.Duration
		for _, d := range skews {
			total += d
		}
		sum.SkewAvgSec = (total / time.Duration(len(skews))).Seconds()
	}
	data, _ := json.MarshalIndent(sum, "", "  ")
	if err := os.WriteFile(summaryPath(root, host), data, 0o644); err != nil {
		return err
	}

	fmt.Printf("\n== %s の結果 ==\n", host)
	fmt.Printf("作成: %d 件、採番の失敗: %d 回、所要: %.1f 秒（%.1f 件/秒）\n", sum.Created, sum.AllocErrors, sum.ElapsedSec, float64(sum.Created)/sum.ElapsedSec)
	fmt.Printf("書きかけの JSON を読んだ回数（読めず、後で読めた）: %d 回\n", sum.BrokenRecover)
	fmt.Printf("終了まで読めなかったファイル: %d 件\n", sum.BrokenNever)
	fmt.Printf("他の端末の events 内で .tmp が見えた回数: %d 回\n", sum.TmpSeen)
	if sum.DelayCount > 0 {
		fmt.Printf("他の端末の Issue が見えるまで: 平均 %.1f 秒、最大 %.1f 秒（%d 件。相手の時計との差を含む）\n", sum.DelayAvgSec, sum.DelayMaxSec, sum.DelayCount)
	} else {
		fmt.Println("他の端末の Issue は見えなかった（1台だけで回した場合はこれで正常）")
	}
	fmt.Printf("サーバの時計と自分の時計の差: %.1f 秒\n", sum.SkewAvgSec)
	fmt.Printf("ログ: %s\n両方の端末が終わってから -check を1台で実行する\n", logPath(root, host))
	return nil
}

// scanOthers は issues フォルダを走査し、自分以外の Issue の events を1ファイルずつ読んで、
// 読めたか、読めなかったか、見えるまでの遅れを記録する。
// created まで読めて読めなかったファイルが無い Issue はそれ以上見ない。
func scanOthers(st *store.Store, own map[int]bool, others map[int]*watch, sum *summary, delays *[]time.Duration) {
	entries, err := os.ReadDir(st.IssuesDir(testRepo))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		number, err := strconv.Atoi(e.Name())
		if err != nil || own[number] {
			continue
		}
		wt := others[number]
		if wt == nil {
			wt = &watch{ok: map[string]bool{}, failed: map[string]int{}}
			others[number] = wt
		}
		if wt.created && len(wt.failed) == len(wt.ok) && allOK(wt) {
			continue
		}
		dir := st.EventsDir(testRepo, number)
		files, err := os.ReadDir(dir)
		if err != nil {
			// フォルダを作った直後で events がまだ無い。次の周で見る
			continue
		}
		for _, f := range files {
			name := f.Name()
			if strings.HasSuffix(name, ".tmp") {
				sum.TmpSeen++
				continue
			}
			if f.IsDir() || filepath.Ext(name) != ".json" || wt.ok[name] {
				continue
			}
			var ev store.Event
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil {
				err = json.Unmarshal(data, &ev)
			}
			if err != nil {
				wt.failed[name]++
				continue
			}
			if wt.failed[name] > 0 {
				sum.BrokenRecover++
			}
			wt.ok[name] = true
			if ev.Type == store.EventCreated && !wt.created {
				wt.created = true
				*delays = append(*delays, time.Since(ev.Time))
			}
		}
	}
}

// allOK は読めなかったファイルがすべて後で読めたかを返す。
func allOK(wt *watch) bool {
	for name := range wt.failed {
		if !wt.ok[name] {
			return false
		}
	}
	return true
}

// renameBehavior は共有上で os.Rename が既存を置き換えるか、置き換え無しの rename が
// 既存があるときに失敗するかを実際に試す。
func renameBehavior(dir, host string) (replaces, noReplaceWorks bool) {
	a := filepath.Join(dir, host+".rename-a.tmp")
	b := filepath.Join(dir, host+".rename-b.tmp")
	defer os.Remove(a)
	defer os.Remove(b)
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	if err := os.Rename(a, b); err == nil {
		data, _ := os.ReadFile(b)
		replaces = string(data) == "a"
	}
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	if err := store.RenameNoReplace(a, b); err != nil {
		data, _ := os.ReadFile(b)
		noReplaceWorks = string(data) == "b"
	}
	return
}

// runCheck は全端末のログと共有フォルダの中身を突き合わせ、合格基準で判定する。
// events フォルダは生で走査し、.tmp の残留と JSON として読めないファイルも数える。
func runCheck(st *store.Store, root string) error {
	logs, _ := filepath.Glob(filepath.Join(logDir(root), "*.log"))
	if len(logs) == 0 {
		return errors.New("ログが無い")
	}
	claimed := map[int][]string{} // 番号 → 取ったと記録した端末
	perHost := map[string]int{}
	for _, p := range logs {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var rec logRecord
			if json.Unmarshal(sc.Bytes(), &rec) != nil {
				continue
			}
			claimed[rec.Number] = append(claimed[rec.Number], rec.Host)
			perHost[rec.Host]++
		}
		f.Close()
	}
	var sums []summary
	if paths, _ := filepath.Glob(filepath.Join(logDir(root), "*.summary.json")); len(paths) > 0 {
		for _, p := range paths {
			var s summary
			if data, err := os.ReadFile(p); err == nil && json.Unmarshal(data, &s) == nil {
				sums = append(sums, s)
			}
		}
	}

	fmt.Println("== 突き合わせ ==")
	for host, c := range perHost {
		fmt.Printf("%s: ログに %d 件\n", host, c)
	}

	problems := 0
	for number, hosts := range claimed {
		if len(hosts) > 1 {
			fmt.Printf("NG 番号 %d を複数の端末が取った: %s\n", number, strings.Join(hosts, ", "))
			problems++
		}
	}

	// events を生で走査する
	entries, err := os.ReadDir(st.IssuesDir(testRepo))
	if err != nil {
		return err
	}
	onDisk := map[int]bool{}
	tmpLeft, broken, total := 0, 0, 0
	for _, e := range entries {
		number, err := strconv.Atoi(e.Name())
		if !e.IsDir() || err != nil {
			continue
		}
		onDisk[number] = true
		total++
		dir := st.EventsDir(testRepo, number)
		files, err := os.ReadDir(dir)
		if err != nil {
			fmt.Printf("NG 番号 %d に events フォルダが無い\n", number)
			problems++
			continue
		}
		created, user := 0, ""
		for _, f := range files {
			name := f.Name()
			if strings.HasSuffix(name, ".tmp") {
				fmt.Printf("NG 番号 %d に .tmp が残っている: %s\n", number, name)
				tmpLeft++
				continue
			}
			if filepath.Ext(name) != ".json" {
				continue
			}
			var ev store.Event
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil {
				err = json.Unmarshal(data, &ev)
			}
			if err != nil {
				fmt.Printf("NG 番号 %d の %s が JSON として読めない: %v\n", number, name, err)
				broken++
				continue
			}
			if ev.Type == store.EventCreated {
				created++
				user = ev.User
			}
		}
		if created != 1 {
			fmt.Printf("NG 番号 %d の created イベントが %d 件\n", number, created)
			problems++
		}
		if hosts := claimed[number]; len(hosts) == 1 && created == 1 && hosts[0] != user {
			fmt.Printf("NG 番号 %d はログでは %s が取ったが、中身の作成者は %s\n", number, hosts[0], user)
			problems++
		}
	}
	for number := range claimed {
		if !onDisk[number] {
			fmt.Printf("NG 番号 %d はログにあるが共有フォルダに無い\n", number)
			problems++
		}
	}
	problems += tmpLeft + broken
	fmt.Printf("共有フォルダの Issue: %d 件、.tmp の残留: %d 件、読めない JSON: %d 件\n", total, tmpLeft, broken)

	fmt.Println("\n== 合格基準（設計メモ 3.14）==")
	pass := problems == 0
	fmt.Printf("番号の重複、created の件数、ログとの一致、.tmp の残留、読めない JSON: 問題 %d 件\n", problems)
	for _, s := range sums {
		ok := s.BrokenRecover == 0 && s.BrokenNever == 0 && s.DelayMaxSec <= 30
		fmt.Printf("%s: 作成 %d 件（要求 %d）、採番の失敗 %d 回、書きかけを読んだ回数 %d、読めないまま %d 件、見えるまでの最大 %.1f 秒（%d 件）、時計の差 %.1f 秒、os.Rename は置き換える=%v、置き換え無し rename=%v → %s\n",
			s.Host, s.Created, s.Requested, s.AllocErrors, s.BrokenRecover, s.BrokenNever, s.DelayMaxSec, s.DelayCount, s.SkewAvgSec, s.RenameReplaces, s.NoReplaceWorks, okText(ok))
		if !ok {
			pass = false
		}
		if s.Created < 1000 {
			fmt.Printf("  （%s は 1000 件に満たない。合格基準は各1000回）\n", s.Host)
		}
	}
	if len(sums) < 2 {
		fmt.Println("結果ファイルが2台分無い。2台で回してから -check を実行する")
		pass = false
	}
	fmt.Printf("\n判定: %s\n", okText(pass))
	return nil
}

func okText(ok bool) string {
	if ok {
		return "OK"
	}
	return "NG"
}
