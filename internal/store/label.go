package store

import (
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// Label はラベル。meta\<リポジトリ名>\labels.json に配列で持つ。項目は Gitea API の Label に合わせる。
type Label struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"` // "#" 無しの6桁16進（Gitea API と同じ）
	Description string `json:"description"`
}

// ErrLabelNotFound はラベルが無いときのエラー。
var ErrLabelNotFound = errors.New("ラベルが無い")

var labelColorPattern = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// NormalizeLabelColor は "#d73a4a" や "D73A4A" を "d73a4a" に揃える。形式が違えば空を返す。
func NormalizeLabelColor(c string) string {
	c = strings.TrimPrefix(strings.TrimSpace(c), "#")
	if !labelColorPattern.MatchString(c) {
		return ""
	}
	return strings.ToLower(c)
}

// ValidateLabel は名前と色を確かめる。
func ValidateLabel(l Label) error {
	if strings.TrimSpace(l.Name) == "" {
		return errors.New("ラベル名を入れる")
	}
	if len(l.Name) > 100 {
		return errors.New("ラベル名が長すぎる")
	}
	if NormalizeLabelColor(l.Color) == "" {
		return errors.New("色は 6桁の16進で指定する")
	}
	return nil
}

func (s *Store) labelsPath(repo string) string {
	return filepath.Join(s.repoMetaDir(repo), "labels.json")
}

// ListLabels はリポジトリのラベルをID順で返す。labels.json が無ければ空。
func (s *Store) ListLabels(repo string) ([]Label, error) {
	if !isSafeName(repo) {
		return nil, ErrRepoNotFound
	}
	var labels []Label
	err := readJSON(s.labelsPath(repo), &labels)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return labels, nil
}

// GetLabel はIDでラベルを返す。
func (s *Store) GetLabel(repo string, id int) (Label, error) {
	labels, err := s.ListLabels(repo)
	if err != nil {
		return Label{}, err
	}
	for _, l := range labels {
		if l.ID == id {
			return l, nil
		}
	}
	return Label{}, ErrLabelNotFound
}

func (s *Store) saveLabels(repo string, labels []Label) error {
	if err := s.writable(); err != nil {
		return err
	}
	if labels == nil {
		labels = []Label{}
	}
	return writeJSONAtomic(s.labelsPath(repo), labels)
}

// CreateLabel はラベルを追加する。IDは既存の最大＋1。同名があればエラー。
func (s *Store) CreateLabel(repo string, l Label) (Label, error) {
	if err := ValidateLabel(l); err != nil {
		return Label{}, err
	}
	if _, err := s.GetRepo(repo); err != nil {
		return Label{}, err
	}
	labels, err := s.ListLabels(repo)
	if err != nil {
		return Label{}, err
	}
	l.Name = strings.TrimSpace(l.Name)
	l.Color = NormalizeLabelColor(l.Color)
	l.Description = strings.TrimSpace(l.Description)
	maxID := 0
	for _, x := range labels {
		if x.ID > maxID {
			maxID = x.ID
		}
		if strings.EqualFold(x.Name, l.Name) {
			return Label{}, errors.New("同じ名前のラベルが既にある")
		}
	}
	l.ID = maxID + 1
	labels = append(labels, l)
	if err := s.saveLabels(repo, labels); err != nil {
		return Label{}, err
	}
	return l, nil
}

// UpdateLabel はラベルの名前、色、説明を変える。
func (s *Store) UpdateLabel(repo string, l Label) error {
	if err := ValidateLabel(l); err != nil {
		return err
	}
	labels, err := s.ListLabels(repo)
	if err != nil {
		return err
	}
	l.Name = strings.TrimSpace(l.Name)
	l.Color = NormalizeLabelColor(l.Color)
	l.Description = strings.TrimSpace(l.Description)
	found := false
	for i, x := range labels {
		if x.ID != l.ID && strings.EqualFold(x.Name, l.Name) {
			return errors.New("同じ名前のラベルが既にある")
		}
		if x.ID == l.ID {
			labels[i] = l
			found = true
		}
	}
	if !found {
		return ErrLabelNotFound
	}
	return s.saveLabels(repo, labels)
}

// DeleteLabel はラベルを消す。Issueに付いたままのIDは、表示時に無視される。
func (s *Store) DeleteLabel(repo string, id int) error {
	labels, err := s.ListLabels(repo)
	if err != nil {
		return err
	}
	kept := labels[:0]
	found := false
	for _, x := range labels {
		if x.ID == id {
			found = true
			continue
		}
		kept = append(kept, x)
	}
	if !found {
		return ErrLabelNotFound
	}
	return s.saveLabels(repo, kept)
}
