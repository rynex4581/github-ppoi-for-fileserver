package gitcmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Clone はツール専用の clone。%LOCALAPPDATA%\github-ppoi\repos\<owner>\<リポジトリ名> に置き、
// マージと Revert のときだけ使う。無ければ作り、使うたびにリモートに強制的に合わせる。
// 各自の作業 clone とは別物で、消えてよい。
type Clone struct {
	Dir       string // clone の置き場所
	Remote    string // ベアリポジトリのパス
	UserName  string // コミットの作者名
	UserEmail string
}

// ErrConflict はマージや Revert がコンフリクトしたときのエラー。
var ErrConflict = errors.New("コンフリクト")

// ErrPushRejected は push が弾かれ続けたときのエラー。
var ErrPushRejected = errors.New("push が弾かれた")

// push が弾かれたときにやり直す回数。
const pushRetries = 3

// noHooksReceivePack は push 先の receive-pack を hooks 無しで起動させる指定。
// runGit の -c は receive-pack に引き継がれないため、起動コマンドの側で付ける。
// core.longpaths も同じ理由でここに付ける（共有フォルダの深いパスで pack の一時名が 260 文字を超える）。
// パスに空白が入り得るので引用符で囲む（git はこの文字列を sh で解釈する）。
func noHooksReceivePack() string {
	return "--receive-pack=git -c \"core.hooksPath=" + noHooksDir + "\" -c core.longpaths=true receive-pack"
}

func (c Clone) git(args ...string) ([]byte, error) {
	extra := []string{"-c", "user.name=" + c.UserName, "-c", "user.email=" + c.UserEmail}
	out, _, err := runGit(c.Dir, extra, args...)
	return out, err
}

// Ensure は clone が無ければ作る。
func (c Clone) Ensure() error {
	if _, err := os.Stat(filepath.Join(c.Dir, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.Dir), 0o755); err != nil {
		return err
	}
	_, _, err := runGit(filepath.Dir(c.Dir), nil, "clone", "--quiet", "--no-checkout", c.Remote, c.Dir)
	return err
}

// sync はリモートを取り込み、branch を origin/branch に強制的に合わせて作業ツリーに出す。
func (c Clone) sync(branch string) error {
	if err := c.Ensure(); err != nil {
		return err
	}
	if _, err := c.git("fetch", "--quiet", "--prune", "origin"); err != nil {
		return err
	}
	if _, err := c.git("checkout", "--quiet", "--force", "-B", branch, "origin/"+branch); err != nil {
		return err
	}
	_, err := c.git("reset", "--quiet", "--hard", "origin/"+branch)
	return err
}

// push は branch を push する。弾かれたら ErrPushRejected を返す。
func (c Clone) push(branch string) error {
	_, err := c.git("push", "--quiet", noHooksReceivePack(), "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "rejected") || strings.Contains(msg, "failed to push") || strings.Contains(msg, "fetch first") {
			return ErrPushRejected
		}
		return err
	}
	return nil
}

func (c Clone) head() (string, error) {
	out, err := c.git("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Merge は origin/head を base に --no-ff でマージして push し、マージコミットの SHA を返す。
// 2台が同時にマージして push が弾かれたら、取り込み直してやり直す。
// コンフリクトすれば ErrConflict を返し、作業ツリーは元に戻す。
func (c Clone) Merge(base, head, message string) (string, error) {
	for attempt := 0; attempt < pushRetries; attempt++ {
		if err := c.sync(base); err != nil {
			return "", err
		}
		if _, err := c.git("merge", "--no-ff", "--no-edit", "-m", message, "origin/"+head); err != nil {
			c.git("merge", "--abort")
			if strings.Contains(err.Error(), "CONFLICT") || strings.Contains(err.Error(), "conflict") {
				return "", ErrConflict
			}
			return "", err
		}
		sha, err := c.head()
		if err != nil {
			return "", err
		}
		err = c.push(base)
		if err == nil {
			return sha, nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return "", err
		}
	}
	return "", ErrPushRejected
}

// ErrTagExists は同名のタグが既にあるときのエラー。
var ErrTagExists = errors.New("同じ名前のタグが既にある")

// CreateTag は target（ブランチ名か SHA）にタグを打って push し、タグが指すコミットの SHA を返す。
// 既に同名のタグがリモートにあれば ErrTagExists。
func (c Clone) CreateTag(name, target string) (string, error) {
	if err := c.Ensure(); err != nil {
		return "", err
	}
	// --prune-tags で手元のタグをリモートと揃える（前回の失敗で残った手元だけのタグも消える）
	if _, err := c.git("fetch", "--quiet", "--prune", "--prune-tags", "origin"); err != nil {
		return "", err
	}
	if _, err := c.git("rev-parse", "--verify", "--quiet", "--end-of-options", "refs/tags/"+name); err == nil {
		return "", ErrTagExists
	}
	ref := "origin/" + target
	if _, err := c.git("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		ref = target // ブランチでなければ SHA とみなす
	}
	out, err := c.git("rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", ErrNotFound
	}
	sha := strings.TrimSpace(string(out))
	if _, err := c.git("tag", "--end-of-options", name, sha); err != nil {
		return "", err
	}
	if _, err := c.git("push", "--quiet", noHooksReceivePack(), "origin", "refs/tags/"+name+":refs/tags/"+name); err != nil {
		c.git("tag", "-d", name)
		if strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "already exists") {
			return "", ErrPushRejected
		}
		return "", err
	}
	return sha, nil
}

// DeleteRemoteBranch はリモートのブランチを消す。
func (c Clone) DeleteRemoteBranch(name string) error {
	_, err := c.git("push", "--quiet", noHooksReceivePack(), "origin", ":refs/heads/"+name)
	return err
}

// Revert はマージコミットを打ち消すコミットを base に作って push し、その SHA を返す。
func (c Clone) Revert(base, mergeSHA, message string) (string, error) {
	for attempt := 0; attempt < pushRetries; attempt++ {
		if err := c.sync(base); err != nil {
			return "", err
		}
		if _, err := c.git("revert", "--no-commit", "-m", "1", mergeSHA); err != nil {
			c.git("revert", "--abort")
			if strings.Contains(err.Error(), "CONFLICT") || strings.Contains(err.Error(), "conflict") {
				return "", ErrConflict
			}
			return "", err
		}
		if _, err := c.git("commit", "--quiet", "-m", message); err != nil {
			c.git("reset", "--quiet", "--hard", "origin/"+base)
			return "", err
		}
		sha, err := c.head()
		if err != nil {
			return "", err
		}
		err = c.push(base)
		if err == nil {
			return sha, nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return "", err
		}
	}
	return "", ErrPushRejected
}
