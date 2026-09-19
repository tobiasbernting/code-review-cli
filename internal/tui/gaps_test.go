package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// gapDiff changes lines 2, 51 and 61 of a hundred-line file, "line N" on
// every line it did not change. Its Gaps: lines 4-49 (46 lines), 53-59 (7),
// and, once the file has been read, 63-100 (38).
const gapDiff = `diff --git a/gap.go b/gap.go
index aaaaaaa..bbbbbbb 100644
--- a/gap.go
+++ b/gap.go
@@ -1,3 +1,3 @@
 line 1
-line 2
+TWO
 line 3
@@ -50,3 +50,3 @@
 line 50
-line 51
+FIFTY-ONE
 line 52
@@ -60,3 +60,3 @@
 line 60
-line 61
+SIXTY-ONE
 line 62
`

// gapFile is gap.go's new side, lines lines long.
func gapFile(lines int) []byte {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		switch i {
		case 2:
			b.WriteString("TWO\n")
		case 51:
			b.WriteString("FIFTY-ONE\n")
		case 61:
			b.WriteString("SIXTY-ONE\n")
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	return []byte(b.String())
}

func readGapFile(string) ([]byte, error) { return gapFile(100), nil }

func newGapModel(t *testing.T, diff string, read func(string) ([]byte, error), opts ...func(*Options)) Model {
	t.Helper()
	o := Options{
		Files:  diffparse.Parse(diff),
		Theme:  render.DefaultTheme(),
		Config: config.Defaults(),
		Source: Source{Kind: SourceLocal, Title: "test", FileText: read},
		Review: newTestReview(t),
	}
	for _, f := range opts {
		f(&o)
	}
	m := New(o)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return run(t, next.(Model), cmd)
}

// run feeds back everything a command produces, the way the program would.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		next, more := m.Update(msg)
		m = next.(Model)
		queue = append(queue, more)
	}
	return m
}

// pressRun presses keys and runs whatever each asks for.
func pressRun(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, cmd := m.Update(keyMsg(k))
		m = run(t, next.(Model), cmd)
	}
	return m
}

// expandedNums is the new-side number of every expanded line, in order.
func expandedNums(m Model) []int {
	var out []int
	for _, row := range m.doc.Rows {
		if row.Expanded {
			out = append(out, row.NewNum())
		}
	}
	return out
}

func lineSpan(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// gapRow is the row of the Gap before hunk index, if it still has one.
func (m Model) gapRow(index int) (int, *render.Gap) {
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowGap && row.Gap.Index == index {
			return i, row.Gap
		}
	}
	return -1, nil
}

func onGap(t *testing.T, m Model, index int) {
	t.Helper()
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowGap || row.Gap.Index != index {
		t.Fatalf("cursor is on %+v, want the Gap before hunk %d", row, index)
	}
}

func TestEnterOnAGapShowsTwentyLinesFromAbove(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	m = press(t, m.cursorOnLine(t, 3, 3), "j")
	onGap(t, m, 1)

	m = pressRun(t, m, "enter")
	if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(4, 23)); got != want {
		t.Errorf("expanded %s, want %s: the lines after the hunk above", got, want)
	}
	onGap(t, m, 1)
	if _, g := m.gapRow(1); g == nil || g.Lines != 26 {
		t.Errorf("Gap left = %+v, want 26 lines", g)
	}
	if !strings.Contains(m.View(), "⋯ 26 unchanged lines") {
		t.Errorf("the Gap row does not count what is left:\n%s", m.View())
	}

	m = pressRun(t, m, "enter")
	if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(4, 43)); got != want {
		t.Errorf("a second enter expanded %s, want %s", got, want)
	}
}

func TestEnterOnAGapShowsTwentyLinesFromBelow(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	m = press(t, m.cursorOnRow(t, render.RowHunk, 1), "k")
	onGap(t, m, 1)

	m = pressRun(t, m, "enter")
	if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(30, 49)); got != want {
		t.Errorf("expanded %s, want %s: the lines before the hunk below", got, want)
	}
	onGap(t, m, 1)
}

func TestEnterShowsASmallGapWhole(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	m = press(t, m.cursorOnLine(t, 52, 52), "j")
	onGap(t, m, 2)

	m = pressRun(t, m, "enter")
	if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(53, 59)); got != want {
		t.Errorf("expanded %s, want the whole 7-line Gap", got)
	}
	if i, _ := m.gapRow(2); i >= 0 {
		t.Error("a Gap shown whole kept its row")
	}
	if got := m.doc.Rows[m.cursor].NewNum(); got != 53 {
		t.Errorf("cursor on line %d, want the first line shown", got)
	}
}

func TestShiftEnterShowsTheWholeGap(t *testing.T) {
	for _, key := range []string{"shift+enter", "alt+enter"} {
		t.Run(key, func(t *testing.T) {
			m := newGapModel(t, gapDiff, readGapFile)
			m = press(t, m.cursorOnLine(t, 3, 3), "j")
			m = pressRun(t, m, key)
			if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(4, 49)); got != want {
				t.Errorf("expanded %s, want the whole Gap", got)
			}
			if i, _ := m.gapRow(1); i >= 0 {
				t.Error("a Gap shown whole kept its row")
			}
		})
	}
}

// Whether a file goes on after its last hunk is only known once it has been
// read, which happens as soon as it is on screen.
func TestGapAfterTheLastHunkAppearsOnceTheFileIsRead(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	if _, g := m.gapRow(3); g == nil || g.Lines != 38 || g.NewStart != 63 {
		t.Fatalf("Gap after the last hunk = %+v, want lines 63-100", g)
	}
	m.cursor, _ = m.gapRow(3)
	m = pressRun(t, m, "enter")
	if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(63, 82)); got != want {
		t.Errorf("expanded %s, want %s: downward from the last hunk", got, want)
	}
}

// A file whose last line ends without a newline cannot go on, so there is
// nothing to read until a Gap is opened; opening one waits for the read.
func TestExpandingWaitsForTheFile(t *testing.T) {
	diff := gapDiff + "\\ No newline at end of file\n"
	reads := 0
	m := newGapModel(t, diff, func(string) ([]byte, error) {
		reads++
		return gapFile(62), nil
	})
	if reads != 0 {
		t.Fatalf("read the file %d times before any Gap was opened", reads)
	}
	m = press(t, m.cursorOnLine(t, 3, 3), "j")

	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)
	if cmd == nil || len(expandedNums(m)) != 0 {
		t.Fatal("enter did not wait for the file to be read")
	}
	if !strings.Contains(m.statusBar(), "reading") {
		t.Errorf("status bar does not say the file is being read:\n%s", m.statusBar())
	}
	m = run(t, m, cmd)
	if got, want := fmt.Sprint(expandedNums(m)), fmt.Sprint(lineSpan(4, 23)); got != want {
		t.Errorf("expanded %s, want %s", got, want)
	}
	onGap(t, m, 1)

	m = pressRun(t, m, "enter")
	if reads != 1 {
		t.Errorf("read the file %d times, want once", reads)
	}
}

func TestClickOnAGapExpandsItOnce(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	now := time.Now()
	m.now = func() time.Time { return now }
	i, _ := m.gapRow(1)
	y := i - m.top

	m = mouse(t, m, click(y))
	if got := len(expandedNums(m)); got != 20 {
		t.Fatalf("a click expanded %d lines, want 20", got)
	}
	onGap(t, m, 1)
	m = mouse(t, m, click(y))
	if got := len(expandedNums(m)); got != 20 {
		t.Errorf("a double click expanded %d lines, want 20", got)
	}
}

func TestExpandedLinesCannotBeCommentedOn(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	m = pressRun(t, press(t, m.cursorOnLine(t, 3, 3), "j"), "enter")

	on := m.cursorOnLine(t, 4, 4)
	if got := press(t, on, "c"); got.err != "can't comment outside the diff" || got.mode != modeDiff {
		t.Errorf("c on an expanded line: err %q, mode %v", got.err, got.mode)
	}
	if got := press(t, on, "C"); got.err != "can't comment outside the diff" || got.mode != modeDiff {
		t.Errorf("C on an expanded line: err %q, mode %v", got.err, got.mode)
	}
	if got := press(t, on, "v"); got.rangeAnchor != 0 {
		t.Error("a selection started on an expanded line")
	}

	// A selection made in the hunk cannot be carried into the Gap.
	sel := press(t, m.cursorOnLine(t, 3, 3), "v")
	sel.cursor, _ = m.doc.LineRow(0, 4, 4)
	if got := press(t, sel, "c"); got.err != "can't comment outside the diff" || got.mode != modeDiff {
		t.Errorf("c with a selection reaching an expanded line: err %q, mode %v", got.err, got.mode)
	}
}

func TestDragStopsBeforeExpandedLines(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	m = pressRun(t, press(t, m.cursorOnLine(t, 3, 3), "j"), "enter")
	m = m.cursorOnLine(t, 1, 1)
	m.top = 0
	from, _ := m.doc.LineRow(0, 1, 1)
	to, _ := m.doc.LineRow(0, 6, 6)
	m = mouse(t, m, click(from-m.top), drag(to-m.top), release(to-m.top))
	if got := m.doc.Rows[m.cursor].NewNum(); got != 3 {
		t.Errorf("the drag ended on line %d, want 3: the last line of the hunk", got)
	}
}

func TestExpandedLinesCanBeCopied(t *testing.T) {
	clip := &fakeClipboard{}
	m := newGapModel(t, gapDiff, readGapFile, func(o *Options) { o.Clipboard = clip })
	m = pressRun(t, press(t, m.cursorOnLine(t, 3, 3), "j"), "enter")
	yank(t, m.cursorOnLine(t, 4, 4), "y")
	if got := clip.last(t); got != "line 4" {
		t.Errorf("copied %q, want the expanded line", got)
	}
}

func TestSyncResetsExpansions(t *testing.T) {
	m := newGapModel(t, gapDiff, readGapFile)
	m = pressRun(t, press(t, m.cursorOnLine(t, 3, 3), "j"), "enter")
	if len(expandedNums(m)) == 0 {
		t.Fatal("nothing was expanded")
	}
	next, cmd := m.Update(syncResultMsg{
		snapshot: ghsrc.Snapshot{FetchedAt: time.Now()},
		files:    diffparse.Parse(gapDiff),
	})
	m = run(t, next.(Model), cmd)
	if got := expandedNums(m); len(got) != 0 {
		t.Errorf("lines %v are still expanded after a sync", got)
	}
	if i, _ := m.gapRow(1); i < 0 {
		t.Error("the Gap lost its row after a sync")
	}
}

func TestUnreadableFileKeepsItsGapRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		read func(string) ([]byte, error)
		want string
	}{
		{"too large", func(string) ([]byte, error) { return make([]byte, 2<<20), nil }, "can't expand: file too large"},
		{"binary", func(string) ([]byte, error) { return []byte("a\x00b"), nil }, "can't expand: binary file"},
		{"fetch error", func(string) ([]byte, error) { return nil, errors.New("network down") }, "can't expand: network down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newGapModel(t, gapDiff, tc.read)
			m = pressRun(t, press(t, m.cursorOnLine(t, 3, 3), "j"), "enter")
			if m.err != tc.want {
				t.Errorf("err = %q, want %q", m.err, tc.want)
			}
			if !strings.Contains(m.statusBar(), tc.want) {
				t.Errorf("status bar does not give the reason:\n%s", m.statusBar())
			}
			onGap(t, m, 1)
			if len(expandedNums(m)) != 0 {
				t.Error("lines were expanded from a file that could not be read")
			}
		})
	}
}

func TestReviewWithNothingToReadSaysSo(t *testing.T) {
	m := newGapModel(t, gapDiff, nil)
	m = pressRun(t, press(t, m.cursorOnLine(t, 3, 3), "j"), "enter")
	if !strings.HasPrefix(m.err, "can't expand: ") {
		t.Errorf("err = %q, want a reason the Gap can't be expanded", m.err)
	}
}
