package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// suggest presses C and returns the composer's text with the cursor marked |.
func suggest(t *testing.T, m Model) (Model, string) {
	t.Helper()
	m = press(t, m, "C")
	if m.mode != modeInput {
		t.Fatalf("C did not open the composer: %s", m.statusBar())
	}
	r := []rune(m.in.value)
	return m, string(r[:m.in.cursor]) + "|" + string(r[m.in.cursor:])
}

func TestSuggestionPrefillsTheLine(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m, got := suggest(t, m.cursorOnLine(t, 2, 0))
	if want := "```suggestion\nTWO|\n```"; got != want {
		t.Errorf("composer holds %q, want %q", got, want)
	}
	if m.pending.startLine != 2 || m.pending.line != 2 {
		t.Errorf("anchored to L%d-%d, want L2", m.pending.startLine, m.pending.line)
	}
}

func TestSuggestionPrefillsTheSelection(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m = press(t, m.cursorOnLine(t, 10, 10), "v")
	m, got := suggest(t, m.cursorOnLine(t, 12, 11))
	if want := "```suggestion\nten\neleven\ntwelve|\n```"; got != want {
		t.Errorf("composer holds %q, want %q", got, want)
	}
	if m.pending.startLine != 10 || m.pending.line != 12 {
		t.Errorf("anchored to L%d-%d, want L10-12", m.pending.startLine, m.pending.line)
	}
}

func TestSuggestionOnAMixedSelectionUsesTheNewSide(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m = press(t, m.cursorOnLine(t, 1, 1), "v")
	m, got := suggest(t, m.cursorOnLine(t, 3, 3))
	if want := "```suggestion\none\nTWO\nthree|\n```"; got != want {
		t.Errorf("composer holds %q, want the deleted line left out", got)
	}
	if m.pending.startLine != 1 || m.pending.line != 3 {
		t.Errorf("anchored to L%d-%d, want L1-3", m.pending.startLine, m.pending.line)
	}
}

func TestComposerKeepsASuggestionOnOneLine(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m, _ = suggest(t, m.cursorOnLine(t, 2, 0))
	line := ansi.Strip(m.in.render(m.width, "", ""))
	if strings.Contains(line, "\n") || !strings.Contains(line, "```suggestion⏎TWO⏎```") {
		t.Errorf("composer drew %q, want the line breaks marked in one row", line)
	}
}

func TestSuggestionOnADeletedLineIsRefused(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m = press(t, m.cursorOnLine(t, 0, 2), "C")
	if m.mode != modeDiff {
		t.Fatal("C on a deleted line opened the composer")
	}
	if !strings.Contains(m.statusBar(), "suggestions replace new lines; this line was deleted") {
		t.Errorf("no reason given:\n%s", m.statusBar())
	}
}

func TestUnchangedSuggestionWarnsOnceThenSaves(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m, _ = suggest(t, m.cursorOnLine(t, 2, 0))

	m = press(t, m, "enter")
	if m.mode != modeInput || len(m.review.Notes) != 0 {
		t.Fatal("an unchanged suggestion was saved without a warning")
	}
	if !strings.Contains(m.statusBar(), "suggestion doesn't change anything — enter again to save anyway") {
		t.Errorf("no warning:\n%s", m.statusBar())
	}

	m = press(t, m, "enter")
	if m.mode != modeDiff || len(m.review.Notes) != 1 {
		t.Fatalf("the second enter did not save the suggestion")
	}
	if got := m.review.Notes[0].Body; got != "```suggestion\nTWO\n```" {
		t.Errorf("saved %q", got)
	}
}

func TestUnchangedSuggestionFromTheEditorWarnsToo(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m, _ = suggest(t, m.cursorOnLine(t, 2, 0))

	next, _ := m.Update(editorFinishedMsg{body: "```suggestion\nTWO\n```\n"})
	m = next.(Model)
	if m.mode != modeInput || len(m.review.Notes) != 0 {
		t.Fatal("an unchanged suggestion from the editor was saved without a warning")
	}
	if !strings.Contains(m.statusBar(), "suggestion doesn't change anything") {
		t.Errorf("no warning:\n%s", m.statusBar())
	}
	if m = press(t, m, "enter"); len(m.review.Notes) != 1 {
		t.Fatal("enter after the warning did not save the suggestion")
	}
}

func TestChangedSuggestionSavesAtOnce(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	m, _ = suggest(t, m.cursorOnLine(t, 2, 0))
	m = press(t, typeText(t, m, "!"), "enter")
	if len(m.review.Notes) != 1 {
		t.Fatal("a changed suggestion was not saved")
	}
	if got := m.review.Notes[0].Body; got != "```suggestion\nTWO!\n```" {
		t.Errorf("saved %q", got)
	}
}
