package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type commentDetail struct {
	annotation render.Annotation
	offset     int
}

func (m Model) openOrToggleComment() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return m, nil
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil {
		return m, nil
	}
	if row.Ann.Kind == render.AnnThread {
		m.expandedThreads[row.Ann.ThreadID] = row.Ann.Collapsed
		m.rebuild()
		return m, nil
	}
	m.detail = commentDetail{annotation: *row.Ann}
	m.mode = modeComment
	return m, nil
}

func (m Model) handleCommentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.viewportHeight() - 3
	if visible < 1 {
		visible = 1
	}
	lines := render.WrapText(m.detail.annotation.Body, max(1, m.width-4))
	maxOffset := len(lines) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	switch msg.String() {
	case "esc", "q", "enter":
		m.mode = modeDiff
	case "j", "down":
		if m.detail.offset < maxOffset {
			m.detail.offset++
		}
	case "k", "up":
		if m.detail.offset > 0 {
			m.detail.offset--
		}
	case "ctrl+d", "pgdown":
		m.detail.offset += visible / 2
		if m.detail.offset > maxOffset {
			m.detail.offset = maxOffset
		}
	case "ctrl+u", "pgup":
		m.detail.offset -= visible / 2
		if m.detail.offset < 0 {
			m.detail.offset = 0
		}
	case "g", "home":
		m.detail.offset = 0
	case "G", "end":
		m.detail.offset = maxOffset
	}
	return m, nil
}

func (m Model) commentView() string {
	a := m.detail.annotation
	title := a.Author
	if title == "" {
		title = "you"
	}
	var states []string
	if a.NeedsReanchor {
		states = append(states, "needs re-anchor")
	}
	if a.Outdated {
		states = append(states, "outdated")
	}
	if a.Kind == render.AnnComment {
		if !a.ResolutionKnown {
			states = append(states, "resolution unavailable")
		} else if a.Resolved {
			states = append(states, "resolved")
		}
	}
	if a.New {
		states = append(states, "new")
	}
	if a.Updated {
		states = append(states, "updated")
	}
	if len(states) > 0 {
		title += " [" + strings.Join(states, ", ") + "]"
	}

	lines := render.WrapText(a.Body, max(1, m.width-4))
	visible := m.viewportHeight() - 3
	if visible < 1 {
		visible = 1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n\n", title)
	for i := 0; i < visible; i++ {
		idx := m.detail.offset + i
		if idx < len(lines) {
			b.WriteString("  " + lines[idx])
		}
		b.WriteString("\n")
	}
	left := fmt.Sprintf(" %d/%d", min(m.detail.offset+1, max(1, len(lines))), max(1, len(lines)))
	b.WriteString(bar(m.theme, m.width, left, fitHint(m.width, left, m.hintKeys())))
	return b.String()
}

func (m *Model) jumpActivity(dir int) {
	type anchor struct {
		row      int
		threadID string
	}
	var anchors []anchor
	seen := map[string]bool{}
	for i, row := range m.doc.Rows {
		if row.Kind != render.RowNote || row.Ann == nil || row.Ann.Kind != render.AnnComment {
			continue
		}
		if (!row.Ann.New && !row.Ann.Updated) || seen[row.Ann.ThreadID] {
			continue
		}
		seen[row.Ann.ThreadID] = true
		anchors = append(anchors, anchor{row: i, threadID: row.Ann.ThreadID})
	}
	if len(anchors) == 0 {
		m.status = "no new or updated comments"
		return
	}
	chosen := -1
	if dir > 0 {
		for i := range anchors {
			if anchors[i].row > m.cursor {
				chosen = i
				break
			}
		}
	} else {
		for i := len(anchors) - 1; i >= 0; i-- {
			if anchors[i].row < m.cursor {
				chosen = i
				break
			}
		}
	}
	if chosen < 0 {
		m.status = map[bool]string{true: "last new thread", false: "first new thread"}[dir > 0]
		return
	}
	m.cursor = anchors[chosen].row
	m.clampScroll()
	m.visitCurrentThread()
}

func (m *Model) visitCurrentThread() {
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil || row.Ann.Kind != render.AnnComment {
		return
	}
	if !row.Ann.New && !row.Ann.Updated {
		return
	}
	threadID := row.Ann.ThreadID
	for _, thread := range m.threads {
		if thread.ID != threadID {
			continue
		}
		for _, comment := range thread.Comments {
			delete(m.newComments, comment.ID)
			delete(m.updatedComments, comment.ID)
		}
		break
	}
	m.rebuild()
}
