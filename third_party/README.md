# 同梱している第三者のソフトウェア

本ツールの画面は以下のOSSを exe に埋め込んで使用している。各ライセンス文は同じ階層のフォルダに置く。

| 名称 | 版 | ライセンス | 埋め込み先 |
| --- | --- | --- | --- |
| Primer CSS | 21.5.1 | MIT | internal/web/static/primer.css |
| Octicons | 19.40.0 | MIT | internal/web/static/octicons/ |
| Gitea の言語ファイル | v28.1.0（options/locale/locale_ja-JP.json、locale_en-US.json） | MIT | third_party/gitea/locale/ |

Primer CSS は 22 系で Box、State、TimelineItem、UnderlineNav などの部品が削除されたため、これらが残っている 21 系の最終版を使う。

Gitea の言語ファイルは編集せずそのまま使い、本ツール独自の文言は internal/i18n/extra/ に別ファイルで持つ。Gitea は 1.26 以降、言語ファイルの形式を ini から json に変えている。
