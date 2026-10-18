//go:build windows

package store

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// renameNoReplace は from を to に改名する。to が既にあれば失敗する。
// os.Rename は Windows では MoveFileExW を置き換えフラグ付きで呼ぶため、同名があると黙って
// 上書きしてしまう。ここでは MoveFileW（置き換えフラグ無し）を直接呼ぶ。
func renameNoReplace(from, to string) error {
	f, err := syscall.UTF16PtrFromString(longPath(from))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	t, err := syscall.UTF16PtrFromString(longPath(to))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	if err := syscall.MoveFile(f, t); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// longPath は 260 文字を超えるパスでも Win32 API に渡せるよう \\?\ 形式にする。
// 短いパスはそのまま返す。
func longPath(path string) string {
	if len(path) < 248 || strings.HasPrefix(path, `\\?\`) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + abs[2:]
	}
	return `\\?\` + abs
}
