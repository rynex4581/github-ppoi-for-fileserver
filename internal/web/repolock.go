package web

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// repoLocks はリポジトリごとの排他。マージと Revert は同じツール専用 clone を使うので、
// 同じリポジトリの2つの PR を同時に操作すると作業ツリーを取り合う。
// プロセス内は Mutex、プロセス間（二重起動の取りこぼし等）は clone の隣の .lock ファイルで排他する。
type repoLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// lockFileStale はこれより古い .lock を落ちたプロセスの残骸と見なす。
const lockFileStale = 10 * time.Minute

func (l *repoLocks) mutex(key string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}
	m := l.locks[key]
	if m == nil {
		m = &sync.Mutex{}
		l.locks[key] = m
	}
	return m
}

// lock はリポジトリの排他を取り、解放する関数を返す。
// 別のプロセスが握っていれば待たずに 409 で返す。
func (l *repoLocks) lock(st *store.Store, repo store.Repo) (func(), error) {
	key := st.Config().Owner + "/" + repo.Name
	m := l.mutex(key)
	m.Lock()
	dir, err := localconfig.Dir()
	if err != nil {
		m.Unlock()
		return nil, err
	}
	path := filepath.Join(dir, "repos", st.Config().Owner, repo.Name+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.Unlock()
		return nil, err
	}
	if err := createLockFile(path); err != nil {
		m.Unlock()
		return nil, opErr(409, "ppoi.pulls.busy")
	}
	return func() {
		os.Remove(path)
		m.Unlock()
	}, nil
}

// createLockFile は .lock を排他作成する。古い残骸があれば消してもう一度試す。
func createLockFile(path string) error {
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			f.Close()
			return nil
		}
		info, statErr := os.Stat(path)
		if statErr != nil || time.Since(info.ModTime()) < lockFileStale {
			return err
		}
		os.Remove(path)
	}
	return os.ErrExist
}
