package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// layoutDiff has unequal change blocks, so split pairs a deletion with an
// addition and leaves filler on the shorter side.
const layoutDiff = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,4 +1,5 @@
 one
-two
+TWO
+three
 four
-five
`

func newLayoutModel(t *testing.T, mode render.Mode, width int) Model {
	t.Helper()
	m := New(Options{
		Files:  diffparse.Parse(layoutDiff),
		Theme:  render.DefaultTheme(),
		Layout: render.Layout{Mode: mode},
		Config: config.Defaults(),
		Source: Source{Kind: SourceLocal, Title: "test"},
		Review: newTestReview(t),
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
	return next.(Model)
}

func (m Model) cursorOnLine(t *testing.T, newNum, oldNum int) Model {
	t.Helper()
	i, ok := m.doc.LineRow(0, newNum, oldNum)
	if !ok {
		t.Fatalf("no row for new %d / old %d", newNum, oldNum)
	}
	m.cursor = i
	return m
}

func TestSplitIsBuiltOnlyWhenWideEnough(t *testing.T) {
	wide := newLayoutModel(t, render.ModeSplit, render.SplitMinWidth)
	if wide.doc.Layout.Mode != render.ModeSplit {
		t.Fatalf("at %d columns the document is %s, want split", render.SplitMinWidth, wide.doc.Layout.Mode)
	}
	narrow := newLayoutModel(t, render.ModeSplit, render.SplitMinWidth-1)
	if narrow.doc.Layout.Mode != render.ModeUnified {
		t.Fatalf("below %d columns the document is %s, want unified", render.SplitMinWidth, narrow.doc.Layout.Mode)
	}
	if !strings.Contains(narrow.statusBar(), "split → unified (narrow)") {
		t.Errorf("a narrow fallback should say so in the status bar:\n%s", narrow.statusBar())
	}
	if strings.Contains(wide.statusBar(), "narrow") {
		t.Errorf("split drawn as asked should not report a fallback:\n%s", wide.statusBar())
	}
}

func TestResizeAcrossThresholdKeepsTheLine(t *testing.T) {
	m := newLayoutModel(t, render.ModeSplit, 200).cursorOnLine(t, 3, 0)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)
	if m.doc.Layout.Mode != render.ModeUnified {
		t.Fatalf("shrinking below the threshold left the document %s", m.doc.Layout.Mode)
	}
	if got := m.doc.Rows[m.cursor].NewNum(); got != 3 {
		t.Errorf("after shrinking, cursor on new line %d, want 3", got)
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 20})
	m = next.(Model)
	if m.doc.Layout.Mode != render.ModeSplit {
		t.Fatalf("growing past the threshold left the document %s", m.doc.Layout.Mode)
	}
	if got := m.doc.Rows[m.cursor].NewNum(); got != 3 {
		t.Errorf("after growing, cursor on new line %d, want 3", got)
	}
}

func TestToggleKeepsTheCursorOnTheSameLine(t *testing.T) {
	cases := []struct {
		name           string
		newNum, oldNum int
	}{
		{"context line", 1, 0},
		{"added line", 3, 0},
		{"deleted-only line", 0, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newLayoutModel(t, render.ModeUnified, 200).cursorOnLine(t, tc.newNum, tc.oldNum)
			want, _ := m.doc.LineRow(0, tc.newNum, tc.oldNum)
			wantRow := m.doc.Rows[want]

			m = press(t, m, "s")
			if m.doc.Layout.Mode != render.ModeSplit {
				t.Fatalf("s left the document %s", m.doc.Layout.Mode)
			}
			row := m.doc.Rows[m.cursor]
			if row.Kind != render.RowPair {
				t.Fatalf("cursor on a %v row after switching to split", row.Kind)
			}
			if tc.newNum > 0 && row.NewNum() != tc.newNum {
				t.Errorf("cursor on new line %d, want %d", row.NewNum(), tc.newNum)
			}
			if tc.newNum == 0 && row.Line.OldNum != tc.oldNum {
				t.Errorf("cursor on old line %d, want %d", row.Line.OldNum, tc.oldNum)
			}

			m = press(t, m, "s")
			if m.doc.Layout.Mode != render.ModeUnified {
				t.Fatalf("second s left the document %s", m.doc.Layout.Mode)
			}
			if got := m.doc.Rows[m.cursor]; got.Line != wantRow.Line {
				t.Errorf("round trip moved the cursor from %+v to %+v", wantRow.Line, got.Line)
			}
		})
	}
}

func TestNotesAttachToTheNewSideInSplit(t *testing.T) {
	m := newLayoutModel(t, render.ModeSplit, 200).cursorOnLine(t, 2, 0)
	path, line, _, ok := m.cursorLine()
	if !ok || path != "a.go" || line != 2 {
		t.Errorf("cursorLine on a paired row = %q, %d, %v; want a.go, 2, true", path, line, ok)
	}

	m = m.cursorOnLine(t, 0, 4)
	if _, _, _, ok := m.cursorLine(); ok {
		t.Error("a row whose new side is filler should not take a note")
	}
}
