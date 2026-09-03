package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
)

// submitState drives the review submission screen. Notes are held locally
// until this point because GitHub reviews are atomic: one review, one
// notification, and a half-written review never reaches the author.
type submitState struct {
	event   string
	body    string
	editing bool // the overall review body is being typed
	sending bool
}

func (m Model) openSubmit() (tea.Model, tea.Cmd) {
	if !m.src.CanSubmit() {
		m.err = "not reviewing a pull request — notes stay local"
		return m, nil
	}
	if len(m.review.Notes) == 0 {
		m.err = "no notes to submit"
		return m, nil
	}
	if m.stale() > 0 {
		m.err = fmt.Sprintf("%d note(s) are stale — the diff moved; delete or re-anchor them first", m.stale())
		return m, nil
	}
	m.submit = submitState{event: ghsrc.EventComment}
	m.mode = modeSubmit
	return m, nil
}

func (m Model) stale() int {
	n := 0
	for _, note := range m.review.Notes {
		if noteStale(note.Blob, m.blobs[note.Path]) {
			n++
		}
	}
	return n
}

func noteStale(noteBlob, current string) bool {
	return noteBlob != "" && current != "" && noteBlob != current
}

type submitResultMsg struct {
	err   error
	event string
}

func (m Model) handleSubmitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.submit.sending {
		return m, nil
	}
	if m.submit.editing {
		done, cancelled := m.in.handle(msg)
		if cancelled {
			m.in.stop()
			m.submit.editing = false
		}
		if done {
			m.submit.body = strings.TrimSpace(m.in.value)
			m.in.stop()
			m.submit.editing = false
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "q":
		m.mode = modeDiff
	case "c":
		m.submit.event = ghsrc.EventComment
	case "a":
		m.submit.event = ghsrc.EventApprove
	case "r":
		m.submit.event = ghsrc.EventRequestChanges
	case "b":
		m.submit.editing = true
		m.in.start("review body ›", m.submit.body)
	case "enter":
		m.submit.sending = true
		return m, m.sendReview()
	}
	return m, nil
}

// sendReview posts every note as one GitHub review.
func (m Model) sendReview() tea.Cmd {
	comments := make([]ghsrc.ReviewComment, 0, len(m.review.Notes))
	for _, n := range m.review.Notes {
		rc := ghsrc.ReviewComment{
			Path: n.Path,
			Body: n.Body,
			Line: n.Line,
			Side: n.Side,
		}
		if n.StartLine > 0 && n.StartLine != n.Line {
			rc.StartLine = n.StartLine
			rc.StartSide = n.Side
		}
		comments = append(comments, rc)
	}

	src, event, body := m.src, m.submit.event, m.submit.body
	return func() tea.Msg {
		err := src.Client.SubmitReview(src.Repo, src.PRNumber, event, body, comments)
		return submitResultMsg{err: err, event: event}
	}
}

func (m Model) applySubmitResult(msg submitResultMsg) (tea.Model, tea.Cmd) {
	m.submit.sending = false
	if msg.err != nil {
		m.err = msg.err.Error()
		m.mode = modeSubmit
		return m, nil
	}

	// GitHub owns these comments now. Dropping the local copies is what keeps
	// there from being two versions of the same review.
	sent := len(m.review.Notes)
	m.review.Notes = nil
	m.save()
	m.rebuild()
	m.mode = modeDiff
	m.status = fmt.Sprintf("submitted %d comment%s as %s — reopen to see them from GitHub",
		sent, plural(sent), strings.ToLower(strings.ReplaceAll(msg.event, "_", " ")))
	return m, nil
}

func (m Model) submitView() string {
	t := m.theme
	title := lipgloss.NewStyle().Bold(true)
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(t.HunkFg)).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(t.MetaFg))
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color(t.ReviewedFg)).Bold(true)

	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", title.Render(fmt.Sprintf("Submit review to %s#%d", m.src.Repo, m.src.PRNumber)))

	for _, opt := range []struct{ key, event, label string }{
		{"c", ghsrc.EventComment, "Comment"},
		{"a", ghsrc.EventApprove, "Approve"},
		{"r", ghsrc.EventRequestChanges, "Request changes"},
	} {
		marker := "  "
		style := dim
		if m.submit.event == opt.event {
			marker, style = "▸ ", sel
		}
		fmt.Fprintf(&b, "  %s%s  %s\n", marker, key.Render(opt.key), style.Render(opt.label))
	}

	body := m.submit.body
	if body == "" {
		body = dim.Render("(none — press b to add one)")
	}
	fmt.Fprintf(&b, "\n  %s %s\n", key.Render("b"), "body: "+body)

	fmt.Fprintf(&b, "\n  %d comment%s will be posted as one review:\n\n",
		len(m.review.Notes), plural(len(m.review.Notes)))
	for i, n := range m.review.Notes {
		if i >= 8 {
			fmt.Fprintf(&b, "    %s\n", dim.Render(fmt.Sprintf("… and %d more", len(m.review.Notes)-8)))
			break
		}
		loc := fmt.Sprintf("L%d", n.Line)
		if n.StartLine > 0 {
			loc = fmt.Sprintf("L%d-%d", n.StartLine, n.Line)
		}
		fmt.Fprintf(&b, "    %s %s  %s\n", dim.Render(n.Path), dim.Render(loc), firstLine(n.Body))
	}

	if m.submit.sending {
		fmt.Fprintf(&b, "\n  %s\n", sel.Render("submitting…"))
	} else if m.err != "" {
		fmt.Fprintf(&b, "\n  %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color(t.DelFg)).Render(m.err))
	}

	fmt.Fprintf(&b, "\n  %s submit   %s cancel\n", key.Render("enter"), key.Render("esc"))
	if m.submit.editing {
		b.WriteString("\n" + m.in.render(m.width, t.NoteFg, t.NoteBg) + "\n")
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
