package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// one file whose index line gives the blob hashes notes anchor to.
const noteDiff = `diff --git a/svc.go b/svc.go
index aaaaaaa..bbbbbbb 100644
--- a/svc.go
+++ b/svc.go
@@ -1,4 +1,6 @@
 package svc
 
-func Run() {}
+func Run() error {
+	return nil
+}
`

func newReviewModel(t *testing.T, opts ...func(*Options)) Model {
	t.Helper()
	review, err := notes.LoadAt(filepath.Join(t.TempDir(), "r.json"), "scope")
	if err != nil {
		t.Fatal(err)
	}
	o := Options{
		Files:  diffparse.Parse(noteDiff),
		Theme:  render.DefaultTheme(),
		Config: config.Defaults(),
		Source: Source{Kind: SourceLocal, Title: "test"},
		Review: review,
	}
	for _, f := range opts {
		f(&o)
	}
	m := New(o)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

// seekLine moves the cursor onto a given new-side line number.
func seekLine(t *testing.T, m Model, line int) Model {
	t.Helper()
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowCode && row.Line.NewNum == line {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no row for line %d", line)
	return m
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func TestAddNoteOnLine(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "c")
	if m.mode != modeInput {
		t.Fatalf("c did not open the note editor, mode = %v (err %q)", m.mode, m.err)
	}

	m = typeText(t, m, "returns a bare nil")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.review.Notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(m.review.Notes))
	}
	n := m.review.Notes[0]
	if n.Path != "svc.go" || n.Line != 3 || n.Body != "returns a bare nil" {
		t.Errorf("note = %+v", n)
	}
	// The note anchors to the new-side blob, so a later change can be detected.
	if n.Blob != "bbbbbbb" {
		t.Errorf("note blob = %q, want bbbbbbb", n.Blob)
	}
	if n.Side != notes.SideRight {
		t.Errorf("side = %q, want RIGHT", n.Side)
	}

	// The note must now be visible in the document.
	if !hasNoteRow(m, "returns a bare nil") {
		t.Error("note was saved but not rendered")
	}
}

func TestMultiLineNoteFromSelection(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "v")
	if m.rangeAnchor != 3 {
		t.Fatalf("v set anchor %d, want 3", m.rangeAnchor)
	}

	m = seekLine(t, m, 5)
	m = press(t, m, "c")
	m = typeText(t, m, "extract this")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	n := m.review.Notes[0]
	if n.StartLine != 3 || n.Line != 5 {
		t.Errorf("note range = %d..%d, want 3..5", n.StartLine, n.Line)
	}
	// The selection is consumed, not left armed for the next note.
	if m.rangeAnchor != 0 {
		t.Errorf("selection survived the note, anchor = %d", m.rangeAnchor)
	}
}

// Selecting backwards must produce the same range as selecting forwards.
func TestSelectionNormalisesDirection(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 5)
	m = press(t, m, "v")
	m = seekLine(t, m, 3)
	m = press(t, m, "c")
	m = typeText(t, m, "backwards")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if n := m.review.Notes[0]; n.StartLine != 3 || n.Line != 5 {
		t.Errorf("note range = %d..%d, want 3..5", n.StartLine, n.Line)
	}
}

// A deleted line has no new-side number, so there is nothing GitHub can
// anchor a RIGHT-side comment to.
func TestCannotCommentOnDeletedLine(t *testing.T) {
	m := newReviewModel(t)
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowCode && row.Line.Kind == diffparse.KindDel {
			m.cursor = i
		}
	}
	m = press(t, m, "c")
	if m.mode == modeInput {
		t.Error("comment editor opened on a deleted line")
	}
	if m.err == "" {
		t.Error("no explanation given for refusing the comment")
	}
}

func TestEmptyNoteIsDiscarded(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "c")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(m.review.Notes) != 0 {
		t.Errorf("empty note was saved: %+v", m.review.Notes)
	}
}

func TestEscapeCancelsNote(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "c")
	m = typeText(t, m, "half written")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if len(m.review.Notes) != 0 {
		t.Errorf("cancelled note was saved: %+v", m.review.Notes)
	}
	if m.mode != modeDiff {
		t.Errorf("mode = %v after cancel, want diff", m.mode)
	}
}

func TestDeleteNoteUnderCursor(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "c")
	m = typeText(t, m, "doomed")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	// Move onto the rendered note row, then delete it.
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowNote {
			m.cursor = i
		}
	}
	m = press(t, m, "d")
	if len(m.review.Notes) != 0 {
		t.Errorf("d left %d notes", len(m.review.Notes))
	}
}

func TestToggleReviewedTracksBlob(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "x")

	reviewed, changed := m.review.ReviewState("svc.go", "bbbbbbb")
	if !reviewed || changed {
		t.Fatalf("reviewed=%v changed=%v, want true/false", reviewed, changed)
	}
	// The same file at a different blob is still reviewed, but flagged.
	if _, changed := m.review.ReviewState("svc.go", "ccccccc"); !changed {
		t.Error("a changed file was not flagged")
	}

	m = press(t, m, "x")
	if reviewed, _ := m.review.ReviewState("svc.go", "bbbbbbb"); reviewed {
		t.Error("second x did not unmark the file")
	}
}

func TestMarkReviewedCollapsesAndAdvances(t *testing.T) {
	second := strings.Replace(noteDiff, "svc.go", "other.go", -1)
	m := newReviewModel(t, func(o *Options) {
		o.Files = diffparse.Parse(noteDiff + "\n" + second)
	})

	m = press(t, m, "x")
	if !m.doc.Rows[m.doc.FileRows[0]].Collapsed {
		t.Fatal("first file was not collapsed")
	}
	if m.cursor != m.doc.FileRows[1] {
		t.Fatalf("cursor = %d, want next file header at %d", m.cursor, m.doc.FileRows[1])
	}

	m = press(t, m, "shift+tab")
	if !m.doc.Rows[m.cursor].Collapsed {
		t.Fatal("first file header was not retained as a collapsed placeholder")
	}
	m = press(t, m, "x")
	if reviewed, _ := m.review.ReviewState("svc.go", "bbbbbbb"); reviewed {
		t.Fatal("x on collapsed placeholder did not clear reviewed state")
	}
	for _, row := range m.doc.Rows {
		if row.FileIdx == 0 && row.Kind != render.RowFile {
			return
		}
	}
	t.Fatal("unmarking collapsed file did not restore its body")
}

func TestFilePickerEntersCollapsedFileHeader(t *testing.T) {
	m := newReviewModel(t)
	m = press(t, m, "x")
	m = press(t, m, "f", "enter")
	if m.cursor != m.doc.FileRows[0] {
		t.Fatalf("cursor = %d, want collapsed file header at %d", m.cursor, m.doc.FileRows[0])
	}
	if !m.doc.Rows[m.cursor].Collapsed {
		t.Fatal("file picker did not enter the collapsed file header")
	}
}

func TestSubmitRefusedForLocalReview(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "c")
	m = typeText(t, m, "a note")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = press(t, next.(Model), "S")

	if m.mode == modeSubmit {
		t.Error("submit screen opened for a local review")
	}
	if !strings.Contains(m.err, "pull request") {
		t.Errorf("err = %q, want an explanation mentioning pull requests", m.err)
	}
}

func TestSubmitAvailableWithNoNotes(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	m = press(t, m, "S")
	if m.mode != modeSubmit {
		t.Error("submit screen should allow a clean approval")
	}
}

func TestSubmitWaitsForSync(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.sync.syncing = true
	m = press(t, m, "S")
	if m.mode == modeSubmit || !strings.Contains(m.err, "sync") {
		t.Errorf("submit was not held during sync: mode=%v err=%q", m.mode, m.err)
	}
}

// A note written against an older version of the file must not be posted:
// its line number no longer means what it meant when it was written.
func TestSubmitRefusedWhenDraftsNeedReanchor(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	m.review.Add("svc.go", 0, 3, "OLDBLOB", "written against an older diff")
	m.rebuild()

	m = press(t, m, "S")
	if m.mode == modeSubmit {
		t.Error("submit screen opened with stale notes")
	}
	if !strings.Contains(m.err, "re-anchor") {
		t.Errorf("err = %q, want it to mention re-anchoring", m.err)
	}
}

func TestSubmitScreenSelectsEvent(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.rebuild()

	m = press(t, m, "S")
	if m.mode != modeSubmit {
		t.Fatalf("submit screen did not open: %q", m.err)
	}
	if m.submit.event != ghsrc.EventComment {
		t.Errorf("default event = %q, want COMMENT", m.submit.event)
	}
	if m = press(t, m, "r"); m.submit.event != ghsrc.EventRequestChanges {
		t.Errorf("r selected %q", m.submit.event)
	}
	if m = press(t, m, "a"); m.submit.event != ghsrc.EventApprove {
		t.Errorf("a selected %q", m.submit.event)
	}

	// Submitting is the only thing that can send; escape must not.
	m = press(t, m, "esc")
	if m.mode != modeDiff {
		t.Errorf("esc left mode %v", m.mode)
	}
}

// After a successful submit the local notes are dropped: GitHub owns them
// now, and keeping a second copy invites the two to disagree.
func TestSubmitResultClearsLocalNotes(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.rebuild()

	next, _ := m.Update(submitResultMsg{event: ghsrc.EventComment})
	m = next.(Model)
	if len(m.review.Notes) != 0 {
		t.Errorf("notes survived submission: %+v", m.review.Notes)
	}
	if !strings.Contains(m.status, "submitted") {
		t.Errorf("status = %q", m.status)
	}
}

func TestSubmitFailureKeepsNotes(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.rebuild()

	next, _ := m.Update(submitResultMsg{err: errFake{}})
	m = next.(Model)
	if len(m.review.Notes) != 1 {
		t.Error("a failed submission threw the notes away")
	}
	if m.mode != modeSubmit {
		t.Errorf("mode = %v after failure, want the submit screen", m.mode)
	}
}

type errFake struct{}

func (errFake) Error() string { return "network is down" }

// Teammates' comments render inline; outdated ones are shown detached rather
// than dropped.
func TestExistingCommentsRender(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
		o.Threads = []ghsrc.Thread{
			{ID: "T1", RootID: 1, Path: "svc.go", Line: 3, ResolutionKnown: true, Comments: []ghsrc.Comment{{ID: 1, Body: "why error?"}}},
			{ID: "T2", RootID: 2, Path: "svc.go", Line: 99, Outdated: true, ResolutionKnown: true, Comments: []ghsrc.Comment{{ID: 2, Body: "from an older push"}}},
		}
		o.Threads[0].Comments[0].User.Login = "ann"
		o.Threads[1].Comments[0].User.Login = "bo"
	})

	if !hasNoteRow(m, "why error?") {
		t.Error("an anchored comment was not rendered")
	}
	if !hasNoteRow(m, "from an older push") {
		t.Error("an outdated comment was dropped instead of shown detached")
	}
	for _, row := range m.doc.Rows {
		if row.Kind == render.RowNote && row.Ann.Author == "bo" && row.Ann.Kind == render.AnnThread && !row.Ann.Outdated {
			t.Error("outdated comment not marked outdated")
		}
	}
}

func hasNoteRow(m Model, body string) bool {
	for _, row := range m.doc.Rows {
		if row.Kind == render.RowNote && row.Ann != nil && strings.Contains(row.Ann.Body, body) {
			return true
		}
	}
	return false
}

// GitHub rejects approving your own pull request with a bare 422, so the
// submit screen must not offer it.
func TestCannotApproveOwnPullRequest(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{
			Kind: SourcePR, Repo: "acme/x", PRNumber: 1,
			Author: "tobias", Viewer: "tobias",
		}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.rebuild()
	m = press(t, m, "S")

	for _, key := range []string{"a", "r"} {
		got := press(t, m, key)
		if got.submit.event != ghsrc.EventComment {
			t.Errorf("%q selected %q on your own pull request", key, got.submit.event)
		}
		if !strings.Contains(got.err, "your own pull request") {
			t.Errorf("%q gave no explanation: %q", key, got.err)
		}
	}

	// Commenting on your own pull request is fine.
	if got := press(t, m, "c"); got.submit.event != ghsrc.EventComment || got.err != "" {
		t.Errorf("comment was refused on own pull request: %q", got.err)
	}
}

func TestCanApproveSomeoneElsesPullRequest(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{
			Kind: SourcePR, Repo: "acme/x", PRNumber: 1,
			Author: "ann", Viewer: "tobias",
		}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.rebuild()
	m = press(t, m, "S")

	if m = press(t, m, "a"); m.submit.event != ghsrc.EventApprove {
		t.Errorf("approve was blocked on someone else's pull request: %q", m.err)
	}
}

// Unknown identity must not block anything: failing to read the viewer should
// degrade to GitHub's own validation, not to a refusal.
func TestUnknownIdentityDoesNotBlockApproval(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1, Author: "ann"}
	})
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "a note")
	m.rebuild()
	m = press(t, m, "S")
	if m = press(t, m, "a"); m.submit.event != ghsrc.EventApprove {
		t.Error("approve was blocked when the viewer is unknown")
	}
}

// GitHub requires both ends of a multi-line comment to be in the same hunk.
const twoHunkDiff = `diff --git a/svc.go b/svc.go
index aaaaaaa..bbbbbbb 100644
--- a/svc.go
+++ b/svc.go
@@ -1,3 +1,4 @@
 package svc
+// first hunk
 
@@ -40,3 +41,4 @@
 func Other() {}
+// second hunk
 
`

func TestSelectionCannotSpanTwoHunks(t *testing.T) {
	m := newReviewModel(t, func(o *Options) { o.Files = diffparse.Parse(twoHunkDiff) })

	m = seekLine(t, m, 2) // first hunk
	m = press(t, m, "v")
	m = seekLine(t, m, 42) // second hunk
	m = press(t, m, "c")

	if m.mode == modeInput {
		t.Error("a note spanning two hunks was allowed — GitHub rejects the whole review for this")
	}
	if !strings.Contains(m.err, "hunk") {
		t.Errorf("err = %q, want it to explain the hunk boundary", m.err)
	}
}

func TestSelectionWithinOneHunkIsAllowed(t *testing.T) {
	m := newReviewModel(t, func(o *Options) { o.Files = diffparse.Parse(twoHunkDiff) })

	m = seekLine(t, m, 1)
	m = press(t, m, "v")
	m = seekLine(t, m, 2)
	m = press(t, m, "c")
	if m.mode != modeInput {
		t.Fatalf("a note inside one hunk was refused: %q", m.err)
	}
	m = typeText(t, m, "fine")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if n := m.review.Notes[0]; n.StartLine != 1 || n.Line != 2 {
		t.Errorf("note range = %d..%d, want 1..2", n.StartLine, n.Line)
	}
}

// A long note is wrapped where it is read, not cut off: the cursor row
// expands, and every line of it hangs under the author's name.
func TestFocusedNoteWrapsUnderItsLabel(t *testing.T) {
	m := seekLine(t, newReviewModel(t), 3)
	m = press(t, m, "c")
	m = typeText(t, m, "GitHub rejects the whole review with a bare 422 when both ends of a "+
		"multi-line comment do not sit in the same hunk, so this wants a guard before it is sent.")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	for i, row := range m.doc.Rows {
		if row.Kind == render.RowNote {
			m.cursor = i
			break
		}
	}
	lines := m.rend.RenderLines(m.doc.Rows[m.cursor], 60, 0, true, 8)
	if len(lines) < 2 {
		t.Fatalf("the focused note rendered %d lines, want it wrapped", len(lines))
	}
	if strings.Contains(strings.Join(lines, ""), "…") {
		t.Error("the note was truncated inside its own expansion")
	}

	// The first rune is the edge marker; the text starts after the spaces
	// that follow it.
	indent := func(s string) int {
		body := []rune(s)[1:]
		return strings.IndexFunc(string(body), func(r rune) bool { return r != ' ' })
	}
	if indent(lines[1]) <= indent(lines[0]) {
		t.Errorf("continuation line does not hang under the label: %q then %q", lines[0], lines[1])
	}

	// Away from the cursor the same note is one line, so a conversation never
	// buries the code it is about.
	if compact := m.rend.RenderLines(m.doc.Rows[m.cursor], 60, 0, false, 1); len(compact) != 1 {
		t.Errorf("unfocused note rendered %d lines, want 1", len(compact))
	}
}
