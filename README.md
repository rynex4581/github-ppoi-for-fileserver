<p align="center">
  <img src="docs/logo/logo-wordmark.png" width="420" height="230" alt="">
</p>

<h1 align="center">GitHub-ppoi for Fileserver</h1>

<p align="center"><strong>えっ、共有ファイルサーバでIssueもコードレビューもPRもGitHubっぽいWebUIから！？</strong></p>

<p align="center">A GitHub-like issue tracker and code review tool that runs on each client PC and keeps all of its data on a plain SMB file server. No server, no database.</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/rynex4581/github-ppoi-for-fileserver" alt="License"></a>
  <img src="https://img.shields.io/github/go-mod/go-version/rynex4581/github-ppoi-for-fileserver" alt="Go version">
  <a href="https://github.com/rynex4581/github-ppoi-for-fileserver/releases/latest"><img src="https://img.shields.io/github/v/release/rynex4581/github-ppoi-for-fileserver" alt="Release"></a>
  <a href="https://github.com/rynex4581/github-ppoi-for-fileserver/stargazers"><img src="https://img.shields.io/github/stars/rynex4581/github-ppoi-for-fileserver?style=social" alt="GitHub stars"></a>
</p>

Githubっぽいのをファイルサーバでやるやつ。

## インストール

[Releases](https://github.com/rynex4581/github-ppoi-for-fileserver/releases/latest) から `github-ppoi.exe` を落として起動する。事前にGit for Windowsをインストールしておくこと。

<p align="center">
  <img src="docs/images/top.png" width="800" alt="トップ画面">
</p>

## 背景

筆者の職場の端末は外部からの接続を受け付けないWindowsの仮想デスクトップで、端末上でサーバを動かしても他の端末からは使えない。使用可能な共有はファイルサーバ（SMB）だけである。<br>
仮想サーバは申請しているが~~予算の関係で~~割り当てられず、GitHubは職場ではDownloadはOKだが内製ツールのリポジトリ置き場としては~~グレーな扱いで~~使えない。

一方で、職場では業務自動化ツールをOpenAI Codexで内製しており、Issueによる管理とコードレビューの仕組みが必要という要望が開発者からあった。<br>~~それなのにGitのリポジトリ置き場すらくれないとか意味わからない。~~<br>
Gitea程度の使い勝手で、ブラウザから直感的に使えることが条件であった。

サーバ不要でIssueを扱えるツールとしてBacklog.mdを検証したが、pullとpushを利用者が意識する必要があり、職場のメンバーには使わせられないと判断した。次にGiteaを各端末で起動し、SQLiteのデータベースとリポジトリを共有フォルダに置く形を検証した。動作はしたが、SQLiteはネットワーク上のデータベースファイルを複数のマシンから使うことを公式に推奨しておらず、Giteaも複数インスタンスではデータベースサーバとRedisを求めている。<br>
公式の想定から異なる形で職場に入れることはできないと判断した。~~よくわからないしねｗ~~

そこで、各自の端末で動き、ファイルサーバをデータ置き場とし、Gitea API互換のAPIを持つツールを自作することにした。<br>
Gitea互換にしたのは、CodexにGiteaのAPIを使わせる形でIssueの読み書きをさせるためである。

本ツールの設計と実装は、Claude（Fable 5.1）とClaude Codeで行った。

## Features

- 🗂️ **サーバもデータベースも使わない。** データの原本はすべて共有フォルダ（データルート）にあり、端末側の exe、設定、キャッシュはあくまでクローン
- 📁 **複数のデータルートを1つの画面で扱える。** データルート1つが owner 1つに対応し、URL は `/<owner>/<リポジトリ名>/`
- 🐞 **Issue。** 状態・ラベル・担当者での絞り込み、コメント、クローズと再オープン、ラベルと担当者の変更、本文とコメントの編集・削除（編集前の本文は畳んで表示）
- 🔀 **Pull Request。** ブランチを選んで作成、Conversation／Commits／Files changed、承認・変更要請・コメントのレビュー、差分への行コメント、`--no-ff` のマージと Revert、コンフリクトと force push の表示、承認必須の設定
- 📄 **Code。** ファイルツリー、Markdown と画像の表示、ブランチ切替、コミット履歴と差分、clone コマンドの表示
- 🏷️ **ラベルの管理**と 📎 **添付ファイル**（本文の入力欄に Ctrl+V で画像を貼り付け）
- 🔍 **横断ページ。** 全 owner の Issue と Pull Request を1つの一覧で見る（すべて／自分に割り当て／自分が作成）
- 🤖 **Gitea API v1 互換の API。** tea（Gitea 公式 CLI）や Codex からそのまま使用可能（筆者の環境はCodexのみ）
- ⚡ **イベントのローカルキャッシュ。** 2回目以降の表示では新しいファイルだけを共有フォルダから読みこむ
- 🌐 **日本語と英語。** 画面の文言は互換性のため Gitea の言語ファイルをそのまま使用する
- 🔔 **運用まわり。** 待ち受けポートの自動選択、二重起動の検知、データルートの `bin\` に置いた新しい exe の通知

## <img src="docs/images/icon-rocket.svg" width="24" height="24" align="top" alt=""> Getting started

### exe をローカル端末に配置

`github-ppoi.exe` はローカル端末のどこに置いてもよい。配布用にはデータルートの `bin\` に置くと、古いバージョンを使っている端末の画面に「新しいバージョンがある」と出力される。この確認は起動時に1回だけで、起動中に `bin\` が更新されても気付かない。<br>
Git for Windows が入っていること（`git` がコマンドラインから呼べること）が前提になる。2.38 以上（`merge-tree --write-tree` を使う）。

> [!IMPORTANT]
> データルートに書ける人は全員、互いの端末で git のコマンドを実行できる関係にあることが前提になる。本ツールは共有フォルダ上のリポジトリを `safe.directory=*` 付きで開き、各自の clone でも同じ設定を勧めるためで、リポジトリの設定ファイルに書かれたコマンドは開いた人の端末で動く。緩和として、本ツールが読み取りに使う git は `diff.external` と `log.showSignature` を無効にして呼ぶ。信頼できるメンバーだけが書ける共有フォルダに置くこと。
> ログオン名は環境変数 `USERNAME` の自己申告で、ツールは検証しない。承認は記録であって統制ではない。共有フォルダに書ける人は他人名義のイベントも書けるので、承認必須の設定を統制の証跡として扱うなら、裏付けは NTFS のファイル所有者（`dir /q` で見える作成者）とファイルサーバの監査ログに求めること。<br>
> ベアリポジトリの `hooks\` に置かれたスクリプトは push した人の端末で動く。絞るなら、`hooks\` を NTFS で利用者には書けなくする（本ツールは hooks を使わない）。利用者自身の git push には、Git for Windows のシステム設定で `core.hooksPath` を空のフォルダに向けておけば塞げる。

### 初回の起動

起動するとブラウザに設定画面が開く。データルートにするフォルダ（共有フォルダのパス）と owner 名を入れて登録する。

- owner 名は URL に使うので半角英数（`.` `_` `-` 可）。表示名は任意で、日本語でよい
- `config.json` の無いフォルダを指定すると、そのフォルダをデータルートとして初期化する。既にデータルートになっているフォルダなら登録だけを行う
- 設定は `%LOCALAPPDATA%\github-ppoi\config.json` に保存される

### 2つ目以降のデータルート

画面右上の歯車から「自分の設定」を開き、データルートを追加する。1つのデータルートが owner 1つに対応し、同じ owner 名のデータルートは2つ登録できない。トップ画面には owner ごとにリポジトリが並ぶ。

> [!TIP]
> 待ち受けポートは 9418 から 9427 の範囲で空いているものを使う。上がったポートは画面のフッターと `%LOCALAPPDATA%\github-ppoi\port.txt` に表示、出力される。
> `config.json` に `"port"` を書けばそのポートだけを使い、使えなければエラーで停止する。
> 既に起動しているときにもう一度起動すると、ブラウザだけ開いて新しい方は終了する。`--no-browser` を付けると起動時にブラウザを開かない。<br>
> ~~こんなツール使う職場はどうせgitのポートである9418なんて使ってるわけないからこっちで使うね☆ﾐ~~

### ソースからビルドする

要 Go 1.27 以上と Git for Windows

```powershell
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0
go generate ./cmd/github-ppoi
go build -o github-ppoi.exe ./cmd/github-ppoi
.\github-ppoi.exe
```

`go generate` は exe のプロパティに出すバージョン（VERSIONINFO）のリソースを作成する。goversioninfo（MIT）はビルドのときだけ使い、exe には入らない。省くとビルドはできるが、exe にバージョンが入らず、更新通知の比較もできない。

## Codex／AI エージェントと使う

`localhost:<ポート>/api/v1/` に Gitea API v1 と同じ形のエンドポイントがあるので、Gitea に対応したツールやエージェントはそのまま使用可能。ポートは `%LOCALAPPDATA%\github-ppoi\port.txt` に記載済み。トークンは空でなければ何でも通る（`Authorization: token x`）。URL のクエリ（`?token=`）は不可。JSON を送るときは `Content-Type: application/json` を付けること。`/api/v1/version` だけは認証無し。内輪のバージョン・履歴管理として使うものなので原則として性善説で。

実装済

- user、users/{login}、user/orgs、orgs/{org}、user/repos、repos/search、orgs/{org}/repos
- repos/{owner}/{repo}：本体、branches、commits、git/commits/{sha}（`.diff` 付きで差分）、contents、raw、compare/{base}...{head}
- issues：一覧（state、type、labels、q）、作成、取得、PATCH（title、body、state、assignees、labels）、comments（一覧、作成、取得、PATCH、DELETE）、labels、assets
- labels：一覧、作成、取得、PATCH、削除
- pulls：一覧、作成、取得、PATCH、`.diff`、commits、files、merge（`Do` は merge のみ）、reviews（APPROVED／REQUEST_CHANGES／COMMENT と行コメント）
- tea（Gitea 公式 CLI）の v0.16 で、ログイン、リポジトリ一覧、Issue の一覧・作成・表示・クローズ、コメント、ラベル、ブランチ、PR の一覧・作成・マージが通ることを確認済

予定（未実装）

- マイルストーン、Wiki、リリース、通知など、上に無い Gitea API の範囲

Codex に使わせるときは、対象のリポジトリの AGENTS.md に次のように記述する。

> Issue と Pull Request は localhost の Gitea 互換 API で扱う。
> ポートは `%LOCALAPPDATA%\github-ppoi\port.txt` に書いてある。
> URL は `http://localhost:<ポート>/api/v1/repos/<owner>/<リポジトリ名>/...` で、
> Authorization ヘッダは `token dummy` のように空でない値を付ける。

## <img src="docs/images/icon-browser.svg" width="24" height="24" align="top" alt=""> Web 画面

Issue の詳細。本文とコメントは Markdown で記述でき、右側で担当者とラベルを変更できる。

<p align="center">
  <img src="docs/images/issue.png" width="800" alt="Issue の詳細">
</p>

Pull Request の Files changed。差分の行にコメントを付けられる。

<p align="center">
  <img src="docs/images/pull-files.png" width="800" alt="Pull Request の Files changed">
</p>

横断ページ。全 owner、全リポジトリの Issue を1つの一覧で見る。

<p align="center">
  <img src="docs/images/cross-issues.png" width="800" alt="横断の Issue 一覧">
</p>

## 設計の要点

- Issue、コメント、状態変更はすべて「イベント1件につき1ファイル」の追記で保存し、既存ファイルの上書きは行わない。現在の状態はイベントを時系列に適用して構成、表示する
- Issue番号は、番号のフォルダを「既に存在すれば失敗する」方式で作成し、作成できた側がその番号を取る。SMB上の排他制御はこの1点のみ
- イベントのファイル名は「UTC時刻（ミリ秒）_端末名_端末内連番」とし、端末間で重複しないようにする
- ファイルは一時名で書いてからrenameし、書きかけが他の端末から見えないようにする
- 画面はGitHubの構成と導線を~~パク~~リスペクトしオマージュする。 CSSはPrimer、アイコンはOcticonsを使い、すべてexeに埋め込む。外部への参照は持たない
- 画面の文言はGiteaの言語ファイルを~~パク~~互換性のためそのまま使用する。ほら用語とか統一したいし。既定は日本語で、英語に変更可能
- gitは各端末のGit for Windowsを使用する。exeには同梱しない
- コメントの削除と本文の編集も追記で行う。削除で画面から消えるのは本文だけで、添付ファイルは残り URL でも取得可能。編集前の本文は「編集済み」を開けば誰でも読める（GitHubと同じ）
- 端末側の `%LOCALAPPDATA%\github-ppoi\` にあるキャッシュ（`cache\`）と専用 clone（`repos\`）に、Issue の全文と履歴の写しが残る。プロファイルを別の場所に写す仕組み（FSLogix 等）があれば、そこにも複製される
- 他の端末が書いたものが見えるまで10秒程度かかる。Windows の SMB クライアントがフォルダの一覧を既定で10秒キャッシュするため

## データルートの構成

```
<データルート>\
  config.json                  このデータルートの設定（owner 名、owner の表示名、既定のブランチ名、データ形式の版など）
  repos\
    <リポジトリ名>.git\        ベアリポジトリ
  meta\
    users\<ログオン名>.json    表示名
    <リポジトリ名>\
      repo.json                説明、既定のブランチ名、承認必須、新しいpushで承認を無効、force push禁止
      labels.json              ラベル
      issues\
        000012\
          events\              イベント（1件1ファイル）
          attachments\         添付ファイル
```

## 書き込みの種類

管理者が ACL を決めるときの材料。データルートへの書き込みは、追記だけのものと上書きするものに分かれる。

| パス | 種類 | 内容 |
| --- | --- | --- |
| `meta\<リポジトリ名>\issues\<番号>\` | 作成のみ | 採番。フォルダの作成に成功した側が番号を取る |
| `meta\<リポジトリ名>\issues\<番号>\events\*.json` | 追記のみ | イベント。既存ファイルの変更・削除は行わない |
| `meta\<リポジトリ名>\issues\<番号>\attachments\*` | 追記のみ | 添付ファイル |
| `meta\<リポジトリ名>\repo.json` | 上書き | Settings の保存 |
| `meta\<リポジトリ名>\labels.json` | 上書き | ラベルの変更 |
| `meta\users\<ログオン名>.json` | 上書き | 表示名の変更 |
| `config.json` | 作成のみ | データルートの初期化のときだけ。以後はファイルを直接編集する |
| `repos\<リポジトリ名>.git\` | git が管理 | push でオブジェクトの追加と refs の更新、Settings で `config` と `HEAD` の変更、メンテナンス実行で gc（パックの作り直し） |

上書きするファイルも、書くときは同じフォルダに `<名前>.<端末名>-<PID>-<連番>.tmp` を作ってから rename する。1時間以上前の `.tmp` は見つけ次第削除する。Issue の作成に失敗したときは作りかけの番号フォルダを削除する。このため「作成だけ許して削除を禁じる」ACL では `.tmp` の後始末と失敗時の片付けができず、git の gc も通らない。

## 設定

端末側 `%LOCALAPPDATA%\github-ppoi\config.json`

| 項目 | 内容 | 既定 |
| --- | --- | --- |
| `data_roots` | データルートのパスの一覧 | 無し（設定画面で登録する） |
| `port` | 待ち受けポート。書くとそのポートだけを使う | 9418〜9427 から空きを探す |
| `lang` | 画面の言語。`ja-JP` または `en-US` | `ja-JP` |

データルート直下の `config.json`

| 項目 | 内容 | 既定 |
| --- | --- | --- |
| `owner` | owner 名。URL に使う半角英数。データルートを作るときに決める | 無し（必須） |
| `owner_name` | owner の表示名。日本語可 | 空（owner 名を表示） |
| `default_branch` | 新規リポジトリの既定ブランチ名 | `main` |
| `attachment_max_bytes` | 添付ファイルのサイズ上限 | 10485760（10 MB） |
| `format` | データ形式の版。exe が知らない値なら、そのデータルートは読み取り専用で開く | 1 |

端末側の `data_roots` と `lang` は設定画面から変更可能。`port` とデータルート側の項目はファイルを直接編集する。端末側のファイルは削除しても、exe を起動してデータルートを登録し直せば元に戻る。

リポジトリの Settings にある「force push 禁止」はうっかり防止。receive-pack は push する本人の端末で動くので、本人が `-c receive.denyNonFastForwards=false` を付ければ通る。

## バックアップと復元

データルートはフォルダ1つで完結する。`config.json`、`repos\`、`meta\` が同じフォルダにあるので、ファイルサーバの既存のバックアップにそのまま含められる。Gitea のようにデータベースとリポジトリの整合を取る専用の手順は要らない。<br>
イベントは追記のみで、ファイルは一時名で書いてから rename するため、シャドウコピーのようなスナップショット方式で足りる。`.tmp` が混ざっても読み手は弾く。<br>
端末側の exe、設定、キャッシュ、専用 clone はあくまでクローンであり、バックアップの対象にしない。git のリポジトリは各自の clone と専用 clone にも写しがあるが、Issue のイベントと添付ファイルの原本は共有フォルダにしか無い。

**取り方**

- バックアップはファイルサーバの運用に含める。利用者の責任にしない。確認するのは、データルートのパスが対象に入っているか、保持期間、復元の所要時間と依頼先、`*.git` フォルダを除外していないか
- スナップショット方式（シャドウコピー等）を推奨する。ファイルを順に写す方式（robocopy やエージェントの差分コピー）はベアリポジトリを壊すことがある。`objects\` → `packed-refs` → `refs\` の順に写す途中で push が入ると、refs が指すオブジェクトの無い複製ができる
- `*.tmp` の除外は問題ない
- Settings の「メンテナンス実行」（gc）は、ファイルを順に写す方式のバックアップと同じ時間帯に行わない

**戻し方**

- 丸ごと戻した後は、各端末で `%LOCALAPPDATA%\github-ppoi` を削除してから起動し直す。端末のキャッシュは共有に無いファイルを捨てて自己修復するが、専用 clone はベアリポジトリより進んだ状態のまま残る
- Issue 番号が巻き戻る。採番は番号フォルダの有無だけで決まり、過去の最大番号はどこにも持たない。復元で最大番号が下がると次の採番が過去の番号を再利用し、メールやコミットメッセージに書いた `#57` が別の Issue を指す。復元後は、失われた最後の番号まで空の番号フォルダ（`issues\000057\`）を作って埋めてから使い始める（番号を予約するサブコマンドは予定）
- ベアリポジトリと `meta\` は同じ時点のスナップショットから戻す。部分的に戻すときは `meta\` をベアリポジトリより新しい時点にしない。ベアリポジトリが新しく `meta\` が古い（merged イベントだけ失われた）状態は、PR 画面を開けばマージ済みとして記録し直す。逆だと PR 画面がベアリポジトリに無いコミットを引く
- Issue 1件だけなら、「以前のバージョン」から `issues\000012\` のフォルダを戻すだけでよい

## Troubleshooting

**起動時に「ポート 9418〜9427 がすべて使用中」と出る**

他のプログラムがその範囲を使っている。`%LOCALAPPDATA%\github-ppoi\config.json` に `"port": 9500` のように空いているポートを書いて起動し直す。本ツールがもう上がっているだけなら、新しい方はブラウザを開いて終了するので、この表示にはならない。

**git が「dubious ownership」「safe.directory」の警告を出す**

共有フォルダ上のベアリポジトリは所有者が自分と違うため、Git for Windows が安全のために弾く。本ツールが git を呼ぶときは `safe.directory=*` を付けているので画面には出ないが、自分で clone や push をするときに出る。次を一度実行する。

```powershell
git config --global --add safe.directory "*"
```

`*` の代わりに特定のパスだけを許可してもよい。

**書いたのに他の端末で見えない**

Windows の SMB クライアントはフォルダの一覧を既定で10秒キャッシュする。他の端末が書いたイベントがこの端末の画面に出るまで、10秒程度かかることがある。少し待って再読み込みする。

## 現在の状態

Ver 0.10。開発中

未実装

- SMB 上の同時更新の検証。本体と同じ採番と書き込みを2台から同時に回す `smbtest.exe` を Releases に置いている。2台で `.\smbtest.exe -root <共有フォルダ>\smbtest -n 1000` を同時に実行し、終わってから `-check` を1台で実行すると、番号の重複、書きかけの読み込み、.tmp の残留、見えるまでの遅れを合格基準（各1000件、重複ゼロ、書きかけゼロ、30秒以内）で判定する
- Gitea API のうち、上の「予定（未実装）」に挙げたもの

リリースは GitHub Actions で行う。`v0.10` のようにタグを打つと、Windows 用の exe をビルドして Releases に置く。タグは `internal/version` の Number と一致していないと失敗する。README の画像は `go run ./tools/screenshot` で撮り直せる（端末の Chrome、Iron、Edge のいずれかを使う）。

## ライセンス

MIT License。同梱しているOSS（Primer CSS、Octicons、Giteaの言語ファイル、goldmark、Go の標準ライブラリ）のライセンスは [third_party/](third_party/) を参照。
