//go:build !windows

package main

// acquireInstanceLock は Windows 以外では何もしない（本ツールの動作環境は Windows で、
// ここは CI のビルドを通すためだけにある）。
func acquireInstanceLock() (func(), error) {
	return func() {}, nil
}
