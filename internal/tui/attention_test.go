package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"strings"
	"testing"
)

func TestAttentionOpeningDoesNotComplete(t *testing.T) {
	task := attention.Task{PR: attention.PR{Repo: "o/r", Number: 1}, Reason: attention.Reason{Kind: attention.Reply}, Active: true}
	m := AttentionQueue{snapshot: attention.Snapshot{Tasks: []attention.Task{task}}}
	m.regroup()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(AttentionQueue)
	if !got.Selected.Chosen || !got.snapshot.Tasks[0].Active {
		t.Fatal(got)
	}
}
func TestAttentionGroupsReasonsAndShowsStaleHealth(t *testing.T) {
	p := attention.PR{Repo: "o/r", Number: 1}
	m := AttentionQueue{snapshot: attention.Snapshot{Error: "offline", Tasks: []attention.Task{{PR: p, Reason: attention.Reason{Kind: attention.Reply}, Active: true}, {PR: p, Reason: attention.Reason{Kind: attention.Direct}, Active: true}}}}
	m.regroup()
	if len(m.groups) != 1 {
		t.Fatal(m.groups)
	}
	view := m.View()
	if !strings.Contains(view, "offline") || !strings.Contains(view, "Not synchronized") {
		t.Fatal(view)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if updated.(AttentionQueue).reason != 1 {
		t.Fatal("reason selection failed")
	}
}
func TestLateAttentionSnapshotCannotUndoDone(t *testing.T) {
	m := AttentionQueue{request: 2, snapshot: attention.Snapshot{Identity: "current"}}
	updated, _ := m.Update(attentionLoaded{request: 1, snapshot: attention.Snapshot{Identity: "old"}})
	if updated.(AttentionQueue).snapshot.Identity != "current" {
		t.Fatal("stale asynchronous result applied")
	}
}
