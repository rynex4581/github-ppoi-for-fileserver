# 作業ログ

何をどこまで作ったかの記録。設計の決定事項は設計メモ（リポジトリには含めない）に、使い方は README にある。ここには進め方、設計メモに無い判断、残作業を書く。

## 2026-10-17

### リポジトリの初期化と画面の最初の版

- go.mod、.gitignore、LICENSE（MIT）、README、CLAUDE.md、GitHub Actions のリリース用ワークフローの雛形
- 保存層：データルートのフォルダ構成、フォルダ作成による Issue 番号の採番、イベント1件1ファイルの書き込み（一時名で書いて rename）と読み込み
- 画面：トップ（リポジトリ一覧、新規）、Issues（一覧、詳細、新規、コメント、クローズと再オープン）、設定（データルート）
- Primer CSS は 21.5.1。22 では Box、State、TimelineItem、UnderlineNav が無くなっていて GitHub の見た目が組めない
- GitHub に公開

### Issues の仕上げと文言の切り替え

- ラベル（管理画面、付け外し、絞り込み）、担当者（指名、絞り込み）。起動した人の利用者ファイルを無ければ作り、担当者の候補にする
- 文言を Gitea v28.1.0 の言語ファイル（json）に切り替え。既定は日本語、英語に切り替え可。独自の文言は internal/i18n/extra
- ポートを 9418 からの自動選択にし、port.txt に書き出す。二重起動の検知、--no-browser、Host ヘッダの拒否

### Code 画面

- ファイルツリー、ファイル表示（Markdown の描画、画像）、ブランチ切替、コミット履歴、コミットの差分、clone コマンド、空リポジトリの手順
- 読み取りは bare リポジトリを git で直接読む。ツール専用 clone はマージのときだけ使う

## 2026-10-18

### Pull requests

- 一覧、新規（比較）、詳細の3タブ、レビュー（承認／変更要請／コメント）、行コメント（前後3行の差分の断片を保存）
- マージは専用 clone で fetch → --no-ff → push。push が弾かれたら取り込み直して3回までやり直す。マージ後に head ブランチを削除
- Revert、コンフリクトの事前判定、force push の検知と古いコメントの畳み込み、承認必須への対応

### Settings

- 説明、デフォルトブランチ（bare の HEAD にも反映）、承認必須、force push 禁止（receive.denyNonFastForwards）

### 複数データルート

- 設計メモの変更を受けて、owner 固定をやめた。端末側の config.json にデータルートを複数登録し、各データルートの config.json が owner 名（ASCII）と表示名を持つ
- トップは owner ごとにまとめて表示。新規リポジトリは作成先の owner を選ぶ
- 横断ページ（全 owner の Issue 一覧と Pull Request 一覧。すべて／自分に割り当て／自分が作成、open／closed）
- 自分の設定にデータルートの一覧（追加・登録解除）と表示名を置いた。表示名は全データルートに書く
- 専用 clone の置き場を %LOCALAPPDATA%\github-ppoi\repos\<owner>\<リポジトリ名> に変更
- 旧形式の端末設定（data_root が1つ）は読み込み時に data_roots へ移す。データルート側の config.json に owner が無い場合は、手で足す

## 設計メモに無い判断

- マージコミットの作者は表示名、メールは `<ログオン名>@github-ppoi.invalid`
- /api/v1/version の Gitea の版は言語ファイルと同じ 28.1.0 を名乗る。本ツールの版は github_ppoi_version
- イベントの labels と assignees は差分でなく変更後の全体を持つ
- ツリーでファイルごとの最終コミットは出さない（SMB 越しの git log の回数を抑える）
- 自分の PR は承認・変更要請できない。承認必須の判定は人ごとの最新状態
- Revert は1回まで

## 残作業

- 添付ファイル（Ctrl+V 貼り付け）
- 使用 OSS のライセンス一覧ページ
- Gitea 互換 API（今は /api/v1/version のみ）
- ローカルキャッシュ（SMB 検証の前までに入れる）
- SMB 上の同時更新の検証
- bin\ の exe の版の比較と更新通知
- GitHub Actions のビルドの確認と VERSIONINFO の埋め込み
