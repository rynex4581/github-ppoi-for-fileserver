// Package version は本ツールのバージョン表記を持つ。
// 表記の規則は CLAUDE.md の「バージョン表記」を参照。
package version

// Number はバージョン番号。英字の修正版が付く場合は "0.10A" のようになる。
const Number = "0.10"

// Display は画面のフッター等に出す表記。
const Display = "Ver " + Number
