// Package tui is the interactive viewport over a rendered diff document.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type view int

const (
	viewDiff view = iota
	viewFiles
	viewHelp
)

type Model struct {
	doc   *render.Document
	rend  *render.Renderer
	theme render.Theme
	title string

	width, height int
	view          view

	cursor  int // index into doc.Rows
	top     int // first visible row
	hoffset int

	fileCursor int
	status     string
}

func New(doc *render.Document, theme render.Theme, title string) Model {
	m := Model{
		doc:   doc,
		rend:  render.NewRenderer(theme, doc),
		theme: theme,
		title: title,
		width: 80, height: 24,
	}
	m.cursor = m.nextSelectable(0, 1)
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status = ""

	if m.view == viewHelp {
		if key == "q" || key == "esc" || key == "?" {
			m.view = viewDiff
		}
		return m, nil
	}
	if m.view == viewFiles {
		return m.handleFilesKey(key)
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.view = viewHelp
	case "f":
		m.view = viewFiles
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
		m.jump(m.doc.HunkRows, 1)
	case "p":
		m.jump(m.doc.HunkRows, -1)
	case "]":
		m.jump(m.doc.FileRows, 1)
	case "[":
		m.jump(m.doc.FileRows, -1)
	case "l", "right":
		m.hoffset += 8
	case "h", "left":
		m.hoffset -= 8
		if m.hoffset < 0 {
			m.hoffset = 0
		}
	case "0":
		m.hoffset = 0
	}
	return m, nil
}

func (m Model) handleFilesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "f":
		m.view = viewDiff
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
	case "enter", " ":
		if len(m.doc.FileRows) > m.fileCursor {
			m.cursor = m.nextSelectable(m.doc.FileRows[m.fileCursor], 1)
			m.top = m.doc.FileRows[m.fileCursor]
			m.clampScroll()
		}
		m.view = viewDiff
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

func (m *Model) jump(anchors []int, dir int) {
	if len(anchors) == 0 {
		return
	}
	if dir > 0 {
		for _, a := range anchors {
			if a > m.cursor {
				m.cursor = a
				m.top = a
				m.clampScroll()
				return
			}
		}
		m.status = "last one"
		return
	}
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchors[i] < m.cursor {
			m.cursor = anchors[i]
			m.top = anchors[i]
			m.clampScroll()
			return
		}
	}
	m.status = "first one"
}

func (m *Model) viewportHeight() int {
	h := m.height - 1 // status bar
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
	switch m.view {
	case viewHelp:
		return m.helpView()
	case viewFiles:
		return m.filesView()
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
	if m.status != "" {
		left += "  · " + m.status
	}
	right := "? help  f files  q quit"
	if m.hoffset > 0 {
		right = fmt.Sprintf("→%d  ", m.hoffset) + right
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
		line := fmt.Sprintf(" %-8s %s  +%d −%d", statusLabel(f), f.Path(), f.Additions, f.Deletions)
		st := lipgloss.NewStyle()
		if idx == m.fileCursor {
			st = st.Background(lipgloss.Color(m.theme.CursorBg)).Bold(true)
		}
		b.WriteString(st.Render(pad(line, m.width)))
		b.WriteString("\n")
	}
	b.WriteString(bar(m.theme, m.width,
		fmt.Sprintf(" %d files", len(m.doc.Files)), "enter open  esc back  q quit"))
	return b.String()
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
		{"] / [", "next / previous file"},
		{"g / G", "top / bottom"},
		{"h / l, ← / →", "scroll horizontally"},
		{"0", "reset horizontal scroll"},
		{"f", "file list"},
		{"?", "this help"},
		{"q", "quit"},
	}
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.HunkFg)).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.FileFg))

	var b strings.Builder
	b.WriteString("\n  " + lipgloss.NewStyle().Bold(true).Render("crv — keys") + "\n\n")
	for _, r := range rows {
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
