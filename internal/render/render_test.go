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
	diffparse.FillStats(files)
	for _, tc := range []struct {
		name    string
		density Density
		golden  string
	}{
		{"comfortable", DensityComfortable, "basic.golden"},
		{"compact", DensityCompact, "basic-compact.golden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := Build(files, NewHighlighter(DefaultTheme().Syntax, true), Overlay{}, Layout{Density: tc.density})
			r := NewRenderer(DefaultTheme(), doc)

			var b strings.Builder
			for _, row := range doc.Rows {
				b.WriteString(strings.TrimRight(r.Render(row, 72, 0, false), " "))
				b.WriteString("\n")
			}
			checkGolden(t, tc.golden, b.String())
		})
	}
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
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

// The dense golden is the one to look at when judging readability: it carries
// a rename, a mode change, word-level changes, a line far wider than the
// terminal, and a conversation of notes and comments hanging off the code.
func TestRenderDenseGolden(t *testing.T) {
	files := denseFiles(t)
	ov := denseOverlay()

	for _, tc := range []struct {
		name    string
		density Density
		golden  string
	}{
		{"comfortable", DensityComfortable, "dense.golden"},
		{"compact", DensityCompact, "dense-compact.golden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := Build(files, NewHighlighter(DefaultTheme().Syntax, true), ov, Layout{Density: tc.density})
			r := NewRenderer(DefaultTheme(), doc)

			var b strings.Builder
			for i, row := range doc.Rows {
				// Focus the added line, so the golden also shows what the
				// cursor row does to the grid.
				focus := row.Kind == RowCode && row.Line.NewNum == 13 && row.Line.Kind == diffparse.KindAdd
				b.WriteString(strings.TrimRight(r.Render(row, 88, 0, focus), " "))
				b.WriteString("\n")
				_ = i
			}
			checkGolden(t, tc.golden, b.String())
		})
	}
}

// denseFiles is the fixture worth judging readability on: a rename, a mode
// change, word-level changes, a line far wider than the terminal, and enough
// hunks to show what density does.
func denseFiles(t *testing.T) []*diffparse.FileDiff {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "dense.diff"))
	if err != nil {
		t.Fatal(err)
	}
	files := diffparse.Parse(string(raw))
	diffparse.FillStats(files)
	return files
}

// denseOverlay hangs a conversation off the fixture: two comments on one line,
// a multi-line note, and one comment that no longer anchors anywhere.
func denseOverlay() Overlay {
	return Overlay{
		At: func(path string, line int) []Annotation {
			switch {
			case path == "internal/server/handler.go" && line == 13:
				return []Annotation{
					{Kind: AnnComment, Author: "robin", Body: "Spelling of authorise is inconsistent with the rest of the package, and the exported helper below still spells it the other way.", Line: 13},
					{Kind: AnnNote, Body: "Check the policy argument is not nil before the call.", Line: 13},
				}
			case path == "internal/server/policy.go" && line == 5:
				return []Annotation{{Kind: AnnNote, Body: "Renaming the type is fine; the package rename needs a deprecation note.", StartLine: 4, Line: 5}}
			}
			return nil
		},
		Detached: func(path string) []AnnotationGroup {
			if path == "internal/server/handler.go" {
				return []AnnotationGroup{{
					Title: "no longer anchored",
					Items: []Annotation{{Kind: AnnComment, Author: "sam", Body: "This whole helper moved in the meantime.", Line: 42, Outdated: true, ResolutionKnown: true}},
				}}
			}
			return nil
		},
		FileState: func(path string) (bool, bool) {
			return path == "internal/server/policy.go", path == "internal/server/policy.go"
		},
	}
}

func normalizeEOL(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func TestBuildIndexesFilesAndHunks(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "basic.diff"))
	doc := Build(diffparse.Parse(string(raw)), NewHighlighter("", false), Overlay{}, Layout{})

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

func TestReviewedUnchangedFileIsCollapsed(t *testing.T) {
	files := diffparse.Parse("diff --git a/one.go b/one.go\n--- a/one.go\n+++ b/one.go\n@@ -1 +1 @@\n-a\n+b\n\ndiff --git a/two.go b/two.go\n--- a/two.go\n+++ b/two.go\n@@ -1 +1 @@\n-c\n+d\n")
	doc := Build(files, NewHighlighter("", false), Overlay{
		FileState: func(path string) (bool, bool) { return path == "one.go", false },
	}, Layout{})

	if len(doc.FileRows) != 2 {
		t.Fatalf("got %d file rows, want 2", len(doc.FileRows))
	}
	if !doc.Rows[doc.FileRows[0]].Collapsed {
		t.Fatal("reviewed unchanged file was not marked collapsed")
	}
	for _, row := range doc.Rows[doc.FileRows[0]+1:] {
		if row.FileIdx == 0 {
			t.Fatal("collapsed file still rendered its body")
		}
	}
}

// Horizontal scrolling must not shift the gutter, only the code column.
func TestRenderHorizontalScroll(t *testing.T) {
	files := diffparse.Parse("diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-abcdefghij\n+ABCDEFGHIJ\n")
	doc := Build(files, NewHighlighter("", false), Overlay{}, Layout{})
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
	if !strings.Contains(scrolled, gutterRule+"   1 + ") {
		t.Errorf("scrolled render lost the gutter: %q", scrolled)
	}
}

func TestRenderExpandsTabs(t *testing.T) {
	files := diffparse.Parse("diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-x\n+\tx\n")
	doc := Build(files, NewHighlighter("", false), Overlay{}, Layout{})
	r := NewRenderer(DefaultTheme(), doc)
	for _, row := range doc.Rows {
		if row.Kind == RowCode && row.Line.Kind == diffparse.KindAdd {
			out := r.Render(row, 40, 0, false)
			if strings.Contains(out, "\t") {
				t.Errorf("tab survived into output: %q", out)
			}
			if !strings.Contains(out, "+     x") {
				t.Errorf("tab not expanded to %d spaces: %q", tabWidth, out)
			}
		}
	}
}

func TestCommentStatesAreExplicit(t *testing.T) {
	doc := &Document{gutterOld: 3, gutterNew: 3}
	r := NewRenderer(DefaultTheme(), doc)
	row := Row{Kind: RowNote, Ann: &Annotation{
		Kind: AnnComment, Author: "ann", Body: "please revisit",
		Outdated: true, Resolved: true, ResolutionKnown: true, New: true,
	}}
	out := strings.Join(r.RenderLines(row, 100, 0, false, 1), "\n")
	for _, want := range []string{"ann", "[outdated]", "[resolved]", "[new]", "please revisit"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered comment %q does not contain %q", out, want)
		}
	}
}
