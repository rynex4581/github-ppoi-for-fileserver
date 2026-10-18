package main

import (
	"flag"
	"fmt"
	"os"

	"github-ppoi-for-fileserver/internal/integrity"
	"github-ppoi-for-fileserver/internal/store"
)

// サブコマンド（設計メモ 3.17）。
//   github-ppoi reserve -root <データルート> -repo <リポジトリ名> -to <番号>
//   github-ppoi check   -root <データルート> [-repo <リポジトリ名>]
// どちらも画面は上げず、結果を標準出力に出して終わる。

// runSubcommand は os.Args[1] がサブコマンドなら実行して真を返す。終了コードは code に入る。
func runSubcommand(args []string) (code int, handled bool) {
	if len(args) < 2 {
		return 0, false
	}
	switch args[1] {
	case "reserve":
		return runReserve(args[2:]), true
	case "check":
		return runCheck(args[2:]), true
	}
	return 0, false
}

// runReserve は指定した番号までの空の番号フォルダを作り、採番を進める。
// バックアップからの復元で最大番号が巻き戻ったときに使う。
func runReserve(args []string) int {
	fs := flag.NewFlagSet("reserve", flag.ContinueOnError)
	root := fs.String("root", "", "データルートのパス")
	repo := fs.String("repo", "", "リポジトリ名")
	to := fs.Int("to", 0, "この番号まで予約する")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" || *repo == "" || *to <= 0 {
		fmt.Fprintln(os.Stderr, "使い方: github-ppoi reserve -root <データルート> -repo <リポジトリ名> -to <番号>")
		return 2
	}
	st, err := store.Open(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	created, err := st.ReserveIssueNumbers(*repo, *to)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(created) == 0 {
		fmt.Printf("%s は既に %d 番まで使われている。何もしない\n", *repo, *to)
		return 0
	}
	fmt.Printf("%s に %d 件の番号フォルダを作った（%d〜%d）。次の Issue は %d 番になる\n", *repo, len(created), created[0], created[len(created)-1], *to+1)
	return 0
}

// runCheck はデータルートの整合性を調べて一覧を出す。問題があれば終了コード 2。
func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	root := fs.String("root", "", "データルートのパス")
	repo := fs.String("repo", "", "リポジトリ名（省略すると全部）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" {
		fmt.Fprintln(os.Stderr, "使い方: github-ppoi check -root <データルート> [-repo <リポジトリ名>]")
		return 2
	}
	n, err := integrity.Check(*root, *repo, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if n > 0 {
		return 2
	}
	return 0
}
