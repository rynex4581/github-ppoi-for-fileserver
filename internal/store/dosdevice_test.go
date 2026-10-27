package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeDOS は DOS デバイス名の定義を端末に作らず、メモリ上で模す。
type fakeDOS struct {
	defs       map[string][]string // 名前 → 定義先（新しい順、\??\ の形）
	defines    int
	removes    int
	failDefine bool
}

func newFakeDOS(t *testing.T) *fakeDOS {
	t.Helper()
	f := &fakeDOS{defs: map[string][]string{}}
	saveDefine, saveRemove, saveQuery := defineDOSDevice, removeDOSDevice, queryDOSDevice
	t.Cleanup(func() { defineDOSDevice, removeDOSDevice, queryDOSDevice = saveDefine, saveRemove, saveQuery })
	defineDOSDevice = func(name, target string) error {
		if f.failDefine {
			return errors.New("拒否された")
		}
		f.defines++
		f.defs[name] = append([]string{`\??\UNC\` + strings.TrimPrefix(target, `\\`)}, f.defs[name]...)
		return nil
	}
	removeDOSDevice = func(name string) error {
		f.removes++
		if len(f.defs[name]) == 0 {
			return errors.New("定義が無い")
		}
		f.defs[name] = f.defs[name][1:]
		if len(f.defs[name]) == 0 {
			delete(f.defs, name)
		}
		return nil
	}
	queryDOSDevice = func(name string) ([]string, error) {
		if d, ok := f.defs[name]; ok {
			return d, nil
		}
		return nil, errors.New("定義が無い")
	}
	return f
}

func TestUNCTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC パスは Windows のみ")
	}
	for _, c := range []struct {
		root   string
		target string
		ok     bool
	}{
		{`\\fs01\share\github-ppoi`, `\\fs01\share\github-ppoi`, true},
		{`\\fs01\share\github-ppoi\`, `\\fs01\share\github-ppoi`, true},
		{`\\fs01\share\`, `\\fs01\share`, true},
		{`//fs01/share/github-ppoi`, `\\fs01\share\github-ppoi`, true},
		{`C:\data\github-ppoi`, "", false},
		{`Z:\github-ppoi`, "", false},
		{`\\.\github-ppoi-team-a\repos`, "", false},
		{`\\?\UNC\fs01\share\github-ppoi`, "", false},
	} {
		target, ok := uncTarget(c.root)
		if ok != c.ok || target != c.target {
			t.Errorf("uncTarget(%q) = %q, %v。期待は %q, %v", c.root, target, ok, c.target, c.ok)
		}
	}
}

// 名前は github-ppoi-<owner 名> で、GitPath が仮想パスを返す。config.json には何も書かない。
func TestDefineDOSDevice(t *testing.T) {
	f := newFakeDOS(t)
	s := newTestStore(t)
	before, err := os.ReadFile(filepath.Join(s.Root(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.defineDOSDevice(`\\fs01\share\team-a`); err != nil {
		t.Fatal(err)
	}
	if got := f.defs["github-ppoi-team-a"]; len(got) != 1 || got[0] != `\??\UNC\fs01\share\team-a` {
		t.Errorf("定義が違う: %v", f.defs)
	}
	if after, _ := os.ReadFile(filepath.Join(s.Root(), "config.json")); string(after) != string(before) {
		t.Errorf("config.json が書き換わった:\n%s", after)
	}
	if got := s.GitPath(filepath.Join(s.Root(), "repos", "sample.git")); got != `\\.\github-ppoi-team-a\repos\sample.git` {
		t.Errorf("ベアリポジトリの仮想パスが違う: %s", got)
	}
	if got := s.GitPath(s.Root()); got != `\\.\github-ppoi-team-a` {
		t.Errorf("データルートの仮想パスが違う: %s", got)
	}
	outside := filepath.Join(t.TempDir(), "x")
	if got := s.GitPath(outside); got != outside {
		t.Errorf("データルートの外のパスまで変換した: %s", got)
	}
}

// 既に同名の定義があれば、積み重なった分も含めて削除してから定義し直す。
func TestDefineDOSDeviceRedefine(t *testing.T) {
	f := newFakeDOS(t)
	s := newTestStore(t)
	f.defs["github-ppoi-team-a"] = []string{`\??\UNC\fs01\share\stale`, `\??\UNC\fs01\share\older`}
	if err := s.defineDOSDevice(`\\fs01\share\team-a`); err != nil {
		t.Fatal(err)
	}
	if got := f.defs["github-ppoi-team-a"]; len(got) != 1 || got[0] != `\??\UNC\fs01\share\team-a` {
		t.Errorf("古い定義が残っている: %v", got)
	}
	if f.removes != 2 || f.defines != 1 {
		t.Errorf("削除 %d 回、定義 %d 回。期待は 2 回と 1 回", f.removes, f.defines)
	}
}

func TestDefineDOSDeviceErrors(t *testing.T) {
	bare := func(s *Store) string { return filepath.Join(s.Root(), "repos", "sample.git") }

	t.Run("定義の失敗", func(t *testing.T) {
		f := newFakeDOS(t)
		f.failDefine = true
		s := newTestStore(t)
		if err := s.defineDOSDevice(`\\fs01\share\team-a`); err == nil {
			t.Fatal("定義の失敗がエラーにならない")
		}
		if got := s.GitPath(bare(s)); got != bare(s) {
			t.Errorf("失敗したのに仮想パスを返した: %s", got)
		}
	})

	t.Run("末尾が . の owner 名", func(t *testing.T) {
		// 新しくは作れないので、0.21A より前に作られたデータルートとして config.json を直接書く
		f := newFakeDOS(t)
		root := t.TempDir()
		if err := writeJSONAtomic(filepath.Join(root, "config.json"), Config{Format: 1, Owner: "team."}); err != nil {
			t.Fatal(err)
		}
		s, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.defineDOSDevice(`\\fs01\share\team`); err == nil {
			t.Fatal("末尾が . の名前で定義した")
		}
		if f.defines != 0 || f.removes != 0 {
			t.Errorf("定義に触った: 定義 %d 回、削除 %d 回", f.defines, f.removes)
		}
		if got := s.GitPath(bare(s)); got != bare(s) {
			t.Errorf("失敗したのに仮想パスを返した: %s", got)
		}
	})
}

// ローカルパスのデータルートには何もしない。
func TestDefineDOSDeviceLocalRoot(t *testing.T) {
	f := newFakeDOS(t)
	s := newTestStore(t)
	if s.IsUNC() {
		t.Fatal("ローカルパスを UNC と判定した")
	}
	if err := s.DefineDOSDevice(); err != nil || s.DOSDeviceErr() != nil {
		t.Fatal(err, s.DOSDeviceErr())
	}
	if f.defines != 0 {
		t.Errorf("ローカルパスのデータルートに定義した: %v", f.defs)
	}
	p := filepath.Join(s.Root(), "repos", "sample.git")
	if got := s.GitPath(p); got != p {
		t.Errorf("ローカルパスを変換した: %s", got)
	}
}

func TestPeekOwner(t *testing.T) {
	s := newTestStore(t)
	if got := PeekOwner(s.Root()); got != "team-a" {
		t.Errorf("owner 名が違う: %q", got)
	}
	if got := PeekOwner(t.TempDir()); got != "" {
		t.Errorf("config.json の無いフォルダで owner 名を返した: %q", got)
	}
}
