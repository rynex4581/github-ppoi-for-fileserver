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

### 添付ファイルとライセンス一覧ページ

- 本文の入力欄に画像を Ctrl+V で貼ると、本文に `![image](attachment:1)` の印が入り、フォーム送信時に画像を一緒に送る。サーバ側で issues\<番号>\attachments\ に保存し、印を `/attachments/<owner>/<リポジトリ名>/<番号>/<ファイル名>` に置き換える
- Issue と PR の作成、コメント、レビュー、行コメントの全部で使える。上限は各データルートの config.json の attachment_max_bytes（既定 10MB）
- /licenses に同梱している OSS（Go、Primer CSS、Octicons、Gitea の言語ファイル、goldmark）のライセンス文を出す。フッターからたどれる

### Gitea 互換 API

- 画面のハンドラから保存の手順を ops.go に出し、画面と API の両方から同じ関数を呼ぶ形にした（HTTP を二重に通さない）
- /api/v1 配下に user、repos、branches、commits、contents、compare、issues、comments、labels、assets、pulls、diff、files、merge、reviews を Gitea の JSON の形で用意した
- tea v0.16.0 で確認。tea が必ず呼ぶ org のラベル、ブランチ保護、コミットの status、reactions などは空で返す
- 認証はトークンが空でなければ通す。version だけ認証無し
- バージョン表記の規則を設計メモ 3.13 の新しい文面に合わせ、CLAUDE.md と AGENTS.md を書き換えた

### ローカルキャッシュと SMB 検証の準備

- イベントのファイルは追記だけで書き換えないので、ファイル名ごとに中身を覚える。画面を出すたびに events フォルダの一覧だけを見て、知らないファイルだけ読み足す。自分が書いたイベントはその場でキャッシュに入れる
- キャッシュは %LOCALAPPDATA%\github-ppoi\cache\<owner>\<リポジトリ名>.json にも書き（5秒に1回まで）、起動時に読む。消えても次の表示で読み直すだけ
- SMB 検証用に cmd/smbtest を作った。本体と同じ採番と書き込みのコードで、2台から同時に回して -check で突き合わせる。手元の2プロセスで 600 件を回し、番号の重複なし、読めないファイルなし（この時点の道具では書きかけは測れていない。後の中11 で直した）、見えるまで最大 0.7 秒
- SMB の認証はツールでは扱わない。データルートを普通のファイルパスとして開くので、Windows にログオンしている人の資格情報がそのまま使われる。別の資格情報が要る共有は先に net use でつなぐ

### VERSIONINFO、更新通知、Actions

- internal/version に表記の解析と比較（0.11A → 0.11.1.0 など）を入れた。go generate で goversioninfo（MIT、ビルド時だけ）を呼び、exe に VERSIONINFO を埋め込む
- 起動時に各データルートの bin\github-ppoi.exe のリソースを自前で読み（Windows API は使わない）、自分より新しければ画面の上部に版とパスを出す
- Actions は ci（push ごとに検査、テスト、ビルド、版の確認）と release（タグ v* でビルドして Releases に置く。タグと Number が違えば失敗）
- タグ v0.10 を打って release を通し、Releases に github-ppoi.exe と smbtest.exe が置かれた。落とした exe の版は Ver 0.10（0.10.0.0）

### README の作り直し

- README を Backlog.md の README と同じ見せ方に組み替えた。ロゴ、キャッチコピー、バッジ、インストールの1行、Features、Getting started、Codex と使う、Web 画面、設定の表、Troubleshooting の順
- ロゴは docs/logo に SVG で3案（共有フォルダと Issue を組み合わせたもの。背景は透過、線は中間色）。README には仮に1案目を入れた
- ロゴは「GitHub」の文字に「っぽい」を斜めに重ね、黄色いフォルダを背景にしたものに決まった（docs/logo/logo-wordmark）。本物のロゴタイプと Windows の画像は使わず、文字は Segoe UI Black、「っぽい」は Noto Sans JP Black で描いた。README には書体の無い環境でも同じに見えるよう PNG を載せ、SVG は元データとして置く
- 画面の画像は tools/screenshot で撮る。一時フォルダに架空のデータルート（リポジトリ2つ、Issue 6件、Pull Request 1件）を作り、本体と同じサーバを上げて、端末の Chromium 系ブラウザ（Chrome、Iron、Edge の順に探す）で4画面を撮って docs/images に置く。`go run ./tools/screenshot` で撮り直せる

### 外部レビュー（v0.10）への対応

docs_local/レビュー指摘_v0.10.txt の「直す順番の案」に従い、指摘の番号ごとにコミットを分けた。

- 高1、高2：画面と API を http.CrossOriginProtection で包み、別サイトからの POST を 403 にした。HTML に X-Frame-Options と CSP。API はトークンを Authorization ヘッダだけで受け、JSON は Content-Type を要求、マージの Do は merge 必須
- 中4、中15：一時名を「本来の名前.端末名-PID-連番.tmp」にし、1時間より古い残骸を掃除。イベント名に PID を足し、イベントと添付の rename は MoveFileW（置き換え無し）
- 中11：smbtest を 3.14 の合格基準に合わせた。events を直接走査して「読めず→読めた」を数え、-check で .tmp の残留と読めない JSON も数えて判定する。os.Rename の置き換えとサーバの時計との差も記録する
- 中7、中12、中13：PR 作成の検証を validatePull に分けて画面と API で共通に。承認と変更要請は open 以外で 405（コメントは可）。API のレビューは検査を先に通す
- 中9、中10：添付の拡張子は英数字だけ。リポジトリ配下の書き込みは MaxBytesReader で上限（添付上限×10＋1MB）
- 中6、中5：マージと Revert はリポジトリごとに Mutex と .lock ファイルで排他。push 後に記録に失敗しても、次にボタンを押せば base 上のマージコミット（Revert は打ち消しコミット）を探して記録だけやり直す
- 中1、中8、中14、中2：git は LC_ALL=C、diff.external と log.showSignature を無効、diff は --no-ext-diff、外から来た引数は --end-of-options の後ろ。キャッシュに format。README に信頼の前提
- 中3：リポジトリ設定「新しいpushで承認を無効にする」。承認時の CommitSHA が今の head と一致するか祖先である承認だけ数える
- 低：指定の14件。他の open PR の base は消さない、refspec、autogc 無効と「メンテナンス実行」、既定ブランチの存在確認、ログオン名の比較は大文字小文字を区別しない、force push 禁止の性格を文言に、instance.lock で二重起動を防ぐ、raw の上限と UTF-16、Windows の予約名、添付失敗の後始末、X-Total-Count、raw の API の ref 検証、Actions の permissions と env
- 軽1〜10：マージした人の表示、文言の欠け、SHA の短縮、/static/ の一覧拒否と Content-Type、作者のリンク、git の版の明記、CSP とインラインの排除、コメントと文書の食い違い、サーバのタイムアウト
- 追加：config.json の format（知らない版は読み取り専用で開いて帯を出す）、edited／deleted（本文とコメントの編集・削除、履歴は畳んで表示、API は PATCH body と comments の GET／PATCH／DELETE）、Markdown のプレビュー（POST /markdown）、README の GitHub, Inc. と無関係の一文、README と Settings に信頼の前提とログオン名の自己申告

### 後回し分の対応

- 低16：自分が書いた直後のイベントは、フォルダの一覧にまだ出てこなくても60秒は捨てない（SMB の一覧の遅れ対策）。API は書いた直後に読み戻せなくても落ちない
- 低7、低15、低19：ラベルの色は6桁の16進だけ style に入れる。Ctrl+C やコンソールを閉じたときにキャッシュを書き出す。マージメッセージの欄は画像の貼り付けを受けない
- 低6：config.json は既存があれば失敗する書き方にし、2台同時の初期化は「やり直せば済む」と分かる文にした
- 低23：README に「bin の更新は起動時にしか見ない」。goversioninfo が出した exe を peversion が読めることをテストで確かめる（CI は goversioninfo を入れてから test を回す）
- 低12：Revert は承認に関係なく push すること、行コメントはコミット単位で Outdated にすることを設計メモ 3.2 と 3.4 に書いた
- 本文とコメントの編集・削除は書いた本人だけにした

## 設計メモに無い判断

- マージコミットの作者は表示名、メールは `<ログオン名>@github-ppoi.invalid`
- /api/v1/version の Gitea の版は言語ファイルと同じ 28.1.0 を名乗る。本ツールの版は github_ppoi_version
- イベントの labels と assignees は差分でなく変更後の全体を持つ
- ツリーでファイルごとの最終コミットは出さない（SMB 越しの git log の回数を抑える）
- 自分の PR は承認・変更要請できない。承認必須の判定は人ごとの最新状態
- Revert は1回まで
- VERSIONINFO の埋め込みは goversioninfo を go generate から呼ぶ形。リソースは go generate で作り、リポジトリには入れない
- 更新通知は起動時に1回だけ見る
- API の数値 ID は Gitea のような連番を持たないため、Issue と PR は番号、コメントとレビューはイベントのファイル名、リポジトリと利用者は名前のハッシュで作る
- API の PATCH で本文は書き換えられない（本文を書き換えるイベントが無い）。tea が空のタイトルを送ってきたときは変更なしとして扱う
- レビューと行コメントは紐付けて保存していないので、reviews/{id}/comments は「前のレビュー以降そのレビューまでに付いた行コメント」を返す
- 承認の鮮度（中3）は、指示のとおり「承認時の head と一致、または head の祖先」を有効とした。設計メモ 3.3 は「head が進んだら数えない」だったので、文を指示に合わせて書き換えた。GitHub と同じにするなら「一致」だけにする
- 本文とコメントの編集・削除は書いた本人だけ（GitHub と同じ）。編集前の本文はファイルに残り、画面でも畳んで見られる
- 画面の CSP は img-src だけ外部を許す（Markdown に外部の画像 URL が書かれることがあるため）
- ボディの上限は添付の上限×10＋1MB。raw で返すファイルは 64MB まで
- 二重起動の排他は %LOCALAPPDATA%\github-ppoi\instance.lock を共有無しで開いて握る。マージと Revert の排他は repos\<owner>\<リポジトリ名>.lock（10分より古ければ残骸として消す）
- 一時ファイルの残骸は1時間より古いものを、同じ対象を書き終えたときに消す
- API の issues/comments/{id} は ServeMux のパターンが issues/{index}/... と衝突するため、Handler の前段で正規表現で振り分ける
- 撮影スクリプトの依存（chromedp）は go.mod に入るが、本体の exe にはリンクされないので third_party の一覧には入れない
- 撮影中は LOCALAPPDATA を一時フォルダに向け、端末側の設定とキャッシュに触らない
- 添付は貼った時点では送らず、フォーム送信時に一緒に送る（作成前の Issue に番号が無いため）。本文中の参照は Gitea と同じ /attachments/ で始め、その後ろに owner、リポジトリ名、番号、ファイル名を続ける

## 残作業

- SMB 上の同時更新の検証（家の2台と NAS で smbtest を回す）
- 外部レビューの後回し分：低9（別途指示）、低14 と 低18（SMB 検証の結果を見てから）
