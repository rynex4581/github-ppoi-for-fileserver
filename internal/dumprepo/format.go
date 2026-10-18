// Package dumprepo は Gitea の dump-repo / restore-repo 形式の読み書き（設計メモ 3.16）。
//
// 参照した Gitea の版は v28.1.0（services/migrations/dump.go、restore.go、modules/migration 配下の構造体と
// schemas/）。フォルダの構成は次のとおり。
//
//	<出力先>/<owner>/<リポジトリ名>/
//	  repo.yml              名前、説明、既定ブランチなど（map）
//	  topic.yml             topics: []
//	  label.yml             ラベルの一覧
//	  issue.yml             Issue の一覧
//	  comments/<番号>.yml   Issue／PR ごとのコメント
//	  pull_request.yml      Pull Request の一覧
//	  reviews/<番号>.yml    PR ごとのレビュー（行コメントを含む）
//	  release.yml           リリースの一覧
//	  release_assets/...    リリースの添付（release.yml の download_url が指す相対パス）
//	  git/                  --mirror の clone。PR の head は refs/pull/<番号>/head にも置く
//
// YAML の項目名は Gitea の構造体の yaml タグ（無ければフィールド名の小文字）に合わせる。
// Gitea の restore-repo --validation が使う JSON スキーマ（issue.json、label.json）に無い項目は書かない。
package dumprepo

import "time"

// ymlLabel は modules/migration/label.go の Label。
type ymlLabel struct {
	Name        string `yaml:"name"`
	Color       string `yaml:"color"`
	Description string `yaml:"description"`
}

// ymlIssue は modules/migration/issue.go の Issue。
type ymlIssue struct {
	Number      int64      `yaml:"number"`
	PosterID    int64      `yaml:"poster_id"`
	PosterName  string     `yaml:"poster_name"`
	PosterEmail string     `yaml:"poster_email"`
	Title       string     `yaml:"title"`
	Content     string     `yaml:"content"`
	Ref         string     `yaml:"ref"`
	Milestone   string     `yaml:"milestone"`
	State       string     `yaml:"state"`
	IsLocked    bool       `yaml:"is_locked"`
	Created     time.Time  `yaml:"created"`
	Updated     time.Time  `yaml:"updated"`
	Closed      *time.Time `yaml:"closed"`
	Labels      []ymlLabel `yaml:"labels"`
	Reactions   []any      `yaml:"reactions"`
	Assignees   []string   `yaml:"assignees"`
}

// ymlComment は modules/migration/comment.go の Comment。
type ymlComment struct {
	IssueIndex  int64          `yaml:"issue_index"`
	Index       int64          `yaml:"index"`
	CommentType string         `yaml:"comment_type"`
	PosterID    int64          `yaml:"poster_id"`
	PosterName  string         `yaml:"poster_name"`
	PosterEmail string         `yaml:"poster_email"`
	Created     time.Time      `yaml:"created"`
	Updated     time.Time      `yaml:"updated"`
	Content     string         `yaml:"content"`
	Reactions   []any          `yaml:"reactions"`
	Meta        map[string]any `yaml:"meta,omitempty"`
}

// ymlPRBranch は PullRequestBranch。
type ymlPRBranch struct {
	CloneURL  string `yaml:"clone_url"`
	Ref       string `yaml:"ref"`
	SHA       string `yaml:"sha"`
	RepoName  string `yaml:"repo_name"`
	OwnerName string `yaml:"owner_name"`
}

// ymlPullRequest は modules/migration/pullrequest.go の PullRequest。
type ymlPullRequest struct {
	Number         int64       `yaml:"number"`
	Title          string      `yaml:"title"`
	PosterName     string      `yaml:"poster_name"`
	PosterID       int64       `yaml:"poster_id"`
	PosterEmail    string      `yaml:"poster_email"`
	Content        string      `yaml:"content"`
	Milestone      string      `yaml:"milestone"`
	State          string      `yaml:"state"`
	Created        time.Time   `yaml:"created"`
	Updated        time.Time   `yaml:"updated"`
	Closed         *time.Time  `yaml:"closed"`
	Labels         []ymlLabel  `yaml:"labels"`
	PatchURL       string      `yaml:"patch_url"`
	Merged         bool        `yaml:"merged"`
	MergedTime     *time.Time  `yaml:"merged_time"`
	MergeCommitSHA string      `yaml:"merge_commit_sha"`
	Head           ymlPRBranch `yaml:"head"`
	Base           ymlPRBranch `yaml:"base"`
	Assignees      []string    `yaml:"assignees"`
	IsLocked       bool        `yaml:"is_locked"`
	Reactions      []any       `yaml:"reactions"`
	ForeignIndex   int64       `yaml:"foreignindex"`
	EnsuredSafe    bool        `yaml:"ensured_safe"`
	IsDraft        bool        `yaml:"is_draft"`
}

// ymlReviewComment は ReviewComment。Line は正なら新しい側、負なら古い側の行番号（Gitea の流儀）。
type ymlReviewComment struct {
	ID        int64     `yaml:"id"`
	InReplyTo int64     `yaml:"in_reply_to"`
	Content   string    `yaml:"content"`
	TreePath  string    `yaml:"tree_path"`
	DiffHunk  string    `yaml:"diff_hunk"`
	Position  int       `yaml:"position"`
	Line      int       `yaml:"line"`
	CommitID  string    `yaml:"commit_id"`
	PosterID  int64     `yaml:"poster_id"`
	Reactions []any     `yaml:"reactions"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// ymlReview は modules/migration/review.go の Review。
type ymlReview struct {
	ID           int64              `yaml:"id"`
	IssueIndex   int64              `yaml:"issue_index"`
	ReviewerID   int64              `yaml:"reviewer_id"`
	ReviewerName string             `yaml:"reviewer_name"`
	Official     bool               `yaml:"official"`
	CommitID     string             `yaml:"commit_id"`
	Content      string             `yaml:"content"`
	CreatedAt    time.Time          `yaml:"created_at"`
	State        string             `yaml:"state"` // PENDING, APPROVED, CHANGES_REQUESTED, COMMENTED, REQUEST_REVIEW
	Comments     []ymlReviewComment `yaml:"comments"`
}

// ymlReleaseAsset は ReleaseAsset。download_url は dump フォルダからの相対パス。
type ymlReleaseAsset struct {
	ID            int64     `yaml:"id"`
	Name          string    `yaml:"name"`
	Size          *int      `yaml:"size"`
	DownloadCount *int      `yaml:"download_count"`
	Created       time.Time `yaml:"created"`
	Updated       time.Time `yaml:"updated"`
	DownloadURL   *string   `yaml:"download_url"`
}

// ymlRelease は modules/migration/release.go の Release。
type ymlRelease struct {
	TagName         string            `yaml:"tag_name"`
	TargetCommitish string            `yaml:"target_commitish"`
	Name            string            `yaml:"name"`
	Body            string            `yaml:"body"`
	Draft           bool              `yaml:"draft"`
	Prerelease      bool              `yaml:"prerelease"`
	PublisherID     int64             `yaml:"publisher_id"`
	PublisherName   string            `yaml:"publisher_name"`
	PublisherEmail  string            `yaml:"publisher_email"`
	Assets          []ymlReleaseAsset `yaml:"assets"`
	Created         time.Time         `yaml:"created"`
	Published       time.Time         `yaml:"published"`
}

// Gitea の structs.GitServiceType。repo.yml の service_type に入れる。
const (
	serviceTypePlainGit = 1
	serviceTypeGitHub   = 2
)
