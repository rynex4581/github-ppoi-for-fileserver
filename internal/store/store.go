// Package store はデータルート（ファイルサーバ上の共有フォルダ）への読み書きを担う。
//
// フォルダ構成
//
//	<データルート>\
//	  config.json                  全体設定
//	  repos\<リポジトリ名>.git\    ベアリポジトリ
//	  meta\users\<ログオン名>.json 表示名
//	  meta\<リポジトリ名>\repo.json
//	  meta\<リポジトリ名>\issues\<6桁番号>\events\<イベントファイル>
//
// イベントは追記だけで、既存ファイルの上書きは行わない。config.json、repo.json、labels.json、
// users\<ログオン名>.json は全体を書き直す上書きファイルである。
// どのファイルも一時名で書いてから rename し、書きかけが他の端末から見えないようにする。
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// CurrentFormat はこの exe が読み書きできるデータ形式の版。フォルダ構成やイベント JSON に
// 互換性の無い変更をしたときに上げる。
const CurrentFormat = 1

// ErrReadOnly は読み取り専用で開いたデータルートに書こうとしたときのエラー。
var ErrReadOnly = errors.New("このデータルートはこの exe が知らないデータ形式なので、読み取り専用で開いている。新しいバージョンの exe に更新する")

// Config はデータルート直下の config.json の内容。1つのデータルートが owner 1つに対応する。
type Config struct {
	// Format はデータ形式の版。無ければ 1。exe が知らない値（CurrentFormat より大きい）なら読み取り専用で開く。
	Format int `json:"format"`
	// Owner はGitea API上のowner名。URLに入るためASCIIに限る。データルートを作るときに決め、既定値は無い。
	Owner string `json:"owner"`
	// OwnerName は owner の表示名（日本語可）。空なら Owner をそのまま表示する。
	OwnerName string `json:"owner_name"`
	// DefaultBranch は新規リポジトリの既定ブランチ名。
	DefaultBranch string `json:"default_branch"`
	// AttachmentMaxBytes は添付ファイルのサイズ上限。
	AttachmentMaxBytes int64 `json:"attachment_max_bytes"`
}

// DisplayOwner は owner の表示名を返す。未設定なら owner 名。
func (c Config) DisplayOwner() string {
	if c.OwnerName != "" {
		return c.OwnerName
	}
	return c.Owner
}

var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,49}$`)

// ValidateOwner は owner 名として使えるか（英数字で始まるASCII、50文字以内）を確かめる。
func ValidateOwner(owner string) error {
	if !ownerPattern.MatchString(owner) || strings.Contains(owner, "..") {
		return errors.New("owner 名は英数字で始まり、英数字と . _ - だけを使う（50文字以内）")
	}
	if isWindowsReserved(owner) {
		return errors.New("その名前は Windows で使えない（CON、NUL、COM1 など）")
	}
	return nil
}

// isWindowsReserved は Windows でファイル名に使えない予約名（CON、PRN、AUX、NUL、COM1〜9、LPT1〜9。
// 拡張子が付いていても同じ）かを返す。
func isWindowsReserved(name string) bool {
	stem := strings.ToUpper(name)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return true
	}
	return false
}

// ErrConfigExists は既に config.json のあるフォルダをデータルートとして新規作成しようとしたときのエラー。
var ErrConfigExists = errors.New("このフォルダには既に config.json がある")

// ErrConfigRace は config.json を書く直前に別の端末が先に書いたときのエラー。
var ErrConfigRace = errors.New("config.json を書けなかった。他の端末が同時にこのフォルダをデータルートとして初期化した可能性がある。もう一度やり直す")

// Create はフォルダをデータルートとして初期化する。config.json を書き、repos と meta を作る。
// フォルダは既に存在していなければならない。config.json が既にあれば ErrConfigExists。
func Create(root string, cfg Config) (*Store, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("データルートを開けない: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("データルートがフォルダではない: %s", root)
	}
	if err := ValidateOwner(cfg.Owner); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "config.json")
	if _, err := os.Stat(path); err == nil {
		return nil, ErrConfigExists
	}
	cfg.Format = CurrentFormat
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = "main"
	}
	if cfg.AttachmentMaxBytes == 0 {
		cfg.AttachmentMaxBytes = 10 * 1024 * 1024
	}
	cfg.OwnerName = strings.TrimSpace(cfg.OwnerName)
	// 2台が同時に初期化しても後の方が上書きしないよう、既存があれば失敗する書き方にする
	if err := writeJSONNew(path, cfg); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, ErrConfigRace
		}
		return nil, err
	}
	return Open(root)
}

// Store はデータルートを指す。
type Store struct {
	root     string
	config   Config
	readOnly bool   // config.json の format が exe より新しい。書き込みはすべて ErrReadOnly
	host     string // 端末名（ファイル名に使える文字に直したもの）
	pid      int    // 自プロセスの ID。同じ端末で2つ動いても名前が重ならないようにする
	seq      atomic.Uint64
	cache    eventCache // イベントのローカルキャッシュ（cache.go）
}

var hostSanitize = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// hostName は端末名をファイル名に使える形で返す。取れなければ unknown。
func hostName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return hostSanitize.ReplaceAllString(hostname, "-")
}

// Open は既存のデータルートを開く。フォルダや config.json が無ければエラーを返す（勝手に作らない）。
// repos と meta のフォルダは無ければ作る。
func Open(root string) (*Store, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("データルートを開けない: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("データルートがフォルダではない: %s", root)
	}
	s := &Store{root: root, host: hostName(), pid: os.Getpid()}

	if err := s.loadOrCreateConfig(); err != nil {
		return nil, err
	}
	for _, dir := range []string{s.reposDir(), filepath.Join(s.metaDir(), "users")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Root はデータルートのパスを返す。
func (s *Store) Root() string { return s.root }

// Config は全体設定を返す。
func (s *Store) Config() Config { return s.config }

// ReadOnly はこのデータルートを読み取り専用で開いているかを返す。
func (s *Store) ReadOnly() bool { return s.readOnly }

// writable は書き込みの前に呼ぶ。読み取り専用なら ErrReadOnly。
func (s *Store) writable() error {
	if s.readOnly {
		return ErrReadOnly
	}
	return nil
}

func (s *Store) reposDir() string { return filepath.Join(s.root, "repos") }
func (s *Store) metaDir() string  { return filepath.Join(s.root, "meta") }

func (s *Store) loadOrCreateConfig() error {
	path := filepath.Join(s.root, "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s が無い。データルートとして初期化されていない", path)
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.config); err != nil {
		return fmt.Errorf("%s の読み込みに失敗: %w", path, err)
	}
	if s.config.Format == 0 {
		s.config.Format = 1
	}
	if s.config.Format > CurrentFormat {
		s.readOnly = true
	}
	if s.config.Owner == "" {
		return fmt.Errorf("%s に owner 名が無い。\"owner\" を足す", path)
	}
	if err := ValidateOwner(s.config.Owner); err != nil {
		return fmt.Errorf("%s の owner 名が使えない: %w", path, err)
	}
	if s.config.DefaultBranch == "" {
		s.config.DefaultBranch = "main"
	}
	// 項目が無い（古い）config.json でも Create と同じ既定値にする。0 のままだと添付が
	// すべて「大きすぎる」になり、POST のボディの上限も 1MB になる
	if s.config.AttachmentMaxBytes <= 0 {
		s.config.AttachmentMaxBytes = 10 * 1024 * 1024
	}
	return nil
}

// tmpTag は一時名に入れる「端末名-PID」。同じ対象に別の端末や別のプロセスが同時に書いても
// 一時名が重ならず、クラッシュで残った前回の .tmp にも邪魔されない。
var tmpTag = fmt.Sprintf("%s-%d", hostName(), os.Getpid())

// tmpSeq は一時名に入れるプロセス内の連番。
var tmpSeq atomic.Uint64

// writeFileAtomic は一時名で書いてから rename する。
// 一時名は「<本来の名前>.<端末名>-<PID>-<連番>.tmp」で、読み込み側は拡張子で対象を絞るので拾わない。
// replace が真なら既存のファイルを置き換える（上書きファイル用）。偽なら既存があれば失敗する
// （イベントと添付用。同じ名前が後勝ちで消えないようにする）。
// 書き終えたら、同じ対象の古い .tmp（1時間以上前のもの）を掃除する。
func writeFileAtomic(path string, data []byte, replace bool) error {
	tmp := fmt.Sprintf("%s.%s-%d.tmp", path, tmpTag, tmpSeq.Add(1))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	rename := renameNoReplace
	if replace {
		rename = os.Rename
	}
	if err := rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	removeStaleTemp(path)
	return nil
}

// RenameNoReplace は既存があれば失敗する rename。検証プログラムが共有上の挙動を確かめるために公開する。
func RenameNoReplace(from, to string) error { return renameNoReplace(from, to) }

// removeStaleTemp は path と同じ対象の .tmp のうち1時間以上前のものを消す。
// クラッシュや SMB の切断で残った書きかけの掃除で、消せなくても何もしない。
func removeStaleTemp(path string) {
	dir, base := filepath.Split(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, base+".") || !strings.HasSuffix(n, ".tmp") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < time.Hour {
			continue
		}
		os.Remove(filepath.Join(dir, n))
	}
}

// writeJSONAtomic はJSONを整形して書き、既存のファイルがあれば置き換える（上書きファイル用）。
// 人がエクスプローラーから開いて読めるよう、< > & はエスケープせずそのまま書く。
func writeJSONAtomic(path string, v any) error {
	return writeJSON(path, v, true)
}

// writeJSONNew はJSONを整形して新しいファイルとして書く。既存のファイルがあれば失敗する（イベント用）。
func writeJSONNew(path string, v any) error {
	return writeJSON(path, v, false)
}

func writeJSON(path string, v any, replace bool) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes(), replace)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s の読み込みに失敗: %w", path, err)
	}
	return nil
}

// isSafeName はリポジトリ名やログオン名として、パスに使って安全な文字列かを返す。
func isSafeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return false
	}
	return true
}
