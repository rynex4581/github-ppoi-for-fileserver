//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github-ppoi-for-fileserver/internal/localconfig"
)

// acquireInstanceLock は %LOCALAPPDATA%\github-ppoi\instance.lock を共有無しで開いて握る。
// 握っている間は他のプロセスが同じファイルを開けないので、起動中のもう1つを締め出せる。
// プロセスが終わればハンドルは閉じられ、残骸の掃除は要らない。
func acquireInstanceLock() (func(), error) {
	dir, err := localconfig.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "instance.lock")
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("%s を開けない: %w", path, err)
	}
	return func() { syscall.CloseHandle(h) }, nil
}
