package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Golden tests run without a TTY, so lipgloss degrades to plain text and the
// golden files stay readable diffs of layout rather than walls of escapes.
func TestRenderGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "basic.diff"))
	if err != nil {
		t.Fatal(err)
	}
	files := diffparse.Parse(string(raw))
	doc := Build(files, NewHighlighter(DefaultTheme().Syntax, true), Overlay{})
	r := NewRenderer(DefaultTheme(), doc)

	var b strings.Builder
	for _, row := range doc.Rows {
		b.WriteString(strings.TrimRight(r.Render(row, 72, 0, false), " "))
		b.WriteString("\n")
	}
	got := b.String()

	golden := filepath.Join("testdata", "basic.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/render -update)", err)
	}
	// .gitattributes keeps golden files LF, but normalise anyway so a stray
	// CRLF checkout fails loudly on content rather than silently on Windows.
	if got != normalizeEOL(string(want)) {
		t.Errorf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func normalizeEOL(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func TestBuildIndexesFilesAndHunks(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "basic.diff"))
	doc := Build(diffparse.Parse(string(raw)), NewHighlighter("", false), Overlay{})

	if len(doc.FileRows) != 3 {
		t.Errorf("got %d file anchors, want 3", len(doc.FileRows))
	}
	// The binary file contributes no hunks.
	if len(doc.HunkRows) != 2 {
		t.Errorf("got %d hunk anchors, want 2", len(doc.HunkRows))
	}
	for i, at := range doc.FileRows {
		if doc.Rows[at].Kind != RowFile || doc.Rows[at].FileIdx != i {
			t.Errorf("file anchor %d points at %+v", i, doc.Rows[at])
		}
	}
	for _, at := range doc.HunkRows {
		if doc.Rows[at].Kind != RowHunk {
			t.Errorf("hunk anchor points at %+v", doc.Rows[at])
		}
	}
}

// Horizontal scrolling must not shift the gutter, only the code column.
func TestRenderHorizontalScroll(t *testing.T) {
	files := diffparse.Parse("diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-abcdefghij\n+ABCDEFGHIJ\n")
	doc := Build(files, NewHighlighter("", false), Overlay{})
	r := NewRenderer(DefaultTheme(), doc)

	var code Row
	for _, row := range doc.Rows {
		if row.Kind == RowCode && row.Line.Kind == diffparse.KindAdd {
			code = row
		}
	}
	full := r.Render(code, 40, 0, false)
	if !strings.Contains(full, "ABCDEFGHIJ") {
		t.Fatalf("unscrolled render lost the text: %q", full)
	}
	scrolled := r.Render(code, 40, 4, false)
	if !strings.Contains(scrolled, "EFGHIJ") || strings.Contains(scrolled, "ABCD") {
		t.Errorf("scrolled render = %q, want the first 4 columns dropped", scrolled)
	}
	if !strings.Contains(scrolled, "1 +") {
		t.Errorf("scrolled render lost the gutter: %q", scrolled)
	}
}

func TestRenderExpandsTabs(t *testing.T) {
	files := diffparse.Parse("diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-x\n+\tx\n")
	doc := Build(files, NewHighlighter("", false), Overlay{})
	r := NewRenderer(DefaultTheme(), doc)
	for _, row := range doc.Rows {
		if row.Kind == RowCode && row.Line.Kind == diffparse.KindAdd {
			out := r.Render(row, 40, 0, false)
			if strings.Contains(out, "\t") {
				t.Errorf("tab survived into output: %q", out)
			}
			if !strings.Contains(out, "+    x") {
				t.Errorf("tab not expanded to %d spaces: %q", tabWidth, out)
			}
		}
	}
}
