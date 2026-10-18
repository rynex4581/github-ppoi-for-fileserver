//go:build !windows

package store

import (
	"io/fs"
	"os"
)

// renameNoReplace は from を to に改名する。to が既にあれば失敗する。
// Windows 以外では既存の有無を先に見てから os.Rename する（厳密な排他ではないが、
// 本ツールの動作環境は Windows なので CI とテストが通れば足りる）。
func renameNoReplace(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrExist}
	}
	return os.Rename(from, to)
}
