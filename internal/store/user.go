package store

import "path/filepath"

type userMeta struct {
	Name string `json:"name"`
}

// DisplayName はログオン名に対する表示名を返す。
// meta\users\<ログオン名>.json に登録が無ければログオン名をそのまま返す。
func (s *Store) DisplayName(login string) string {
	if !isSafeName(login) {
		return login
	}
	var u userMeta
	if err := readJSON(filepath.Join(s.metaDir(), "users", login+".json"), &u); err != nil || u.Name == "" {
		return login
	}
	return u.Name
}
