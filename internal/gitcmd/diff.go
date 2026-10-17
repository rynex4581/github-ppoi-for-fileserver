package gitcmd

import (
	"strconv"
	"strings"
)

// 1ファイルの差分として表示する行数の上限。超えたら本文を出さない。
const maxDiffLinesPerFile = 3000

// DiffFile は差分の1ファイル分。
type DiffFile struct {
	OldPath   string
	NewPath   string
	Status    string // added / deleted / modified / renamed
	IsBinary  bool
	TooLarge  bool
	Additions int
	Deletions int
	Hunks     []DiffHunk
}

// Path は表示用のパス。改名なら「旧 → 新」。
func (f DiffFile) Path() string {
	if f.Status == "renamed" && f.OldPath != f.NewPath {
		return f.OldPath + " → " + f.NewPath
	}
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// DiffHunk は @@ で始まる1つの塊。
type DiffHunk struct {
	Header string
	Lines  []DiffLine
}

// DiffLine は差分の1行。
type DiffLine struct {
	Kind   string // context / add / del
	OldNum int    // 0 なら無し
	NewNum int
	Text   string
}

// ParseDiff は git の unified diff を解析する。
func ParseDiff(s string) []DiffFile {
	var files []DiffFile
	var cur *DiffFile
	var hunk *DiffHunk
	oldNum, newNum := 0, 0
	lines := strings.Split(s, "\n")

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			total := 0
			for _, h := range cur.Hunks {
				total += len(h.Lines)
			}
			if total > maxDiffLinesPerFile {
				cur.TooLarge = true
				cur.Hunks = nil
			}
			files = append(files, *cur)
		}
		cur = nil
	}

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &DiffFile{Status: "modified"}
			// "diff --git a/x b/y"。空白を含むパスは後続の ---/+++ 行で上書きされる
			rest := strings.TrimPrefix(line, "diff --git ")
			if a, b, ok := strings.Cut(rest, " b/"); ok {
				cur.OldPath = strings.TrimPrefix(a, "a/")
				cur.NewPath = b
			}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case strings.HasPrefix(line, "rename from "):
			cur.Status = "renamed"
			cur.OldPath = strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "rename to "):
			cur.NewPath = strings.TrimPrefix(line, "rename to ")
		case strings.HasPrefix(line, "Binary files "):
			cur.IsBinary = true
		case strings.HasPrefix(line, "--- "):
			if p := strings.TrimPrefix(line, "--- "); p != "/dev/null" {
				cur.OldPath = strings.TrimPrefix(p, "a/")
			}
		case strings.HasPrefix(line, "+++ "):
			if p := strings.TrimPrefix(line, "+++ "); p != "/dev/null" {
				cur.NewPath = strings.TrimPrefix(p, "b/")
			}
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			hunk = &DiffHunk{Header: line}
			oldNum, newNum = parseHunkHeader(line)
		case hunk == nil:
			continue
		case strings.HasPrefix(line, "+"):
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: "add", NewNum: newNum, Text: line[1:]})
			newNum++
			cur.Additions++
		case strings.HasPrefix(line, "-"):
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: "del", OldNum: oldNum, Text: line[1:]})
			oldNum++
			cur.Deletions++
		case strings.HasPrefix(line, " "):
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: "context", OldNum: oldNum, NewNum: newNum, Text: line[1:]})
			oldNum++
			newNum++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" は表示しない
		}
	}
	flushFile()
	return files
}

// parseHunkHeader は "@@ -a,b +c,d @@" から旧側と新側の開始行を取る。
func parseHunkHeader(h string) (int, int) {
	parts := strings.Fields(h)
	if len(parts) < 3 {
		return 0, 0
	}
	start := func(s string) int {
		s = strings.TrimLeft(s, "-+")
		if i := strings.Index(s, ","); i >= 0 {
			s = s[:i]
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	return start(parts[1]), start(parts[2])
}

// DiffStat は差分全体の集計。
type DiffStat struct {
	Files     int
	Additions int
	Deletions int
}

// Stat は差分全体の集計を返す。
func Stat(files []DiffFile) DiffStat {
	var s DiffStat
	for _, f := range files {
		s.Files++
		s.Additions += f.Additions
		s.Deletions += f.Deletions
	}
	return s
}
