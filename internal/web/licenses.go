package web

import (
	"io/fs"
	"net/http"

	thirdparty "github-ppoi-for-fileserver/third_party"
)

type licenseRow struct {
	thirdparty.Component
	Text string
}

// handleLicenses は使用している OSS とライセンス文の一覧を出す。
func (s *Server) handleLicenses(w http.ResponseWriter, r *http.Request) {
	var rows []licenseRow
	for _, c := range thirdparty.Components {
		text, err := fs.ReadFile(thirdparty.LicenseFS, c.File)
		if err != nil {
			text = []byte(err.Error())
		}
		rows = append(rows, licenseRow{Component: c, Text: string(text)})
	}
	p := s.newPage(s.tr("ppoi.licenses.title"))
	p.Data = rows
	s.render(w, http.StatusOK, "licenses", p)
}
