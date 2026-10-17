// Package localconfig は端末側の設定（%LOCALAPPDATA%\github-ppoi\config.json）と、
// 起動中のポートを知らせるファイル（port.txt）を扱う。
// ここにあるものは消えてよい。消えた場合は初回起動と同じ扱いになる。
package localconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 待ち受けポートの既定の範囲。9418 はgitプロトコルの公式ポート。
// 使用中なら +1 ずつ順に空きを探す。
const (
	PortRangeStart = 9418
	PortRangeEnd   = 9427
)

// DefaultLang は画面の言語の既定値。
const DefaultLang = "ja-JP"

// Config は端末側の設定。
type Config struct {
	// DataRoot はデータルート（ファイルサーバ上の共有フォルダ）のパス。空なら未設定。
	DataRoot string `json:"data_root"`
	// Port は待ち受けポート。0 なら既定の範囲から空きを探す。
	// 明示してある場合は空きを探さず、使えなければエラーで止める。
	Port int `json:"port,omitempty"`
	// Lang は画面の言語（ja-JP または en-US）。空なら DefaultLang。
	Lang string `json:"lang,omitempty"`
}

// Dir は設定ファイルを置くフォルダ（%LOCALAPPDATA%\github-ppoi）を返す。
func Dir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("設定フォルダの場所が分からない: %w", err)
		}
	}
	return filepath.Join(base, "github-ppoi"), nil
}

// Path は設定ファイルのパスを返す。
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// PortFilePath は起動中のポートを書くファイル（port.txt）のパスを返す。
func PortFilePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "port.txt"), nil
}

// Load は設定を読む。ファイルが無ければ既定値を返す。
func Load() (Config, error) {
	c := Config{}
	path, err := Path()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s の読み込みに失敗: %w", path, err)
	}
	return c, nil
}

// Save は設定を書く。フォルダが無ければ作る。
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// WritePortFile は起動中のポートを port.txt に書く。Codex や tea はこのファイルを参照する。
func WritePortFile(port int) error {
	path, err := PortFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(port)+"\n"), 0o644)
}

// ReadPortFile は port.txt のポートを返す。無い、または読めなければ 0。
func ReadPortFile() int {
	path, err := PortFilePath()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}
