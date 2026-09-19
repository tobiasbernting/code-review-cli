package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// replyModel is a pull request review in the diff, with one open thread of
// two comments and one resolved thread, both on svc.go.
func replyModel(t *testing.T) Model {
	t.Helper()
	open := testThread("T1", 1, "why not a map?")
	open.Comments = append(open.Comments, ghsrc.Comment{ID: 2, Body: "order matters", UpdatedAt: "2026-01-01T00:00:00Z"})
	open.Comments[1].User.Login = "bo"
	resolved := testThread("T2", 5, "typo")
	resolved.Line = 4
	resolved.Resolved = true
	return newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1, HeadSHA: "head"}
		o.Threads = []ghsrc.Thread{open, resolved}
	})
}

// onThreadRow puts the cursor on a row of the thread: its summary, or the nth
// comment when nth > 0.
func onThreadRow(t *testing.T, m Model, threadID string, nth int) Model {
	t.Helper()
	seen := 0
	for i, row := range m.doc.Rows {
		if row.Kind != render.RowNote || row.Ann == nil || row.Ann.ThreadID != threadID {
			continue
		}
		if (nth == 0 && row.Ann.Kind == render.AnnThread) || (nth > 0 && row.Ann.Kind == render.AnnComment) {
			if nth > 0 {
				seen++
				if seen < nth {
					continue
				}
			}
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no row %d of thread %s", nth, threadID)
	return m
}

func sendReply(t *testing.T, m Model, body string) (Model, tea.Cmd) {
	t.Helper()
	m = press(t, m, "c")
	if m.mode != modeReply {
		t.Fatalf("c did not start a reply: mode %v err %q", m.mode, m.err)
	}
	m = typeText(t, m, body)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(Model), cmd
}

func posted(m Model, id int64, body string) threadActionMsg {
	c := ghsrc.Comment{ID: id, Body: body}
	c.User.Login = "me"
	return threadActionMsg{req: reqReply(m.reply.threadID), id: m.reply.threadID, reply: &c}
}

func TestReplyFromDiffPostsToThreadRoot(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
echo "$*" > "$KRV_REPLY_ARGS"
cat > "$KRV_REPLY_BODY"
printf '{"id":9,"body":"kept for order","user":{"login":"me"}}'
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	args, body := filepath.Join(dir, "args"), filepath.Join(dir, "body")
	t.Setenv("KRV_REPLY_ARGS", args)
	t.Setenv("KRV_REPLY_BODY", body)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := onThreadRow(t, replyModel(t), "T1", 2)
	m = press(t, m, "c")
	if !strings.Contains(m.in.prompt, "@ann") || !strings.Contains(stripANSI(m.View()), "reply to @ann") {
		t.Errorf("the composer does not name the thread: %q", m.in.prompt)
	}
	m = typeText(t, m, "kept for order")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("enter posted nothing: %s", m.err)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)

	if got, _ := os.ReadFile(args); !strings.Contains(string(got), "repos/acme/x/pulls/1/comments/1/replies") {
		t.Errorf("reply went to %q, not the thread's root comment", got)
	}
	if got, _ := os.ReadFile(body); !strings.Contains(string(got), "kept for order") {
		t.Errorf("reply body lost: %q", got)
	}
	if m.mode != modeDiff || m.status != "reply posted to GitHub" || m.err != "" {
		t.Errorf("after posting: mode %v status %q err %q", m.mode, m.status, m.err)
	}
	if n := len(m.threads[0].Comments); n != 3 || m.threads[0].Comments[2].ID != 9 {
		t.Errorf("the reply is not in the thread: %d comments", n)
	}
	if !hasNoteRow(m, "kept for order") {
		t.Error("the reply is not drawn in the diff")
	}
}

func TestCOnCodeLineStillDrafts(t *testing.T) {
	m := seekLine(t, replyModel(t), 3)
	m = press(t, m, "c")
	if m.mode != modeInput {
		t.Errorf("c on a code line did not start a draft: mode %v", m.mode)
	}
}

func TestReplyToCollapsedThreadExpandsIt(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T2", 0)
	if !m.doc.Rows[m.cursor].Ann.Collapsed {
		t.Fatal("the resolved thread should start collapsed")
	}
	m = press(t, m, "c")
	if m.mode != modeReply || !m.expandedThreads["T2"] {
		t.Fatalf("replying did not expand the thread: mode %v", m.mode)
	}
	if !hasNoteRow(m, "typo") {
		t.Error("the thread's comments are not shown while replying")
	}
}

func TestReplyFailureKeepsTheText(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T1", 0)
	m, cmd := sendReply(t, m, "please rename")
	if cmd == nil {
		t.Fatal("enter posted nothing")
	}
	next, _ := m.Update(threadActionMsg{req: reqReply("T1"), id: "T1", reply: &ghsrc.Comment{},
		err: errors.New("gh api: Not Found (HTTP 404)")})
	m = next.(Model)
	if m.mode != modeReply || m.in.value != "please rename" {
		t.Fatalf("a failed reply lost the text: mode %v value %q", m.mode, m.in.value)
	}
	if !strings.Contains(m.err, "r to sync") {
		t.Errorf("a 404 does not suggest syncing: %q", m.err)
	}
	if len(m.requests) != 0 {
		t.Errorf("the failed request is still recorded: %v", m.requests)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Error("enter did not retry the reply")
	}
}

func TestReplyIsPostedOnce(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T1", 0)
	m, first := sendReply(t, m, "once")
	next, second := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if first == nil || second != nil {
		t.Errorf("a second enter posted again (first %v, second %v)", first != nil, second != nil)
	}
}

func TestReplyWaitsForSync(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T1", 0)
	m.requests.start(reqSync)
	m = press(t, m, "c")
	if m.mode == modeReply || !strings.Contains(m.err, "sync") {
		t.Errorf("a reply started during sync: mode %v err %q", m.mode, m.err)
	}
}

func TestEscDiscardsReply(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T1", 0)
	m = press(t, m, "c")
	m = typeText(t, m, "never mind")
	m = press(t, m, "esc")
	if m.mode != modeDiff || m.in.active {
		t.Errorf("esc did not discard the reply: mode %v", m.mode)
	}
}

func TestPostedReplyIsNotNewOnNextSync(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T1", 0)
	m, _ = sendReply(t, m, "done")
	next, _ := m.Update(posted(m, 9, "done"))
	m = next.(Model)

	next, _ = m.Update(syncResultMsg{
		snapshot: ghsrc.Snapshot{
			Threads:   ghsrc.ThreadFeed{Threads: m.threads, ResolutionKnown: true},
			FetchedAt: time.Now(),
		},
		files: diffparse.Parse(noteDiff),
	})
	m = next.(Model)
	if m.newComments[9] {
		t.Error("the reply this review posted came back marked new")
	}
}

func TestLateReplyForVanishedThreadIsDropped(t *testing.T) {
	m := onThreadRow(t, replyModel(t), "T1", 0)
	m, _ = sendReply(t, m, "late")
	msg := posted(m, 9, "late")
	m.threads = m.threads[1:]
	next, _ := m.Update(msg)
	m = next.(Model)
	if m.mode != modeDiff || hasNoteRow(m, "late") {
		t.Errorf("a reply to a thread no longer shown landed anyway: mode %v", m.mode)
	}
}

func TestFollowupReplyReturnsToThread(t *testing.T) {
	m := followupModel(t)
	m, cmd := sendReply(t, m, "still too late")
	if cmd == nil {
		t.Fatal("enter posted nothing")
	}
	next, _ := m.Update(posted(m, 12, "still too late"))
	m = next.(Model)
	if m.mode != modeThread || len(m.follow.threads[0].Comments) != 2 || len(m.threads[0].Comments) != 2 {
		t.Errorf("follow-up reply: mode %v, %d own comments, %d snapshot comments",
			m.mode, len(m.follow.threads[0].Comments), len(m.threads[0].Comments))
	}
}
