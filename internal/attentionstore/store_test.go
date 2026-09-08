package attentionstore

import (
	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"path/filepath"
	"testing"
	"time"
)

func fixture() attention.Evidence {
	return attention.Evidence{PR: attention.PR{Repo: "o/r", Number: 1}, Complete: true, Reasons: []attention.Reason{{Kind: attention.Reply, Source: "comment", Generation: "a"}}}
}
func TestRestartConcurrentDoneAndOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ev := fixture()
	n := attention.Notification{ID: "1", Repo: "o/r", Number: 1, Version: "v1", Unread: true}
	if err = s.Save(ev, []attention.Notification{n}, true); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.Tasks()
	observed := tasks[0]
	ev.Reasons[0].Generation = "b"
	if err = s.Save(ev, nil, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Done(observed); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tasks, _ = s.Tasks()
	if len(tasks) != 1 || !tasks[0].Active || tasks[0].Reason.Fingerprint != "b" {
		t.Fatal(tasks)
	}
	pending, err := s.Pending([]string{"o/r"}, time.Now())
	if err != nil || len(pending) != 2 {
		t.Fatal(pending, err)
	}
	for _, o := range pending {
		if err = s.Finish(o, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Save(ev, []attention.Notification{n}, true); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.Pending([]string{"o/r"}, time.Now())
	if len(pending) != 0 {
		t.Fatal(pending)
	}
}
func TestTransactionFailureCannotScheduleRead(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.Exec(`CREATE TRIGGER fail_evidence BEFORE INSERT ON evidence BEGIN SELECT RAISE(ABORT,'disk failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Save(fixture(), []attention.Notification{{ID: "n", Unread: true}}, true)
	if err == nil {
		t.Fatal("expected failure")
	}
	tasks, _ := s.Tasks()
	pending, _ := s.Pending([]string{"o/r"}, time.Now())
	if len(tasks) != 0 || len(pending) != 0 {
		t.Fatal(tasks, pending)
	}
}
func TestLeaseAndIdentityIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = a.Acquire("a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = b.Acquire("b", time.Now()); err == nil {
		t.Fatal("two reconcilers")
	}
	a.Release("a")
	if err = b.Acquire("b", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = a.Pin("host/1/user"); err != nil {
		t.Fatal(err)
	}
	if err = b.Pin("host/2/other"); err == nil {
		t.Fatal("account change accepted")
	}
}
func TestRemovedScopeCancelsDelivery(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Save(fixture(), []attention.Notification{{ID: "1", Unread: true}}, true); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending([]string{"other/repo"}, time.Now())
	if err != nil || len(pending) > 0 {
		t.Fatal(pending, err)
	}
	pending, _ = s.Pending([]string{"o/r"}, time.Now())
	if len(pending) > 0 {
		t.Fatal("scope removal did not cancel")
	}
}

func TestTriageDecisionIsVersionScoped(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev := fixture()
	ev.Complete = false
	ev.Problem = "permissions unavailable"
	ev.UncertaintyVersion = "v1"
	n := attention.Notification{ID: "n", Repo: "o/r", Number: 1, Version: "v1", Unread: true}
	if err = s.Save(ev, []attention.Notification{n}, false); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.Tasks()
	var triage attention.Task
	for _, task := range tasks {
		if task.Reason.Kind == attention.Triage {
			triage = task
		}
	}
	if err = s.DecideTriage(triage, true); err != nil {
		t.Fatal(err)
	}
	if !s.ReadDecided(n) {
		t.Fatal("decision missing")
	}
	n.Version = "v2"
	if s.ReadDecided(n) {
		t.Fatal("decision covered new event")
	}
	ev.UncertaintyVersion = "v2"
	if err = s.Save(ev, []attention.Notification{n}, false); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := s.Snapshot([]string{"o/r"})
	triaged, kept := false, false
	for _, task := range snapshot.Tasks {
		triaged = triaged || task.Reason.Kind == attention.Triage
		kept = kept || task.Reason.Kind == attention.Retained
	}
	if !triaged || !kept {
		t.Fatal(snapshot)
	}
	if err = s.DecideTriage(triage, false); err == nil {
		t.Fatal("stale triage dismissed new event")
	}
}
func TestConcurrentDoneAndSaveAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.sqlite")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ev := fixture()
	if err = a.Save(ev, nil, false); err != nil {
		t.Fatal(err)
	}
	tasks, _ := a.Tasks()
	ev.Reasons[0].Generation = "b"
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- a.Save(ev, nil, false) }()
	go func() { <-start; results <- b.Done(tasks[0]) }()
	close(start)
	for i := 0; i < 2; i++ {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	tasks, _ = a.Tasks()
	if len(tasks) != 1 || !tasks[0].Active || tasks[0].Reason.Fingerprint != "b" {
		t.Fatal(tasks)
	}
}
func TestWorkerAndReconcilerHaveSeparateLeases(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.AcquireWorker("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.AcquireWorker("other", time.Now()); err == nil {
		t.Fatal("duplicate worker")
	}
	if err = s.Acquire("foreground", time.Now()); err != nil {
		t.Fatal("worker blocks foreground refresh", err)
	}
}
