// Package diffparse turns `git diff` output into structured file diffs.
//
// Parsing is two-phase on purpose: the file list is built eagerly so a
// 500-file PR opens instantly, while hunks are parsed only when a file is
// actually opened (see FileDiff.Hunks).
package diffparse

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type Status int

const (
	Modified Status = iota
	Added
	Deleted
	Renamed
	Copied
)

func (s Status) String() string {
	switch s {
	case Added:
		return "added"
	case Deleted:
		return "deleted"
	case Renamed:
		return "renamed"
	case Copied:
		return "copied"
	default:
		return "modified"
	}
}

type LineKind int

const (
	KindContext LineKind = iota
	KindAdd
	KindDel
)

// Line is one row inside a hunk. OldNum/NewNum are 1-based, 0 when the line
// does not exist on that side.
type Line struct {
	Kind      LineKind
	Text      string
	OldNum    int
	NewNum    int
	NoNewline bool // the "\ No newline at end of file" marker followed this line
}

type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Section            string // trailing context after @@, usually the enclosing func
	Lines              []Line
}

// FileDiff is one file's worth of a diff. Hunk bodies are parsed lazily.
type FileDiff struct {
	OldPath  string
	NewPath  string
	Status   Status
	IsBinary bool
	OldMode  string
	NewMode  string

	// Blob hashes from the diff's index line. Notes anchor to NewBlob: when
	// the file changes underneath a note, the hash no longer matches and the
	// note is shown as stale rather than pointing at a line that moved.
	OldBlob string
	NewBlob string

	// Additions/Deletions come from `git diff --numstat`, not from parsing,
	// so they are available without touching hunk bodies.
	Additions int
	Deletions int

	body  []string // raw lines from the first @@ onward
	once  sync.Once
	hunks []Hunk
}

// Path is the path to show in the UI: the new path, except for deletions.
func (f *FileDiff) Path() string {
	if f.Status == Deleted || f.NewPath == "" {
		return f.OldPath
	}
	return f.NewPath
}

// Hunks parses the file body on first call.
func (f *FileDiff) Hunks() []Hunk {
	f.once.Do(func() { f.hunks = parseHunks(f.body) })
	return f.hunks
}

// FillStats counts additions and deletions from the hunks themselves, for
// diffs that did not come with a --numstat companion — a pull request diff
// fetched through gh, for instance. It forces hunk parsing, so call it only
// when the stats are actually going to be shown.
func FillStats(files []*FileDiff) {
	for _, f := range files {
		if f.Additions > 0 || f.Deletions > 0 || f.IsBinary {
			continue
		}
		for _, h := range f.Hunks() {
			for _, l := range h.Lines {
				switch l.Kind {
				case KindAdd:
					f.Additions++
				case KindDel:
					f.Deletions++
				}
			}
		}
	}
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

// Parse reads the output of `git diff` (no color, no ext diff).
func Parse(raw string) []*FileDiff {
	lines := splitLines(raw)
	var files []*FileDiff
	i := 0
	for i < len(lines) {
		if !strings.HasPrefix(lines[i], "diff --git ") {
			i++
			continue
		}
		start := i
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "diff --git ") {
			i++
		}
		if fd := parseFile(lines[start:i]); fd != nil {
			files = append(files, fd)
		}
	}
	return files
}

func parseFile(block []string) *FileDiff {
	f := &FileDiff{Status: Modified}
	oldP, newP := pathsFromGitLine(block[0])

	j := 1
	for ; j < len(block); j++ {
		l := block[j]
		switch {
		case strings.HasPrefix(l, "@@"):
			goto body
		case strings.HasPrefix(l, "index "):
			f.OldBlob, f.NewBlob = blobsFromIndexLine(l)
		case strings.HasPrefix(l, "new file mode "):
			f.Status = Added
			f.NewMode = strings.TrimPrefix(l, "new file mode ")
		case strings.HasPrefix(l, "deleted file mode "):
			f.Status = Deleted
			f.OldMode = strings.TrimPrefix(l, "deleted file mode ")
		case strings.HasPrefix(l, "old mode "):
			f.OldMode = strings.TrimPrefix(l, "old mode ")
		case strings.HasPrefix(l, "new mode "):
			f.NewMode = strings.TrimPrefix(l, "new mode ")
		case strings.HasPrefix(l, "rename from "):
			f.Status = Renamed
			oldP = unquotePath(strings.TrimPrefix(l, "rename from "))
		case strings.HasPrefix(l, "rename to "):
			f.Status = Renamed
			newP = unquotePath(strings.TrimPrefix(l, "rename to "))
		case strings.HasPrefix(l, "copy from "):
			f.Status = Copied
			oldP = unquotePath(strings.TrimPrefix(l, "copy from "))
		case strings.HasPrefix(l, "copy to "):
			f.Status = Copied
			newP = unquotePath(strings.TrimPrefix(l, "copy to "))
		case strings.HasPrefix(l, "Binary files ") || strings.HasPrefix(l, "GIT binary patch"):
			f.IsBinary = true
		case strings.HasPrefix(l, "--- "):
			p := strings.TrimPrefix(l, "--- ")
			if p == "/dev/null" {
				f.Status = Added
			} else {
				oldP = stripPrefix(unquotePath(p))
			}
		case strings.HasPrefix(l, "+++ "):
			p := strings.TrimPrefix(l, "+++ ")
			if p == "/dev/null" {
				f.Status = Deleted
			} else {
				newP = stripPrefix(unquotePath(p))
			}
		}
	}
body:
	f.OldPath, f.NewPath = oldP, newP
	if j < len(block) {
		f.body = block[j:]
	}
	return f
}

func parseHunks(body []string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	oldN, newN := 0, 0

	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	for _, l := range body {
		if m := hunkRe.FindStringSubmatch(l); m != nil {
			flush()
			cur = &Hunk{
				OldStart: atoi(m[1]), OldLines: atoiDefault(m[2], 1),
				NewStart: atoi(m[3]), NewLines: atoiDefault(m[4], 1),
				Section: m[5],
			}
			oldN, newN = cur.OldStart, cur.NewStart
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(l, `\`): // "\ No newline at end of file"
			if n := len(cur.Lines); n > 0 {
				cur.Lines[n-1].NoNewline = true
			}
		case strings.HasPrefix(l, "+"):
			cur.Lines = append(cur.Lines, Line{Kind: KindAdd, Text: l[1:], NewNum: newN})
			newN++
		case strings.HasPrefix(l, "-"):
			cur.Lines = append(cur.Lines, Line{Kind: KindDel, Text: l[1:], OldNum: oldN})
			oldN++
		case strings.HasPrefix(l, " "):
			cur.Lines = append(cur.Lines, Line{Kind: KindContext, Text: l[1:], OldNum: oldN, NewNum: newN})
			oldN++
			newN++
		case l == "":
			// git emits a bare empty line for an empty context line
			cur.Lines = append(cur.Lines, Line{Kind: KindContext, Text: "", OldNum: oldN, NewNum: newN})
			oldN++
			newN++
		default:
			// trailer such as "-- \n" from a mailbox patch: stop this hunk
			flush()
		}
	}
	flush()
	return hunks
}

// pathsFromGitLine is the fallback path source. It is only correct for paths
// without " b/" inside them; the ---/+++ and rename lines override it whenever
// git emits them, which is every case except a pure mode change.
func pathsFromGitLine(l string) (string, string) {
	rest := strings.TrimPrefix(l, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		// both paths quoted; split on the space between them
		if i := strings.Index(rest, `" "`); i >= 0 {
			return stripPrefix(unquotePath(rest[:i+1])), stripPrefix(unquotePath(rest[i+2:]))
		}
	}
	if i := strings.Index(rest, " b/"); i >= 0 {
		return stripPrefix(rest[:i]), stripPrefix(rest[i+1:])
	}
	return rest, rest
}

// blobsFromIndexLine reads "index <old>..<new>[ <mode>]". Both hashes are
// abbreviated by git; they are only ever compared to each other, so the short
// form is enough.
func blobsFromIndexLine(l string) (string, string) {
	rest := strings.TrimPrefix(l, "index ")
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i]
	}
	old, new, ok := strings.Cut(rest, "..")
	if !ok {
		return "", ""
	}
	return old, new
}

func stripPrefix(p string) string {
	if len(p) > 2 && (p[0] == 'a' || p[0] == 'b' || p[0] == 'i' || p[0] == 'w' || p[0] == 'c' || p[0] == 'o') && p[1] == '/' {
		return p[2:]
	}
	return p
}

func unquotePath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	return atoi(s)
}
