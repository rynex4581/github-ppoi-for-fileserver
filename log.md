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

### 外部レビュー（2回目）への対応

- 新1：読み取り専用（format が exe より新しい）のデータルートでは、マージ・Revert・Settings の保存・メンテナンスも先頭で止める。画面のボタンも出さない
- 新4：削除した行コメントを reviews/{id}/comments の API でも出さない
- 新8：attachment_max_bytes の無い config.json でも既定の 10MB を使う
- 新6・新7：smbtest の走査の打ち切り条件が逆だったのを直し、合格判定に「各 1000 件」と「採番の失敗 0 回」を入れた
- 設計メモ 4 に、1人1VMが必要な理由、DFS レプリケーション不可、FSRM のファイルスクリーン、監査ログ、バックアップ、端末の時刻同期を追記
- 新5：.tmp の掃除を「同じフォルダの1時間以上前の *.tmp」にし、events の一覧を取るときと上書きファイルを書くときだけ行う
- 新10：API の updated_at は編集されていればその時刻
- 新13：プレビューで送信前の添付画像を Blob URL で見せる（CSP の img-src に blob: を追加）
- README に「バックアップと復元」「書き込みの種類」の章、削除で残るもの、端末の写し、承認は記録、hooks を絞る手段、見えるまでの遅れ（横断ページにも一文）
- 運2(b)：runGit に -c core.hooksPath= を足した。ローカルの push では git が設定の環境変数を落としてから receive-pack を起動するので -c だけでは効かず、push には --receive-pack="git -c core.hooksPath= receive-pack" を付ける。テストで確かめた
- 新2：approvalStillValid と stripIndexLines のテスト。判定結果は (base の SHA, 承認 SHA, head SHA) をキーにプロセス内で覚える
- 新11〜新17：README のファイル名の説明、PATCH の本文のコメント、labels の 415、二重削除は 404、ロックの I/O エラーは 409 にしない、編集画面は本文を先に書く

### 外部レビュー2回目の後回し分と設計メモの追加分（3.6、3.8、3.10、3.17）

- 新3：タイトルの変更も作成者本人だけ。setTitle の中で確かめるので画面と API で同じ
- 運9：「ファイルを添付」で画像以外も添付できる。印は画像なら ![名前](attachment:N)、それ以外は [名前](attachment:N)。編集画面の保存でも添付を保存する
- 運11：横断ページに検索欄。タイトル・本文・コメントの部分一致、全 owner をまたぐ
- 運10：新着の印。前回開いた位置（一番新しいイベントのファイル名）を %LOCALAPPDATA%\github-ppoi\seen.json に持つ
- 3.17：reserve と check のサブコマンド（check の本体は internal/integrity）

### リリースとインポート／エクスポート（3.18、3.16）

- 3.18：リリースを meta\<リポジトリ名>\releases\<タグ名>\ にイベント追記方式で持ち、配布物は attachments\ に置く。画面は一覧・詳細・新規・編集・削除・ダウンロードとタグ一覧（/tags）。Code 画面の見出しにタグ数とリリース数。API は Gitea の /releases と /tags に合わせた
- 3.16 export：Gitea v28.1.0 の dump-repo 形式で書き出す。WSL の Ubuntu に Gitea 28.1.0 を立て、restore-repo --validation で取り込めることをテスト（GITEA_BIN を与えたときだけ動く）で確認した
- 3.16 import：dump-repo 形式を取り込む。GitHub の本リポジトリを gitea dump-repo --git_service github で取った実物（ラベル 10、リリース 1 と添付 2、タグ 1、98 コミット）を取り込み、check が問題 0 件で、画面に出ることを確認した
- 3.16 export-github：GitHub の API を模したサーバと手元の bare に対するテストで確認した。本物の GitHub に対しては未実施
- git に core.longpaths=true を付けた（深いパスのデータルートで import の push が 260 文字制限に掛かった）

### 低9（ラベル・担当者の同時変更）

- 詳細画面のラベルと担当者のフォームに、開いた時点の最後の変更イベントのファイル名を hidden で持たせる。保存時に今のものと違えば 409 で「他の人が変更している。読み直してから適用」と出して書かない。データ形式と API は変えていない

### README の整理

- README は入口（インストール、背景、Features、Getting started、Codex との使い方、画面、設計の要点、現在の状態）だけにした。データルートの構成、書き込みの種類、設定、バックアップと復元、インポートとエクスポート、Troubleshooting は docs/運用.md に、API の一覧は docs/API.md に移した。IMPORTANT は 2 点に絞り、hooks の絞り方と監査ログの話は docs/運用.md のセキュリティ上の前提に移した
- 直訳調の文を直した（「前回開いた」は端末側に持つ、要らない、流し込める、叩く、家で実行する など）

### Ver 0.20

- 外部レビュー 2 回の対応、Releases、検索、新着の印、ファイル添付、reserve／check、import／export／export-github、低9、README の整理を含めて Ver 0.20 とした。タグ v0.20 で release を通す
- Releases には exe を直接置かず、`github-ppoi-<タグ>.7z`（github-ppoi.exe と LICENSE）を置く。smbtest.exe は同梱せず、`go build ./cmd/smbtest` で作る
- アイコンは docs/logo/icon.svg（ロゴの黄色いフォルダを正方形いっぱいに広げ、表に GitHub の文字、その上に「っぽい」を斜めに重ねる）。Edge の headless で 512px に描画して favicon.ico（256／48／32／16）にし、internal/web/static に置く。画面の favicon と exe のアイコン（versioninfo.json の IconPath）の両方に使う。横長のロゴをそのまま縮めると 16px で何も見えないため

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
- API の PATCH で本文を書き換えると edited イベントを追記する（画面の編集と同じ）。tea が空のタイトルを送ってきたときは変更なしとして扱う
- レビューと行コメントは紐付けて保存していないので、reviews/{id}/comments は「前のレビュー以降そのレビューまでに付いた行コメント」を返す
- 「新しいpushで承認を無効にする」（中3）は Gitea のやり方に合わせた。承認したコミットが今の head と同じか、承認した時点と今とで PR の差分の中身が同じなら有効。設計メモ 3.3 も同じ文にした
- 本文とコメントの編集・削除は書いた本人だけ（GitHub と同じ）。編集前の本文はファイルに残り、画面でも畳んで見られる
- 画面の CSP は img-src だけ外部と blob: を許す（Markdown に外部の画像 URL が書かれることがあるため。blob: はプレビューで送信前の添付を見せるため）
- ボディの上限は添付の上限×10＋1MB。raw で返すファイルは 64MB まで
- 二重起動の排他は %LOCALAPPDATA%\github-ppoi\instance.lock を共有無しで開いて握る。マージと Revert の排他は repos\<owner>\<リポジトリ名>.lock（10分より古ければ残骸として消す）
- 一時ファイルの残骸は同じフォルダの1時間より古い *.tmp を、events の一覧を取るときと上書きファイルを書いたときに消す
- 本ツールが呼ぶ git は hooks を動かさない（core.hooksPath を空にする）。push 先の receive-pack には -c が渡らないので、--receive-pack で同じ設定を付けて起動させる
- 承認がまだ有効かの判定（git diff 2回）は (base の SHA, 承認 SHA, head SHA) をキーにプロセス内で覚える。git が失敗したときの結果は覚えない
- 新着の印は「前回開いた位置より新しい他人のイベント」で判定する。一度も開いていない Issue は、他人のイベントが1つでもあれば印を付ける。位置はイベントのファイル名（UTC 時刻で始まる）の比較なので、端末の時計のずれの範囲で取りこぼし得る
- check は head_sha と commit_sha が無いことも報告する（force push で消えるのは正常なので、その旨を添える）。git fsck は --no-dangling
- リリースのタグ名はフォルダ名になるので、英数字と . _ - + だけに限る（/ を含むタグにはリリースを付けられない）。削除は deleted の追記で、タグと添付のファイルは残る。削除後に同じタグで作り直せる
- リリースの編集と削除は誰でもできる（Issue の本文と違い、GitHub でも書き込み権があれば誰でも編集できるため）
- リリースの本文には Ctrl+V の添付を付けず、配布物のファイル選択だけにした
- dump-repo の export では、Gitea の restore が PR のコメントとレビューを foreignindex で引くため pull_request.yml に番号を入れる。issue.yml はスキーマ（additionalProperties: false）に無い項目を書かない
- dump-repo の import では、マージした人が dump に無いので merged イベントの user は PR の作成者にする。コメント種別は comment／close／reopen／change_title だけ取り込み、他は読み飛ばして表示する
- export-github では open の PR は head のブランチが push 済みなら PR として作る（設計メモは「マージ済み PR は Issue として作る」とだけ決めている）
- 添付の書き出し名は保存名の先頭の時刻（UTC時刻_）を外した元の名前にする
- 低9 の比較は、イベント全体の末尾ではなく、ラベルはラベルの、担当者は担当者の最後の変更イベント名で行う（末尾で比べると、無関係なコメントが 1 つ付いただけで弾かれるため）。hidden が無い送信も古い画面として弾く
- API の issues/comments/{id} は ServeMux のパターンが issues/{index}/... と衝突するため、Handler の前段で正規表現で振り分ける
- 撮影スクリプトの依存（chromedp）は go.mod に入るが、本体の exe にはリンクされないので third_party の一覧には入れない
- 撮影中は LOCALAPPDATA を一時フォルダに向け、端末側の設定とキャッシュに触らない
- 添付は貼った時点では送らず、フォーム送信時に一緒に送る（作成前の Issue に番号が無いため）。本文中の参照は Gitea と同じ /attachments/ で始め、その後ろに owner、リポジトリ名、番号、ファイル名を続ける

## 残作業

- SMB 上の同時更新の検証（家の2台と NAS で smbtest を回す）
- 外部レビューの後回し分：低14 と 低18（SMB 検証の結果を見てから）
- export-github を本物の GitHub に対して一度試す（テストは模したサーバだけ）
