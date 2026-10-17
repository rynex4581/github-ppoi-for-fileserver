package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Repo はリポジトリの情報。meta\<名前>\repo.json に対応する。
type Repo struct {
	Name            string `json:"-"`
	Description     string `json:"description"`
	DefaultBranch   string `json:"default_branch"`
	RequireApproval bool   `json:"require_approval"`
	DenyForcePush   bool   `json:"deny_force_push"`
}

// ErrRepoNotFound はリポジトリが無いときのエラー。
var ErrRepoNotFound = errors.New("リポジトリが無い")

// ErrRepoExists は同名のリポジトリが既にあるときのエラー。
var ErrRepoExists = errors.New("同じ名前のリポジトリが既にある")

var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ValidateRepoName はリポジトリ名として使えるかを確かめる。
// 英数字で始まり、英数字と . _ - だけからなる100文字以内。末尾の .git は認めない。
func ValidateRepoName(name string) error {
	if !repoNamePattern.MatchString(name) {
		return errors.New("リポジトリ名は英数字で始まり、英数字と . _ - だけを使う")
	}
	if strings.HasSuffix(strings.ToLower(name), ".git") || strings.Contains(name, "..") {
		return errors.New("リポジトリ名の末尾に .git は付けられない")
	}
	return nil
}

func (s *Store) bareRepoDir(name string) string {
	return filepath.Join(s.reposDir(), name+".git")
}

func (s *Store) repoMetaDir(name string) string {
	return filepath.Join(s.metaDir(), name)
}

// ListRepos は repos 配下の *.git をすべて拾い、名前順に返す。登録作業は無い。
func (s *Store) ListRepos() ([]Repo, error) {
	entries, err := os.ReadDir(s.reposDir())
	if err != nil {
		return nil, err
	}
	var repos []Repo
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".git") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".git")
		repos = append(repos, s.readRepoMeta(name))
	}
	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name)
	})
	return repos, nil
}

// GetRepo は1つのリポジトリの情報を返す。ベアリポジトリが無ければ ErrRepoNotFound。
func (s *Store) GetRepo(name string) (Repo, error) {
	if !isSafeName(name) {
		return Repo{}, ErrRepoNotFound
	}
	info, err := os.Stat(s.bareRepoDir(name))
	if err != nil || !info.IsDir() {
		return Repo{}, ErrRepoNotFound
	}
	return s.readRepoMeta(name), nil
}

// readRepoMeta は repo.json を読む。無い、または読めないときは既定値で返す。
func (s *Store) readRepoMeta(name string) Repo {
	r := Repo{Name: name, DefaultBranch: s.config.DefaultBranch}
	_ = readJSON(filepath.Join(s.repoMetaDir(name), "repo.json"), &r)
	r.Name = name
	if r.DefaultBranch == "" {
		r.DefaultBranch = s.config.DefaultBranch
	}
	return r
}

// CreateRepo は repos\<名前>.git に git init --bare し、meta\<名前>\repo.json を作る。
func (s *Store) CreateRepo(name, description string) (Repo, error) {
	if err := ValidateRepoName(name); err != nil {
		return Repo{}, err
	}
	bare := s.bareRepoDir(name)

	// フォルダの作成を排他にし、2台が同時に同名で作ろうとしても片方だけが通るようにする
	if err := os.Mkdir(bare, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Repo{}, ErrRepoExists
		}
		return Repo{}, err
	}
	cmd := exec.Command("git", "init", "--bare", "--quiet", "--initial-branch="+s.config.DefaultBranch, bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(bare)
		return Repo{}, fmt.Errorf("git init --bare に失敗: %w: %s", err, strings.TrimSpace(string(out)))
	}

	r := Repo{Name: name, Description: description, DefaultBranch: s.config.DefaultBranch}
	if err := os.MkdirAll(s.repoMetaDir(name), 0o755); err != nil {
		return Repo{}, err
	}
	if err := writeJSONAtomic(filepath.Join(s.repoMetaDir(name), "repo.json"), r); err != nil {
		return Repo{}, err
	}
	return r, nil
}
