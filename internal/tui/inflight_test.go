package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
)

func TestQueueRefreshIsNotSentTwice(t *testing.T) {
	q := newQueue(t)
	next, first := q.Update(keyMsg("r"))
	q = next.(QueueModel)
	next, second := q.Update(keyMsg("r"))
	q = next.(QueueModel)
	if first == nil || second != nil {
		t.Fatalf("r while refreshing fetched again (first %v, second %v)", first != nil, second != nil)
	}

	next, _ = q.Update(queueLoadedMsg{filter: q.filter, items: queueItems(), fetched: time.Now()})
	q = next.(QueueModel)
	if _, again := q.Update(keyMsg("r")); again == nil {
		t.Error("r after the refresh landed did nothing")
	}
}

func TestQueueStaleFilterDoesNotEndCurrentLoad(t *testing.T) {
	q := newQueue(t)
	q = pressQ(t, q, "r", "t")
	if q.filter != ghsrc.FilterAuthored || !q.loading() {
		t.Fatalf("switching filter did not load it: filter %q loading %v", q.filter, q.loading())
	}
	next, _ := q.Update(queueLoadedMsg{filter: ghsrc.FilterReviewRequested, items: queueItems(), fetched: time.Now()})
	q = next.(QueueModel)
	if !q.loading() {
		t.Error("the other filter's reply cleared this filter's loading state")
	}
}

func TestSyncIsNotSentTwice(t *testing.T) {
	m := followupModel(t)
	m.mode = modeDiff
	next, first := m.Update(keyMsg("r"))
	m = next.(Model)
	next, second := m.Update(keyMsg("r"))
	m = next.(Model)
	if first == nil || second != nil {
		t.Fatalf("r while syncing synced again (first %v, second %v)", first != nil, second != nil)
	}
}

func TestReplyIsNotPostedTwice(t *testing.T) {
	m := followupModel(t)
	m = press(t, m, "c")
	m = typeText(t, m, "Still too late")
	next, first := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, second := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if first == nil || second != nil {
		t.Fatalf("a second enter posted again (first %v, second %v)", first != nil, second != nil)
	}
	if !m.requests.has(reqReply("thread")) {
		t.Error("the reply in flight is not recorded")
	}
	next, _ = m.Update(threadActionMsg{req: reqReply("thread"), id: "thread", reply: &ghsrc.Comment{ID: 12, Body: "Still too late"}})
	m = next.(Model)
	if len(m.requests) != 0 {
		t.Errorf("requests left running after the reply landed: %v", m.requests)
	}
}
