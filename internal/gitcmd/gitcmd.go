// Package gitcmd は各端末の Git for Windows を呼んで、ベアリポジトリを読む。
// ほとんどは読み取りだが、HasConflict の merge-tree は結果のツリーを共有のベアリポジトリに書く。
// 設定を変えるのは SetDenyNonFastForwards、SetHead、Maintenance だけ。
// ファイルサーバ上のリポジトリは所有者が自分と違うため、常に safe.directory=* を付けて呼ぶ。
// そのため、データルートに書ける人はリポジトリの設定（diff.external 等）を通じて他の端末で
// コマンドを動かせる関係にある。緩和として diff.external と log.showSignature を無効にし、
// diff には --no-ext-diff を付ける。メッセージの判定が英語の文字列に依存するので LC_ALL=C で呼ぶ。
// ブランチ名など外から来た引数は --end-of-options の後ろに置き、オプションとして解釈させない。
package gitcmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	out, _, err := runGit(r.Dir, nil, args...)
	return out, err
}

// noHooksDir は core.hooksPath に渡す、中身の無いフォルダ（%LOCALAPPDATA%\github-ppoi\nohooks）。
// 空文字を渡すと git は「ルート直下」と解釈し、Windows ではカレントのドライブや UNC のルートに
// 解決されるので、存在する空のフォルダを絶対パスで渡す。テストから差し替える。
var noHooksDir = initNoHooksDir()

func initNoHooksDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "github-ppoi", "nohooks")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// runGit は dir で git を実行し、標準出力と終了コードを返す。extra は -c の追加設定。
// core.hooksPath を空のフォルダに向け、リポジトリに置かれた hooks を本ツールの呼び出しでは動かさない。
// ただし -c は push 先の receive-pack には渡らない（git がローカルの push で設定の環境変数を
// 落とす）ので、push 側は noHooksReceivePack() で receive-pack にも同じ設定を付ける。
func runGit(dir string, extra []string, args ...string) ([]byte, int, error) {
	// core.longpaths は Windows の 260 文字制限に掛からないようにするため（共有フォルダの深いパスで pack の一時名が超える）
	full := []string{"-c", "safe.directory=*", "-c", "core.quotepath=false", "-c", "diff.external=", "-c", "log.showSignature=false", "-c", "core.hooksPath=" + noHooksDir, "-c", "core.longpaths=true"}
	full = append(full, extra...)
	full = append(full, "-C", dir)
	full = append(full, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), code, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.Bytes(), 0, nil
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
	out, err := r.run("ls-tree", "-l", "-z", "--end-of-options", spec)
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
	out, err := r.run("cat-file", "-t", "--end-of-options", ref+":"+path)
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(out)), nil
}

// BlobSize は ref:path のファイルの大きさを返す。
func (r Repo) BlobSize(ref, path string) (int64, error) {
	out, err := r.run("cat-file", "-s", "--end-of-options", ref+":"+path)
	if err != nil {
		return 0, ErrNotFound
	}
	return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
}

// Blob は ref:path のファイルの中身を返す。
func (r Repo) Blob(ref, path string) ([]byte, error) {
	out, err := r.run("cat-file", "blob", "--end-of-options", ref+":"+path)
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
	args := []string{"log", logFormat, "-n", strconv.Itoa(limit), "--skip=" + strconv.Itoa(skip), "--end-of-options", ref}
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
	return r.CountCommitsPath(ref, "")
}

// CountCommitsPath は ref から遡れるコミットのうち、path に触れたものの数を返す。path が空なら全部。
func (r Repo) CountCommitsPath(ref, path string) (int, error) {
	args := []string{"rev-list", "--count", "--end-of-options", ref}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := r.run(args...)
	if err != nil {
		return 0, ErrNotFound
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// GetCommit は1つのコミットの情報を返す。
func (r Repo) GetCommit(sha string) (Commit, error) {
	out, err := r.run("log", logFormat, "-n", "1", "--end-of-options", sha)
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
		out, err = r.run("diff-tree", "-p", "--root", "-M", "--no-color", "--no-ext-diff", "--end-of-options", c.SHA)
	} else {
		out, err = r.run("diff", "-M", "--no-color", "--no-ext-diff", "--end-of-options", c.Parents[0], c.SHA)
	}
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// MergeBase は2つのコミットの共通の祖先を返す。
func (r Repo) MergeBase(a, b string) (string, error) {
	out, err := r.run("merge-base", "--end-of-options", a, b)
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(out)), nil
}

// IsAncestor は a が b の祖先（a が b に含まれている）かを返す。a が無くなっていれば false。
func (r Repo) IsAncestor(a, b string) bool {
	_, code, err := runGit(r.Dir, nil, "merge-base", "--is-ancestor", "--end-of-options", a, b)
	return err == nil && code == 0
}

// RangeLog は base に無く head にあるコミット（base..head）を新しい順に返す。
func (r Repo) RangeLog(base, head string, limit int) ([]Commit, error) {
	out, err := r.run("log", logFormat, "-n", strconv.Itoa(limit), "--end-of-options", base+".."+head)
	if err != nil {
		return nil, ErrNotFound
	}
	return parseCommits(out), nil
}

// MergeCommitOf は base の履歴から、headSHA を2つ目以降の親に持つマージコミットを探す。
// マージの push は通ったのに記録に失敗したときの復旧に使う。無ければ ErrNotFound。
func (r Repo) MergeCommitOf(base, headSHA string) (string, error) {
	out, err := r.run("log", "--merges", "--format=%H %P", "-n", "200", "refs/heads/"+base)
	if err != nil {
		return "", ErrNotFound
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		for _, parent := range f[min(len(f), 2):] {
			if parent == headSHA {
				return f[0], nil
			}
		}
	}
	return "", ErrNotFound
}

// RevertCommitOf は base の履歴から、mergeSHA を打ち消した revert コミットを探す。
// Revert の push は通ったのに記録に失敗したときの復旧に使う。無ければ ErrNotFound。
func (r Repo) RevertCommitOf(base, mergeSHA string) (string, error) {
	out, err := r.run("log", "--fixed-strings", "--grep=This reverts commit "+mergeSHA, "--format=%H", "-n", "1", "refs/heads/"+base)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(out)), nil
}

// RangeDiff は base と head の共通の祖先から head までの差分（base...head）を返す。
func (r Repo) RangeDiff(base, head string) (string, error) {
	out, err := r.run("diff", "-M", "--no-color", "--no-ext-diff", "--end-of-options", base+"..."+head)
	if err != nil {
		return "", ErrNotFound
	}
	return string(out), nil
}

// HasConflict は head を base にマージしたときにコンフリクトするかを、作業ツリー無しで調べる。
func (r Repo) HasConflict(base, head string) (bool, error) {
	_, code, err := runGit(r.Dir, nil, "merge-tree", "--write-tree", "--no-messages", "--end-of-options", base, head)
	if err == nil {
		return false, nil
	}
	if code == 1 {
		return true, nil
	}
	return false, err
}

// SetDenyNonFastForwards は receive.denyNonFastForwards を設定する。
// true にすると、履歴を書き換える push（force push）をベアリポジトリ側で弾く。
func (r Repo) SetDenyNonFastForwards(deny bool) error {
	value := "false"
	if deny {
		value = "true"
	}
	_, err := r.run("config", "receive.denyNonFastForwards", value)
	return err
}

// Maintenance はベアリポジトリの git gc を実行する。push のたびに各自の端末で自動実行されないよう
// receive.autogc は無効にしてあるので、たまに Settings からこれを呼ぶ。
func (r Repo) Maintenance() error {
	if _, err := r.run("config", "receive.autogc", "false"); err != nil {
		return err
	}
	_, err := r.run("gc", "--quiet")
	return err
}

// Tag はタグ1つ。SHA はタグが指すコミット（注釈付きタグなら剥がした先）。
type Tag struct {
	Name string
	SHA  string
	Time time.Time // タグの作成日時（注釈付き）またはコミットの日時
}

// Tags はタグを新しい順で返す。
func (r Repo) Tags() ([]Tag, error) {
	out, err := r.run("for-each-ref", "--sort=-creatordate", "--format=%(refname:short)%00%(objectname)%00%(*objectname)%00%(creatordate:iso-strict)", "refs/tags/")
	if err != nil {
		return nil, err
	}
	var tags []Tag
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 4 || parts[0] == "" {
			continue
		}
		sha := parts[2] // 注釈付きタグの指す先
		if sha == "" {
			sha = parts[1]
		}
		t, _ := time.Parse(time.RFC3339, parts[3])
		tags = append(tags, Tag{Name: parts[0], SHA: sha, Time: t})
	}
	return tags, nil
}

// CloneMirror は src を dst に --mirror で clone する（エクスポート用）。
func CloneMirror(src, dst string) error {
	_, _, err := runGit(filepath.Dir(dst), nil, "clone", "--quiet", "--mirror", "--end-of-options", src, dst)
	return err
}

// PushBranchesAndTags は src（bare か mirror）のブランチとタグを dst に push する（インポートと GitHub への書き出し用）。
// --mirror にしないのは、refs/pull/* を送ると GitHub が拒むため（新19）。
func PushBranchesAndTags(src, dst string) error {
	_, _, err := runGit(src, nil, "push", "--quiet", noHooksReceivePack(), "--end-of-options", dst, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
	return err
}

// UpdateRef は参照を sha に向ける（無ければ作る）。
func (r Repo) UpdateRef(ref, sha string) error {
	_, err := r.run("update-ref", "--no-deref", "--end-of-options", ref, sha)
	return err
}

// Fsck は git fsck を実行する。force push で参照されなくなったオブジェクトは正常なので報告しない。
// 問題があれば出力とエラーを返す。
func (r Repo) Fsck() (string, error) {
	out, err := r.run("fsck", "--no-progress", "--no-dangling")
	return string(out), err
}

// SetHead はベアリポジトリの HEAD が指すブランチを変える。clone したときの既定のブランチになる。
func (r Repo) SetHead(branch string) error {
	_, err := r.run("symbolic-ref", "HEAD", "refs/heads/"+branch)
	return err
}
