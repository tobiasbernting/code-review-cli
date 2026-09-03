package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// TestLiveSubmit posts a real review to a real pull request. It is skipped
// unless CRV_LIVE_PR and CRV_LIVE_REPO are set, because it writes to GitHub.
//
//	CRV_LIVE_REPO=owner/name CRV_LIVE_PR=8 go test ./internal/tui -run Live -v
//
// It drives the model through the same keys a person would press, so what it
// proves is the whole path — note taking, anchoring, and submission — rather
// than the API call alone.
func TestLiveSubmit(t *testing.T) {
	repo, number := os.Getenv("CRV_LIVE_REPO"), os.Getenv("CRV_LIVE_PR")
	if repo == "" || number == "" {
		t.Skip("set CRV_LIVE_REPO and CRV_LIVE_PR to run against a real pull request")
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		t.Fatalf("CRV_LIVE_PR: %v", err)
	}

	client := ghsrc.Client{Dir: os.Getenv("CRV_LIVE_DIR")}
	if err := client.Preflight(); err != nil {
		t.Fatal(err)
	}
	raw, err := client.Diff(n)
	if err != nil {
		t.Fatal(err)
	}
	files := diffparse.Parse(raw)
	diffparse.FillStats(files)
	if len(files) == 0 {
		t.Fatal("the pull request has no files")
	}

	comments, err := client.Comments(repo, n)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pull request %s#%d: %d files, %d existing comments", repo, n, len(files), len(comments))

	review, err := notes.LoadAt(filepath.Join(t.TempDir(), "r.json"), notes.PRScope(repo, n))
	if err != nil {
		t.Fatal(err)
	}

	m := New(Options{
		Files:    files,
		Theme:    render.DefaultTheme(),
		Config:   config.Defaults(),
		Source:   Source{Kind: SourcePR, Repo: repo, PRNumber: n, Client: client},
		Review:   review,
		Comments: comments,
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	// Find a real added line to comment on: GitHub rejects a comment whose
	// line is not part of the diff.
	target := -1
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowCode && row.Line.Kind == diffparse.KindAdd && row.Line.NewNum > 0 {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("no added line to comment on")
	}
	m.cursor = target
	path := m.files[m.doc.Rows[target].FileIdx].Path()
	line := m.doc.Rows[target].Line.NewNum
	t.Logf("commenting on %s line %d", path, line)

	m = press(t, m, "c")
	if m.mode != modeInput {
		t.Fatalf("could not open the note editor: %q", m.err)
	}
	m = typeText(t, m, "Posted by crv itself, proving the submit path end to end.")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if len(m.review.Notes) != 1 {
		t.Fatalf("note not recorded: %+v", m.review.Notes)
	}

	m = press(t, m, "S")
	if m.mode != modeSubmit {
		t.Fatalf("submit screen refused to open: %q", m.err)
	}
	m.submit.body = "Automated end-to-end check of crv's review submission."

	// Pressing enter returns the command that actually talks to GitHub.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("enter produced no submit command")
	}
	msg := cmd()
	result, ok := msg.(submitResultMsg)
	if !ok {
		t.Fatalf("got %T, want submitResultMsg", msg)
	}
	if result.err != nil {
		t.Fatalf("submitting failed: %v", result.err)
	}

	next, _ = m.Update(result)
	m = next.(Model)
	if len(m.review.Notes) != 0 {
		t.Errorf("local notes survived submission: %+v", m.review.Notes)
	}
	if !strings.Contains(m.status, "submitted") {
		t.Errorf("status = %q", m.status)
	}
	t.Logf("status: %s", m.status)
}
