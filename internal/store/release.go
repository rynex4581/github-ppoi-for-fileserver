package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// リリース（設計メモ 3.18）。
//
// meta\<リポジトリ名>\releases\<タグ名>\events\ に Issue と同じイベント追記方式で持つ。
// created がリリースの作成、edited がタイトル・本文・プレリリースの変更、deleted が削除。
// 削除したあとに同じタグでもう一度作ると、新しい created が後ろに付く。
// 添付（配布物）は同じ階層の attachments\ に置く。
// タグ名はフォルダ名になるので、フォルダ名に使える文字だけのタグに限る。

// ErrReleaseNotFound はリリースが無いときのエラー。
var ErrReleaseNotFound = errors.New("リリースが無い")

// ErrReleaseExists は同じタグのリリースが既にあるときのエラー。
var ErrReleaseExists = errors.New("このタグのリリースは既にある")

// ErrBadTagName はタグ名がフォルダ名に使えないときのエラー。
var ErrBadTagName = errors.New("タグ名はフォルダ名に使える文字（英数字と . _ - +）だけにする")

var tagNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,99}$`)

// ValidateTagName はリリースに使えるタグ名かを確かめる。
func ValidateTagName(tag string) error {
	if !tagNamePattern.MatchString(tag) || strings.Contains(tag, "..") || strings.HasSuffix(tag, ".lock") || isWindowsReserved(tag) {
		return ErrBadTagName
	}
	return nil
}

// Release はイベントを適用して組み立てた今の状態。
type Release struct {
	Tag        string
	Title      string
	Body       string
	Prerelease bool
	TagSHA     string // 作成時にタグが指していたコミット
	User       string // 作成者
	CreatedAt  time.Time
	UpdatedAt  time.Time
	EditedBy   string
	Events     []Event
}

func (s *Store) releasesDir(repo string) string {
	return filepath.Join(s.repoMetaDir(repo), "releases")
}

func (s *Store) releaseDir(repo, tag string) string {
	return filepath.Join(s.releasesDir(repo), tag)
}

func (s *Store) releaseEventsDir(repo, tag string) string {
	return filepath.Join(s.releaseDir(repo, tag), "events")
}

func (s *Store) releaseAttachmentsDir(repo, tag string) string {
	return filepath.Join(s.releaseDir(repo, tag), "attachments")
}

// ReleaseEventsDir はリリースの events フォルダのパスを返す（検証用）。
func (s *Store) ReleaseEventsDir(repo, tag string) string { return s.releaseEventsDir(repo, tag) }

// writeReleaseEvent はリリースのイベントを1ファイルとして書く。
func (s *Store) writeReleaseEvent(repo, tag string, ev Event) error {
	if err := s.writable(); err != nil {
		return err
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	ev.Time = ev.Time.UTC().Truncate(time.Millisecond)
	dir := s.releaseEventsDir(repo, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJSONNew(filepath.Join(dir, s.eventFileName(ev.Time)), ev)
}

// readReleaseEvents はリリースのイベントをファイル名順に読む。フォルダが無ければ ErrReleaseNotFound。
func (s *Store) readReleaseEvents(repo, tag string) ([]Event, error) {
	entries, err := os.ReadDir(s.releaseEventsDir(repo, tag))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.releaseEventsDir(repo, tag), name))
		if err != nil {
			continue
		}
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		ev.File = name
		events = append(events, ev)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].File < events[j].File })
	return events, nil
}

// buildRelease はイベントから今の状態を組み立てる。削除済み（または created が無い）なら偽。
func buildRelease(tag string, events []Event) (Release, bool) {
	var rel Release
	exists := false
	for _, ev := range events {
		switch ev.Type {
		case EventCreated:
			rel = Release{Tag: tag, Title: ev.Title, Body: ev.Body, Prerelease: ev.Prerelease, TagSHA: ev.CommitSHA, User: ev.User, CreatedAt: ev.Time, UpdatedAt: ev.Time}
			exists = true
		case EventEdited:
			if !exists {
				continue
			}
			rel.Title, rel.Body, rel.Prerelease = ev.Title, ev.Body, ev.Prerelease
			rel.EditedBy, rel.UpdatedAt = ev.User, ev.Time
		case EventDeleted:
			exists = false
		}
	}
	rel.Events = events
	return rel, exists
}

// GetRelease は1件のリリースを返す。無い、または削除済みなら ErrReleaseNotFound。
func (s *Store) GetRelease(repo, tag string) (Release, error) {
	if !isSafeName(repo) || ValidateTagName(tag) != nil {
		return Release{}, ErrReleaseNotFound
	}
	events, err := s.readReleaseEvents(repo, tag)
	if err != nil {
		return Release{}, err
	}
	rel, ok := buildRelease(tag, events)
	if !ok {
		return Release{}, ErrReleaseNotFound
	}
	return rel, nil
}

// ListReleases はリポジトリのリリースを作成日時の新しい順で返す。削除済みは含めない。
func (s *Store) ListReleases(repo string) ([]Release, error) {
	if !isSafeName(repo) {
		return nil, ErrRepoNotFound
	}
	entries, err := os.ReadDir(s.releasesDir(repo))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel, err := s.GetRelease(repo, e.Name())
		if err != nil {
			continue
		}
		out = append(out, rel)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// CreateRelease はリリースを作る。同じタグのリリースが生きていれば ErrReleaseExists。
func (s *Store) CreateRelease(repo, user, tag, tagSHA, title, body string, prerelease bool) (Release, error) {
	if err := s.writable(); err != nil {
		return Release{}, err
	}
	if !isSafeName(repo) {
		return Release{}, ErrRepoNotFound
	}
	if err := ValidateTagName(tag); err != nil {
		return Release{}, err
	}
	if _, err := s.GetRepo(repo); err != nil {
		return Release{}, err
	}
	if _, err := s.GetRelease(repo, tag); err == nil {
		return Release{}, ErrReleaseExists
	}
	ev := Event{Type: EventCreated, User: user, Title: title, Body: body, Prerelease: prerelease, CommitSHA: tagSHA}
	if err := s.writeReleaseEvent(repo, tag, ev); err != nil {
		return Release{}, err
	}
	return s.GetRelease(repo, tag)
}

// EditRelease はタイトル・本文・プレリリースを書き換える（edited を追記する）。
func (s *Store) EditRelease(repo, user, tag, title, body string, prerelease bool) error {
	rel, err := s.GetRelease(repo, tag)
	if err != nil {
		return err
	}
	if rel.Title == title && rel.Body == body && rel.Prerelease == prerelease {
		return nil
	}
	return s.writeReleaseEvent(repo, tag, Event{Type: EventEdited, User: user, Title: title, Body: body, Prerelease: prerelease})
}

// DeleteRelease はリリースを削除する（deleted を追記する）。タグと添付のファイルは残る。
func (s *Store) DeleteRelease(repo, user, tag string) error {
	if _, err := s.GetRelease(repo, tag); err != nil {
		return err
	}
	return s.writeReleaseEvent(repo, tag, Event{Type: EventDeleted, User: user})
}

// SaveReleaseAttachment はリリースの添付（配布物）を保存し、ファイル名を返す。
func (s *Store) SaveReleaseAttachment(repo, tag, original string, data []byte) (string, error) {
	if err := s.writable(); err != nil {
		return "", err
	}
	if _, err := s.GetRelease(repo, tag); err != nil {
		return "", err
	}
	return s.saveAttachmentTo(s.releaseAttachmentsDir(repo, tag), original, data)
}

// ListReleaseAttachments はリリースの添付を名前順で返す。
func (s *Store) ListReleaseAttachments(repo, tag string) ([]Attachment, error) {
	if !isSafeName(repo) || ValidateTagName(tag) != nil {
		return nil, ErrReleaseNotFound
	}
	return listAttachmentsIn(s.releaseAttachmentsDir(repo, tag))
}

// ReleaseAttachmentPath はリリースの添付のパスを返す。無ければ ErrAttachmentNotFound。
func (s *Store) ReleaseAttachmentPath(repo, tag, name string) (string, error) {
	if !isSafeName(repo) || ValidateTagName(tag) != nil || !isSafeName(name) || strings.HasSuffix(name, ".tmp") {
		return "", ErrAttachmentNotFound
	}
	p := filepath.Join(s.releaseAttachmentsDir(repo, tag), name)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", ErrAttachmentNotFound
	}
	return p, nil
}

// RemoveReleaseAttachment はリリースの添付を消す。
func (s *Store) RemoveReleaseAttachment(repo, tag, name string) error {
	if err := s.writable(); err != nil {
		return err
	}
	p, err := s.ReleaseAttachmentPath(repo, tag, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
