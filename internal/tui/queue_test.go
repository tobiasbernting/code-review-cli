package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/render"
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

	next, cmd := q.Update(tea.KeyMsg{Type: tea.KeyEnter})
	q = next.(QueueModel)
	if cmd == nil {
		t.Error("choosing a row did not end the queue")
	}
	if q.Selected != (Selection{Repo: "acme/y", Number: 4, Chosen: true}) {
		t.Errorf("selected %+v", q.Selected)
	}
}

func TestQueueQuitSelectsNothing(t *testing.T) {
	q := pressQ(t, newQueue(t), "q")
	if q.Selected.Chosen {
		t.Error("quitting chose a pull request")
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

func TestQueueEmptyStateExplainsItself(t *testing.T) {
	q := NewQueue(ghsrc.Client{}, render.DefaultTheme(), 30)
	next, _ := q.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	next, _ = next.(QueueModel).Update(queueLoadedMsg{filter: ghsrc.FilterReviewRequested, fetched: time.Now()})

	if view := next.(QueueModel).View(); !strings.Contains(view, "nothing waiting") {
		t.Errorf("empty queue said nothing useful:\n%s", view)
	}
}
