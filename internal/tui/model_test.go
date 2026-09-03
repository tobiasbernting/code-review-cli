package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// two files, the first with two hunks, so file jumps and hunk jumps are
// distinguishable.
const navDiff = `diff --git a/one.go b/one.go
--- a/one.go
+++ b/one.go
@@ -1,2 +1,2 @@
-a
+b
@@ -20,2 +20,2 @@
-c
+d
diff --git a/two.go b/two.go
--- a/two.go
+++ b/two.go
@@ -1,2 +1,2 @@
-e
+f
`

func newTestModel(t *testing.T) Model {
	t.Helper()
	files := diffparse.Parse(navDiff)
	doc := render.Build(files, render.NewHighlighter("", false))
	m := New(doc, render.DefaultTheme(), "test")
	// Deliberately shorter than the document, so scroll behaviour is exercised
	// rather than clamped away.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 6})
	return next.(Model)
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(keyMsg(k))
		m = next.(Model)
	}
	return m
}

// keyMsg spells a key the way bubbletea delivers it, so tests exercise the
// same strings handleKey switches on.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func (m Model) fileIdx() int { return m.doc.Rows[m.cursor].FileIdx }

func TestFileJumpKeysAreEquivalent(t *testing.T) {
	for _, key := range []string{"tab", "J", "]"} {
		t.Run(key, func(t *testing.T) {
			m := press(t, newTestModel(t), key)
			if got := m.fileIdx(); got != 1 {
				t.Errorf("%q landed on file %d, want 1", key, got)
			}
			if m.doc.Rows[m.cursor].Kind != render.RowFile {
				t.Errorf("%q did not land on a file header: %+v", key, m.doc.Rows[m.cursor])
			}
			// The target must be scrolled to the top, not left at the bottom edge.
			if m.top != m.cursor {
				t.Errorf("%q left the header at viewport row %d", key, m.cursor-m.top)
			}
		})
	}
}

// The bug that prompted this work: n steps hunk by hunk, so it takes three
// presses to leave a two-hunk file, while J leaves it in one.
func TestHunkJumpStepsThroughHunksNotFiles(t *testing.T) {
	m := newTestModel(t) // cursor starts on file 0's header

	m = press(t, m, "n")
	if got := m.doc.Rows[m.cursor]; got.FileIdx != 0 || got.HunkIdx != 0 {
		t.Fatalf("first n → file %d hunk %d, want file 0 hunk 0", got.FileIdx, got.HunkIdx)
	}
	m = press(t, m, "n")
	if got := m.doc.Rows[m.cursor]; got.FileIdx != 0 || got.HunkIdx != 1 {
		t.Fatalf("second n → file %d hunk %d, want file 0 hunk 1", got.FileIdx, got.HunkIdx)
	}
	m = press(t, m, "n")
	if got := m.fileIdx(); got != 1 {
		t.Errorf("third n → file %d, want 1", got)
	}

	// One tab does what those three did.
	if got := press(t, newTestModel(t), "tab").fileIdx(); got != 1 {
		t.Errorf("tab → file %d, want 1", got)
	}
}

func TestJumpAtEdgesReportsRatherThanWraps(t *testing.T) {
	m := press(t, newTestModel(t), "shift+tab")
	if m.fileIdx() != 0 {
		t.Errorf("shift+tab wrapped to file %d from the first file", m.fileIdx())
	}
	if m.status != "first file" {
		t.Errorf("status = %q, want %q", m.status, "first file")
	}

	m = press(t, newTestModel(t), "tab", "tab")
	if m.fileIdx() != 1 {
		t.Errorf("tab wrapped past the last file to %d", m.fileIdx())
	}
	if m.status != "last file" {
		t.Errorf("status = %q, want %q", m.status, "last file")
	}
}

func TestCursorNeverRestsOnSpacer(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < len(m.doc.Rows)+2; i++ {
		if m.doc.Rows[m.cursor].Kind == render.RowSpacer {
			t.Fatalf("cursor landed on a spacer at row %d", m.cursor)
		}
		m = press(t, m, "j")
	}
}
