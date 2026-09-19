package tui

import (
	"errors"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// gapStep is how many lines enter shows of a Gap at a time. A Gap no larger
// is shown whole.
const gapStep = 20

// gapState is how far each file's Gaps are open and the text they open onto,
// by path. Every Gap starts closed, and a sync closes them all again.
type gapState struct {
	shown   map[string]map[int]render.Shown
	text    map[string][]string
	failed  map[string]string // why a file's text could not be read
	loading map[string]bool
	// waiting is the expansion asked for while its file was being read.
	waiting map[string]gapRequest
	// gen is bumped by every reset, so text read before one is dropped.
	gen int
}

func newGapState(gen int) gapState {
	return gapState{
		shown:   map[string]map[int]render.Shown{},
		text:    map[string][]string{},
		failed:  map[string]string{},
		loading: map[string]bool{},
		waiting: map[string]gapRequest{},
		gen:     gen,
	}
}

// resetGaps closes every Gap and forgets what was read: the diff they sat in
// has been replaced.
func (m *Model) resetGaps() {
	m.gaps = newGapState(m.gaps.gen + 1)
}

func (s gapState) expansion(path string) render.Expansion {
	return render.Expansion{Text: s.text[path], Shown: s.shown[path]}
}

// read reports whether a file's text has been read, or failed to be.
func (s gapState) read(path string) bool {
	_, ok := s.text[path]
	_, failed := s.failed[path]
	return ok || failed
}

// gapRequest is one expansion: lines more of the Gap before hunk index, from
// its top or from its bottom.
type gapRequest struct {
	index   int
	lines   int
	fromTop bool
	// newStart and newEnd are the hidden lines when it was asked for, which
	// is where the cursor goes if the Gap is shown whole.
	newStart, newEnd int
}

type gapTextMsg struct {
	gen  int
	path string
	text string
	err  error
}

// onGap reports whether the cursor is on a Gap row.
func (m Model) onGap() bool {
	return m.cursor >= 0 && m.cursor < len(m.doc.Rows) && m.doc.Rows[m.cursor].Kind == render.RowGap
}

// expandGap shows more of the Gap under the cursor: gapStep lines toward the
// side the cursor came from, or all of it when whole is set or there is no
// more than that left.
func (m Model) expandGap(whole bool) (tea.Model, tea.Cmd) {
	row := m.doc.Rows[m.cursor]
	if row.FileIdx >= len(m.files) || row.Gap == nil {
		return m, nil
	}
	f := m.files[row.FileIdx]
	path := f.Path()
	g := *row.Gap
	req := gapRequest{index: g.Index, lines: g.Lines, newStart: g.NewStart, newEnd: g.NewStart + g.Lines - 1}
	if !whole && g.Lines > gapStep {
		req.lines = gapStep
	}
	// A Gap with a hunk on one side only opens from that side; one between
	// two opens toward the cursor.
	switch {
	case g.Index == 0:
		req.fromTop = false
	case g.Index == len(f.Hunks()):
		req.fromTop = true
	default:
		req.fromTop = m.arrived >= 0
	}

	if reason, ok := m.gaps.failed[path]; ok {
		m.err = "can't expand: " + reason
		return m, nil
	}
	if _, ok := m.gaps.text[path]; ok {
		m.openGap(path, row.FileIdx, req)
		return m, nil
	}
	read := m.gapReader()
	if read == nil {
		m.err = "can't expand: this review has no file content to read"
		return m, nil
	}
	m.gaps.waiting[path] = req
	m.status = "reading " + path + "…"
	if m.gaps.loading[path] {
		return m, nil
	}
	return m, m.readGapText(path, read)
}

// openGap applies an expansion. With the cursor on the Gap's row, the row
// stays under it on the same screen line, so enter can go on opening it; a
// Gap shown whole leaves the cursor on the first line it showed, counted
// from the side it was opened from.
func (m *Model) openGap(path string, fileIdx int, req gapRequest) {
	onRow := m.onGap() && m.doc.Rows[m.cursor].FileIdx == fileIdx && m.doc.Rows[m.cursor].Gap.Index == req.index
	screenLine := m.cursor - m.top

	shown := m.gaps.shown[path]
	if shown == nil {
		shown = map[int]render.Shown{}
		m.gaps.shown[path] = shown
	}
	s := shown[req.index]
	if req.fromTop {
		s.Top += req.lines
	} else {
		s.Bottom += req.lines
	}
	shown[req.index] = s
	m.rebuild()
	if !onRow {
		return
	}

	cursor, found := -1, false
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowGap && row.FileIdx == fileIdx && row.Gap.Index == req.index {
			cursor, found = i, true
			break
		}
	}
	if !found {
		line := req.newEnd
		if req.fromTop {
			line = req.newStart
		}
		cursor, found = m.doc.LineRow(fileIdx, line, 0)
	}
	if found {
		m.cursor = cursor
		m.top = max(0, cursor-screenLine)
	}
	m.clampScroll()
}

func (m Model) applyGapText(msg gapTextMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.gaps.gen {
		return m, nil
	}
	delete(m.gaps.loading, msg.path)
	req, waiting := m.gaps.waiting[msg.path]
	delete(m.gaps.waiting, msg.path)
	if msg.err != nil {
		reason := gapReason(msg.err)
		m.gaps.failed[msg.path] = reason
		if waiting {
			m.status = ""
			m.err = "can't expand: " + reason
		}
		return m, nil
	}
	m.gaps.text[msg.path] = splitText(msg.text)
	if !waiting {
		// Only the Gap after the last hunk depended on it.
		m.rebuild()
		return m, nil
	}
	if m.status == "reading "+msg.path+"…" {
		m.status = ""
	}
	for fi, f := range m.files {
		if f.Path() == msg.path {
			m.openGap(msg.path, fi, req)
			break
		}
	}
	return m, nil
}

// gapReason says why a file's Gaps can't be shown, in as few words as the
// status bar has room for.
func gapReason(err error) string {
	switch {
	case errors.Is(err, ghsrc.ErrTooLarge):
		return "file too large"
	case errors.Is(err, ghsrc.ErrBinary):
		return "binary file"
	}
	return err.Error()
}

// splitText is a file's lines. It is never nil, even for an empty file, so
// that a file which has been read can be told from one which has not.
func splitText(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// readGapText reads a file in the background.
func (m Model) readGapText(path string, read func(string) (string, error)) tea.Cmd {
	m.gaps.loading[path] = true
	gen := m.gaps.gen
	return func() tea.Msg {
		text, err := read(path)
		return gapTextMsg{gen: gen, path: path, text: text, err: err}
	}
}

// gapReader reads a file's new side for its Gaps, or is nil when this review
// has nowhere to read one from.
func (m Model) gapReader() func(path string) (string, error) {
	if read := m.src.FileText; read != nil {
		return func(path string) (string, error) {
			data, err := read(path)
			if err != nil {
				return "", err
			}
			return ghsrc.Text(data)
		}
	}
	if m.src.Kind != SourcePR || m.src.Repo == "" || m.src.HeadSHA == "" {
		return nil
	}
	src, cache := m.src, m.headText
	return func(path string) (string, error) {
		return cache.get(src.HeadSHA, path, func() (string, error) {
			return src.Client.FileText(src.Repo, src.HeadSHA, path)
		})
	}
}

// prefetchGaps reads the files on screen that may go on after their last
// hunk: only the text says whether a Gap follows, and its row should be there
// before anyone goes looking for it.
func (m Model) prefetchGaps() tea.Cmd {
	if m.mode != modeDiff || m.doc == nil {
		return nil
	}
	read := m.gapReader()
	if read == nil {
		return nil
	}
	var cmds []tea.Cmd
	seen := map[int]bool{}
	for idx := m.top; idx < len(m.doc.Rows) && idx < m.top+m.viewportHeight(); idx++ {
		row := m.doc.Rows[idx]
		if row.FileIdx >= len(m.files) || seen[row.FileIdx] || row.Collapsed {
			continue
		}
		seen[row.FileIdx] = true
		f := m.files[row.FileIdx]
		path := f.Path()
		if !render.MayContinue(f) || m.gaps.read(path) || m.gaps.loading[path] {
			continue
		}
		cmds = append(cmds, m.readGapText(path, read))
	}
	return tea.Batch(cmds...)
}

// textCache keeps what a pull request's files read at its head, for the
// session. The head is a commit SHA and the blob is read by its own SHA, so
// nothing kept here can go stale; the diff itself is still never cached.
type textCache struct {
	mu   sync.Mutex
	text map[string]string
}

func (c *textCache) get(head, path string, read func() (string, error)) (string, error) {
	key := head + "\x00" + path
	c.mu.Lock()
	text, ok := c.text[key]
	c.mu.Unlock()
	if ok {
		return text, nil
	}
	text, err := read()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.text[key] = text
	c.mu.Unlock()
	return text, nil
}
