package store

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrAttachmentNotFound は添付ファイルが無いときのエラー。
var ErrAttachmentNotFound = errors.New("添付ファイルが無い")

// ErrAttachmentTooLarge は添付ファイルが上限を超えたときのエラー。
var ErrAttachmentTooLarge = errors.New("添付ファイルが大きすぎる")

var attachmentNameSanitize = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Store) attachmentsDir(repo string, number int) string {
	return filepath.Join(s.issueDir(repo, number), "attachments")
}

// SaveAttachment は添付ファイルを issues\<番号>\attachments\<UTC時刻>_<元の名前> に保存し、そのファイル名を返す。
// 元の名前はパス区切りや記号を落とし、空になれば file にする。大きさが上限を超えれば ErrAttachmentTooLarge。
func (s *Store) SaveAttachment(repo string, number int, original string, data []byte) (string, error) {
	if int64(len(data)) > s.config.AttachmentMaxBytes {
		return "", ErrAttachmentTooLarge
	}
	if _, err := os.Stat(s.eventsDir(repo, number)); err != nil {
		return "", ErrIssueNotFound
	}
	base := path.Base(strings.ReplaceAll(original, "\\", "/"))
	ext := strings.ToLower(path.Ext(base))
	stem := attachmentNameSanitize.ReplaceAllString(strings.TrimSuffix(base, path.Ext(base)), "-")
	stem = strings.Trim(stem, "-.")
	if stem == "" {
		stem = "file"
	}
	if len(stem) > 60 {
		stem = stem[:60]
	}
	dir := s.attachmentsDir(repo, number)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	t := time.Now().UTC()
	name := fmt.Sprintf("%s%03dZ_%s%s", t.Format("20060102T150405"), t.Nanosecond()/1e6, stem, ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			break
		}
		name = fmt.Sprintf("%s%03dZ_%s-%d%s", t.Format("20060102T150405"), t.Nanosecond()/1e6, stem, i, ext)
	}
	if err := writeFileAtomic(filepath.Join(dir, name), data, false); err != nil {
		return "", err
	}
	return name, nil
}

// AttachmentPath は添付ファイルのパスを返す。名前が不正、またはファイルが無ければ ErrAttachmentNotFound。
func (s *Store) AttachmentPath(repo string, number int, name string) (string, error) {
	if !isSafeName(repo) || number <= 0 || !isSafeName(name) || strings.HasSuffix(name, ".tmp") {
		return "", ErrAttachmentNotFound
	}
	p := filepath.Join(s.attachmentsDir(repo, number), name)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", ErrAttachmentNotFound
	}
	return p, nil
}

// Attachment は添付ファイル1つの情報。
type Attachment struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// ListAttachments は Issue の添付ファイルを名前順で返す。無ければ空。
func (s *Store) ListAttachments(repo string, number int) ([]Attachment, error) {
	if !isSafeName(repo) || number <= 0 {
		return nil, ErrIssueNotFound
	}
	entries, err := os.ReadDir(s.attachmentsDir(repo, number))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Attachment
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Attachment{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	return out, nil
}

// AttachmentMaxBytes は添付ファイルの上限を返す。
func (s *Store) AttachmentMaxBytes() int64 {
	return s.config.AttachmentMaxBytes
}
