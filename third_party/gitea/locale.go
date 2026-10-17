// Package gitea は Gitea から取り込んだファイルを exe に埋め込むためのパッケージ。
// 取り込み元は go-gitea/gitea のタグ v28.1.0。ファイルは編集しない（MIT、LICENSE を参照）。
package gitea

import "embed"

// LocaleFS は Gitea の言語ファイル（options/locale/locale_<言語>.json）を持つ。
//
//go:embed locale/*.json
var LocaleFS embed.FS
