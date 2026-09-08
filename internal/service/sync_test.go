package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"github.com/tobiasbernting/code-review-cli/internal/attentionstore"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
)

type fakeGitHub struct {
	identity      ghsrc.AttentionIdentity
	ev            attention.Evidence
	notifications []attention.Notification
	failure       error
	reads         int
	beforeRead    func()
}

func (f *fakeGitHub) AttentionIdentity() (ghsrc.AttentionIdentity, error) { return f.identity, nil }
func (f *fakeGitHub) AttentionTeams() (map[int64]bool, error)             { return map[int64]bool{1: true}, nil }
func (f *fakeGitHub) AttentionNotifications() ([]attention.Notification, error) {
	return f.notifications, nil
}
func (f *fakeGitHub) AttentionCandidates(string) ([]attention.PR, error) {
	return []attention.PR{f.ev.PR}, nil
}
func (f *fakeGitHub) AttentionEvidence(string, int, string, map[int64]bool) (attention.Evidence, error) {
	return f.ev, f.failure
}
func (f *fakeGitHub) AttentionRead(attention.Notification) error {
	if f.beforeRead != nil {
		f.beforeRead()
	}
	f.reads++
	return nil
}
func setup(t *testing.T) (*Engine, *fakeGitHub) {
	t.Helper()
	s, err := attentionstore.Open(filepath.Join(t.TempDir(), "attention.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	p := attention.PR{Repo: "o/r", Number: 1, Title: "A PR", URL: "https://github.com/o/r/pull/1"}
	f := &fakeGitHub{identity: ghsrc.AttentionIdentity{ID: 1, Login: "me"}, ev: attention.Evidence{PR: p, Complete: true, Reasons: []attention.Reason{{Kind: attention.Direct, Source: "me", Generation: "requested"}}}, notifications: []attention.Notification{{ID: "1", Repo: "o/r", Number: 1, Version: "v1", Unread: true}, {ID: "2", Repo: "outside/repo", Number: 2, Version: "v1", Unread: true}}}
	return &Engine{Store: s, GitHub: f, Host: "github.com", Repositories: []string{"o/r"}}, f
}
func TestPersistBeforeReadAndRepeatedPoll(t *testing.T) {
	e, f := setup(t)
	f.beforeRead = func() {
		tasks, err := e.Store.Tasks()
		if err != nil || len(tasks) != 1 || !tasks[0].Active {
			t.Fatalf("read before durable work: %v %v", tasks, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := e.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.reads != 1 {
		t.Fatal(f.reads)
	}
	tasks, _ := e.Store.Tasks()
	if !tasks[0].Active {
		t.Fatal("read completed local task")
	}
}
func TestIncompleteEvidencePreservesAndNeverReads(t *testing.T) {
	e, f := setup(t)
	f.notifications = nil
	if err := e.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.failure = errors.New("GraphQL partial result")
	f.ev.Reasons = nil
	f.notifications = []attention.Notification{{ID: "1", Repo: "o/r", Number: 1, Version: "v2", Unread: true}}
	if err := e.Sync(context.Background()); err == nil {
		t.Fatal("healthy failure")
	}
	if f.reads != 0 {
		t.Fatal("read incomplete evidence")
	}
	tasks, _ := e.Store.Tasks()
	if len(tasks) != 2 {
		t.Fatal(tasks)
	}
	if !strings.Contains(e.Store.Meta("error"), "partial") {
		t.Fatal("missing health failure")
	}
}
func TestAccountChangePausesOutbox(t *testing.T) {
	e, f := setup(t)
	if err := e.Store.Pin("github.com/2/other"); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(context.Background()); err == nil {
		t.Fatal("account switch accepted")
	}
	if f.reads != 0 {
		t.Fatal("wrong account read")
	}
}
func TestAlertFailureKeepsTaskAndIsDegraded(t *testing.T) {
	e, f := setup(t)
	e.Alerts = true
	e.Notify = func(context.Context, string, string) error { return errors.New("notifications denied") }
	if err := e.Sync(context.Background()); err == nil {
		t.Fatal("alert failure hidden")
	}
	tasks, _ := e.Store.Tasks()
	if len(tasks) != 1 || !tasks[0].Active {
		t.Fatal(tasks)
	}
	if e.Store.Meta("alert_error") == "" || f.reads != 1 {
		t.Fatal("delivery health missing")
	}
}
func TestGroupedReasonsAlertOnce(t *testing.T) {
	e, f := setup(t)
	f.ev.Reasons = append(f.ev.Reasons, attention.Reason{Kind: attention.Team, Source: "team", Generation: "requested"})
	count := 0
	e.Alerts = true
	e.Notify = func(_ context.Context, _ string, body string) error {
		count++
		if !strings.Contains(body, "team review") {
			t.Fatal(body)
		}
		return nil
	}
	if err := e.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal(count)
	}
}
