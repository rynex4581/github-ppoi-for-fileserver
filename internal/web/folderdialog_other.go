//go:build !windows

package web

import "errors"

// errFolderDialogUnsupported は Windows 以外でフォルダ選択のダイアログを求められたときに返す。
var errFolderDialogUnsupported = errors.New("folder dialog is only available on Windows")

func pickFolder(title string) (path string, cancelled bool, err error) {
	return "", false, errFolderDialogUnsupported
}
