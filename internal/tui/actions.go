package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/notes"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// cursorLine is the file, new-side line and hunk the cursor sits on, or
// ok=false when the cursor is not on a commentable line. Deleted lines are
// excluded: only the RIGHT side is written today.
func (m Model) cursorLine() (path string, line, hunk int, ok bool) {
	if m.cursor >= len(m.doc.Rows) {
		return "", 0, -1, false
	}
	row := m.doc.Rows[m.cursor]
	if row.FileIdx >= len(m.files) {
		return "", 0, -1, false
	}
	path = m.files[row.FileIdx].Path()

	switch row.Kind {
	case render.RowCode, render.RowPair:
		if row.NewNum() == 0 {
			return path, 0, row.HunkIdx, false
		}
		return path, row.NewNum(), row.HunkIdx, true
	case render.RowNote:
		if row.HunkIdx >= 0 && row.Ann != nil && row.Ann.Line > 0 {
			return path, row.Ann.Line, row.HunkIdx, true
		}
	}
	return path, 0, row.HunkIdx, false
}

func (m Model) startComment() (tea.Model, tea.Cmd) {
	path, start, line, _, err := m.draftAnchor()
	if err != "" {
		m.err = err
		return m, nil
	}

	m.pending = pendingNote{path: path, startLine: start, line: line}
	m.in.start(draftPrompt("draft", start, line), "")
	m.mode = modeInput
	return m, nil
}

// draftAnchor is the lines a new draft goes on: the selection, else the
// line under the cursor, or a reason there is none.
func (m Model) draftAnchor() (path string, start, line, hunk int, err string) {
	if m.changesView {
		return "", 0, 0, -1, "press D for the current PR diff before editing draft anchors"
	}
	path, line, hunk, ok := m.cursorLine()
	if !ok {
		return "", 0, 0, -1, "put the cursor on an added or unchanged line to comment"
	}

	start = line
	if m.rangeAnchor > 0 {
		if m.rangeAnchorPath != path {
			return "", 0, 0, -1, "the selection started in another file"
		}
		// GitHub requires both ends of a multi-line comment to sit in the
		// same diff hunk, and rejects the whole review with a bare 422 when
		// they do not.
		if m.rangeAnchorHunk != hunk {
			return "", 0, 0, -1, "a selection cannot span two hunks — press v to clear it"
		}
		start = m.rangeAnchor
		if start > line {
			start, line = line, start
		}
	}
	return path, start, line, hunk, ""
}

func draftPrompt(what string, start, line int) string {
	if start != line {
		return fmt.Sprintf("%s L%d-%d ›", what, start, line)
	}
	return fmt.Sprintf("%s L%d ›", what, line)
}

func (m *Model) clearSelection() {
	m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = 0, "", -1
}

func (m Model) toggleRangeAnchor() (tea.Model, tea.Cmd) {
	if m.changesView {
		m.err = "press D for the current PR diff before editing draft anchors"
		return m, nil
	}
	if m.rangeAnchor > 0 {
		m.clearSelection()
		m.status = "selection cleared"
		return m, nil
	}
	path, line, hunk, ok := m.cursorLine()
	if !ok {
		m.err = "no line to select here"
		return m, nil
	}
	m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = line, path, hunk
	m.status = fmt.Sprintf("selecting from L%d — move, then c to comment or y to copy", line)
	return m, nil
}

// noteUnderCursor finds the local note the cursor is on. Comments from
// teammates are not editable here.
func (m Model) noteUnderCursor() (notes.Note, bool) {
	if m.cursor >= len(m.doc.Rows) {
		return notes.Note{}, false
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil || row.Ann.Kind != render.AnnNote {
		return notes.Note{}, false
	}
	for _, n := range m.review.Notes {
		if n.ID == row.Ann.ID {
			return n, true
		}
	}
	return notes.Note{}, false
}

func (m Model) editNoteUnderCursor() (tea.Model, tea.Cmd) {
	n, ok := m.noteUnderCursor()
	if !ok {
		m.err = "put the cursor on one of your drafts to edit it"
		return m, nil
	}
	m.pending = pendingNote{path: n.Path, startLine: n.StartLine, line: n.Line, editingID: n.ID}
	m.in.start("edit ›", n.Body)
	m.mode = modeInput
	return m, nil
}

func (m Model) deleteNoteUnderCursor() (tea.Model, tea.Cmd) {
	n, ok := m.noteUnderCursor()
	if !ok {
		m.err = "put the cursor on one of your drafts to delete it"
		return m, nil
	}
	m.review.Delete(n.ID)
	m.save()
	m.rebuild()
	m.status = "draft deleted"
	return m, nil
}

func (m Model) toggleReviewed() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.doc.Rows) {
		return m, nil
	}
	row := m.doc.Rows[m.cursor]
	if row.FileIdx >= len(m.files) {
		return m, nil
	}
	path := m.files[row.FileIdx].Path()
	reviewed, _ := m.review.ReviewState(path, m.blobs[path])
	m.review.SetReviewed(path, m.blobs[path], !reviewed)
	m.save()
	m.rebuild()
	if reviewed {
		m.status = path + " unmarked"
	} else {
		m.status = path + " marked reviewed"
		m.advanceToNextUnreviewed(row.FileIdx)
	}
	return m, nil
}

// advanceToNextUnreviewed moves to the next file that is not already reviewed
// at its current blob. It deliberately does not wrap: reaching the end is a
// useful stopping point for a review queue.
func (m *Model) advanceToNextUnreviewed(current int) {
	for i := current + 1; i < len(m.files); i++ {
		reviewed, changed := m.review.ReviewState(m.files[i].Path(), m.blobs[m.files[i].Path()])
		if reviewed && !changed {
			continue
		}
		if i < len(m.doc.FileRows) {
			m.seek(m.doc.FileRows[i])
		}
		return
	}
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+e escalates a short note to a real editor, keeping what is typed.
	if msg.String() == "ctrl+e" {
		return m, m.openEditor(m.in.value, m.draftEditorHeader())
	}

	done, cancelled := m.in.handle(msg)
	switch {
	case cancelled:
		m.in.stop()
		m.mode = modeDiff
		m.status = "cancelled"
	case done:
		body := strings.TrimSpace(m.in.value)
		if m.unchangedSuggestion(body) {
			return m, nil
		}
		m.in.stop()
		m.mode = modeDiff
		m.commit(body)
	}
	return m, nil
}

// commit saves the composed note, or deletes it when the body was emptied.
func (m *Model) commit(body string) {
	if body == "" {
		if m.pending.editingID != "" {
			m.review.Delete(m.pending.editingID)
			m.status = "draft deleted"
		} else {
			m.status = "empty draft discarded"
		}
	} else if m.pending.editingID != "" {
		m.review.Update(m.pending.editingID, body)
		m.status = "draft updated"
	} else {
		m.review.Add(m.pending.path, m.pending.startLine, m.pending.line, m.blobs[m.pending.path], body)
		m.status = "draft added"
	}

	m.clearSelection()
	m.pending = pendingNote{}
	m.save()
	m.rebuild()
}

func (m *Model) save() {
	if err := m.review.Save(); err != nil {
		m.err = "could not save drafts: " + err.Error()
	}
}

type editorFinishedMsg struct {
	body string
	err  error
}

// openEditor hands the terminal to $EDITOR with the current text, followed
// by header: comment lines saying what is being written.
func (m Model) openEditor(body, header string) tea.Cmd {
	file, err := os.CreateTemp("", "krv-note-*.md")
	if err != nil {
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	name := file.Name()

	var b strings.Builder
	b.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(header)
	if _, err := file.WriteString(b.String()); err != nil {
		file.Close()
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	file.Close()

	editor := m.cfg.EditorCommand()
	parts := strings.Fields(editor)
	parts = append(parts, name)
	cmd := exec.Command(parts[0], parts[1:]...)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			os.Remove(name)
			return editorFinishedMsg{err: err}
		}
		data, readErr := os.ReadFile(name)
		os.Remove(name)
		return editorFinishedMsg{body: stripComments(string(data)), err: readErr}
	})
}

// draftEditorHeader names the lines a draft is written against.
func (m Model) draftEditorHeader() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Draft on %s", m.pending.path)
	if m.pending.startLine > 0 && m.pending.startLine != m.pending.line {
		fmt.Fprintf(&b, " lines %d-%d", m.pending.startLine, m.pending.line)
	} else {
		fmt.Fprintf(&b, " line %d", m.pending.line)
	}
	b.WriteString("\n# Lines starting with # are ignored. An empty draft is discarded.\n")
	return b.String()
}

func (m Model) applyEditorResult(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeReply {
		return m.applyReplyEditorResult(msg)
	}
	prompt := m.in.prompt
	m.in.stop()
	m.mode = modeDiff
	if msg.err != nil {
		m.err = "editor: " + msg.err.Error()
		return m, nil
	}
	body := strings.TrimSpace(msg.body)
	// The warning needs somewhere to press enter again, so the text comes
	// back to the composer.
	if m.unchangedSuggestion(body) {
		m.in.start(prompt, body)
		m.mode = modeInput
		return m, nil
	}
	m.commit(body)
	return m, nil
}

func stripComments(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
