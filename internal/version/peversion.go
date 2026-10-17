package version

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

// Windows の exe に埋め込まれた VERSIONINFO を読む。
// Windows の API を使わず、PE のリソース（.rsrc）から VS_FIXEDFILEINFO を探す。
// 配布用の exe の版を比べるためだけに使うので、ファイルの版（dwFileVersion）だけを返す。

// VS_FIXEDFILEINFO の先頭にある印。
const fixedFileInfoSignature = 0xFEEF04BD

// ErrNoVersionInfo は exe に VERSIONINFO が無いときのエラー。
var ErrNoVersionInfo = errors.New("VERSIONINFO が無い")

// ReadFileVersion は exe のファイルの版（数値4組）を返す。
func ReadFileVersion(path string) (major, minor, patch, build int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer f.Close()
	pf, err := pe.NewFile(f)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	sec := pf.Section(".rsrc")
	if sec == nil {
		return 0, 0, 0, 0, ErrNoVersionInfo
	}
	data, err := io.ReadAll(io.LimitReader(sec.Open(), 16<<20))
	if err != nil {
		return 0, 0, 0, 0, err
	}
	// VS_FIXEDFILEINFO は 4 バイト境界に置かれる。印を探して、その後ろの dwFileVersionMS と dwFileVersionLS を読む
	sig := make([]byte, 4)
	binary.LittleEndian.PutUint32(sig, fixedFileInfoSignature)
	for off := 0; off+16 <= len(data); off += 4 {
		if !bytes.Equal(data[off:off+4], sig) {
			continue
		}
		// 次の 4 バイトは dwStrucVersion。その次が dwFileVersionMS、dwFileVersionLS
		ms := binary.LittleEndian.Uint32(data[off+8:])
		ls := binary.LittleEndian.Uint32(data[off+12:])
		return int(ms >> 16), int(ms & 0xffff), int(ls >> 16), int(ls & 0xffff), nil
	}
	return 0, 0, 0, 0, ErrNoVersionInfo
}
