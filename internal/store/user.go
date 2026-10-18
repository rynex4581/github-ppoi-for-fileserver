package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// User は利用者。ログオン名が識別子で、表示名は meta\users\<ログオン名>.json に持つ。
type User struct {
	Login string `json:"-"`
	Name  string `json:"name"`
}

// DisplayName はログオン名に対する表示名を返す。
// meta\users\<ログオン名>.json に登録が無ければログオン名をそのまま返す。
func (s *Store) DisplayName(login string) string {
	if !isSafeName(login) {
		return login
	}
	var u User
	if err := readJSON(s.userPath(login), &u); err != nil || u.Name == "" {
		return login
	}
	return u.Name
}

func (s *Store) userPath(login string) string {
	return filepath.Join(s.metaDir(), "users", login+".json")
}

// ListUsers は meta\users 配下にファイルのある利用者をログオン名順で返す。担当者の候補に使う。
func (s *Store) ListUsers() ([]User, error) {
	entries, err := os.ReadDir(filepath.Join(s.metaDir(), "users"))
	if err != nil {
		return nil, err
	}
	var users []User
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		login := strings.TrimSuffix(e.Name(), ".json")
		u := User{Login: login}
		_ = readJSON(s.userPath(login), &u)
		u.Login = login
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool {
		return strings.ToLower(users[i].Login) < strings.ToLower(users[j].Login)
	})
	return users, nil
}

// SetUserName は利用者の表示名を書く。自分のファイルだけを書くので他の端末と競合しない。
func (s *Store) SetUserName(login, name string) error {
	if !isSafeName(login) {
		return errors.New("ログオン名に使えない文字が含まれている")
	}
	if err := s.writable(); err != nil {
		return err
	}
	return writeJSONAtomic(s.userPath(login), User{Name: strings.TrimSpace(name)})
}

// EnsureUser は利用者のファイルが無ければ表示名なしで作る。
// これにより、一度でもツールを起動した人が担当者の候補に出る。
func (s *Store) EnsureUser(login string) error {
	if !isSafeName(login) {
		return errors.New("ログオン名に使えない文字が含まれている")
	}
	_, err := os.Stat(s.userPath(login))
	if err == nil || s.readOnly {
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeJSONAtomic(s.userPath(login), User{})
}
