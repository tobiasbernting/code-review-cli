package attention

import "testing"

func TestGenerationAcknowledgementAndIndependentReasons(t *testing.T) {
	p := PR{Repo: "o/r", Number: 1}
	r := Reason{Kind: Reply, Source: "1", Generation: "a"}
	review := Reason{Kind: Team, Source: "team", Generation: "request"}
	old := Reconcile(nil, Evidence{PR: p, Complete: true, Reasons: []Reason{r, review}}, nil)
	acks := map[string]bool{AckKey(p, r): true}
	next := Reconcile(old, Evidence{PR: p, Complete: true, Reasons: []Reason{r, review}}, acks)
	active := 0
	for _, v := range next {
		if v.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatal(next)
	}
	r.Generation = "b"
	next = Reconcile(next, Evidence{PR: p, Complete: true, Reasons: []Reason{r}}, acks)
	if len(next) != 1 || !next[0].Active {
		t.Fatal(next)
	}
}
func TestFailurePreservesWorkAndClosedConversation(t *testing.T) {
	p := PR{Repo: "o/r", Number: 1}
	old := []Task{{PR: p, Reason: Reason{Kind: Direct}, Active: true}, {PR: p, Reason: Reason{Kind: Reply}, Active: true}}
	next := Reconcile(old, Evidence{PR: p, Problem: "offline"}, nil)
	if len(next) != 3 {
		t.Fatal(next)
	}
	p.Closed = true
	next = Reconcile(old, Evidence{PR: p, Complete: true}, nil)
	if len(next) != 1 || next[0].Reason.Kind != Reply {
		t.Fatal(next)
	}
}
func TestMentionTargets(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{{"hi @alice!", true}, {"@alice-other", false}, {"@alice2", false}, {"> @alice", false}, {"`@alice`", false}, {"```go\n@alice\n```", false}, {"@ALICE", true}, {"mail@example.com", false}} {
		if got := Mentioned(tc.body, "alice"); got != tc.want {
			t.Errorf("%q: %v", tc.body, got)
		}
	}
}
func TestRerunningAndSupersededCI(t *testing.T) {
	p := PR{Repo: "o/r", Number: 1, Head: "old"}
	old := []Task{{PR: p, Reason: Reason{Kind: CI, Source: "check:test", Generation: "old"}, Active: true}}
	e := Evidence{PR: p, Complete: true, PendingCI: []string{"check:test"}}
	next := Reconcile(old, e, nil)
	if len(next) != 1 || !next[0].Active {
		t.Fatal(next)
	}
	e.PR.Head = "new"
	next = Reconcile(old, e, nil)
	if len(next) != 0 {
		t.Fatal(next)
	}
}
func TestContentReversionCreatesNewGeneration(t *testing.T) {
	p := PR{Repo: "o/r", Number: 1}
	e := Evidence{PR: p, Complete: true, Reasons: []Reason{{Kind: Reply, Source: "comment", Generation: "a"}}}
	first := Reconcile(nil, e, nil)
	acks := map[string]bool{AckKey(p, first[0].Reason): true}
	e.Reasons[0].Generation = "b"
	second := Reconcile(first, e, acks)
	e.Reasons[0].Generation = "a"
	third := Reconcile(second, e, acks)
	if !third[0].Active || third[0].Reason.Generation == first[0].Reason.Generation {
		t.Fatal(third)
	}
	same := Reconcile(third, e, acks)
	if same[0].Reason.Generation != third[0].Reason.Generation {
		t.Fatal("unchanged content reopened", same)
	}
}
