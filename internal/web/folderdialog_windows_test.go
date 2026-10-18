//go:build windows

package web

import (
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// フォルダ選択のダイアログが開き、閉じると取り消しとして返ることを確かめる。
// 画面が要るので、環境変数 PPOI_DIALOG_TEST=1 のときだけ動かす（CI では飛ばす）。
func TestPickFolderCancel(t *testing.T) {
	if os.Getenv("PPOI_DIALOG_TEST") == "" {
		t.Skip("PPOI_DIALOG_TEST が無いので飛ばす")
	}
	const title = "github-ppoi フォルダ選択のテスト"
	findWindow := user32.NewProc("FindWindowW")
	postMessage := user32.NewProc("PostMessageW")
	className, _ := syscall.UTF16PtrFromString("#32770")
	windowName, _ := syscall.UTF16PtrFromString(title)
	closed := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			h, _, _ := findWindow.Call(uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)))
			if h != 0 {
				postMessage.Call(h, 0x0010, 0, 0) // WM_CLOSE
				closed <- true
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		closed <- false
	}()
	path, cancelled, err := pickFolder(title)
	if err != nil {
		t.Fatal(err)
	}
	if !<-closed {
		t.Fatal("ダイアログのウィンドウが見つからなかった")
	}
	if !cancelled || path != "" {
		t.Fatalf("閉じたのに cancelled=%v path=%q", cancelled, path)
	}
}
