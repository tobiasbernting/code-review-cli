package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

func queueItems() []ghsrc.QueueItem {
	return []ghsrc.QueueItem{
		{Repo: "acme/x", Number: 8, Title: "feat: notes", Author: "ann",
			Checks: "SUCCESS", UpdatedAt: time.Now().Add(-2 * time.Hour)},
		{Repo: "acme/y", Number: 4, Title: "fix: thing", Author: "bo",
			Checks: "FAILURE", UpdatedAt: time.Now().Add(-49 * time.Hour)},
	}
}

func newQueue(t *testing.T) QueueModel {
	t.Helper()
	q := NewQueue(ghsrc.Client{}, render.DefaultTheme(), 30)
	next, _ := q.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	q = next.(QueueModel)
	next, _ = q.Update(queueLoadedMsg{filter: q.filter, items: queueItems(), fetched: time.Now()})
	return next.(QueueModel)
}

func pressQ(t *testing.T, q QueueModel, keys ...string) QueueModel {
	t.Helper()
	for _, k := range keys {
		next, _ := q.Update(keyMsg(k))
		q = next.(QueueModel)
	}
	return q
}

func TestQueueSelectsPullRequest(t *testing.T) {
	q := newQueue(t)
	q = pressQ(t, q, "j")

	_, cmd := q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("choosing a row asked for nothing")
	}
	if got, ok := cmd().(openMsg); !ok || got.sel != (Selection{Repo: "acme/y", Number: 4}) {
		t.Errorf("asked to open %+v", got)
	}
}

// A pull request with commits since your review opens on those changes;
// every other one opens as it always did.
func TestQueueEnterAsksForChangesSinceReview(t *testing.T) {
	cases := []struct {
		filter ghsrc.Filter
		row    int
		want   bool
	}{
		{ghsrc.FilterReviewRequested, 0, true},  // new commits since your review
		{ghsrc.FilterReviewRequested, 1, false}, // reviewed at its head
		{ghsrc.FilterReviewRequested, 2, false}, // never reviewed
		{ghsrc.FilterAuthored, 0, false},        // your own list has no marker
	}
	for _, tc := range cases {
		q := reviewStateQueue(t, tc.filter, 100)
		q.cursor = tc.row
		_, cmd := q.Update(tea.KeyMsg{Type: tea.KeyEnter})
		got, ok := cmd().(openMsg)
		if !ok {
			t.Fatalf("%s row %d: enter asked for nothing", tc.filter.Label(), tc.row)
		}
		if got.sinceReview != tc.want {
			t.Errorf("%s row %d: sinceReview = %v, want %v", tc.filter.Label(), tc.row, got.sinceReview, tc.want)
		}
	}
}

func TestQueueQuitOpensNothing(t *testing.T) {
	_, cmd := newQueue(t).Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q did nothing")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}
}

func TestQueueCursorStaysInBounds(t *testing.T) {
	q := newQueue(t)
	q = pressQ(t, q, "k", "k")
	if q.cursor != 0 {
		t.Errorf("cursor went above the first row: %d", q.cursor)
	}
	q = pressQ(t, q, "j", "j", "j", "j")
	if q.cursor != len(q.items)-1 {
		t.Errorf("cursor went past the last row: %d", q.cursor)
	}
}

func TestQueueTogglesFilter(t *testing.T) {
	q := newQueue(t)
	if q.filter != ghsrc.FilterReviewRequested {
		t.Fatalf("default filter = %q", q.filter)
	}
	q = pressQ(t, q, "t")
	if q.filter != ghsrc.FilterAuthored {
		t.Errorf("t did not switch to authored, got %q", q.filter)
	}
	if q = pressQ(t, q, "t"); q.filter != ghsrc.FilterReviewRequested {
		t.Errorf("t did not switch back, got %q", q.filter)
	}
}

// Switching filters twice in quick succession must not let the first reply
// overwrite the list the user is now looking at.
func TestQueueIgnoresLateReplyForOldFilter(t *testing.T) {
	q := newQueue(t)
	q = pressQ(t, q, "t") // now showing "mine"

	stale := []ghsrc.QueueItem{{Repo: "acme/z", Number: 99, Title: "stale reply"}}
	next, _ := q.Update(queueLoadedMsg{filter: ghsrc.FilterReviewRequested, items: stale, fetched: time.Now()})
	q = next.(QueueModel)

	for _, it := range q.items {
		if it.Number == 99 {
			t.Fatal("a reply for the previous filter replaced the current list")
		}
	}
}

// A failed refresh should show what was cached rather than an empty screen.
func TestQueueKeepsCachedItemsOnError(t *testing.T) {
	q := newQueue(t)
	next, _ := q.Update(queueLoadedMsg{
		filter: q.filter, items: queueItems(), fetched: time.Now(), err: errFake{},
	})
	q = next.(QueueModel)

	if len(q.items) == 0 {
		t.Error("the cached list was thrown away on error")
	}
	if !strings.Contains(q.err, "cached") {
		t.Errorf("err = %q, want it to say the list is cached", q.err)
	}
}

func TestQueueViewShowsEssentials(t *testing.T) {
	view := newQueue(t).View()
	for _, want := range []string{"acme/x#8", "feat: notes", "ann", "acme/y#4", "to review"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

// reviewStateItems are one pull request of each kind the review state
// columns tell apart.
func reviewStateItems() []ghsrc.QueueItem {
	return []ghsrc.QueueItem{
		{Repo: "acme/x", Number: 8, Title: "feat: notes", Author: "ann", Checks: "SUCCESS",
			Decision: "CHANGES_REQUESTED", Additions: 123, Deletions: 45,
			HeadSHA: "bbbb", ReviewedSHA: "aaaa", UpdatedAt: time.Now().Add(-2 * time.Hour)},
		{Repo: "acme/y", Number: 4, Title: "fix: thing", Author: "bo", Checks: "SUCCESS",
			Decision: "APPROVED", Additions: 3, Deletions: 1,
			HeadSHA: "cccc", ReviewedSHA: "cccc", UpdatedAt: time.Now().Add(-3 * time.Hour)},
		{Repo: "acme/z", Number: 5, Title: "docs: readme", Author: "cy", Checks: "SUCCESS",
			Decision: "REVIEW_REQUIRED", Additions: 9, HeadSHA: "dddd",
			UpdatedAt: time.Now().Add(-4 * time.Hour)},
		{Repo: "acme/w", Number: 6, Title: "chore: deps", Author: "di", Checks: "SUCCESS",
			Additions: 1, Deletions: 1, HeadSHA: "eeee", UpdatedAt: time.Now().Add(-5 * time.Hour)},
	}
}

func reviewStateQueue(t *testing.T, filter ghsrc.Filter, width int) QueueModel {
	t.Helper()
	q := NewQueue(ghsrc.Client{}, render.DefaultTheme(), 30)
	q.filter, q.width, q.height = filter, width, 12
	q.items = reviewStateItems()
	return q
}

func plainRows(q QueueModel) []string {
	rows := make([]string, len(q.items))
	for i, it := range q.items {
		rows[i] = ansiCodes.ReplaceAllString(q.row(it, false), "")
	}
	return rows
}

func TestQueueToReviewShowsReviewState(t *testing.T) {
	q := reviewStateQueue(t, ghsrc.FilterReviewRequested, 100)
	rows := plainRows(q)

	if !strings.Contains(rows[0], newCommitsMark) {
		t.Errorf("new commits since your review are not marked: %q", rows[0])
	}
	for i, r := range rows[1:] {
		if strings.Contains(r, newCommitsMark) {
			t.Errorf("row %d is marked without new commits since a review: %q", i+1, r)
		}
	}
	for i, want := range []string{"✗ changes", "✓ approved", "○ required"} {
		if !strings.Contains(rows[i], want) {
			t.Errorf("row %d is missing %q: %q", i, want, rows[i])
		}
	}
	// No decision reported is shown as none, not as a guessed one.
	for _, word := range []string{"approved", "changes", "required"} {
		if strings.Contains(rows[3], word) {
			t.Errorf("a pull request with no decision claims %q: %q", word, rows[3])
		}
	}
	if !strings.Contains(rows[0], "+123 −45") || !strings.Contains(rows[1], "+3 −1") {
		t.Errorf("sizes missing:\n%s", strings.Join(rows, "\n"))
	}
	// The columns line up: every row ends its size at the same place.
	for i, r := range rows {
		if lipgloss.Width(r) != 100 {
			t.Errorf("row %d is %d wide, want 100: %q", i, lipgloss.Width(r), r)
		}
	}
	column := func(row, word string) int { return lipgloss.Width(row[:strings.Index(row, word)]) }
	if column(rows[0], "changes") != column(rows[1], "approved") {
		t.Errorf("the decision column does not line up:\n%s", strings.Join(rows, "\n"))
	}
}

func TestQueueMineShowsDecisionAndSizeButNoMarker(t *testing.T) {
	q := reviewStateQueue(t, ghsrc.FilterAuthored, 100)
	rows := plainRows(q)
	for i, r := range rows {
		if strings.Contains(r, newCommitsMark) {
			t.Errorf("row %d of your own pull requests has the new-commits marker: %q", i, r)
		}
	}
	if !strings.Contains(rows[0], "✗ changes") || !strings.Contains(rows[0], "+123 −45") {
		t.Errorf("decision or size missing from your own list: %q", rows[0])
	}
}

// A narrow terminal gives up the size first, then the decision's word, and
// keeps its glyph; the title keeps the room it had before.
func TestQueueReviewStateDegradesWhenNarrow(t *testing.T) {
	cases := []struct {
		width         int
		size, words   bool
		decisionGlyph bool
	}{
		{100, true, true, true},
		{50, false, true, true},
		{40, false, false, true},
	}
	for _, tc := range cases {
		q := reviewStateQueue(t, ghsrc.FilterReviewRequested, tc.width)
		row := plainRows(q)[2]
		if got := strings.Contains(row, "+9 −0"); got != tc.size {
			t.Errorf("width %d: size shown = %v, want %v: %q", tc.width, got, tc.size, row)
		}
		if got := strings.Contains(row, "required"); got != tc.words {
			t.Errorf("width %d: decision word shown = %v, want %v: %q", tc.width, got, tc.words, row)
		}
		if got := strings.Contains(row, "○"); got != tc.decisionGlyph {
			t.Errorf("width %d: decision glyph shown = %v: %q", tc.width, got, row)
		}
		// The marker is a single column and never given up.
		if !strings.Contains(plainRows(q)[0], newCommitsMark) {
			t.Errorf("width %d: the new-commits marker was dropped", tc.width)
		}
		if lipgloss.Width(row) != tc.width {
			t.Errorf("width %d: row is %d wide: %q", tc.width, lipgloss.Width(row), row)
		}
	}
}

func TestQueueEmptyStateExplainsItself(t *testing.T) {
	q := NewQueue(ghsrc.Client{}, render.DefaultTheme(), 30)
	next, _ := q.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	next, _ = next.(QueueModel).Update(queueLoadedMsg{filter: ghsrc.FilterReviewRequested, fetched: time.Now()})

	if view := next.(QueueModel).View(); !strings.Contains(view, "nothing waiting") {
		t.Errorf("empty queue said nothing useful:\n%s", view)
	}
}

// The queue is where a review starts, so it has to be as themed as the diff:
// the selected row must state its own colours everywhere, and the screen must
// paint the theme's surface rather than borrowing the terminal's.
func TestQueueSelectionIsVisibleInEveryTheme(t *testing.T) {
	// The suite runs without a TTY, where lipgloss emits no colour at all and
	// there would be nothing to check; ask for the colours a real terminal
	// would get, and put the profile back for everyone else.
	defer lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetColorProfile(termenv.TrueColor)

	for _, name := range render.ThemeNames() {
		theme, _ := render.ThemeByName(name)
		q := NewQueue(ghsrc.Client{}, theme, 30)
		q.width, q.height = 100, 12
		q.items = []ghsrc.QueueItem{
			{Repo: "acme/api", Number: 7, Title: "tighten the upload limit", Author: "robin",
				Checks: "SUCCESS", UpdatedAt: time.Now().Add(-3 * time.Hour)},
			{Repo: "acme/api", Number: 9, Title: "rename the rules package", Author: "sam",
				Checks: "FAILURE", UpdatedAt: time.Now().Add(-20 * time.Minute)},
		}

		selected := q.row(q.items[0], true)
		plain := q.row(q.items[1], false)

		if selected == plain {
			t.Errorf("%s: the selected row looks like an unselected one", name)
		}
		if !strings.Contains(selected, render.FocusBar) {
			t.Errorf("%s: the selected row has no focus bar: %q", name, selected)
		}
		if lipgloss.Width(selected) != 100 {
			t.Errorf("%s: selected row width = %d, want 100", name, lipgloss.Width(selected))
		}
		// A row that leaves any span without a background paints a hole in
		// the highlight — which is what made the cursor unreadable.
		if holes := unbackedRuns(selected); holes != 0 {
			t.Errorf("%s: %d span(s) of the selected row set no background: %q", name, holes, selected)
		}
		if holes := unbackedRuns(plain); holes != 0 {
			t.Errorf("%s: %d span(s) of an ordinary row set no background: %q", name, holes, plain)
		}
	}
}

// unbackedRuns counts the styled spans of a line that carry visible text
// without setting a background colour.
func unbackedRuns(line string) int {
	holes := 0
	for _, run := range strings.Split(line, "\x1b[0m") {
		text := ansiCodes.ReplaceAllString(run, "")
		if strings.TrimSpace(text) == "" {
			continue
		}
		if !strings.Contains(run, "48;2;") && !strings.Contains(run, "48;5;") {
			holes++
		}
	}
	return holes
}

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)
