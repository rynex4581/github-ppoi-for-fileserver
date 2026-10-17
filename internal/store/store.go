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
// 書き込みはすべて追記で、既存ファイルの上書きは行わない。
// ファイルは一時名で書いてから rename し、書きかけが他の端末から見えないようにする。
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
)

// Config はデータルート直下の config.json の内容。
type Config struct {
	// Owner はGitea API上のowner名。データルートにつき1つで固定する。
	Owner string `json:"owner"`
	// DefaultBranch は新規リポジトリの既定ブランチ名。
	DefaultBranch string `json:"default_branch"`
	// AttachmentMaxBytes は添付ファイルのサイズ上限。
	AttachmentMaxBytes int64 `json:"attachment_max_bytes"`
}

// Store はデータルートを指す。
type Store struct {
	root   string
	config Config
	host   string
	seq    atomic.Uint64
}

var hostSanitize = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// Open はデータルートを開く。フォルダが無ければエラーを返す（勝手に作らない）。
// config.json が無ければ既定値で作る。repos と meta のフォルダは無ければ作る。
func Open(root string) (*Store, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("データルートを開けない: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("データルートがフォルダではない: %s", root)
	}
	s := &Store{root: root}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	s.host = hostSanitize.ReplaceAllString(hostname, "-")

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

func (s *Store) reposDir() string { return filepath.Join(s.root, "repos") }
func (s *Store) metaDir() string  { return filepath.Join(s.root, "meta") }

func (s *Store) loadOrCreateConfig() error {
	path := filepath.Join(s.root, "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.config = Config{
			Owner:              "team",
			DefaultBranch:      "main",
			AttachmentMaxBytes: 10 * 1024 * 1024,
		}
		return writeJSONAtomic(path, s.config)
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.config); err != nil {
		return fmt.Errorf("%s の読み込みに失敗: %w", path, err)
	}
	if s.config.Owner == "" {
		return fmt.Errorf("%s に owner が無い", path)
	}
	if s.config.DefaultBranch == "" {
		s.config.DefaultBranch = "main"
	}
	return nil
}

// writeFileAtomic は一時名で書いてから rename する。
// 一時名は末尾に .tmp を付けたもので、読み込み側は拡張子で対象を絞るので拾わない。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
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
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// writeJSONAtomic はJSONを整形して書く。
// 人がエクスプローラーから開いて読めるよう、< > & はエスケープせずそのまま書く。
func writeJSONAtomic(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes())
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
