// Package thirdparty は exe に同梱している第三者のソフトウェアのライセンス文を埋め込む。
// 画面のライセンス一覧ページで表示する。
package thirdparty

import "embed"

// LicenseFS は各ライセンス文を持つ。
//
//go:embed primer-css/LICENSE octicons/LICENSE gitea/LICENSE goldmark/LICENSE go/LICENSE
var LicenseFS embed.FS

// Component は同梱しているソフトウェア1つ分。
type Component struct {
	Name    string
	Version string
	License string // ライセンスの名前
	URL     string
	File    string // LicenseFS 内のパス
	Note    string // 使い方（画面に出す）
}

// Components は同梱しているソフトウェアの一覧。
var Components = []Component{
	{Name: "Go", Version: "1.27", License: "BSD-3-Clause", URL: "https://go.dev/", File: "go/LICENSE", Note: "実行ファイルに含まれる標準ライブラリとランタイム"},
	{Name: "Primer CSS", Version: "21.5.1", License: "MIT", URL: "https://primer.style/", File: "primer-css/LICENSE", Note: "画面の CSS"},
	{Name: "Octicons", Version: "19.40.0", License: "MIT", URL: "https://primer.style/octicons/", File: "octicons/LICENSE", Note: "画面のアイコン"},
	{Name: "Gitea", Version: "v28.1.0", License: "MIT", URL: "https://gitea.com/", File: "gitea/LICENSE", Note: "画面の文言（言語ファイル）と API の形"},
	{Name: "goldmark", Version: "v1.8.6", License: "MIT", URL: "https://github.com/yuin/goldmark", File: "goldmark/LICENSE", Note: "Markdown の描画"},
}
