// smbtest は SMB 上での同時更新の検証用。本体と同じ採番と書き込みのコード（internal/store）を使う。
//
// 2台の端末から同じデータルートに向けて同時に回し、終わったら -check で突き合わせる。
//
//	smbtest.exe -root \\server\share\smbtest -n 2000
//	smbtest.exe -root \\server\share\smbtest -check
//
// 確かめること
//  1. 2台が同時に同じ番号を取ろうとしても片方だけが成功するか（番号の重複が無いか）
//  2. 一時名で書いて rename したファイルが、もう1台から書きかけで見えないか（JSON として読めないファイルが無いか）
//  3. 片方が作ったファイルがもう片方から何秒で見えるか
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

func openOrCreate(root string) (*store.Store, error) {
	if _, err := os.Stat(filepath.Join(root, "config.json")); err == nil {
		return store.Open(root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	st, err := store.Create(root, store.Config{Owner: "smbtest", OwnerName: "SMB 検証"})
	if errors.Is(err, store.ErrConfigExists) {
		return store.Open(root)
	}
	return st, err
}

// logRecord はこの端末が取った番号の記録。1行1件で <root>\smbtest-logs\<端末名>.log に書く。
type logRecord struct {
	Number int       `json:"number"`
	Host   string    `json:"host"`
	Time   time.Time `json:"time"`
}

func logPath(root, host string) string {
	return filepath.Join(root, "smbtest-logs", host+".log")
}

func runLoop(st *store.Store, root, host string, n int) error {
	if _, err := st.GetRepo(testRepo); err != nil {
		if _, err := st.CreateRepo(testRepo, "SMB 検証用"); err != nil && !errors.Is(err, store.ErrRepoExists) {
			return fmt.Errorf("検証用リポジトリを作れない（git が要る）: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(logPath(root, host)), 0o755); err != nil {
		return err
	}
	lf, err := os.Create(logPath(root, host))
	if err != nil {
		return err
	}
	defer lf.Close()
	w := bufio.NewWriter(lf)
	defer w.Flush()

	seen := map[int]bool{}
	var delays []time.Duration
	partial := 0
	allocErrors := 0
	start := time.Now()
	fmt.Printf("%s: %d 件を作成する（%s）\n", host, n, root)

	for i := 1; i <= n; i++ {
		number, err := st.AllocateIssue(testRepo)
		if err != nil {
			allocErrors++
			fmt.Printf("採番に失敗: %v\n", err)
			continue
		}
		ev := store.Event{User: host, Title: fmt.Sprintf("%s %d", host, i), Body: "smbtest"}
		if _, err := st.WriteCreated(testRepo, number, ev, nil, nil); err != nil {
			return fmt.Errorf("書き込みに失敗 #%d: %w", number, err)
		}
		rec := logRecord{Number: number, Host: host, Time: time.Now().UTC()}
		data, _ := json.Marshal(rec)
		w.Write(data)
		w.WriteByte('\n')
		seen[number] = true

		// 他の端末が作った新しい Issue を探し、見えるまでの遅れと、読めないファイルの有無を記録する
		numbers, _ := listNumbers(st, testRepo)
		for _, m := range numbers {
			if seen[m] {
				continue
			}
			seen[m] = true
			events, err := st.ReadEvents(testRepo, m)
			if err != nil || len(events) == 0 {
				partial++
				continue
			}
			for _, e := range events {
				if e.Type == store.EventCreated && e.User != host {
					delays = append(delays, time.Since(e.Time))
				}
			}
		}
		if i%100 == 0 {
			fmt.Printf("%s: %d 件（%.0f 秒）\n", host, i, time.Since(start).Seconds())
		}
	}
	w.Flush()

	elapsed := time.Since(start)
	fmt.Printf("\n== %s の結果 ==\n", host)
	fmt.Printf("作成: %d 件、採番の失敗: %d 回、所要: %.1f 秒（%.1f 件/秒）\n", n-allocErrors, allocErrors, elapsed.Seconds(), float64(n-allocErrors)/elapsed.Seconds())
	fmt.Printf("他の端末の Issue で読めなかった（書きかけと思われる）もの: %d 件\n", partial)
	if len(delays) > 0 {
		sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
		var sum time.Duration
		for _, d := range delays {
			sum += d
		}
		fmt.Printf("他の端末の Issue が見えるまで: 平均 %.1f 秒、中央 %.1f 秒、最大 %.1f 秒（%d 件）\n",
			(sum / time.Duration(len(delays))).Seconds(), delays[len(delays)/2].Seconds(), delays[len(delays)-1].Seconds(), len(delays))
	} else {
		fmt.Println("他の端末の Issue は見えなかった（1台だけで回した場合はこれで正常）")
	}
	fmt.Printf("ログ: %s\n終わったら両方の端末が終わってから -check を1台で実行する\n", logPath(root, host))
	return nil
}

func listNumbers(st *store.Store, repo string) ([]int, error) {
	issues, err := st.ListIssues(repo)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.Number)
	}
	return out, nil
}

// runCheck は全端末のログと共有フォルダの中身を突き合わせる。
func runCheck(st *store.Store, root string) error {
	logs, _ := filepath.Glob(filepath.Join(root, "smbtest-logs", "*.log"))
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
	issues, err := st.ListIssues(testRepo)
	if err != nil {
		return err
	}
	fmt.Println("== 突き合わせ ==")
	for host, c := range perHost {
		fmt.Printf("%s: ログに %d 件\n", host, c)
	}
	fmt.Printf("共有フォルダの Issue: %d 件\n", len(issues))

	problems := 0
	for number, hosts := range claimed {
		if len(hosts) > 1 {
			fmt.Printf("NG 番号 %d を複数の端末が取った: %s\n", number, strings.Join(hosts, ", "))
			problems++
		}
	}
	onDisk := map[int]bool{}
	for _, is := range issues {
		onDisk[is.Number] = true
		created := 0
		for _, ev := range is.Events {
			if ev.Type == store.EventCreated {
				created++
			}
		}
		if created != 1 {
			fmt.Printf("NG 番号 %d の created イベントが %d 件\n", is.Number, created)
			problems++
		}
		if hosts := claimed[is.Number]; len(hosts) == 1 && hosts[0] != is.User {
			fmt.Printf("NG 番号 %d はログでは %s が取ったが、中身の作成者は %s\n", is.Number, hosts[0], is.User)
			problems++
		}
	}
	for number := range claimed {
		if !onDisk[number] {
			fmt.Printf("NG 番号 %d はログにあるが共有フォルダに無い\n", number)
			problems++
		}
	}
	if problems == 0 {
		fmt.Println("OK 番号の重複なし、created は各1件、ログと中身が一致")
	} else {
		fmt.Printf("問題 %d 件\n", problems)
	}
	return nil
}
