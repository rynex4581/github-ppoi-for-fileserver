//go:build windows

package store

import (
	"errors"

	"golang.org/x/sys/windows"
)

// sysDefineDOSDevice は name を target に定義する（DefineDosDeviceW。フラグ無し）。
func sysDefineDOSDevice(name, target string) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.DefineDosDevice(0, n, t)
}

// sysRemoveDOSDevice は name の最も新しい定義を削除する。
func sysRemoveDOSDevice(name string) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	return windows.DefineDosDevice(windows.DDD_REMOVE_DEFINITION, n, nil)
}

// sysQueryDOSDevice は name の定義先を新しい順に返す（\??\UNC\サーバ\共有\... の形）。定義が無ければエラー。
func sysQueryDOSDevice(name string) ([]string, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	for size := 1024; size <= 65536; size *= 4 {
		buf := make([]uint16, size)
		got, err := windows.QueryDosDevice(n, &buf[0], uint32(len(buf)))
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// 定義先は NUL 区切りで並び、最後は NUL が 2 つ続く
		var targets []string
		start := 0
		for i := 0; i < int(got) && i < len(buf); i++ {
			if buf[i] == 0 {
				if i > start {
					targets = append(targets, windows.UTF16ToString(buf[start:i]))
				}
				start = i + 1
			}
		}
		return targets, nil
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}
