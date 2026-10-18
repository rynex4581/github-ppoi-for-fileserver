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

## ダウンロード

[Releases](https://github.com/rynex4581/github-ppoi-for-fileserver/releases/latest) から `github-ppoi-<版>.7z` を落として展開する。配置と初回起動は [Getting started](#-getting-started) を参照。

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
- 🐞 **Issue、Pull Request、Code、Releases。** レビューと行コメント、`--no-ff` のマージと Revert、承認必須の設定、ファイルツリーと差分、タグに紐づくリリース
- 🏷️ **ラベル、添付ファイル、横断ページ、新着の印。** 全 owner の Issue と PR を1つの一覧で検索。自分が関わる Issue に他の人の書き込みが増えていれば印が付く
- 🤖 **Gitea API v1 互換の API。** tea（Gitea 公式 CLI）や Codex からそのまま使用可能（筆者の環境はCodexのみ）
- 🌐 **日本語と英語。** 画面の文言は互換性のため Gitea の言語ファイルをそのまま使用する
- 📁 **複数のデータルートを1つの画面で扱える。** データルート1つが owner 1つに対応し、URL は `/<owner>/<リポジトリ名>/`

## <img src="docs/images/icon-rocket.svg" width="24" height="24" align="top" alt=""> Getting started

### インストール

要 Git for Windows 2.38 以上。事前にインストールしておくこと。<BR>
`github-ppoi.exe` を端末の任意のフォルダに配置する。レジストリは使っていない。<BR>
データルートの `bin\` に置くと、古い exe を使っている端末に更新の通知が出力される（確認は起動時に1回）。<br>

> [!IMPORTANT]
> 共有フォルダへの書き込み権限は、信頼できるメンバーに限定すること。
> - 本ツールは共有フォルダ上のリポジトリを `safe.directory=*` で開く。リポジトリの設定ファイルにコマンドを仕込まれた場合、そのリポジトリを開いた利用者の端末で実行される
> - ログオン名は環境変数 `USERNAME` の値をそのまま使用し、検証しない。承認必須の設定も、なりすましを防ぐものではない
>
> 詳細は [docs/運用.md](docs/運用.md) を参照。

### 初回起動

起動するとブラウザに設定画面が開く。データルートにするフォルダ（共有フォルダ）を「参照…」で選ぶか、パスを直接入力し、owner 名と合わせて登録する。

- owner 名は URL に使うので半角英数（`.` `_` `-` 可）。表示名は任意。日本語可
- `config.json` の無いフォルダを指定すると、そのフォルダをデータルートとして初期化する。既にデータルートになっているフォルダなら登録だけを行う
- 設定は `%LOCALAPPDATA%\github-ppoi\config.json` に保存される
- 2つ目以降のデータルートは、画面右上の歯車の「自分の設定」から追加する

> [!TIP]
> 待ち受けポートは 9418 から 9427 の範囲で空いているものを使用する。使用中のポートは画面のフッターに表示し、`%LOCALAPPDATA%\github-ppoi\port.txt` にも書き出す。<br>
> ~~こんなツール使う職場はどうせgitのポートである9418なんて使ってるわけないからこっちで使うね☆ﾐ~~

### ソースからのビルド

要 Go 1.27 以上と Git for Windows

```powershell
go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0
go generate ./cmd/github-ppoi
go build -o github-ppoi.exe ./cmd/github-ppoi
.\github-ppoi.exe
```

`go generate` は exe のプロパティに出すバージョン（VERSIONINFO）のリソースを作成する。省いてもビルドは通るが、更新通知の比較ができない。

## Codex／AI エージェントからの利用

`localhost:<ポート>/api/v1/` に Gitea API v1 と同じ形のエンドポイントがあるので、Gitea に対応したツールやエージェントはそのまま使用可能。トークンは空でなければ何でも通る（`Authorization: token x`）。<BR>
内輪のバージョン・履歴管理として使うものなので原則として性善説で。~~そういうの気にする人はちゃんとしたサーバを使いましょう。~~

Codex から使用する場合は、対象のリポジトリの AGENTS.md に次のように記述する。

> Issue と Pull Request は localhost の Gitea 互換 API で扱う。
> ポートは `%LOCALAPPDATA%\github-ppoi\port.txt` に書いてある。
> URL は `http://localhost:<ポート>/api/v1/repos/<owner>/<リポジトリ名>/...` で、
> Authorization ヘッダは `token dummy` のように空でない値を付ける。

実装済の API の一覧は [docs/API.md](docs/API.md) を参照。

## <img src="docs/images/icon-browser.svg" width="24" height="24" align="top" alt=""> Web 画面

Issue の詳細。本文とコメントは Markdown で記述でき、右側で担当者とラベルを変更可能。

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
- Issue番号は、番号のフォルダを「既に存在すれば失敗する」方式で作成し、作成できた側がその番号を取得する。SMB上の排他制御はこの1点のみ
- イベントのファイル名は「UTC時刻（ミリ秒）_端末名_PID_端末内連番」とし、端末間でもプロセス間でも重複しないようにする
- ファイルは一時名で書いてからrenameし、書きかけが他の端末から見えないようにする
- 画面はGitHubの構成と導線を~~パク~~リスペクトしオマージュする。 CSSはPrimer、アイコンはOcticonsを使い、すべてexeに埋め込む。外部への参照は持たない
- 画面の文言はGiteaの言語ファイルを~~パク~~互換性のためそのまま使用する。ほら用語とか統一したいし。既定は日本語で、英語に変更可能
- gitは各端末のGit for Windowsを使用する。exeには同梱しない
- コメントの削除と本文の編集も追記で行う。削除で画面から消えるのは本文だけで、添付ファイルは残り URL でも取得可能。編集前の本文は「編集済み」を開けば誰でも読める（GitHubと同じ）
- 他の端末が書いたものが表示されるようになるまで10秒程度かかる。Windows の SMB クライアントがフォルダの一覧を既定で10秒キャッシュするため

## ドキュメント

- [docs/運用.md](docs/運用.md)：データルートの構成、書き込みの種類、設定、バックアップと復元、インポートとエクスポート、Troubleshooting
- [docs/API.md](docs/API.md)：実装済の Gitea 互換 API
- [docs/設計メモ.md](docs/設計メモ.md)：設計の決定事項

## 現在の状態

Ver 0.20。開発中

未実装

- SMB 上の同時更新の検証。本体と同じ採番と書き込みを2台から同時に回す `smbtest.exe`（`go build ./cmd/smbtest`）を用意している。2台で `.\smbtest.exe -root <共有フォルダ>\smbtest -n 1000` を同時に実行し、終わってから `-check` を1台で実行すると、番号の重複、書きかけの読み込み、.tmp の残留、見えるまでの遅れを合格基準（各1000件、重複ゼロ、書きかけゼロ、30秒以内）で判定する
- マイルストーン、Wiki、通知など、[docs/API.md](docs/API.md) に無い Gitea API の範囲

リリースは GitHub Actions で行う。`v0.10` のようにタグを打つと、Windows 用の exe をビルドし、`github-ppoi-v0.10.7z` の形で Releases に置く。タグは `internal/version` の Number と一致していないと失敗する。README の画像は `go run ./tools/screenshot` で撮り直せる（端末の Chrome、Iron、Edge のいずれかを使う）。

## ライセンス

MIT License。同梱しているOSS（Primer CSS、Octicons、Giteaの言語ファイル、goldmark、Go の標準ライブラリ）のライセンスは [third_party/](third_party/) を参照。
