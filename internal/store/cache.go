package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// イベントのローカルキャッシュ。
//
// イベントのファイルは追記だけで書き換えられないので、ファイル名ごとに中身を覚えておけば、
// 画面を出すたびに events フォルダの一覧だけを見て、知らないファイルだけを読み足せばよい。
// ファイルサーバ越しの JSON の読み込みを減らすのが目的で、フォルダの一覧は毎回見る。
//
// キャッシュは端末側（%LOCALAPPDATA%\github-ppoi\cache\<owner>\<リポジトリ名>.json）にも書き、
// 起動時に読む。消えていても次の表示で読み直すだけで、動作には影響しない。

// キャッシュをディスクに書く間隔の下限。短い間に何度も画面を出しても毎回は書かない。
const cacheSaveInterval = 5 * time.Second

// cacheFormat はキャッシュの形式の版。Event に項目を足したときなどに上げる。
// 版が違うキャッシュは読まずに捨て、共有フォルダから読み直す。
const cacheFormat = 2

// ownWriteGrace は自分が書いたイベントを、フォルダの一覧に出てこなくても信じ続ける時間。
// SMB クライアントは一覧を数秒〜十数秒キャッシュするので、書いた直後の一覧には自分の
// ファイルが無いことがある。その間に捨てると、書いた直後の GetIssue でイベントが消える。
const ownWriteGrace = 60 * time.Second

type repoCache struct {
	Format int                      `json:"format"`
	Issues map[int]map[string]Event `json:"issues"` // 番号 → ファイル名 → イベント

	recent map[string]time.Time // 自分が書いたイベントのファイル名 → 書いた時刻。ディスクには書かない

	loaded    bool
	dirty     bool
	lastSaved time.Time
}

type eventCache struct {
	mu    sync.Mutex
	dir   string // 空ならメモリだけ
	repos map[string]*repoCache
}

// SetCacheDir はキャッシュを書くフォルダを決める。空なら書かない（メモリだけ）。
func (s *Store) SetCacheDir(dir string) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	s.cache.dir = dir
	s.cache.repos = map[string]*repoCache{}
}

func (s *Store) cachePath(repo string) string {
	return filepath.Join(s.cache.dir, repo+".json")
}

// repoCacheLocked はリポジトリのキャッシュを返す。無ければディスクから読むか、空で作る。
func (s *Store) repoCacheLocked(repo string) *repoCache {
	if s.cache.repos == nil {
		s.cache.repos = map[string]*repoCache{}
	}
	rc := s.cache.repos[repo]
	if rc == nil {
		rc = &repoCache{Format: cacheFormat, Issues: map[int]map[string]Event{}}
		s.cache.repos[repo] = rc
	}
	if !rc.loaded {
		rc.loaded = true
		if s.cache.dir != "" {
			if data, err := os.ReadFile(s.cachePath(repo)); err == nil {
				var saved repoCache
				if json.Unmarshal(data, &saved) == nil && saved.Format == cacheFormat && saved.Issues != nil {
					rc.Issues = saved.Issues
				}
			}
		}
		rc.lastSaved = time.Now()
	}
	return rc
}

// cachedEvents はキャッシュを使ってイベントを読む。
// events フォルダの一覧を見て、キャッシュに無いファイルだけ読み足し、無くなったファイルは捨てる。
func (s *Store) cachedEvents(repo string, number int) ([]Event, error) {
	dir := s.eventsDir(repo, number)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		s.forgetIssue(repo, number)
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}

	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	rc := s.repoCacheLocked(repo)
	known := rc.Issues[number]
	if known == nil {
		known = map[string]Event{}
		rc.Issues[number] = known
	}
	present := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		present[name] = true
		if _, ok := known[name]; ok {
			continue
		}
		var ev Event
		if err := readJSON(filepath.Join(dir, name), &ev); err != nil {
			continue
		}
		ev.File = name
		known[name] = ev
		rc.dirty = true
	}
	for name := range known {
		if present[name] {
			delete(rc.recent, name)
			continue
		}
		// 一覧に無い。自分が書いた直後なら一覧の遅れとみなして残す
		if t, ok := rc.recent[name]; ok && time.Since(t) < ownWriteGrace {
			continue
		}
		delete(rc.recent, name)
		delete(known, name)
		rc.dirty = true
	}
	events := make([]Event, 0, len(known))
	for name, ev := range known {
		ev.File = name
		events = append(events, ev)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].File < events[j].File })
	s.saveCacheLocked(repo, rc)
	return events, nil
}

// rememberEvent は自分が書いたイベントをキャッシュに入れる。読み直さなくて済む。
func (s *Store) rememberEvent(repo string, number int, name string, ev Event) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	rc := s.repoCacheLocked(repo)
	if rc.Issues[number] == nil {
		rc.Issues[number] = map[string]Event{}
	}
	ev.File = name
	rc.Issues[number][name] = ev
	if rc.recent == nil {
		rc.recent = map[string]time.Time{}
	}
	rc.recent[name] = time.Now()
	rc.dirty = true
}

// forgetIssue は無くなった Issue をキャッシュから外す。
func (s *Store) forgetIssue(repo string, number int) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	rc := s.repoCacheLocked(repo)
	if _, ok := rc.Issues[number]; ok {
		delete(rc.Issues, number)
		rc.dirty = true
	}
}

// saveCacheLocked は変更があり、前回の書き込みから間が空いていればディスクに書く。
func (s *Store) saveCacheLocked(repo string, rc *repoCache) {
	if s.cache.dir == "" || !rc.dirty || time.Since(rc.lastSaved) < cacheSaveInterval {
		return
	}
	s.flushCacheLocked(repo, rc)
}

func (s *Store) flushCacheLocked(repo string, rc *repoCache) {
	if s.cache.dir == "" || !rc.dirty {
		return
	}
	if err := os.MkdirAll(s.cache.dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(rc)
	if err != nil {
		return
	}
	if err := os.WriteFile(s.cachePath(repo), data, 0o644); err != nil {
		return
	}
	rc.dirty = false
	rc.lastSaved = time.Now()
}

// FlushCache は全リポジトリのキャッシュをディスクに書く。終了時などに呼ぶ。
func (s *Store) FlushCache() {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	for repo, rc := range s.cache.repos {
		s.flushCacheLocked(repo, rc)
	}
}

// CacheStats はキャッシュに入っている Issue の数とイベントの数を返す（確認用）。
func (s *Store) CacheStats(repo string) (issues, events int) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	rc := s.repoCacheLocked(repo)
	for _, m := range rc.Issues {
		issues++
		events += len(m)
	}
	return
}

// issueNumbersFromCache はキャッシュにある Issue 番号を返す（確認用）。
func (s *Store) issueNumbersFromCache(repo string) []int {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	rc := s.repoCacheLocked(repo)
	var out []int
	for n := range rc.Issues {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}
