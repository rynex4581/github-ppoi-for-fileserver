package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github-ppoi-for-fileserver/internal/localconfig"
	"github-ppoi-for-fileserver/internal/store"
)

// 新着の印（設計メモ 3.10）。
//
// Issue か PR の詳細を開いたとき、その時点で一番新しいイベントのファイル名を「前回開いた位置」として
// 端末側の %LOCALAPPDATA%\github-ppoi\seen.json に書く。一覧では、自分が関わる Issue に
// その位置より新しい他人のイベントがあれば印を付ける。ファイルが消えても、開き直せば直る。
// イベントのファイル名は UTC 時刻で始まるので、文字列の比較で新旧が分かる。

type seenStore struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]string // "owner/リポジトリ名#番号" → 前回開いたときの一番新しいイベントのファイル名
}

func seenKey(owner, repo string, number int) string {
	return owner + "/" + repo + "#" + strconv.Itoa(number)
}

func seenPath() string {
	dir, err := localconfig.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "seen.json")
}

func (ss *seenStore) loadLocked() {
	if ss.loaded {
		return
	}
	ss.loaded = true
	ss.m = map[string]string{}
	if p := seenPath(); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			json.Unmarshal(data, &ss.m)
		}
	}
}

// get は前回開いた位置を返す。開いたことが無ければ空。
func (ss *seenStore) get(key string) string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.loadLocked()
	return ss.m[key]
}

// set は前回開いた位置を更新してディスクに書く。書けなくても何もしない（印が残るだけ）。
func (ss *seenStore) set(key, file string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.loadLocked()
	if ss.m[key] == file {
		return
	}
	ss.m[key] = file
	p := seenPath()
	if p == "" {
		return
	}
	data, err := json.MarshalIndent(ss.m, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	os.WriteFile(p, data, 0o644)
}

// newestEventFile は Issue の一番新しいイベントのファイル名を返す。
func newestEventFile(is store.Issue) string {
	newest := ""
	for _, ev := range is.Events {
		if ev.File > newest {
			newest = ev.File
		}
	}
	return newest
}

// markSeen は詳細を開いたときに呼ぶ。今の一番新しいイベントを前回開いた位置にする。
func (s *Server) markSeen(st *store.Store, repo store.Repo, is store.Issue) {
	s.seen.set(seenKey(st.Config().Owner, repo.Name, is.Number), newestEventFile(is))
}

// involved は自分が関わる Issue か（自分が作成、担当、レビューまたはコメントしたもの）。
func involved(is store.Issue, login string) bool {
	if strings.EqualFold(is.User, login) || is.HasAssignee(login) {
		return true
	}
	for _, ev := range is.Events {
		switch ev.Type {
		case store.EventComment, store.EventReview, store.EventReviewComment:
			if strings.EqualFold(ev.User, login) {
				return true
			}
		}
	}
	return false
}

// isNew は一覧で新着の印を付けるかを返す。自分が関わる Issue に、前回開いたあとに増えた
// 他人のイベントがあれば真。一度も開いていなければ、他人のイベントが1つでもあれば真。
func (s *Server) isNew(owner string, repoName string, is store.Issue) bool {
	if !involved(is, s.login) {
		return false
	}
	last := s.seen.get(seenKey(owner, repoName, is.Number))
	for _, ev := range is.Events {
		if ev.File > last && !strings.EqualFold(ev.User, s.login) {
			return true
		}
	}
	return false
}
