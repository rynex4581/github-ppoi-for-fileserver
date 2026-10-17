package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github-ppoi-for-fileserver/internal/store"
)

// 本文に入る添付の印。貼り付けのたびに JS が ![image](attachment:1) の形で入れ、送信時に本物の URL に置き換える。
var attachmentMark = regexp.MustCompile(`\(attachment:(\d+)\)`)

// multipart の読み込みでメモリに置く上限。超えた分は一時ファイルに落ちる。
const multipartMemory = 8 << 20

// errAttachmentTooLarge は画面向けのメッセージを作るために store のエラーを包む。
type attachmentError struct {
	msg string
}

func (e attachmentError) Error() string { return e.msg }

// saveAttachments はフォームで一緒に送られた添付ファイルを Issue の配下に保存し、
// 本文の (attachment:N) を /attachments/<owner>/<repo>/<番号>/<ファイル名> に置き換えて返す。
// N はフォームの attachments の順番（1 から）。対応するファイルが無い印はそのまま残す。
func (s *Server) saveAttachments(st *store.Store, repo store.Repo, number int, r *http.Request, body string) (string, error) {
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(multipartMemory); err != nil {
			if strings.Contains(err.Error(), "multipart") {
				return body, nil // 添付の無い通常のフォーム
			}
			return body, err
		}
	}
	if r.MultipartForm == nil {
		return body, nil
	}
	files := r.MultipartForm.File["attachments"]
	if len(files) == 0 {
		return body, nil
	}
	urls := map[string]string{}
	for i, fh := range files {
		if fh.Size > st.AttachmentMaxBytes() {
			return body, attachmentError{s.tr("ppoi.attach.too_large", st.AttachmentMaxBytes()/(1024*1024))}
		}
		f, err := fh.Open()
		if err != nil {
			return body, err
		}
		data, err := io.ReadAll(io.LimitReader(f, st.AttachmentMaxBytes()+1))
		f.Close()
		if err != nil {
			return body, err
		}
		name, err := st.SaveAttachment(repo.Name, number, fh.Filename, data)
		if errors.Is(err, store.ErrAttachmentTooLarge) {
			return body, attachmentError{s.tr("ppoi.attach.too_large", st.AttachmentMaxBytes()/(1024*1024))}
		}
		if err != nil {
			return body, err
		}
		urls[strconv.Itoa(i+1)] = fmt.Sprintf("/attachments/%s/%s/%d/%s", url.PathEscape(repo.Owner), url.PathEscape(repo.Name), number, url.PathEscape(name))
	}
	return attachmentMark.ReplaceAllStringFunc(body, func(m string) string {
		n := attachmentMark.FindStringSubmatch(m)[1]
		if u, ok := urls[n]; ok {
			return "(" + u + ")"
		}
		return m
	}), nil
}

// handleAttachment は /attachments/<owner>/<repo>/<番号>/<ファイル名> を返す。
// 画像は画像として、それ以外はダウンロードとして返す。
func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/attachments/"), "/")
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	st := s.storeByOwner(parts[0])
	number, err := strconv.Atoi(parts[2])
	if st == nil || err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := st.AttachmentPath(parts[1], number, parts[3])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctype := "application/octet-stream"
	switch strings.ToLower(path.Ext(parts[3])) {
	case ".png":
		ctype = "image/png"
	case ".jpg", ".jpeg":
		ctype = "image/jpeg"
	case ".gif":
		ctype = "image/gif"
	case ".webp":
		ctype = "image/webp"
	case ".bmp":
		ctype = "image/bmp"
	case ".txt", ".log", ".csv", ".md":
		ctype = "text/plain; charset=utf-8"
	default:
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(parts[3]))
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeFile(w, r, p)
}
