package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
)

// splitWidth is wide enough for split to be drawn at all.
const splitWidth = 160

var splitLayout = Layout{Mode: ModeSplit}

func TestSplitGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "basic.diff"))
	if err != nil {
		t.Fatal(err)
	}
	files := diffparse.Parse(string(raw))
	diffparse.FillStats(files)
	for _, tc := range []struct {
		name   string
		files  []*diffparse.FileDiff
		ov     Overlay
		golden string
	}{
		{"basic", files, Overlay{}, "basic-split.golden"},
		{"dense", denseFiles(t), denseOverlay(), "dense-split.golden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := Build(tc.files, NewHighlighter(DefaultTheme().Syntax, true), tc.ov, splitLayout.Fit(splitWidth))
			r := NewRenderer(DefaultTheme(), doc)

			var b strings.Builder
			for _, row := range doc.Rows {
				// Focus the pair holding the added line, as the unified dense
				// golden does.
				focus := row.Kind == RowPair && row.Right.NewNum == 13 && row.Right.Kind == diffparse.KindAdd
				b.WriteString(strings.TrimRight(r.Render(row, splitWidth, 0, focus), " "))
				b.WriteString("\n")
			}
			checkGolden(t, tc.golden, b.String())
		})
	}
}

func TestLayoutFit(t *testing.T) {
	split := Layout{Density: DensityCompact, Mode: ModeSplit}
	if got := split.Fit(SplitMinWidth); got != split {
		t.Errorf("Fit(%d) = %+v, want split kept", SplitMinWidth, got)
	}
	narrow := split.Fit(SplitMinWidth - 1)
	if narrow.Mode != ModeUnified || narrow.Density != DensityCompact {
		t.Errorf("Fit(%d) = %+v, want unified with density kept", SplitMinWidth-1, narrow)
	}
	if got := (Layout{}).Fit(400); got.Mode != ModeUnified {
		t.Error("Fit turned a unified layout into split")
	}
}

// Change blocks of unequal length pair line by line from the top, and the
// shorter side gets filler — never a line borrowed from outside the block.
func TestSplitPairsChangeBlocks(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -1,6 +1,5 @@\n ctx\n-old := 2\n-gone1\n-gone2\n+new := 2\n same\n+extra\n"
	doc := Build(diffparse.Parse(diff), NewHighlighter("", false), Overlay{}, splitLayout)

	type side struct{ old, new string }
	var got []side
	for _, row := range doc.Rows {
		if row.Kind == RowCode {
			t.Fatal("split document contains a unified code row")
		}
		if row.Kind != RowPair {
			continue
		}
		var s side
		if row.Line.OldNum > 0 {
			s.old = row.Line.Text
		}
		if row.Right.NewNum > 0 {
			s.new = row.Right.Text
		}
		got = append(got, s)
	}
	want := []side{
		{"ctx", "ctx"},
		{"old := 2", "new := 2"},
		{"gone1", ""},
		{"gone2", ""},
		{"same", "same"},
		{"", "extra"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d pair rows %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pair %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Intra-line marks follow the unified rule — unequal blocks are left
	// unmarked — so the two layouts never disagree about what changed.
	for _, row := range doc.Rows {
		if row.Kind == RowPair && (row.Marks != nil || row.RightMarks != nil) {
			t.Errorf("unequal change block was word-diffed: %+v", row)
		}
	}
	even := Build(diffparse.Parse(sampleDiff), NewHighlighter("", false), Overlay{}, splitLayout)
	for _, row := range even.Rows {
		if row.Kind == RowPair && row.Line.Kind == diffparse.KindDel {
			if row.Marks == nil || row.RightMarks == nil {
				t.Errorf("paired change is missing its word marks: %+v", row)
			}
		}
	}
}

// Without colour each pane still has to say what it is: its own edge marker
// and sign, just as a unified row does.
func TestSplitAddAndDeleteReadWithoutColour(t *testing.T) {
	doc := Build(diffparse.Parse(sampleDiff), NewHighlighter("", false), Overlay{}, splitLayout)
	r := NewRenderer(DefaultTheme(), doc)
	pw := paneWidth(splitWidth)

	for _, row := range doc.Rows {
		if row.Kind != RowPair {
			continue
		}
		out := r.Render(row, splitWidth, 0, false)
		if got := lipgloss.Width(out); got != splitWidth {
			t.Errorf("pair row width = %d, want %d", got, splitWidth)
		}
		runes := []rune(out)
		left, rule, right := string(runes[:pw]), string(runes[pw]), string(runes[pw+1:])
		if rule != gutterRule {
			t.Errorf("no rule between the panes: %q", out)
		}
		changed := row.Line.Kind != diffparse.KindContext
		for _, pane := range []struct {
			name, text, sign string
		}{
			{"old", left, signDel},
			{"new", right, signAdd},
		} {
			if strings.HasPrefix(pane.text, edgeChange) != changed {
				t.Errorf("%s pane edge marker wrong for a changed=%v row: %q", pane.name, changed, pane.text)
			}
			if strings.Contains(pane.text, pane.sign+" ") != changed {
				t.Errorf("%s pane sign wrong for a changed=%v row: %q", pane.name, changed, pane.text)
			}
		}
		if changed && (!strings.Contains(left, "old := 2") || !strings.Contains(right, "new := 2")) {
			t.Errorf("pair row lost its code: %q", out)
		}
	}
}

// Focus marks the row once, at the far left, and lifts both panes; the right
// pane keeps the marker that says what it is.
func TestSplitFocus(t *testing.T) {
	doc := Build(diffparse.Parse(sampleDiff), NewHighlighter("", false), Overlay{}, splitLayout)
	r := NewRenderer(DefaultTheme(), doc)
	pw := paneWidth(splitWidth)
	for _, row := range doc.Rows {
		if row.Kind == RowSpacer {
			continue
		}
		out := r.Render(row, splitWidth, 0, true)
		if !strings.HasPrefix(out, edgeFocus) {
			t.Errorf("row kind %d does not show focus at the far left: %q", row.Kind, out)
		}
		if row.Kind == RowPair && row.Right.Kind == diffparse.KindAdd {
			if right := string([]rune(out)[pw+1:]); !strings.HasPrefix(right, edgeChange) {
				t.Errorf("focus took the right pane's marker: %q", right)
			}
		}
	}
}

// One hoffset scrolls both panes, and each pane says for itself when its line
// runs on.
func TestSplitScrollsBothPanes(t *testing.T) {
	long := strings.Repeat("abcdefghij", 10)
	diff := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-" +
		long + "\n+" + strings.ToUpper(long) + "\n"
	doc := Build(diffparse.Parse(diff), NewHighlighter("", false), Overlay{}, splitLayout)
	r := NewRenderer(DefaultTheme(), doc)
	pw := paneWidth(splitWidth)

	var row Row
	for _, rw := range doc.Rows {
		if rw.Kind == RowPair {
			row = rw
		}
	}
	out := []rune(r.Render(row, splitWidth, 4, false))
	left, right := string(out[:pw]), string(out[pw+1:])
	if !strings.Contains(left, "− efghij") || !strings.Contains(right, "+ EFGHIJ") {
		t.Errorf("hoffset did not scroll both panes: %q | %q", left, right)
	}
	for _, pane := range []string{left, right} {
		if !strings.HasSuffix(strings.TrimRight(pane, " "), overflowMark) {
			t.Errorf("pane does not mark its overflow: %q", pane)
		}
	}
}

// Annotations hang full width under the pair row that shows their new-side
// line, indented to the left pane's code.
func TestSplitAnnotationsFollowTheNewSide(t *testing.T) {
	ov := Overlay{At: func(path string, line int) []Annotation {
		if line == 2 {
			return []Annotation{{Author: "robin", Body: "here", ResolutionKnown: true, Line: 2}}
		}
		return nil
	}}
	doc := Build(diffparse.Parse(sampleDiff), NewHighlighter("", false), ov, splitLayout)
	r := NewRenderer(DefaultTheme(), doc)
	for i, row := range doc.Rows {
		if row.Kind != RowNote {
			continue
		}
		above := doc.Rows[i-1]
		if above.Kind != RowPair || above.Right.NewNum != 2 {
			t.Errorf("annotation hangs under %+v, want the pair showing new line 2", above)
		}
		out := r.Render(row, splitWidth, 0, false)
		if lipgloss.Width(out) != splitWidth {
			t.Errorf("annotation is not full width: %d", lipgloss.Width(out))
		}
		return
	}
	t.Fatal("annotation was not drawn")
}

func TestLineRow(t *testing.T) {
	files := diffparse.Parse(sampleDiff +
		"diff --git a/g.go b/g.go\n--- a/g.go\n+++ b/g.go\n@@ -1 +1 @@\n-a\n+b\n")
	for _, layout := range []Layout{{}, splitLayout} {
		t.Run(layout.Mode.String(), func(t *testing.T) {
			doc := Build(files, NewHighlighter("", false), Overlay{}, layout)
			for _, tc := range []struct {
				file, newNum, oldNum int
				want                 string
				ok                   bool
			}{
				{0, 1, 1, "ctx := 1", true},
				{0, 2, 0, "new := 2", true},
				{0, 0, 2, "old := 2", true},
				{0, 9, 2, "old := 2", true}, // falls back to the old side
				{1, 1, 0, "b", true},        // the same number, in the other file
				{0, 9, 9, "", false},
				{5, 1, 1, "", false},
			} {
				i, ok := doc.LineRow(tc.file, tc.newNum, tc.oldNum)
				if ok != tc.ok {
					t.Errorf("LineRow(%d, %d, %d) ok = %v, want %v", tc.file, tc.newNum, tc.oldNum, ok, tc.ok)
					continue
				}
				if !ok {
					continue
				}
				row := doc.Rows[i]
				if row.FileIdx != tc.file {
					t.Errorf("LineRow(%d, …) landed in file %d", tc.file, row.FileIdx)
				}
				// A pair row shows both texts; the one asked for has to be one.
				if row.Line.Text != tc.want && !(row.Kind == RowPair && row.Right.Text == tc.want) {
					t.Errorf("LineRow(%d, %d, %d) = %+v, want the row showing %q",
						tc.file, tc.newNum, tc.oldNum, row, tc.want)
				}
			}
		})
	}
}

func TestSplitIndexesFilesAndHunks(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "basic.diff"))
	files := diffparse.Parse(string(raw))
	unified := Build(files, NewHighlighter("", false), Overlay{}, Layout{})
	split := Build(files, NewHighlighter("", false), Overlay{}, splitLayout)

	if len(split.FileRows) != len(unified.FileRows) || len(split.HunkRows) != len(unified.HunkRows) {
		t.Fatalf("split indexes %d files and %d hunks, unified %d and %d",
			len(split.FileRows), len(split.HunkRows), len(unified.FileRows), len(unified.HunkRows))
	}
	for _, at := range split.FileRows {
		if split.Rows[at].Kind != RowFile {
			t.Errorf("file anchor points at %+v", split.Rows[at])
		}
	}
	for _, at := range split.HunkRows {
		if split.Rows[at].Kind != RowHunk {
			t.Errorf("hunk anchor points at %+v", split.Rows[at])
		}
	}
}
