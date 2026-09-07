package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

func testThread(id string, root int64, body string) ghsrc.Thread {
	thread := ghsrc.Thread{
		ID: id, RootID: root, Path: "svc.go", Line: 3,
		ResolutionKnown: true,
		Comments:        []ghsrc.Comment{{ID: root, Body: body, UpdatedAt: "2026-01-01T00:00:00Z"}},
	}
	thread.Comments[0].User.Login = "ann"
	return thread
}

func TestCompareThreadsFindsRemoteChanges(t *testing.T) {
	old := []ghsrc.Thread{testThread("T1", 1, "old"), testThread("T2", 2, "deleted")}
	fresh := []ghsrc.Thread{testThread("T1", 1, "edited"), testThread("T3", 3, "new")}
	fresh[0].Resolved = true
	fresh[0].Outdated = true

	newSet, updatedSet, changes := compareThreads(old, fresh)
	if !newSet[3] || len(newSet) != 1 {
		t.Errorf("new = %v", newSet)
	}
	if !updatedSet[1] || len(updatedSet) != 1 {
		t.Errorf("updated = %v", updatedSet)
	}
	if changes.deletedComments != 1 || changes.resolved != 1 || changes.outdated != 1 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestFailedSyncKeepsCurrentSnapshot(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
		o.Threads = []ghsrc.Thread{testThread("T1", 1, "current")}
	})
	originalFiles := m.files

	next, _ := m.Update(syncResultMsg{err: errFake{}})
	m = next.(Model)
	if m.files[0] != originalFiles[0] || m.threads[0].Comments[0].Body != "current" {
		t.Error("failed sync replaced part of the current snapshot")
	}
	if !strings.Contains(m.sync.err, "network") {
		t.Errorf("sync error = %q", m.sync.err)
	}
}

func TestSuccessfulSyncMarksAndNavigatesNewThread(t *testing.T) {
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1}
	})
	fresh := testThread("T3", 3, "new feedback")
	next, _ := m.Update(syncResultMsg{
		snapshot: ghsrc.Snapshot{
			Threads:   ghsrc.ThreadFeed{Threads: []ghsrc.Thread{fresh}, ResolutionKnown: true},
			FetchedAt: time.Now(),
		},
		files: diffparse.Parse(noteDiff),
	})
	m = next.(Model)
	if !m.newComments[3] || !m.expandedThreads["T3"] {
		t.Fatalf("new activity not recorded: new=%v expanded=%v", m.newComments, m.expandedThreads)
	}

	m = press(t, m, "N")
	if len(m.newComments) != 0 {
		t.Errorf("visiting the thread did not clear its new marker: %v", m.newComments)
	}
	if row := m.doc.Rows[m.cursor]; row.Ann == nil || row.Ann.ThreadID != "T3" {
		t.Errorf("N did not land in the new thread: %+v", row)
	}
}

func TestSubmittedDraftIsNotMarkedNew(t *testing.T) {
	thread := testThread("T1", 1, "same body")
	thread.Comments[0].User.Login = "me"
	newSet := map[int64]bool{1: true}
	suppressSubmitted(newSet, []ghsrc.Thread{thread}, []notes.Note{{
		Path: "svc.go", Line: 3, Body: "same body",
	}}, "me")
	if len(newSet) != 0 {
		t.Errorf("submitted comment still marked new: %v", newSet)
	}
}

func TestThreadStateControlsCollapse(t *testing.T) {
	resolved := testThread("resolved", 1, "done")
	resolved.Resolved = true
	outdatedOpen := testThread("outdated-open", 2, "still open")
	outdatedOpen.Outdated = true
	outdatedResolved := testThread("outdated-resolved", 3, "history")
	outdatedResolved.Outdated = true
	outdatedResolved.Resolved = true
	m := newReviewModel(t, func(o *Options) {
		o.Threads = []ghsrc.Thread{resolved, outdatedOpen, outdatedResolved}
	})

	states := map[string]bool{}
	for _, row := range m.doc.Rows {
		if row.Kind == render.RowNote && row.Ann != nil && row.Ann.Kind == render.AnnThread {
			states[row.Ann.ThreadID] = row.Ann.Collapsed
		}
	}
	if !states["resolved"] || states["outdated-open"] || !states["outdated-resolved"] {
		t.Errorf("collapsed states = %v", states)
	}
}

func TestMissingPathDraftAndThreadRemainVisible(t *testing.T) {
	m := newReviewModel(t)
	m.review.Add("gone.go", 0, 7, "old", "detached draft")
	thread := testThread("gone", 9, "detached thread")
	thread.Path = "gone.go"
	thread.Outdated = true
	m.threads = []ghsrc.Thread{thread}
	m.rebuild()

	if !hasNoteRow(m, "detached draft") || !hasNoteRow(m, "detached thread") {
		t.Error("an item whose path left the diff disappeared")
	}
}
