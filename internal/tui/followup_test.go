package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/followup"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
)

func followupModel(t *testing.T) Model {
	t.Helper()
	comment := ghsrc.Comment{ID: 11, Body: "Keep the nil guard", DiffHunk: "@@ -1 +1 @@\n-guard()\n+run()", OriginalCommitID: "original"}
	comment.User.Login = "reviewer"
	s := &followup.Session{
		PR: &ghsrc.PR{Number: 42, HeadSHA: "head"}, Viewer: "reviewer",
		Baseline: &ghsrc.SubmittedReview{CommitID: "baseline", SubmittedAt: time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)},
		Files:    diffparse.Parse(noteDiff),
		Threads:  []ghsrc.Thread{{ID: "thread", GraphQLID: "thread", Path: "svc.go", Line: 3, Resolved: true, ViewerCanResolve: true, ViewerCanUnresolve: true, Comments: []ghsrc.Comment{comment}}},
		Comparison: &ghsrc.RevisionComparison{BaseSHA: "baseline", HeadSHA: "head", Diff: noteDiff,
			HeadFiles: map[string]string{"svc.go": "100644:bbbbbbb"}},
	}
	return newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "team/project", PRNumber: 42, HeadSHA: "head", Viewer: "reviewer", Author: "teammate", FollowUp: s}
	})
}

func TestFollowupStartsWithResolvedThreadsVisible(t *testing.T) {
	m := followupModel(t)
	if m.mode != modeThreads {
		t.Fatalf("started in %v", m.mode)
	}
	for _, want := range []string{"resolved on GitHub", "needs verification", "Keep the nil guard", "baseline → head"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("missing %q in %s", want, m.View())
		}
	}
	m.mode = modeThread
	text := strings.Join(m.threadLines(), "\n")
	for _, want := range []string{"Original code context", "guard()", "Changes in this file", "Run() error", "Replies:"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
}

func TestVerificationIsSeparateFromResolutionAndInvalidatesOnRefresh(t *testing.T) {
	m := followupModel(t)
	m = press(t, m, "x")
	thread := m.follow.threads[0]
	if !thread.Resolved || !strings.Contains(m.threadStatus(thread), "verified by you") {
		t.Fatal("local verification changed resolution or was not saved")
	}
	s := *m.follow.session
	s.Comparison = &ghsrc.RevisionComparison{HeadFiles: map[string]string{"svc.go": "100644:new-content"}}
	s.PR = &ghsrc.PR{Number: 42, HeadSHA: "new-head"}
	next, _ := m.Update(followupSync(&s))
	m = next.(Model)
	if !strings.Contains(m.threadStatus(m.follow.threads[0]), "changed; verify again") {
		t.Fatal("changed code stayed verified")
	}
	if len(m.follow.threads) != 1 || !m.follow.threads[0].Resolved {
		t.Fatal("resolved thread disappeared")
	}
}

func TestAllChangesDoesNotLoseDraftEvidenceOrPermitWrongInlineAnchors(t *testing.T) {
	m := followupModel(t)
	m.review.Add("svc.go", 0, 3, "bbbbbbb", "draft")
	m = press(t, m, "a")
	if !m.changesView || len(m.files) == 0 || m.blobs["svc.go"] != "bbbbbbb" {
		t.Fatal("comparison replaced draft blob evidence")
	}
	m = press(t, m, "c")
	if m.mode == modeInput || !strings.Contains(m.err, "current PR diff") {
		t.Fatal("comparison allowed an invalid PR line anchor")
	}
	m = press(t, m, "D")
	if m.changesView || m.mode != modeDiff {
		t.Fatal("cannot return to current PR diff")
	}
	m = press(t, m, "t")
	if m.mode != modeThreads {
		t.Fatal("cannot return to threads")
	}
}

func TestUnavailableHistoryKeepsCommentsAndNeverShowsFakeComparison(t *testing.T) {
	m := followupModel(t)
	m.follow.session.Comparison = nil
	m.follow.session.ComparisonError = "Historical comparison unavailable"
	m = press(t, m, "a")
	if m.mode != modeThreads || m.changesView || !strings.Contains(m.err, "unavailable") {
		t.Fatal("unavailable baseline silently became PR diff")
	}
	m = press(t, m, "x")
	if len(m.review.Threads) != 0 {
		t.Fatal("missing evidence marked verified")
	}
	if !strings.Contains(m.View(), "Keep the nil guard") {
		t.Fatal("lost original comment")
	}
}

func TestReplyFailurePreservesDraftAndSuccessKeepsResolvedThread(t *testing.T) {
	m := followupModel(t)
	m = press(t, m, "c")
	m = typeText(t, m, "The check still runs too late")
	next, _ := m.Update(threadActionMsg{id: "thread", err: fmt.Errorf("offline")})
	m = next.(Model)
	if m.mode != modeReply || m.in.value != "The check still runs too late" {
		t.Fatal("failed reply lost text")
	}
	next, _ = m.Update(threadActionMsg{id: "thread", reply: &ghsrc.Comment{ID: 12, Body: m.in.value}})
	m = next.(Model)
	if m.mode != modeThread || len(m.follow.threads[0].Comments) != 2 || !m.follow.threads[0].Resolved {
		t.Fatal("reply lost conversation state")
	}
}

func TestResolveActionAndReplyUseSelectedThread(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
cat > "$CRV_TUI_THREAD_PAYLOAD"
case "$*" in
*graphql*) printf '{"data":{"result":{"thread":{"id":"thread","isResolved":false}}}}' ;;
*replies*) printf '{"id":12,"body":"Please keep the guard","user":{"login":"reviewer"}}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "request")
	t.Setenv("CRV_TUI_THREAD_PAYLOAD", payload)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := followupModel(t)
	next, cmd := m.Update(keyMsg("R"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("reopen did not run")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.follow.threads[0].Resolved || len(m.follow.threads) != 1 {
		t.Fatal("thread was not reopened in place")
	}
	m = press(t, m, "c")
	m = typeText(t, m, "Please keep the guard")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("reply did not run")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(m.follow.threads[0].Comments) != 2 || m.err != "" {
		t.Fatalf("reply failed: %s", m.err)
	}
	data, _ := os.ReadFile(payload)
	if !strings.Contains(string(data), "Please keep the guard") {
		t.Fatal("reply body lost")
	}
}

func TestDeletedThreadsAndEmptyComparisonRemainNavigable(t *testing.T) {
	m := followupModel(t)
	m.follow.session.Files = nil
	m.follow.session.Comparison.Diff = ""
	m.follow.threads[0].Path = "deleted.go"
	m = press(t, m, "a", "f", "G", "x", "enter", "f", "G", "enter", "D", "t")
	if m.mode != modeThreads || !strings.Contains(m.View(), "deleted.go") {
		t.Fatal("empty diff hid thread")
	}
}

func TestLateContextDoesNotOverwriteAnotherThread(t *testing.T) {
	m := followupModel(t)
	m.follow.contextThread = "current"
	m.follow.contextHead = "head"
	m.follow.context = "current content"
	next, _ := m.Update(threadContextMsg{thread: "previous", head: "head", text: "wrong content"})
	if next.(Model).follow.context != "current content" {
		t.Fatal("late reply overwrote selected context")
	}
}

func TestMissingContextClearsPreviousLoadingState(t *testing.T) {
	m := followupModel(t)
	m.follow.contextLoading = true
	m.follow.contextThread = "previous"
	m.follow.threads[0].Path = "deleted.go"
	if cmd := m.loadThreadContext(); cmd != nil {
		t.Fatal("missing file started context request")
	}
	if m.follow.contextLoading || !strings.Contains(m.follow.contextErr, "absent") {
		t.Fatal("missing context still loading")
	}
}

func TestLeftSideCommentNeverUsesOldLineAsCurrentAnchor(t *testing.T) {
	m := followupModel(t)
	m.follow.threads[0].Side = "LEFT"
	m.follow.threads[0].Line = 100
	m.follow.contextThread = "thread"
	m.follow.contextHead = "head"
	m.follow.context = "current line one\ncurrent line two"
	text := strings.Join(m.threadLines(), "\n")
	if !strings.Contains(text, "no reliable current-side anchor") || !strings.Contains(text, "current line one") {
		t.Fatal("old-side line treated as current line")
	}
}

func TestRefreshRetainsDraftForFileRemovedFromDiff(t *testing.T) {
	m := followupModel(t)
	note := m.review.Add("svc.go", 0, 3, "bbbbbbb", "Keep this feedback")
	s := *m.follow.session
	s.Files = nil
	next, _ := m.Update(followupSync(&s))
	m = next.(Model)
	m = press(t, m, "D")
	if m.needsReanchor() != 1 || !hasNoteRow(m, "Keep this feedback") {
		t.Fatal("detached draft lost or still submittable")
	}
	m = press(t, m, "S")
	if m.mode == modeSubmit {
		t.Fatal("unknown anchor allowed submission")
	}
	for i, row := range m.doc.Rows {
		if row.Ann != nil && row.Ann.ID == note.ID {
			m.cursor = i
			break
		}
	}
	m = press(t, m, "d")
	if len(m.review.Notes) != 0 {
		t.Fatal("detached draft could not be deleted")
	}
}

func TestFollowupScreensFitViewport(t *testing.T) {
	for _, size := range [][2]int{{100, 24}, {60, 20}, {30, 10}} {
		m := followupModel(t)
		m.width, m.height = size[0], size[1]
		m.src.Title = "team/project#42 An intentionally long title for reviewing a teammate's validation fix"
		for _, viewMode := range []mode{modeThreads, modeThread} {
			m.mode = viewMode
			lines := strings.Split(m.View(), "\n")
			if len(lines) != m.height {
				t.Fatalf("%dx%d mode %v renders %d rows", m.width, m.height, viewMode, len(lines))
			}
			for _, line := range lines {
				if lipgloss.Width(line) > m.width {
					t.Fatalf("%dx%d mode %v overflow: %q", m.width, m.height, viewMode, line)
				}
			}
		}
	}
}

func followupSync(s *followup.Session) syncResultMsg {
	return syncResultMsg{files: s.Files, snapshot: ghsrc.Snapshot{PR: s.PR, HeadSHA: s.PR.HeadSHA, Viewer: s.Viewer, Baseline: s.Baseline, Threads: ghsrc.ThreadFeed{Threads: s.Threads}, Comparison: s.Comparison, ComparisonError: s.ComparisonError, Warnings: s.Warnings}}
}

func TestFollowupRefreshFailureKeepsSnapshotAndReportsError(t *testing.T) {
	m := followupModel(t)
	before := m.follow.session
	m.sync.syncing = true
	next, _ := m.Update(syncResultMsg{err: fmt.Errorf("offline")})
	m = next.(Model)
	if m.follow.session != before || !strings.Contains(m.View(), "sync failed: offline") {
		t.Fatal("refresh failure hid state or error")
	}
}

func TestComparisonCannotReanchorDraft(t *testing.T) {
	m := followupModel(t)
	m.review.Add("svc.go", 0, 3, "old-blob", "draft")
	m = press(t, m, "a", "m")
	if m.reanchor.id != "" || !strings.Contains(m.err, "current PR diff") {
		t.Fatal("comparison accepted draft anchor")
	}
}

func TestSnapshotSyncUpdatesFullDiffAndComparisonTogether(t *testing.T) {
	m := followupModel(t)
	m = press(t, m, "a")
	snapshot := ghsrc.Snapshot{PR: &ghsrc.PR{HeadSHA: "new-head"}, HeadSHA: "new-head", Viewer: "reviewer", RawDiff: noteDiff,
		Baseline: m.follow.session.Baseline, Threads: ghsrc.ThreadFeed{Threads: m.threads}, Comparison: &ghsrc.RevisionComparison{HeadSHA: "new-head", Diff: "", HeadFiles: map[string]string{"svc.go": "100644:changed"}}}
	next, _ := m.Update(syncResultMsg{snapshot: snapshot, files: diffparse.Parse(noteDiff)})
	m = next.(Model)
	if !m.changesView || len(m.files) != 0 || m.src.HeadSHA != "new-head" {
		t.Fatal("comparison not refreshed coherently")
	}
	m = press(t, m, "D")
	if len(m.files) != 1 || m.blobs["svc.go"] != "bbbbbbb" {
		t.Fatal("full diff not preserved across comparison refresh")
	}
}
