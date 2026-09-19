package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// replyState is the reply being written to a GitHub thread. A reply is posted
// the moment it is sent, never held as a draft, so it needs no anchor: the
// thread's root comment is all GitHub asks for.
type replyState struct {
	threadID string
	// from is the screen the reply was started on, and returns to.
	from mode
}

// threadUnderCursor is the GitHub thread the cursor is on in the diff, from
// its summary row or any of its comments.
func (m Model) threadUnderCursor() (string, bool) {
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return "", false
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil || row.Ann.ThreadID == "" {
		return "", false
	}
	if row.Ann.Kind != render.AnnThread && row.Ann.Kind != render.AnnComment {
		return "", false
	}
	return row.Ann.ThreadID, true
}

func (m Model) findThread(id string) (ghsrc.Thread, bool) {
	for _, t := range m.threads {
		if t.ID == id {
			return t, true
		}
	}
	for _, t := range m.follow.threads {
		if t.ID == id {
			return t, true
		}
	}
	return ghsrc.Thread{}, false
}

// updateThread applies change to a thread wherever the review holds it. The
// change is made once, to a copy, and the copy stored in each place, because
// those places can share a backing array.
func (m *Model) updateThread(id string, change func(*ghsrc.Thread)) bool {
	t, ok := m.findThread(id)
	if !ok {
		return false
	}
	t.Comments = append([]ghsrc.Comment(nil), t.Comments...)
	change(&t)
	replace := func(threads []ghsrc.Thread) {
		for i := range threads {
			if threads[i].ID == id {
				threads[i] = t
			}
		}
	}
	replace(m.threads)
	replace(m.follow.threads)
	if m.follow.session != nil {
		replace(m.follow.session.Threads)
	}
	return true
}

func (m Model) startReply(threadID string) (tea.Model, tea.Cmd) {
	if m.requests.has(reqSync) {
		m.err = "wait for sync to finish before replying"
		return m, nil
	}
	t, ok := m.findThread(threadID)
	if !ok || len(t.Comments) == 0 {
		return m, nil
	}
	from := m.mode
	if from == modeThreads {
		// The reply is written beside the thread it answers.
		from = modeThread
	}
	m.reply = replyState{threadID: threadID, from: from}
	if from == modeDiff && !m.expandedThreads[threadID] {
		m.expandedThreads[threadID] = true
		m.rebuild()
	}
	m.mode = modeReply
	m.in.start(fmt.Sprintf("reply to @%s (enter sends) ›", threadAuthor(t)), "")
	return m, nil
}

func threadAuthor(t ghsrc.Thread) string {
	if len(t.Comments) == 0 || t.Comments[0].User.Login == "" {
		return "unknown"
	}
	return t.Comments[0].User.Login
}

func (m Model) handleReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+e" {
		return m, m.openEditor(m.in.value, m.replyEditorHeader())
	}
	done, cancelled := m.in.handle(msg)
	switch {
	case cancelled:
		m.in.stop()
		m.mode = m.reply.from
		m.status = "reply discarded"
	case done:
		return m.sendReply(strings.TrimSpace(m.in.value))
	}
	return m, nil
}

func (m Model) replyEditorHeader() string {
	t, _ := m.findThread(m.reply.threadID)
	return fmt.Sprintf("# Reply to @%s on %s. Saving sends it to GitHub.\n"+
		"# Lines starting with # are ignored. An empty reply is discarded.\n", threadAuthor(t), t.Path)
}

// sendReply posts body to the thread being replied to. The composer stays
// open until GitHub answers, so a failure keeps the text for another try.
func (m Model) sendReply(body string) (tea.Model, tea.Cmd) {
	if body == "" {
		m.err = "write a reply, or press esc to cancel"
		return m, nil
	}
	t, ok := m.findThread(m.reply.threadID)
	if !ok || len(t.Comments) == 0 {
		m.err = "this thread is no longer in the snapshot — esc, then r to sync"
		return m, nil
	}
	req := reqReply(t.ID)
	if !m.requests.start(req) {
		return m, nil
	}
	src, root := m.src, t.Comments[0].ID
	return m, func() tea.Msg {
		comment, err := src.Client.Reply(src.Repo, src.PRNumber, root, body)
		return threadActionMsg{req: req, id: t.ID, reply: &comment, err: err}
	}
}

// applyReply lands a posted reply in the snapshot. Adding it here, rather
// than syncing, keeps the diff still while it is read, and means the next
// sync already knows the comment and does not call it new.
func (m Model) applyReply(msg threadActionMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		if strings.Contains(m.err, "HTTP 404") {
			m.err = "thread not found on GitHub — esc, then r to sync: " + m.err
		}
		return m, nil
	}
	m.in.stop()
	m.mode = m.reply.from
	m.status = "reply posted to GitHub"
	m.updateThread(msg.id, func(t *ghsrc.Thread) {
		for _, c := range t.Comments {
			if c.ID == msg.reply.ID {
				return
			}
		}
		t.Comments = append(t.Comments, *msg.reply)
	})
	m.rebuild()
	return m, nil
}

// applyReplyEditorResult sends what was written in $EDITOR, as saving a draft
// there commits it. The text is put back in the composer first, so a failed
// post still has it.
func (m Model) applyReplyEditorResult(msg editorFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = "editor: " + msg.err.Error()
		return m, nil
	}
	body := strings.TrimSpace(msg.body)
	if body == "" {
		m.in.stop()
		m.mode = m.reply.from
		m.status = "empty reply discarded"
		return m, nil
	}
	m.in.start(m.in.prompt, body)
	return m.sendReply(body)
}
