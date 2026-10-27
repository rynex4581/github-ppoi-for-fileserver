package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

// UNC パスのデータルートと DOS デバイス名（設計メモ 3.19）
//
// git は対象のフォルダへ移った後にカレントのパスを取り直す。UNC パスでは共有のルートから各階層の
// 親フォルダを照会してパスを組み立てるため、途中に一覧の権限が無い階層があると失敗する
// （fatal: unable to get current working directory）。そこで UNC のデータルートごとに DOS デバイス名を
// 定義し、git にはデータルートを根元にした \\.\<名前>\... の形（仮想パス）を渡す。
// 名前は github-ppoi-<owner 名>。owner 名は同じ端末で重複しないので、名前も重ならず、
// どの端末でも同じ名前になる。定義はログオンセッション内のみで、再起動で消える。
// ツールの終了時には削除しない（ツールの停止中も Codex 等の git が仮想パスで動作できるようにするため）。

// dosDevicePrefix は DOS デバイス名の先頭。後ろに owner 名を付ける。テストから差し替える。
var dosDevicePrefix = "github-ppoi-"

// DOS デバイス名の定義、削除、照会。テストから差し替える。
var (
	defineDOSDevice = sysDefineDOSDevice
	removeDOSDevice = sysRemoveDOSDevice
	queryDOSDevice  = sysQueryDOSDevice
)

// uncTarget はデータルートが UNC パス（\\サーバ\共有\...）なら、DOS デバイス名の定義先にする
// パス（末尾の \ を除いたもの）を返す。\\.\ と \\?\ で始まるものは UNC として扱わない。
func uncTarget(root string) (string, bool) {
	clean := filepath.Clean(root)
	vol := filepath.VolumeName(clean)
	if !strings.HasPrefix(vol, `\\`) || strings.HasPrefix(vol, `\\.\`) || strings.HasPrefix(vol, `\\?\`) {
		return "", false
	}
	return strings.TrimRight(clean, `\`), true
}

// IsUNC はデータルートが UNC パスで登録されているかを返す。
func (s *Store) IsUNC() bool {
	_, ok := uncTarget(s.root)
	return ok
}

// DefineDOSDevice は UNC のデータルートに DOS デバイス名を定義し、以後 GitPath が仮想パスを返すようにする。
// ローカルパスのデータルートには何もしない。失敗したときは GitPath が実体のパスのまま返し、
// 理由は DOSDeviceErr でも返す。
func (s *Store) DefineDOSDevice() error {
	target, ok := uncTarget(s.root)
	if !ok {
		return nil
	}
	s.dosErr = s.defineDOSDevice(target)
	return s.dosErr
}

func (s *Store) defineDOSDevice(target string) error {
	name := dosDevicePrefix + s.config.Owner
	// \\.\ のパスでは末尾の . が落ち、別の名前を探しに行くので定義しない
	if strings.HasSuffix(name, ".") {
		return fmt.Errorf("owner 名 %s は末尾が . なので、DOS デバイス名として使用できない", s.config.Owner)
	}
	// 既に定義があれば削除してから定義し直す（異常終了などで残った定義への対処）。
	// 同じ名前に定義が積み重なっていることがあるので、照会できなくなるまで削除する
	for i := 0; i < 16; i++ {
		if _, err := queryDOSDevice(name); err != nil {
			break
		}
		if err := removeDOSDevice(name); err != nil {
			return fmt.Errorf("DOS デバイス名 %s の古い定義を削除できない: %w", name, err)
		}
	}
	if err := defineDOSDevice(name, target); err != nil {
		return fmt.Errorf("DOS デバイス名 %s を定義できない: %w", name, err)
	}
	s.gitRoot = `\\.\` + name
	return nil
}

// GitPath はデータルート配下のパス p を git に渡す形にする。DOS デバイス名を定義できていれば、
// データルートの部分を \\.\<名前> に置き換えた仮想パスを返し、それ以外は p のまま返す。
// ツール自身のファイル操作には使わず、実体のパスのまま扱う。
func (s *Store) GitPath(p string) string {
	if s.gitRoot == "" {
		return p
	}
	rel, err := filepath.Rel(s.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, `..\`) {
		return p
	}
	if rel == "." {
		return s.gitRoot
	}
	return s.gitRoot + `\` + rel
}

// DOSDeviceErr は DOS デバイス名を定義できなかった理由を返す。定義できたか、UNC でなければ nil。
func (s *Store) DOSDeviceErr() error { return s.dosErr }

// PeekOwner はデータルートの config.json の owner 名を返す。読めなければ空。
// サブコマンドが、端末に登録されている他のデータルートを開かずに owner 名を調べるために使う。
func PeekOwner(root string) string {
	var cfg Config
	if err := readJSON(filepath.Join(root, "config.json"), &cfg); err != nil {
		return ""
	}
	return cfg.Owner
}
