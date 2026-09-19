package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const suggestionFence = "```suggestion\n"

// startSuggestion opens the composer on a suggestion block holding the lines
// under the cursor, or the selection, as they read after the change: the code
// y copies. Only new-side lines can be replaced, so a deleted line is refused
// and a selection that spans one leaves it out.
func (m Model) startSuggestion() (tea.Model, tea.Cmd) {
	if m.cursor < len(m.doc.Rows) {
		if row := m.doc.Rows[m.cursor]; row.IsCode() && row.NewNum() == 0 {
			m.err = "suggestions replace new lines; this line was deleted"
			return m, nil
		}
	}
	path, start, line, hunk, err := m.draftAnchor()
	if err != "" {
		m.err = err
		return m, nil
	}
	file := m.files[m.doc.Rows[m.cursor].FileIdx]
	code := strings.Join(newSide(file.Hunks()[hunk].Lines, start, line), "\n")

	m.pending = pendingNote{path: path, startLine: start, line: line, suggestion: code, suggesting: true}
	m.in.start(draftPrompt("suggest", start, line), suggestionFence+code+"\n```")
	m.in.cursor = len([]rune(suggestionFence + code))
	m.mode = modeInput
	return m, nil
}

// unchangedSuggestion reports whether body is a suggestion that would leave
// its lines as they are, and has not been warned about yet. It warns once.
func (m *Model) unchangedSuggestion(body string) bool {
	if !m.pending.suggesting || m.pending.warned {
		return false
	}
	code, ok := suggestedCode(body)
	if !ok || code != m.pending.suggestion {
		return false
	}
	m.pending.warned = true
	m.err = "suggestion doesn't change anything — enter again to save anyway"
	return true
}

// suggestedCode is the content of the first suggestion block in body.
func suggestedCode(body string) (string, bool) {
	_, rest, ok := strings.Cut(body, suggestionFence)
	if !ok {
		return "", false
	}
	code, _, ok := strings.Cut("\n"+rest, "\n```")
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(code, "\n"), true
}
