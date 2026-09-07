package tui

import (
	"strings"
	"testing"

	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

func TestFocusedLongCommentExpandsAndOpens(t *testing.T) {
	thread := testThread("T1", 1, strings.Repeat("long comment content ", 80))
	m := newReviewModel(t, func(o *Options) { o.Threads = []ghsrc.Thread{thread} })
	for i, row := range m.doc.Rows {
		if row.Ann != nil && row.Ann.Kind == render.AnnComment {
			m.cursor = i
			break
		}
	}
	lines := m.rend.RenderLines(m.doc.Rows[m.cursor], 60, 0, true, 8)
	if len(lines) != 8 {
		t.Fatalf("focused comment rendered %d lines, want 8", len(lines))
	}
	if !strings.Contains(lines[7], "…") {
		t.Error("truncated expansion has no overflow marker")
	}
	m = press(t, m, "enter")
	if m.mode != modeComment {
		t.Fatalf("enter left mode %v, want comment detail", m.mode)
	}
}
