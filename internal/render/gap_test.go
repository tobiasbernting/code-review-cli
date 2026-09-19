package render

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
)

func gapFile(t *testing.T, diff string) *diffparse.FileDiff {
	t.Helper()
	files := diffparse.Parse(diff)
	if len(files) != 1 {
		t.Fatalf("parsed %d files, want 1", len(files))
	}
	return files[0]
}

// twoHunks changes line 2 and line 12 of a twenty-line file, with a line
// added in the first hunk so the two sides are numbered differently after it.
const twoHunks = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
	"@@ -1,3 +1,4 @@\n one\n-two\n+TWO\n+two and a half\n three\n" +
	"@@ -11,3 +12,3 @@\n eleven\n-twelve\n+TWELVE\n thirteen\n"

func TestGapsBetweenHunks(t *testing.T) {
	got := Gaps(gapFile(t, twoHunks), 0)
	want := []Gap{{Index: 1, OldStart: 4, NewStart: 5, Lines: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %+v, want %+v", got, want)
	}
}

func TestGapAtFileStart(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -5,3 +5,3 @@\n five\n-six\n+SIX\n seven\n"
	got := Gaps(gapFile(t, diff), 0)
	want := []Gap{{Index: 0, OldStart: 1, NewStart: 1, Lines: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %+v, want %+v", got, want)
	}
}

// The Gap after the last hunk is only known once the file's length is: a
// diff does not say whether the file goes on.
func TestGapAtFileEnd(t *testing.T) {
	f := gapFile(t, twoHunks)
	got := Gaps(f, 20)
	want := []Gap{
		{Index: 1, OldStart: 4, NewStart: 5, Lines: 7},
		{Index: 2, OldStart: 14, NewStart: 15, Lines: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps(20) = %+v, want %+v", got, want)
	}
	if got := Gaps(f, 14); len(got) != 1 {
		t.Errorf("Gaps(14) = %+v, want no Gap after a hunk that ends the file", got)
	}
}

func TestNoGapBetweenAdjacentHunks(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n" +
		"@@ -4,3 +4,3 @@\n four\n-five\n+FIVE\n six\n"
	if got := Gaps(gapFile(t, diff), 6); len(got) != 0 {
		t.Errorf("Gaps = %+v, want none", got)
	}
}

// A hunk with no lines on one side is numbered from the line before it, so
// the unchanged line it names is still part of the Gap.
func TestGapsAroundAnEmptySide(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -5,2 +4,0 @@\n-five\n-six\n"
	got := Gaps(gapFile(t, diff), 10)
	want := []Gap{
		{Index: 0, OldStart: 1, NewStart: 1, Lines: 4},
		{Index: 1, OldStart: 7, NewStart: 5, Lines: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %+v, want %+v", got, want)
	}
}

func TestMayContinue(t *testing.T) {
	ends := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n\\ No newline at end of file\n"
	if MayContinue(gapFile(t, ends)) {
		t.Error("a hunk ending without a newline was taken for one the file goes on after")
	}
	if !MayContinue(gapFile(t, twoHunks)) {
		t.Error("a hunk in the middle of a file was taken for its end")
	}
}

// Added and deleted files are shown whole, and a binary one not at all.
func TestNoGapsInWholeFiles(t *testing.T) {
	added := "diff --git a/f.go b/f.go\nnew file mode 100644\n--- /dev/null\n+++ b/f.go\n@@ -0,0 +1,2 @@\n+a\n+b\n"
	deleted := "diff --git a/f.go b/f.go\ndeleted file mode 100644\n--- a/f.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n"
	binary := "diff --git a/f.png b/f.png\nBinary files a/f.png and b/f.png differ\n"
	for name, diff := range map[string]string{"added": added, "deleted": deleted, "binary": binary} {
		if got := Gaps(gapFile(t, diff), 50); len(got) != 0 {
			t.Errorf("%s: Gaps = %+v, want none", name, got)
		}
	}
}

// twenty is the new side of twoHunks' file, one "line N" per line.
func twenty() []string {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	return lines
}

func expanding(exp Expansion) Overlay {
	return Overlay{Expansion: func(string) Expansion { return exp }}
}

// Piped output has no way to expand a Gap, so it keeps the diff as git
// prints it.
func TestPlainDocumentHasNoGapRows(t *testing.T) {
	doc := Build(diffparse.Parse(twoHunks), NewHighlighter("", false), Overlay{}, Layout{})
	for _, row := range doc.Rows {
		if row.Kind == RowGap {
			t.Fatalf("plain document has a Gap row: %+v", row)
		}
	}
}

func TestGapRowSaysWhatIsLeftOut(t *testing.T) {
	doc := Build(diffparse.Parse(twoHunks), NewHighlighter("", false), expanding(Expansion{}), Layout{})
	r := NewRenderer(DefaultTheme(), doc)
	var gaps []int
	for i, row := range doc.Rows {
		if row.Kind == RowGap {
			gaps = append(gaps, i)
		}
	}
	if len(gaps) != 1 {
		t.Fatalf("got %d Gap rows, want 1 between the hunks", len(gaps))
	}
	row := doc.Rows[gaps[0]]
	if row.Gap == nil || row.Gap.Index != 1 || row.HunkIdx != -1 {
		t.Errorf("Gap row = %+v, want the Gap before hunk 1, in no hunk", row)
	}
	if gaps[0] != doc.HunkRows[1]-1 {
		t.Errorf("Gap row at %d, want it just above hunk 1's header at %d", gaps[0], doc.HunkRows[1])
	}
	out := r.Render(row, 60, 0, false)
	if !strings.Contains(out, "⋯ 7 unchanged lines") {
		t.Errorf("Gap row = %q, want it to count the lines", out)
	}
	if got := lipgloss.Width(out); got != 60 {
		t.Errorf("Gap row width = %d, want 60", got)
	}
	if focused := r.Render(row, 60, 0, true); !strings.HasPrefix(focused, edgeFocus) {
		t.Errorf("focused Gap row = %q, want the focus bar", focused)
	}
}

// The Gap separates the hunks, so the spacer comfortable density would put
// between them does not also split the Gap's lines from the hunk above.
func TestGapTakesTheSpacersPlace(t *testing.T) {
	exp := Expansion{Text: twenty(), Shown: map[int]Shown{1: {Top: 1}}}
	doc := Build(diffparse.Parse(twoHunks), NewHighlighter("", false), expanding(exp), Layout{})
	for i, row := range doc.Rows {
		if row.Kind == RowSpacer && i < doc.HunkRows[1] {
			t.Errorf("spacer at row %d, above the second hunk", i)
		}
	}
}

type shownLine struct{ old, new int }

func expandedLines(doc *Document) (lines []shownLine, gapRows []string) {
	r := NewRenderer(DefaultTheme(), doc)
	for _, row := range doc.Rows {
		switch {
		case row.Kind == RowGap:
			gapRows = append(gapRows, strings.TrimSpace(r.Render(row, splitWidth, 0, false)))
		case row.Expanded && row.Kind == RowPair:
			lines = append(lines, shownLine{row.Line.OldNum, row.Right.NewNum})
		case row.Expanded:
			lines = append(lines, shownLine{row.Line.OldNum, row.Line.NewNum})
		}
	}
	return lines, gapRows
}

// Lines shown from the top of a Gap follow the hunk above it, lines shown
// from the bottom lead into the hunk below, and the row keeps count of what
// is still left out between them. A Gap shown whole has no row.
func TestExpandedLinesSitInTheirGap(t *testing.T) {
	exp := Expansion{Text: twenty(), Shown: map[int]Shown{1: {Top: 2, Bottom: 1}, 2: {Top: 20}}}
	for _, layout := range []Layout{{}, splitLayout} {
		t.Run(layout.Mode.String(), func(t *testing.T) {
			doc := Build(diffparse.Parse(twoHunks), NewHighlighter("", false), expanding(exp), layout)
			lines, gapRows := expandedLines(doc)
			want := []shownLine{{4, 5}, {5, 6}, {10, 11}, {14, 15}, {15, 16}, {16, 17}, {17, 18}, {18, 19}, {19, 20}}
			if fmt.Sprint(lines) != fmt.Sprint(want) {
				t.Errorf("expanded lines = %v, want %v", lines, want)
			}
			if len(gapRows) != 1 || !strings.Contains(gapRows[0], "⋯ 4 unchanged lines") {
				t.Errorf("Gap rows = %q, want one with 4 lines left", gapRows)
			}
		})
	}
}

// An expanded line is an unchanged line: no marker, no sign, both sides
// numbered — in split, the same line on both panes.
func TestExpandedLineReadsAsContext(t *testing.T) {
	exp := Expansion{Text: twenty(), Shown: map[int]Shown{1: {Top: 1}}}
	doc := Build(diffparse.Parse(twoHunks), NewHighlighter("", false), expanding(exp), Layout{})
	r := NewRenderer(DefaultTheme(), doc)
	for _, row := range doc.Rows {
		if !row.Expanded {
			continue
		}
		out := r.Render(row, 60, 0, false)
		if strings.HasPrefix(out, edgeChange) || strings.Contains(out, signAdd) || strings.Contains(out, signDel) {
			t.Errorf("expanded row reads as a change: %q", out)
		}
		if !strings.Contains(out, "4 │   5   line 5") {
			t.Errorf("expanded row = %q, want old 4, new 5 and the text", out)
		}
	}

	doc = Build(diffparse.Parse(twoHunks), NewHighlighter("", false), expanding(exp), splitLayout)
	r = NewRenderer(DefaultTheme(), doc)
	for _, row := range doc.Rows {
		if !row.Expanded {
			continue
		}
		out := r.Render(row, splitWidth, 0, false)
		if strings.Count(out, "line 5") != 2 {
			t.Errorf("split expanded row = %q, want the line on both panes", out)
		}
	}
}
