// Package i18n は画面の文言を返す。
// 文言は Gitea の言語ファイル（third_party/gitea/locale）をそのまま使い、
// Gitea に無い本ツール独自の文言は extra/ 配下の別ファイルに持つ。
// 文言のキーは Gitea と同じもの。独自の文言のキーは "ppoi." で始める。
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"

	"github-ppoi-for-fileserver/third_party/gitea"
)

//go:embed extra/*.json
var extraFS embed.FS

// Langs は選べる言語。先頭が既定。
var Langs = []string{"ja-JP", "en-US"}

// Locale は1つの言語の文言表。無いキーは en-US に、それも無ければキーそのものに落ちる。
type Locale struct {
	lang     string
	msgs     map[string]string
	fallback map[string]string
}

// Load は言語の文言表を読む。
func Load(lang string) (*Locale, error) {
	if !IsSupported(lang) {
		lang = Langs[0]
	}
	msgs, err := loadLang(lang)
	if err != nil {
		return nil, err
	}
	l := &Locale{lang: lang, msgs: msgs}
	if lang != "en-US" {
		l.fallback, err = loadLang("en-US")
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

// IsSupported は選べる言語かを返す。
func IsSupported(lang string) bool {
	for _, l := range Langs {
		if l == lang {
			return true
		}
	}
	return false
}

func loadLang(lang string) (map[string]string, error) {
	msgs := map[string]string{}
	if err := readJSON(gitea.LocaleFS, "locale/locale_"+lang+".json", msgs); err != nil {
		return nil, err
	}
	if err := readJSON(extraFS, "extra/"+lang+".json", msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func readJSON(fsys fs.FS, name string, into map[string]string) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("言語ファイル %s を読めない: %w", name, err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("言語ファイル %s の形式が違う: %w", name, err)
	}
	for k, v := range m {
		into[k] = v
	}
	return nil
}

// Lang は言語名を返す。
func (l *Locale) Lang() string { return l.lang }

// Tr はキーの文言を返す。引数があれば fmt.Sprintf で埋める。
func (l *Locale) Tr(key string, args ...any) string {
	s, ok := l.msgs[key]
	if !ok && l.fallback != nil {
		s, ok = l.fallback[key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}
