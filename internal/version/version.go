// Package version は本ツールのバージョン表記を持つ。
// 表記の規則は CLAUDE.md の「バージョン表記」を参照。
package version

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// Number はバージョン番号。英字の修正版が付く場合は "0.10A" のようになる。
const Number = "0.20"

// Display は画面のフッター等に出す表記。
const Display = "Ver " + Number

// Parsed は "X.YZ" と英字に分けたバージョン。
type Parsed struct {
	X      int // 1.00 以降、データ形式に互換性の無い変更で上がる
	YZ     int // Y は大きい修正、Z は小さい修正。2桁で持つ
	Letter int // 英字の修正版。無ければ 0、A なら 1
}

var numberPattern = regexp.MustCompile(`^(\d+)\.(\d{2})([A-Z]?)$`)

// Parse は "0.11A" のような表記を分ける。
func Parse(s string) (Parsed, error) {
	m := numberPattern.FindStringSubmatch(s)
	if m == nil {
		return Parsed{}, errors.New("バージョン表記が X.YZ の形でない: " + s)
	}
	p := Parsed{}
	p.X, _ = strconv.Atoi(m[1])
	p.YZ, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		p.Letter = int(m[3][0]-'A') + 1
	}
	return p, nil
}

// FromNumeric は VERSIONINFO の数値4組（0.11.1.0）から戻す。4つ目は使わない。
func FromNumeric(major, minor, patch int) Parsed {
	return Parsed{X: major, YZ: minor, Letter: patch}
}

// Numeric は VERSIONINFO の数値4組を返す。英字は数字に置き換える（0.11A→0.11.1.0）。
func (p Parsed) Numeric() (major, minor, patch, build int) {
	return p.X, p.YZ, p.Letter, 0
}

// String は "0.11A" の形に戻す。
func (p Parsed) String() string {
	s := fmt.Sprintf("%d.%02d", p.X, p.YZ)
	if p.Letter > 0 && p.Letter <= 26 {
		s += string(rune('A' + p.Letter - 1))
	}
	return s
}

// Compare は a と b を比べ、a が古ければ負、同じなら 0、新しければ正を返す。
func Compare(a, b Parsed) int {
	switch {
	case a.X != b.X:
		return a.X - b.X
	case a.YZ != b.YZ:
		return a.YZ - b.YZ
	default:
		return a.Letter - b.Letter
	}
}

// Current は Number を分けたもの。Number が規則に合っていなければ起動時に落ちる。
var Current = mustParse(Number)

func mustParse(s string) Parsed {
	p, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return p
}
