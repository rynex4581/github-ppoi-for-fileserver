# github-ppoi for Fileserver
## Githubっぽいのをファイルサーバでやる
A GitHub-like issue tracker and code review tool that runs on each client PC and keeps all of its data on a plain SMB file server. No server, no database.

Githubっぽいのをファイルサーバでやるやつ。

## 概要

- 各メンバーが自分の端末で `github-ppoi.exe` を起動すると、localhost（既定ポート 9418）にWeb画面とGitea API v1互換のAPIが上がり、ブラウザが開く
- データの正本はすべてファイルサーバ上の共有フォルダ（データルート）に置く。サーバもデータベースも使わない
- コードのgitリポジトリはデータルート配下のベアリポジトリで、各自が従来どおり直接push/pullする
- Issue、Pull Request、コメント、レビュー、添付ファイルは、データルート配下にファイルとして保存する。gitには入れない
- 端末側にあるもの（exe、設定、キャッシュ）はすべて消えてよい。端末を作り直しても、exeを置いてデータルートを指定すれば元に戻る

## 背景

職場の端末は外部からの接続を受け付けないWindowsの仮想デスクトップで、端末上でサーバを動かしても他の端末からは使えない。使える共有の場所はファイルサーバ（SMB）だけである。仮想サーバは申請しているが予算の関係で割り当てられず、GitHubは職場ではDownloadはOKだが内製ツールのリポジトリ置き場としてはグレーな扱いで使えない。

一方で、職場では業務自動化ツールをOpenAI Codexで内製しており、Issueによる管理とコードレビューの仕組みが要る。それなのにGitのリポジトリ置き場すらくれないとか意味わからない。
Gitea程度の使い勝手で、ブラウザから直感的に使えることが条件であった。

サーバ不要でIssueを扱えるツールとしてBacklog.mdを検証したが、pullとpushを利用者が意識する必要があり、職場のメンバーには使わせられないと判断した。次にGiteaを各端末で起動し、SQLiteのデータベースとリポジトリを共有フォルダに置く形を検証した。動作はしたが、SQLiteはネットワーク上のデータベースファイルを複数のマシンから使うことを公式に推奨しておらず、Giteaも複数インスタンスではデータベースサーバとRedisを求めている。公式の想定から外れる形で職場に入れることはできないと判断した。

そこで、各自の端末で動き、ファイルサーバをデータ置き場とし、Gitea API互換のAPIを持つツールを自作することにした。Gitea互換にしたのは、CodexにGiteaのAPIを使わせる形でIssueの読み書きをさせるためである。

本ツールの設計と実装は、Claude（Fable 5.1）とClaude Codeで行った。

## 設計の要点

- Issue、コメント、状態変更はすべて「イベント1件につき1ファイル」の追記で保存し、既存ファイルの上書きは行わない。現在の状態はイベントを時系列に適用して組み立てる
- Issue番号は、番号のフォルダを「既に存在すれば失敗する」方式で作成し、作成できた側がその番号を取る。SMB上の排他をこの1点に絞る
- イベントのファイル名は「UTC時刻（ミリ秒）_端末名_端末内連番」とし、端末間で重複しないようにする
- ファイルは一時名で書いてからrenameし、書きかけが他の端末から見えないようにする
- 画面はGitHubの構成と導線をパク……リスペクトする！ CSSはPrimer、アイコンはOcticonsを使い、すべてexeに埋め込む。外部への参照は持たない
- 画面の文言はGiteaの言語ファイルをそのまま使う。既定は日本語で、英語に切り替えられる
- gitは各端末のGit for Windowsを呼ぶ。exeには同梱しない

## 現在の状態

Ver 0.10。開発中であり、職場で使える状態ではない。

実装済み

- 手元のディスク上のデータルートでの動作
- トップ画面：全データルートのリポジトリを owner ごとに一覧、新規リポジトリの作成（作成先の owner を選ぶ。ベアリポジトリの作成を含む）
- Code：ファイルツリー、ファイル表示（Markdown の描画、画像）、ブランチ切替、コミット履歴、コミットの差分、clone コマンドの表示、空リポジトリへの push 手順の表示
- Issues：一覧（open／closed、ラベル、担当者で絞り込み）、詳細、新規作成、コメント、クローズと再オープン、ラベルと担当者の変更
- ラベルの管理（作成、編集、削除）
- Settings：説明、デフォルトブランチ（ベアリポジトリの HEAD にも反映）、承認必須、force push 禁止（receive.denyNonFastForwards）
- Pull requests：一覧、新規（ブランチを選んで比較）、詳細（Conversation／Commits／Files changed）、レビュー（承認／変更要請／コメント）、差分への行コメント、--no-ff でのマージとマージ後の head ブランチ削除、Revert、コンフリクトと force push の表示、承認必須の設定への対応
- 自分の設定：データルートの一覧（追加・登録解除。1つのデータルートが owner 1つに対応し、複数登録できる）、表示名、言語（日本語／英語）
- 横断ページ：全 owner をまたいだ Issue 一覧と Pull Request 一覧（すべて／自分に割り当て／自分が作成、open／closed）
- 待ち受けポートの自動選択（9418〜9427）、二重起動の検知、`%LOCALAPPDATA%\github-ppoi\port.txt` へのポートの書き出し
- マージと Revert に使うツール専用 clone（`%LOCALAPPDATA%\github-ppoi
epos\<owner>\<リポジトリ名>`。無ければ作り、使うたびにリモートに合わせる）

未実装

- 添付ファイル、ライセンス一覧ページ
- Gitea互換API（現在は `/api/v1/version` のみ）
- ローカルキャッシュ
- SMB上の同時更新の検証
- GitHub Actionsによるリリース

## 動かし方（開発中）

Go 1.27以上とGit for Windowsが要る。

```powershell
go build -o github-ppoi.exe ./cmd/github-ppoi
.\github-ppoi.exe
```

初回はブラウザに設定画面が開くので、データルートにするフォルダのパスと owner 名（URL に使う半角英数）を入れて登録する。データルートは複数登録できる。設定は `%LOCALAPPDATA%\github-ppoi\config.json` に保存される。

- 待ち受けポートは 9418 から順に空いているものを使う。上がったポートは画面のフッターと `%LOCALAPPDATA%\github-ppoi\port.txt` に出る
- `config.json` に `"port"` を書いてあれば、そのポートだけを使い、使えなければエラーで止まる
- 既に起動しているときにもう一度起動すると、ブラウザだけ開いて新しい方は終了する
- `--no-browser` を付けると起動時にブラウザを開かない

## データルートの構成

```
<データルート>\
  config.json                  このデータルートの設定（owner 名、owner の表示名、既定のブランチ名など）
  repos\
    <リポジトリ名>.git\        ベアリポジトリ
  meta\
    users\<ログオン名>.json    表示名
    <リポジトリ名>\
      repo.json                説明、既定のブランチ名、承認必須、force push禁止
      labels.json              ラベル
      issues\
        000012\
          events\              イベント（1件1ファイル）
          attachments\         添付ファイル
```

## ライセンス

MIT License。同梱しているOSS（Primer CSS、Octicons、Giteaの言語ファイル）のライセンスは [third_party/](third_party/) を参照。
