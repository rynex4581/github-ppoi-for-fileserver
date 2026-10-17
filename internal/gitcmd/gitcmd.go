// Package gitcmd は各端末の Git for Windows を呼んで、ベアリポジトリを読む。
// 読み取り専用で、リポジトリの中身は変更しない。
// ファイルサーバ上のリポジトリは所有者が自分と違うため、常に safe.directory=* を付けて呼ぶ。
package gitcmd

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Repo は1つのベアリポジトリ。
type Repo struct {
	Dir string
}

// ErrNotFound はブランチ、コミット、パスが無いときのエラー。
var ErrNotFound = errors.New("見つからない")

// run は git を実行して標準出力を返す。失敗時は標準エラーをメッセージに含める。
func (r Repo) run(args ...string) ([]byte, error) {
	full := append([]string{"-c", "safe.directory=*", "-c", "core.quotepath=false", "-C", r.Dir}, args...)
	cmd := exec.Command("git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}

// Branch はブランチ名と先頭のコミット。
type Branch struct {
	Name string
	SHA  string
}

// Branches はブランチを名前順で返す。コミットが1つも無ければ空。
func (r Repo) Branches() ([]Branch, error) {
	out, err := r.run("for-each-ref", "--sort=refname", "--format=%(refname:short)%00%(objectname)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, sha, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		branches = append(branches, Branch{Name: name, SHA: sha})
	}
	return branches, nil
}

// HeadBranch はベアリポジトリの HEAD が指すブランチ名を返す。
func (r Repo) HeadBranch() (string, error) {
	out, err := r.run("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveCommit はブランチ名やSHAをコミットのSHAにする。無ければ ErrNotFound。
func (r Repo) ResolveCommit(ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", ErrNotFound
	}
	out, err := r.run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(out)), nil
}

// Entry はツリーの1項目。
type Entry struct {
	Mode string
	Type string // blob / tree / commit（サブモジュール）
	SHA  string
	Name string
	Path string
	Size int64 // blob のみ
}

// IsDir はフォルダかを返す。
func (e Entry) IsDir() bool { return e.Type == "tree" }

// Tree は ref の path 配下の項目を返す。フォルダを先に、名前順。
func (r Repo) Tree(ref, path string) ([]Entry, error) {
	spec := ref
	if path != "" {
		spec = ref + ":" + path
	}
	out, err := r.run("ls-tree", "-l", "-z", spec)
	if err != nil {
		return nil, ErrNotFound
	}
	var entries []Entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// 形式: <mode> SP <type> SP <sha> SP <size or -> TAB <name>
		meta, name, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 4 {
			continue
		}
		e := Entry{Mode: f[0], Type: f[1], SHA: f[2], Name: name, Path: name}
		if path != "" {
			e.Path = path + "/" + name
		}
		if f[3] != "-" {
			e.Size, _ = strconv.ParseInt(f[3], 10, 64)
		}
		entries = append(entries, e)
	}
	// ls-tree は名前順で返す。フォルダを前に出すだけ安定に並べ替える
	var dirs, files []Entry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	return append(dirs, files...), nil
}

// ObjectType は ref:path の種類（blob / tree）を返す。無ければ ErrNotFound。
func (r Repo) ObjectType(ref, path string) (string, error) {
	out, err := r.run("cat-file", "-t", ref+":"+path)
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(out)), nil
}

// BlobSize は ref:path のファイルの大きさを返す。
func (r Repo) BlobSize(ref, path string) (int64, error) {
	out, err := r.run("cat-file", "-s", ref+":"+path)
	if err != nil {
		return 0, ErrNotFound
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

// Blob は ref:path のファイルの中身を返す。
func (r Repo) Blob(ref, path string) ([]byte, error) {
	out, err := r.run("cat-file", "blob", ref+":"+path)
	if err != nil {
		return nil, ErrNotFound
	}
	return out, nil
}

// Commit はコミットの情報。
type Commit struct {
	SHA     string
	Parents []string
	Author  string
	Email   string
	Time    time.Time
	Subject string
	Body    string
}

// ShortSHA は先頭7桁。
func (c Commit) ShortSHA() string {
	if len(c.SHA) > 7 {
		return c.SHA[:7]
	}
	return c.SHA
}

const logFormat = "--format=%H%x1f%P%x1f%an%x1f%ae%x1f%aI%x1f%s%x1f%b%x1e"

func parseCommits(out []byte) []Commit {
	var commits []Commit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 7 {
			continue
		}
		c := Commit{SHA: f[0], Author: f[2], Email: f[3], Subject: f[5], Body: strings.TrimRight(f[6], "\n")}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		c.Time, _ = time.Parse(time.RFC3339, f[4])
		commits = append(commits, c)
	}
	return commits
}

// Log は ref から遡ったコミットを新しい順に返す。path が空でなければそのパスに触れたものだけ。
// skip 件飛ばして limit 件まで返す。
func (r Repo) Log(ref, path string, skip, limit int) ([]Commit, error) {
	args := []string{"log", logFormat, "-n", strconv.Itoa(limit), "--skip=" + strconv.Itoa(skip), ref}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.run(args...)
	if err != nil {
		return nil, ErrNotFound
	}
	return parseCommits(out), nil
}

// CountCommits は ref から遡れるコミットの数を返す。
func (r Repo) CountCommits(ref string) (int, error) {
	out, err := r.run("rev-list", "--count", ref)
	if err != nil {
		return 0, ErrNotFound
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// GetCommit は1つのコミットの情報を返す。
func (r Repo) GetCommit(sha string) (Commit, error) {
	out, err := r.run("log", logFormat, "-n", "1", sha)
	if err != nil {
		return Commit{}, ErrNotFound
	}
	commits := parseCommits(out)
	if len(commits) == 0 {
		return Commit{}, ErrNotFound
	}
	return commits[0], nil
}

// CommitDiff はコミットの差分（unified diff）を返す。
// 親が複数あるマージコミットは最初の親との差分を返す（GitHubと同じ）。
func (r Repo) CommitDiff(c Commit) (string, error) {
	var out []byte
	var err error
	if len(c.Parents) == 0 {
		out, err = r.run("diff-tree", "-p", "--root", "-M", "--no-color", c.SHA)
	} else {
		out, err = r.run("diff", "-M", "--no-color", c.Parents[0], c.SHA)
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}
