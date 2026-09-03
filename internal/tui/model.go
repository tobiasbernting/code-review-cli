// Package tui is the interactive viewport over a rendered diff document.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type mode int

const (
	modeDiff mode = iota
	modeFiles
	modeHelp
	modeInput
	modeSubmit
)

// Options are everything New needs that is not the diff itself.
type Options struct {
	Files    []*diffparse.FileDiff
	Theme    render.Theme
	Config   config.Config
	Source   Source
	Review   *notes.Review
	Comments []ghsrc.Comment
}

type Model struct {
	files  []*diffparse.FileDiff
	hl     *render.Highlighter
	doc    *render.Document
	rend   *render.Renderer
	theme  render.Theme
	cfg    config.Config
	src    Source
	review *notes.Review

	// blobs maps a path to the hash of its new-side content, so notes can be
	// anchored and staleness detected without re-reading the file.
	blobs map[string]string

	// byLine indexes comments and notes for the renderer's overlay.
	comments []ghsrc.Comment

	width, height int
	mode          mode

	cursor  int
	top     int
	hoffset int

	fileCursor int
	status     string
	err        string

	in input

	// rangeAnchor is the line a multi-line note starts from, 0 when not
	// selecting.
	rangeAnchor     int
	rangeAnchorPath string

	// pending describes the note being composed.
	pending pendingNote

	submit submitState
}

type pendingNote struct {
	path      string
	startLine int
	line      int
	editingID string
}

func New(opts Options) Model {
	m := Model{
		files:    opts.Files,
		theme:    opts.Theme,
		cfg:      opts.Config,
		src:      opts.Source,
		review:   opts.Review,
		comments: opts.Comments,
		width:    80,
		height:   24,
		hl:       render.NewHighlighter(opts.Theme.Syntax, opts.Config.Color),
	}
	m.blobs = Blobs(opts.Files)
	if m.review == nil {
		m.review = &notes.Review{Files: map[string]notes.FileMark{}}
	}
	m.rebuild()
	m.cursor = m.nextSelectable(0, 1)
	return m
}

// rebuild regenerates the document after notes or marks change, keeping the
// cursor on the same line rather than on the same row index.
func (m *Model) rebuild() {
	var anchorPath string
	var anchorLine int
	var anchorKind render.RowKind
	if m.doc != nil && m.cursor < len(m.doc.Rows) {
		row := m.doc.Rows[m.cursor]
		anchorKind = row.Kind
		if row.FileIdx < len(m.files) {
			anchorPath = m.files[row.FileIdx].Path()
		}
		anchorLine = row.Line.NewNum
	}

	m.doc = render.Build(m.files, m.hl, m.overlay())
	m.rend = render.NewRenderer(m.theme, m.doc)

	if anchorPath == "" {
		return
	}
	for i, row := range m.doc.Rows {
		if row.Kind != anchorKind || row.FileIdx >= len(m.files) {
			continue
		}
		if m.files[row.FileIdx].Path() == anchorPath && row.Line.NewNum == anchorLine {
			m.cursor = i
			m.clampScroll()
			return
		}
	}
	if m.cursor >= len(m.doc.Rows) {
		m.cursor = m.nextSelectable(len(m.doc.Rows)-1, -1)
	}
	m.clampScroll()
}

func (m *Model) overlay() render.Overlay {
	return Overlay(m.review, m.comments, m.blobs)
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil
	case editorFinishedMsg:
		return m.applyEditorResult(msg)
	case submitResultMsg:
		return m.applySubmitResult(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status, m.err = "", ""

	switch m.mode {
	case modeInput:
		return m.handleInputKey(msg)
	case modeSubmit:
		return m.handleSubmitKey(msg)
	case modeHelp:
		if key == "q" || key == "esc" || key == "?" {
			m.mode = modeDiff
		}
		return m, nil
	case modeFiles:
		return m.handleFilesKey(key)
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
	case "f":
		m.mode = modeFiles
		m.fileCursor = m.doc.Rows[m.cursor].FileIdx
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "ctrl+d", "pgdown":
		m.moveCursor(m.viewportHeight() / 2)
	case "ctrl+u", "pgup":
		m.moveCursor(-m.viewportHeight() / 2)
	case "g", "home":
		m.cursor = m.nextSelectable(0, 1)
		m.clampScroll()
	case "G", "end":
		m.cursor = m.nextSelectable(len(m.doc.Rows)-1, -1)
		m.clampScroll()
	case "n":
		m.jump(m.doc.HunkRows, 1, "hunk")
	case "p":
		m.jump(m.doc.HunkRows, -1, "hunk")
	case "tab", "J", "]":
		m.jump(m.doc.FileRows, 1, "file")
	case "shift+tab", "K", "[":
		m.jump(m.doc.FileRows, -1, "file")
	case "l", "right":
		m.hoffset += 8
	case "h", "left":
		m.hoffset -= 8
		if m.hoffset < 0 {
			m.hoffset = 0
		}
	case "0":
		m.hoffset = 0

	// review actions
	case "c":
		return m.startComment()
	case "v":
		return m.toggleRangeAnchor()
	case "e":
		return m.editNoteUnderCursor()
	case "d":
		return m.deleteNoteUnderCursor()
	case "x":
		return m.toggleReviewed()
	case "S":
		return m.openSubmit()
	}
	return m, nil
}

func (m Model) handleFilesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "f":
		m.mode = modeDiff
	case "j", "down":
		if m.fileCursor < len(m.doc.Files)-1 {
			m.fileCursor++
		}
	case "k", "up":
		if m.fileCursor > 0 {
			m.fileCursor--
		}
	case "g", "home":
		m.fileCursor = 0
	case "G", "end":
		m.fileCursor = len(m.doc.Files) - 1
	case "x":
		if m.fileCursor < len(m.files) {
			path := m.files[m.fileCursor].Path()
			reviewed, _ := m.review.ReviewState(path, m.blobs[path])
			m.review.SetReviewed(path, m.blobs[path], !reviewed)
			m.save()
			m.rebuild()
		}
	case "enter", " ":
		if len(m.doc.FileRows) > m.fileCursor {
			m.cursor = m.nextSelectable(m.doc.FileRows[m.fileCursor], 1)
			m.top = m.doc.FileRows[m.fileCursor]
			m.clampScroll()
		}
		m.mode = modeDiff
	}
	return m, nil
}

// nextSelectable finds the nearest row the cursor is allowed to rest on,
// searching in direction dir. Spacers are skipped so the cursor never appears
// to vanish between files.
func (m Model) nextSelectable(from, dir int) int {
	for i := from; i >= 0 && i < len(m.doc.Rows); i += dir {
		if m.doc.Rows[i].Kind != render.RowSpacer {
			return i
		}
	}
	for i := from; i >= 0 && i < len(m.doc.Rows); i -= dir {
		if m.doc.Rows[i].Kind != render.RowSpacer {
			return i
		}
	}
	return 0
}

func (m *Model) moveCursor(delta int) {
	target := m.cursor + delta
	if target < 0 {
		target = 0
	}
	if target >= len(m.doc.Rows) {
		target = len(m.doc.Rows) - 1
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	m.cursor = m.nextSelectable(target, dir)
	m.clampScroll()
}

// jump moves the cursor to the next or previous anchor row, scrolling that
// anchor to the top of the viewport so the file or hunk header is the first
// thing read rather than appearing at the bottom edge.
func (m *Model) jump(anchors []int, dir int, what string) {
	if len(anchors) == 0 {
		return
	}
	if dir > 0 {
		for _, a := range anchors {
			if a > m.cursor {
				m.seek(a)
				return
			}
		}
		m.status = "last " + what
		return
	}
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchors[i] < m.cursor {
			m.seek(anchors[i])
			return
		}
	}
	m.status = "first " + what
}

// seek puts row at the top of the viewport with the cursor on it.
func (m *Model) seek(row int) {
	m.cursor = row
	m.top = row
	m.clampScroll()
}

func (m *Model) viewportHeight() int {
	h := m.height - 1 // status bar
	if m.mode == modeInput {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) clampScroll() {
	vh := m.viewportHeight()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+vh {
		m.top = m.cursor - vh + 1
	}
	if maxTop := len(m.doc.Rows) - vh; m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m Model) View() string {
	switch m.mode {
	case modeHelp:
		return m.helpView()
	case modeFiles:
		return m.filesView()
	case modeSubmit:
		return m.submitView()
	}
	return m.diffView()
}

func (m Model) diffView() string {
	vh := m.viewportHeight()
	var b strings.Builder
	for i := 0; i < vh; i++ {
		idx := m.top + i
		if idx >= len(m.doc.Rows) {
			b.WriteString("\n")
			continue
		}
		b.WriteString(m.rend.Render(m.doc.Rows[idx], m.width, m.hoffset, idx == m.cursor))
		b.WriteString("\n")
	}
	if m.mode == modeInput {
		b.WriteString(m.in.render(m.width, m.theme.NoteFg, m.theme.NoteBg))
		b.WriteString("\n")
	}
	b.WriteString(m.statusBar())
	return b.String()
}

func (m Model) statusBar() string {
	if len(m.doc.Rows) == 0 {
		return bar(m.theme, m.width, " no changes", "? help  q quit")
	}
	row := m.doc.Rows[m.cursor]
	name := ""
	if row.FileIdx < len(m.doc.Files) {
		name = m.doc.Files[row.FileIdx].Path()
	}

	left := fmt.Sprintf(" [%d/%d] %s", row.FileIdx+1, len(m.doc.Files), name)
	if n := len(m.review.Notes); n > 0 {
		left += fmt.Sprintf("  ·  %d note%s", n, plural(n))
	}
	if m.rangeAnchor > 0 {
		left += fmt.Sprintf("  ·  range from L%d", m.rangeAnchor)
	}
	switch {
	case m.err != "":
		left += "  ·  " + m.err
	case m.status != "":
		left += "  ·  " + m.status
	}

	right := "c note  x reviewed  ? help  q quit"
	if m.src.CanSubmit() {
		right = "c note  S submit  ? help  q quit"
	}
	if m.width < 70 {
		right = "? help  q quit"
	}
	return bar(m.theme, m.width, left, right)
}

func (m Model) filesView() string {
	var b strings.Builder
	vh := m.viewportHeight()
	top := 0
	if m.fileCursor >= vh {
		top = m.fileCursor - vh + 1
	}
	for i := 0; i < vh; i++ {
		idx := top + i
		if idx >= len(m.doc.Files) {
			b.WriteString("\n")
			continue
		}
		f := m.doc.Files[idx]
		reviewed, changed := m.review.ReviewState(f.Path(), m.blobs[f.Path()])
		mark := " "
		switch {
		case reviewed && changed:
			mark = "~"
		case reviewed:
			mark = "✓"
		}
		line := fmt.Sprintf(" %s %-8s %s  +%d −%d", mark, statusLabel(f), f.Path(), f.Additions, f.Deletions)
		if n := m.notesFor(f.Path()); n > 0 {
			line += fmt.Sprintf("  %d note%s", n, plural(n))
		}

		st := lipgloss.NewStyle()
		if idx == m.fileCursor {
			st = st.Background(lipgloss.Color(m.theme.CursorBg)).Bold(true)
		}
		b.WriteString(st.Render(pad(line, m.width)))
		b.WriteString("\n")
	}
	b.WriteString(bar(m.theme, m.width,
		fmt.Sprintf(" %d files", len(m.doc.Files)), "enter open  x reviewed  esc back"))
	return b.String()
}

func (m Model) notesFor(path string) int {
	n := 0
	for _, note := range m.review.Notes {
		if note.Path == path {
			n++
		}
	}
	return n
}

func statusLabel(f *diffparse.FileDiff) string {
	if f.IsBinary {
		return "binary"
	}
	return f.Status.String()
}

func (m Model) helpView() string {
	rows := [][2]string{
		{"j / k, ↓ / ↑", "move down / up"},
		{"ctrl+d / ctrl+u", "half page down / up"},
		{"n / p", "next / previous hunk"},
		{"tab / shift+tab", "next / previous file"},
		{"J / K, ] / [", "next / previous file (aliases)"},
		{"g / G", "top / bottom"},
		{"h / l, ← / →", "scroll horizontally, 0 resets"},
		{"f", "file list"},
		{"", ""},
		{"c", "comment on this line"},
		{"v", "start / clear a multi-line selection"},
		{"e", "edit the note under the cursor"},
		{"d", "delete the note under the cursor"},
		{"x", "mark this file reviewed"},
		{"ctrl+e", "compose in $EDITOR while writing a note"},
		{"S", "submit the review to GitHub"},
		{"", ""},
		{"?", "this help"},
		{"q", "quit"},
	}
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.HunkFg)).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.FileFg))

	var b strings.Builder
	b.WriteString("\n  " + lipgloss.NewStyle().Bold(true).Render("crv — keys") + "\n\n")
	for _, r := range rows {
		if r[0] == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  " + keyStyle.Render(fmt.Sprintf("%-16s", r[0])) + descStyle.Render(r[1]) + "\n")
	}
	b.WriteString("\n  " + descStyle.Render("press any of q / esc / ? to return") + "\n")
	return b.String()
}

func bar(t render.Theme, width int, left, right string) string {
	st := lipgloss.NewStyle().Background(lipgloss.Color(t.FileBg)).Foreground(lipgloss.Color(t.FileFg))
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return st.Render(pad(left, width))
	}
	return st.Render(left + strings.Repeat(" ", gap) + right + " ")
}

func pad(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
