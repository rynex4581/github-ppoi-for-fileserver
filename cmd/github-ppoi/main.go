// github-ppoi は本ツールの起動部。
// 端末側の設定を読み、localhost でWeb画面を上げ、ブラウザを開く。
//
// ビルドの前に go generate で VERSIONINFO のリソース（resource.syso）を作る。
// goversioninfo（github.com/josephspurrier/goversioninfo、MIT）が要る。無いときは
// リソース無しでもビルドできるが、exe のプロパティに版が出ず、更新通知の比較もできない。
//
//go:generate go run ../mkversioninfo versioninfo.json
//go:generate goversioninfo -64 -o resource.syso versioninfo.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/version"
	"github-ppoi-for-fileserver/internal/web"
)

func main() {
	if code, ok := runSubcommand(os.Args); ok {
		os.Exit(code)
	}
	noBrowser := flag.Bool("no-browser", false, "起動時にブラウザを開かない")
	flag.Parse()

	cfg, err := localconfig.Load()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		log.Printf("git が PATH に無い。Git for Windows（2.38 以上）を入れないとコードの画面とマージが動かない")
	}

	// 二重起動の検知。既に自分が上がっていれば、ブラウザだけ開いて終わる
	if port := localconfig.ReadPortFile(); port > 0 && isRunning(port) {
		url := fmt.Sprintf("http://localhost:%d/", port)
		log.Printf("既に起動している: %s", url)
		if !*noBrowser {
			openBrowser(url)
		}
		return
	}
	// 起動中の相手がまだ応答できない間に2つ目が上がらないよう、ロックファイルを握る。
	// 握っている間は他のプロセスが開けず、プロセスが終わればロックも外れる
	release, err := acquireInstanceLock()
	if err != nil {
		log.Printf("別の github-ppoi が起動中: %v", err)
		if port := waitForRunning(15 * time.Second); port > 0 {
			url := fmt.Sprintf("http://localhost:%d/", port)
			if !*noBrowser {
				openBrowser(url)
			}
			return
		}
		log.Fatal("別の github-ppoi が起動中のようだが、応答が無い。少し待ってからやり直す")
	}
	defer release()

	ln, port, err := listen(cfg.Port)
	if err != nil {
		log.Fatal(err)
	}
	if err := localconfig.WritePortFile(port); err != nil {
		log.Printf("port.txt を書けない: %v", err)
	}

	srv, err := web.New(cfg, web.CurrentLogin(), port)
	if err != nil {
		log.Fatal(err)
	}

	url := fmt.Sprintf("http://localhost:%d/", port)
	log.Printf("github-ppoi for Fileserver %s  %s", version.Display, url)
	if !*noBrowser {
		openBrowser(url)
	}

	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	// Ctrl+C やコンソールを閉じたときに、キャッシュを書き出してから終わる
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		srv.Close()
		os.Exit(0)
	}()
	if err := hs.Serve(ln); err != nil {
		log.Fatal(err)
	}
}

// listen は待ち受けを開始する。
// ポートが明示されていればそのポートだけを試し、使えなければエラーにする。
// 明示が無ければ既定の範囲から順に空きを探す。
func listen(fixed int) (net.Listener, int, error) {
	if fixed > 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", fixed))
		if err != nil {
			return nil, 0, fmt.Errorf("config.json で指定されたポート %d で待ち受けできない: %w", fixed, err)
		}
		return ln, fixed, nil
	}
	for port := localconfig.PortRangeStart; port <= localconfig.PortRangeEnd; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("ポート %d〜%d がすべて使用中で待ち受けできない", localconfig.PortRangeStart, localconfig.PortRangeEnd)
}

// waitForRunning は port.txt のポートで本ツールが応答するまで待ち、応答したポートを返す。時間切れなら 0。
func waitForRunning(limit time.Duration) int {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if port := localconfig.ReadPortFile(); port > 0 && isRunning(port) {
			return port
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0
}

// isRunning は指定ポートで本ツールが動いているかを、バージョン応答の独自の欄で確かめる。
func isRunning(port int) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/version", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v struct {
		Own string `json:"github_ppoi_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return false
	}
	return v.Own != ""
}

// openBrowser は既定のブラウザでURLを開く。開けなくても起動は続ける。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("ブラウザを開けない: %v", err)
	}
}
