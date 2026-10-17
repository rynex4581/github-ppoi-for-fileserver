// github-ppoi は本ツールの起動部。
// 端末側の設定を読み、localhost でWeb画面を上げ、ブラウザを開く。
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/version"
	"github-ppoi-for-fileserver/internal/web"
)

func main() {
	cfg, err := localconfig.Load()
	if err != nil {
		log.Fatal(err)
	}
	srv, err := web.New(cfg, web.CurrentLogin())
	if err != nil {
		log.Fatal(err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("ポート %d で待ち受けできない（既に起動していないか確認）: %v", cfg.Port, err)
	}
	url := fmt.Sprintf("http://localhost:%d/", cfg.Port)
	log.Printf("github-ppoi for Fileserver %s  %s", version.Display, url)
	openBrowser(url)

	if err := http.Serve(ln, srv.Handler()); err != nil {
		log.Fatal(err)
	}
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
