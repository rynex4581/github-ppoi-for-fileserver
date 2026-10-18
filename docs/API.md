# Gitea 互換 API

`localhost:<ポート>/api/v1/` に Gitea API v1 と同じ形のエンドポイントを置く。ポートは `%LOCALAPPDATA%\github-ppoi\port.txt` に記載済み。

- トークンは空でなければ何でも通る（`Authorization: token x`）。URL のクエリ（`?token=`）は不可
- JSON を送るときは `Content-Type: application/json` を付けること
- `/api/v1/version` だけは認証無し
- 別サイトからの書き込みは拒否する（Origin と Host を確認する）

## 実装済

- user、users/{login}、user/orgs、orgs/{org}、user/repos、repos/search、orgs/{org}/repos
- repos/{owner}/{repo}：本体、branches、commits、git/commits/{sha}（`.diff` 付きで差分）、contents、raw、compare/{base}...{head}
- issues：一覧（state、type、labels、q）、作成、取得、PATCH（title、body、state、assignees、labels）、comments（一覧、作成、取得、PATCH、DELETE）、labels、assets
- labels：一覧、作成、取得、PATCH、削除
- pulls：一覧、作成、取得、PATCH、`.diff`、commits、files、merge（`Do` は merge のみ）、reviews（APPROVED／REQUEST_CHANGES／COMMENT と行コメント）
- releases：一覧、作成、取得（id と tags/{tag}）、latest、PATCH、削除、assets（一覧、アップロード、取得、削除）。tags：一覧
- tea（Gitea 公式 CLI）の v0.16 で、ログイン、リポジトリ一覧、Issue の一覧・作成・表示・クローズ、コメント、ラベル、ブランチ、PR の一覧・作成・マージが通ることを確認済

## 予定（未実装）

- マイルストーン、Wiki、通知など、上に無い Gitea API の範囲

## Codex に使わせる

対象のリポジトリの AGENTS.md に次のように記述する。

> Issue と Pull Request は localhost の Gitea 互換 API で扱う。
> ポートは `%LOCALAPPDATA%\github-ppoi\port.txt` に書いてある。
> URL は `http://localhost:<ポート>/api/v1/repos/<owner>/<リポジトリ名>/...` で、
> Authorization ヘッダは `token dummy` のように空でない値を付ける。
