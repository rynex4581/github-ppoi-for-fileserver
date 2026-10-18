package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// インポート（設計メモ 3.16）で使う、番号や時刻を指定して書く入口。

// ErrIssueExists は指定した番号の Issue が既にあるときのエラー。
var ErrIssueExists = errors.New("その番号の Issue は既にある")

// CreateIssueAt は指定した番号のフォルダを作る。既にあれば ErrIssueExists。
// イベントはこのあと WriteCreated と AppendEvent で書く（Time を入れれば元の時刻のまま残る）。
func (s *Store) CreateIssueAt(repo string, number int) error {
	if err := s.writable(); err != nil {
		return err
	}
	if !isSafeName(repo) || number <= 0 {
		return ErrRepoNotFound
	}
	if _, err := s.GetRepo(repo); err != nil {
		return err
	}
	dir := s.issuesDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("%06d", number)), 0o755)
	if errors.Is(err, fs.ErrExist) {
		// 予約しただけの空のフォルダなら使ってよい
		if entries, rerr := os.ReadDir(s.eventsDir(repo, number)); rerr == nil && len(entries) > 0 {
			return ErrIssueExists
		}
	} else if err != nil {
		return err
	}
	return os.MkdirAll(s.eventsDir(repo, number), 0o755)
}

// CreateReleaseAt は作成時刻を指定してリリースを作る（インポート用）。
func (s *Store) CreateReleaseAt(repo, user, tag, tagSHA, title, body string, prerelease bool, created time.Time) (Release, error) {
	return s.createRelease(repo, user, tag, tagSHA, title, body, prerelease, created)
}
