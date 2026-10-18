package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// goversioninfo が実際に出した VERSIONINFO 付きの exe を peversion が正しく読めるかを確かめる。
// goversioninfo が PATH に無ければ飛ばす（CI では先に入れてから test を回す）。
func TestReadFileVersionFromRealExe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows でだけ確かめる")
	}
	gvi, err := exec.LookPath("goversioninfo")
	if err != nil {
		t.Skip("goversioninfo が無い")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module peversiontest\n\ngo 1.22\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("versioninfo.json", `{
  "FixedFileInfo": {
    "FileVersion": {"Major": 3, "Minor": 21, "Patch": 5, "Build": 0},
    "ProductVersion": {"Major": 3, "Minor": 21, "Patch": 5, "Build": 0}
  },
  "StringFileInfo": {"FileVersion": "Ver 3.21E", "ProductVersion": "Ver 3.21E", "ProductName": "peversiontest"},
  "VarFileInfo": {"Translation": {"LangID": "0411", "CharsetID": "04B0"}}
}`)
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run(gvi, "-64", "-o", "resource.syso", "versioninfo.json")
	run("go", "build", "-o", "t.exe", ".")

	major, minor, patch, build, err := ReadFileVersion(filepath.Join(dir, "t.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if major != 3 || minor != 21 || patch != 5 || build != 0 {
		t.Fatalf("読めた版が違う: %d.%d.%d.%d", major, minor, patch, build)
	}
	p := FromNumeric(major, minor, patch)
	if p.String() != "3.21E" {
		t.Fatalf("表記に戻すと %s", p.String())
	}
}
