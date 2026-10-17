// Package localconfig は端末側の設定（%LOCALAPPDATA%\github-ppoi\config.json）を扱う。
// ここにあるものは消えてよい。消えた場合は初回起動と同じ扱いになる。
package localconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultPort は待ち受けポートの既定値（Giteaの既定と同じ）。
const DefaultPort = 3000

// Config は端末側の設定。
type Config struct {
	// DataRoot はデータルート（ファイルサーバ上の共有フォルダ）のパス。空なら未設定。
	DataRoot string `json:"data_root"`
	// Port は待ち受けポート。
	Port int `json:"port"`
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

// Load は設定を読む。ファイルが無ければ既定値を返す。
func Load() (Config, error) {
	c := Config{Port: DefaultPort}
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
	if c.Port == 0 {
		c.Port = DefaultPort
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
