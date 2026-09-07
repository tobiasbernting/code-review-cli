package tui

import "testing"

func TestReanchorDetachedDraft(t *testing.T) {
	m := newReviewModel(t)
	n := m.review.Add("svc.go", 0, 3, "oldblob", "move me")
	m.rebuild()
	for i, row := range m.doc.Rows {
		if row.Ann != nil && row.Ann.ID == n.ID {
			m.cursor = i
			break
		}
	}
	m = press(t, m, "m")
	if m.reanchor.id != n.ID {
		t.Fatalf("re-anchor did not start: %+v", m.reanchor)
	}
	m = seekLine(t, m, 4)
	m = press(t, m, "enter")

	got := m.review.Notes[0]
	if got.Line != 4 || got.StartLine != 0 || got.Blob != "bbbbbbb" {
		t.Errorf("new anchor = %+v", got)
	}
	if got.Body != "move me" || got.ID != n.ID {
		t.Error("re-anchoring changed the draft identity or body")
	}
}

func TestReanchorDraftToRange(t *testing.T) {
	m := newReviewModel(t)
	n := m.review.Add("missing.go", 0, 2, "oldblob", "range me")
	m.rebuild()
	for i, row := range m.doc.Rows {
		if row.Ann != nil && row.Ann.ID == n.ID {
			m.cursor = i
			break
		}
	}
	m = press(t, m, "m")
	m = seekLine(t, m, 3)
	m = press(t, m, "v")
	m = seekLine(t, m, 5)
	m = press(t, m, "enter")

	got := m.review.Notes[0]
	if got.Path != "svc.go" || got.StartLine != 3 || got.Line != 5 {
		t.Errorf("new range = %+v", got)
	}
}
