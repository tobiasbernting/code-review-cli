package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// openerFor returns an opener that serves a small review for any selection
// and records what it was asked for.
func openerFor(t *testing.T, asked *[]Selection) func(Selection) (Options, error) {
	return func(sel Selection) (Options, error) {
		if asked != nil {
			*asked = append(*asked, sel)
		}
		return Options{
			Files:  diffparse.Parse(navDiff),
			Theme:  render.DefaultTheme(),
			Config: config.Defaults(),
			Source: Source{Kind: SourcePR, Repo: sel.Repo, PRNumber: sel.Number, Title: "pr"},
			Review: newTestReview(t),
		}, nil
	}
}

func newApp(t *testing.T, open func(Selection) (Options, error)) App {
	t.Helper()
	q := newQueue(t)
	// The list never comes from gh in tests; returning to it reloads the
	// same rows.
	q.fetch = func(ghsrc.Filter, int, bool) ([]ghsrc.QueueItem, time.Time, error) {
		return queueItems(), time.Now(), nil
	}
	a := NewApp(q, open)
	next, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(App)
}

// settle feeds back every message the command produces within a short
// window, the way the program would. Ticks outlive the window and are
// dropped, so animation and sync timers never run in tests.
func settle(t *testing.T, a App, cmd tea.Cmd) (App, bool) {
	t.Helper()
	quit := false
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		got := make(chan tea.Msg, 1)
		go func() { got <- c() }()
		var msg tea.Msg
		select {
		case msg = <-got:
		case <-time.After(50 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case tea.QuitMsg:
			quit = true
			continue
		}
		next, more := a.Update(msg)
		a = next.(App)
		queue = append(queue, more)
	}
	return a, quit
}

func pressA(t *testing.T, a App, keys ...string) (App, bool) {
	t.Helper()
	quit := false
	for _, k := range keys {
		msg := keyMsg(k)
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}
		next, cmd := a.Update(msg)
		var q bool
		a, q = settle(t, next.(App), cmd)
		quit = quit || q
	}
	return a, quit
}

func TestAppOpensChosenPullRequestWithoutQuitting(t *testing.T) {
	var asked []Selection
	a := newApp(t, openerFor(t, &asked))

	a, quit := pressA(t, a, "j", "enter")
	if quit {
		t.Fatal("choosing a pull request ended the program")
	}
	if len(asked) != 1 || asked[0].Repo != "acme/y" || asked[0].Number != 4 {
		t.Fatalf("opener asked for %+v", asked)
	}
	if a.screen != screenReview {
		t.Fatalf("screen = %v, want the review", a.screen)
	}
}

func TestAppLeavingReviewReturnsToQueue(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "j", "enter")

	a, quit := pressA(t, a, "q")
	if quit {
		t.Fatal("q in a review opened from the queue ended the program")
	}
	if a.screen != screenQueue {
		t.Fatalf("screen = %v, want the queue", a.screen)
	}
	if a.queue.cursor != 1 {
		t.Errorf("cursor = %d, want it where it was left", a.queue.cursor)
	}
}

func TestAppReturningRefreshesTheQueue(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "j", "enter")

	// Approving the second pull request took it off the list.
	var forced bool
	a.queue.fetch = func(_ ghsrc.Filter, _ int, force bool) ([]ghsrc.QueueItem, time.Time, error) {
		forced = force
		return queueItems()[:1], time.Now(), nil
	}
	a, _ = pressA(t, a, "q")

	if !forced {
		t.Error("the list was not refreshed from GitHub on return")
	}
	if len(a.queue.items) != 1 || a.queue.cursor != 0 {
		t.Errorf("items=%d cursor=%d, want the one remaining row selected", len(a.queue.items), a.queue.cursor)
	}
}

func TestAppDropsMessagesFromALeftReview(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	first := a.gen
	a, _ = pressA(t, a, "q", "enter")

	// A sync the first review started finishes after the second has opened.
	late := reviewMsg{gen: first, msg: syncResultMsg{err: errors.New("from the old review")}}
	next, _ := a.Update(late)
	a = next.(App)
	if a.review.sync.err != "" {
		t.Errorf("the new review took the old one's sync result: %q", a.review.sync.err)
	}
}

func TestAppQWaitsForGitHubBeforeLeaving(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	a.review.follow.busy = true

	a, quit := pressA(t, a, "q")
	if quit || a.screen != screenReview {
		t.Fatalf("q left a review with a GitHub change in flight (quit=%v, screen=%v)", quit, a.screen)
	}
	if !strings.Contains(a.View(), "ctrl+c to quit") {
		t.Error("the review does not say how to get out")
	}
}

func TestAppEscCancelsALoad(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	slow := func(sel Selection) (Options, error) {
		<-release
		return openerFor(t, nil)(sel)
	}
	a := newApp(t, slow)
	a, _ = pressA(t, a, "enter")
	if a.screen != screenLoading {
		t.Fatalf("screen = %v, want the loading page", a.screen)
	}
	loading := a.gen

	a, quit := pressA(t, a, "esc")
	if quit || a.screen != screenQueue {
		t.Fatalf("esc did not return to the queue (quit=%v, screen=%v)", quit, a.screen)
	}

	opts, _ := openerFor(t, nil)(Selection{Repo: "acme/x", Number: 8})
	next, _ := a.Update(openedMsg{gen: loading, opts: opts})
	if next.(App).screen != screenQueue {
		t.Error("a cancelled load still opened its review")
	}
}

func stripANSI(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// loadingApp is an App parked on the loading page for acme/x#8.
func loadingApp(t *testing.T, width, height int) App {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a := newApp(t, func(sel Selection) (Options, error) {
		<-release
		return Options{}, errors.New("released")
	})
	next, _ := a.Update(tea.WindowSizeMsg{Width: width, Height: height})
	a, _ = pressA(t, next.(App), "enter")
	return a
}

func TestAppLoadingPageNamesThePullRequest(t *testing.T) {
	a := loadingApp(t, 100, 30)
	view := stripANSI(a.View())

	for _, want := range []string{"acme/x#8", "feat: notes", "ann", "esc"} {
		if !strings.Contains(view, want) {
			t.Errorf("loading page lacks %q:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines != 30 {
		t.Errorf("loading page is %d lines, want the full 30", lines)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d is %d wide on a 100-column terminal", i, w)
		}
	}
}

func TestAppLoadingPageAnimates(t *testing.T) {
	a := loadingApp(t, 100, 30)
	before := a.View()
	next, cmd := a.Update(frameMsg{gen: a.gen})
	a = next.(App)
	if a.View() == before {
		t.Error("a frame did not change the page")
	}
	if cmd == nil {
		t.Error("the animation stopped while still loading")
	}

	a, _ = pressA(t, a, "esc")
	if _, cmd := a.Update(frameMsg{gen: a.gen - 1}); cmd != nil {
		t.Error("the animation kept ticking after the load was cancelled")
	}
}

func TestAppLoadingPageFallsBackOnSmallTerminals(t *testing.T) {
	a := loadingApp(t, 40, 6)
	view := stripANSI(a.View())
	if !strings.Contains(view, "acme/x#8") {
		t.Errorf("small loading page lacks the pull request:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("line %d is %d wide on a 40-column terminal", i, w)
		}
	}
}

func TestAppLoadFailureStaysOnQueueWithError(t *testing.T) {
	failing := func(Selection) (Options, error) { return Options{}, errors.New("HTTP 502") }
	a := newApp(t, failing)

	a, quit := pressA(t, a, "enter")
	if quit || a.screen != screenQueue {
		t.Fatalf("a failed load left the queue (quit=%v, screen=%v)", quit, a.screen)
	}
	view := a.View()
	if !strings.Contains(view, "HTTP 502") || !strings.Contains(view, "acme/x#8") {
		t.Errorf("the queue does not say what failed:\n%s", view)
	}
	if !strings.Contains(view, "feat: notes") {
		t.Error("the error replaced the list")
	}
}

func TestAppReviewSaysQGoesBack(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	if view := stripANSI(a.View()); !strings.Contains(view, "q queue") || strings.Contains(view, "q quit") {
		t.Errorf("status bar does not say q goes back:\n%s", view)
	}
	a, _ = pressA(t, a, "?")
	if view := stripANSI(a.View()); !strings.Contains(view, "back to the queue") {
		t.Errorf("help does not say q goes back:\n%s", view)
	}
}

func TestAppCtrlCInANoteCancelsTheNote(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	for i, row := range a.review.doc.Rows {
		if _, _, _, ok := (Model{doc: a.review.doc, files: a.review.files, cursor: i}).cursorLine(); ok && row.IsCode() {
			a.review.cursor = i
			break
		}
	}
	a, _ = pressA(t, a, "c")
	if a.review.mode != modeInput {
		t.Fatalf("mode = %v, want a note being typed (err %q, status %q)", a.review.mode, a.review.err, a.review.status)
	}

	a, quit := pressA(t, a, "ctrl+c")
	if quit || a.screen != screenReview {
		t.Fatalf("ctrl+c in a note ended the review (quit=%v, screen=%v)", quit, a.screen)
	}
	if a.review.mode == modeInput {
		t.Error("ctrl+c did not cancel the note")
	}
}

func TestAppCtrlCQuitsWhileSubmitting(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	a.review.mode = modeSubmit
	a.review.submit.sending = true

	if _, quit := pressA(t, a, "ctrl+c"); !quit {
		t.Error("a hung submit made the program impossible to leave")
	}
}

func TestAppLoadFailureDoesNotOfferRetryOfTheList(t *testing.T) {
	failing := func(Selection) (Options, error) { return Options{}, errors.New("HTTP 502") }
	a := newApp(t, failing)
	a, _ = pressA(t, a, "enter")
	if strings.Contains(stripANSI(a.View()), "r retry") {
		t.Error("r is offered as a retry, but it refreshes the list")
	}

	a, _ = pressA(t, a, "j")
	if strings.Contains(stripANSI(a.View()), "HTTP 502") {
		t.Error("the failure stays after moving on")
	}
}

func TestAppCtrlCQuitsFromLoading(t *testing.T) {
	a := loadingApp(t, 100, 30)
	if _, quit := pressA(t, a, "ctrl+c"); !quit {
		t.Error("ctrl+c did not end the program while loading")
	}
}

func TestAppCtrlCQuitsFromReview(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")

	if _, quit := pressA(t, a, "ctrl+c"); !quit {
		t.Error("ctrl+c did not end the program")
	}
}
