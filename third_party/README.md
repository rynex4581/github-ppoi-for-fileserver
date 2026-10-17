# 同梱している第三者のソフトウェア

本ツールの画面は以下のOSSを exe に埋め込んで使用している。各ライセンス文は同じ階層のフォルダに置く。

| 名称 | 版 | ライセンス | 埋め込み先 |
| --- | --- | --- | --- |
| Primer CSS | 21.5.1 | MIT | internal/web/static/primer.css |
| Octicons | 19.40.0 | MIT | internal/web/static/octicons/ |

Primer CSS は 22 系で Box、State、TimelineItem、UnderlineNav などの部品が削除されたため、これらが残っている 21 系の最終版を使う。
