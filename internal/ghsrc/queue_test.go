package ghsrc

import (
	"encoding/json"
	"strings"
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

const reviewStateJSON = `{"data":{"search":{"nodes":[
 {"number":8,"title":"feat: notes","repository":{"nameWithOwner":"acme/x"},
  "reviewDecision":"CHANGES_REQUESTED","additions":123,"deletions":45,
  "headRefOid":"bbbb",
  "viewerLatestReview":{"commit":{"oid":"aaaa"},"submittedAt":"2026-09-02T10:00:00Z","state":"CHANGES_REQUESTED"}},
 {"number":4,"title":"fix: thing","repository":{"nameWithOwner":"acme/y"},
  "reviewDecision":null,"additions":0,"deletions":7,"headRefOid":"cccc",
  "viewerLatestReview":null},
 {"number":5,"title":"wip","repository":{"nameWithOwner":"acme/z"},
  "reviewDecision":"REVIEW_REQUIRED","headRefOid":"dddd",
  "viewerLatestReview":{"commit":{"oid":"eeee"},"submittedAt":null,"state":"PENDING"}}
]}}}`

func TestParseQueueReviewState(t *testing.T) {
	items, err := parseQueue(reviewStateJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	reviewed := items[0]
	if reviewed.Decision != "CHANGES_REQUESTED" || reviewed.Additions != 123 || reviewed.Deletions != 45 {
		t.Errorf("reviewed item = %+v", reviewed)
	}
	if reviewed.HeadSHA != "bbbb" || reviewed.ReviewedSHA != "aaaa" {
		t.Errorf("head %q, reviewed %q", reviewed.HeadSHA, reviewed.ReviewedSHA)
	}

	// A null decision is no decision, not a guessed one.
	never := items[1]
	if never.Decision != "" || never.ReviewedSHA != "" || never.Deletions != 7 {
		t.Errorf("never-reviewed item = %+v", never)
	}

	// A pending review has not been submitted, so nothing was reviewed yet.
	if items[2].ReviewedSHA != "" {
		t.Errorf("a pending review counted as reviewed at %q", items[2].ReviewedSHA)
	}
}

func TestQueueQueryAsksForReviewState(t *testing.T) {
	for _, field := range []string{"reviewDecision", "additions", "deletions", "headRefOid",
		"viewerLatestReview", "commit { oid }", "submittedAt", "state"} {
		if !strings.Contains(queueQuery, field) {
			t.Errorf("the queue query does not ask for %s", field)
		}
	}
}

// A cache written before the review state was fetched must still load: the
// new fields are simply absent.
func TestOldQueueCacheDecodes(t *testing.T) {
	old := `{"fetched":"2026-09-18T08:00:00Z","items":{"review-requested:@me":[
	 {"Repo":"acme/x","Number":8,"Title":"feat: notes","Author":"ann","URL":"https://x/8",
	  "IsDraft":false,"Checks":"SUCCESS","UpdatedAt":"2026-09-17T08:00:00Z"}]}}`
	var cache queueCache
	if err := json.Unmarshal([]byte(old), &cache); err != nil {
		t.Fatal(err)
	}
	items := cache.Items[string(FilterReviewRequested)]
	if len(items) != 1 || items[0].Number != 8 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ReviewedSHA != "" || items[0].HeadSHA != "" || items[0].Decision != "" {
		t.Errorf("an old entry claimed a review state: %+v", items[0])
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
