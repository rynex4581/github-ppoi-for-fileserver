// mkversioninfo は internal/version の Number から、goversioninfo が読む versioninfo.json を作る。
// cmd/github-ppoi の go:generate から呼ぶ。出力先は引数で指定する。
//
// 数値4組の欄は英字を数字に置き換え（0.11A→0.11.1.0）、文字列の欄には "Ver 0.11A" を入れる。
// アイコンは internal/web/static/favicon.ico（ロゴを正方形にしたもの）を exe にも使う。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github-ppoi-for-fileserver/internal/version"
)

type numeric struct {
	Major int `json:"Major"`
	Minor int `json:"Minor"`
	Patch int `json:"Patch"`
	Build int `json:"Build"`
}

type fixedFileInfo struct {
	FileVersion    numeric `json:"FileVersion"`
	ProductVersion numeric `json:"ProductVersion"`
	FileFlagsMask  string  `json:"FileFlagsMask"`
	FileFlags      string  `json:"FileFlags"`
	FileOS         string  `json:"FileOS"`
	FileType       string  `json:"FileType"`
	FileSubType    string  `json:"FileSubType"`
}

type stringFileInfo struct {
	Comments         string `json:"Comments"`
	CompanyName      string `json:"CompanyName"`
	FileDescription  string `json:"FileDescription"`
	FileVersion      string `json:"FileVersion"`
	InternalName     string `json:"InternalName"`
	LegalCopyright   string `json:"LegalCopyright"`
	LegalTrademarks  string `json:"LegalTrademarks"`
	OriginalFilename string `json:"OriginalFilename"`
	PrivateBuild     string `json:"PrivateBuild"`
	ProductName      string `json:"ProductName"`
	ProductVersion   string `json:"ProductVersion"`
	SpecialBuild     string `json:"SpecialBuild"`
}

type translation struct {
	LangID    string `json:"LangID"`
	CharsetID string `json:"CharsetID"`
}

type versionInfo struct {
	FixedFileInfo  fixedFileInfo  `json:"FixedFileInfo"`
	StringFileInfo stringFileInfo `json:"StringFileInfo"`
	VarFileInfo    struct {
		Translation translation `json:"Translation"`
	} `json:"VarFileInfo"`
	IconPath string `json:"IconPath"` // exe のアイコン。画面の favicon と同じファイルを使う
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "使い方: mkversioninfo <出力先の versioninfo.json>")
		os.Exit(2)
	}
	major, minor, patch, build := version.Current.Numeric()
	n := numeric{Major: major, Minor: minor, Patch: patch, Build: build}
	v := versionInfo{
		FixedFileInfo: fixedFileInfo{
			FileVersion: n, ProductVersion: n,
			FileFlagsMask: "3f", FileFlags: "00", FileOS: "040004", FileType: "01", FileSubType: "00",
		},
		StringFileInfo: stringFileInfo{
			FileDescription:  "github-ppoi for Fileserver",
			FileVersion:      version.Display,
			InternalName:     "github-ppoi",
			LegalCopyright:   "MIT License",
			OriginalFilename: "github-ppoi.exe",
			ProductName:      "github-ppoi for Fileserver",
			ProductVersion:   version.Display,
		},
	}
	v.VarFileInfo.Translation = translation{LangID: "0411", CharsetID: "04B0"}
	v.IconPath = "../../internal/web/static/favicon.ico"
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
