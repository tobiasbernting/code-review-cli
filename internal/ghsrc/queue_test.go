package ghsrc

import (
	"testing"
	"time"
)

const queueJSON = `{"data":{"search":{"nodes":[
 {"number":8,"title":"feat: notes","url":"https://x/8","isDraft":false,
  "updatedAt":"2026-09-03T19:00:00Z","author":{"login":"ann"},
  "repository":{"nameWithOwner":"acme/x"},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
 {"number":4,"title":"fix: thing","url":"https://y/4","isDraft":true,
  "updatedAt":"2026-09-01T19:00:00Z","author":{"login":"bo"},
  "repository":{"nameWithOwner":"acme/y"},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}},
 {}
]}}}`

func TestParseQueue(t *testing.T) {
	items, err := parseQueue(queueJSON)
	if err != nil {
		t.Fatal(err)
	}
	// The empty node is a search result that is not a pull request.
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	first := items[0]
	if first.Repo != "acme/x" || first.Number != 8 || first.Author != "ann" {
		t.Errorf("first item = %+v", first)
	}
	if first.Checks != "SUCCESS" {
		t.Errorf("checks = %q, want SUCCESS", first.Checks)
	}

	// No checks at all is not the same as checks that have not finished.
	if items[1].Checks != "" {
		t.Errorf("a pull request with no checks reported %q", items[1].Checks)
	}
	if !items[1].IsDraft {
		t.Error("draft flag lost")
	}
}

func TestParseQueueRejectsGarbage(t *testing.T) {
	if _, err := parseQueue("not json"); err == nil {
		t.Error("expected an error for malformed output")
	}
}

func TestQueueItemAge(t *testing.T) {
	cases := []struct {
		since time.Duration
		want  string
	}{
		{30 * time.Minute, "30m"},
		{5 * time.Hour, "5h"},
		{50 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		it := QueueItem{UpdatedAt: time.Now().Add(-tc.since)}
		if got := it.Age(); got != tc.want {
			t.Errorf("age for %v = %q, want %q", tc.since, got, tc.want)
		}
	}
}

func TestFilterLabels(t *testing.T) {
	if FilterReviewRequested.Label() != "to review" || FilterAuthored.Label() != "mine" {
		t.Error("filter labels are wrong")
	}
}
