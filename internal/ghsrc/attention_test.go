package ghsrc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAttentionNotificationScopeAndPagination(t *testing.T) {
	c := Client{runOverride: func(_ []byte, args ...string) (string, error) {
		if !strings.Contains(strings.Join(args, " "), "all=true&per_page=50") {
			t.Fatal(args)
		}
		return `[{"id":"1","unread":true,"updated_at":"v1","repository":{"full_name":"o/r"},"subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/1"}},{"id":"issue","subject":{"type":"Issue","url":"https://api.github.com/repos/o/r/issues/2"}}]
[{"id":"read","unread":false,"repository":{"full_name":"o/r"},"subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/3"}}]`, nil
	}}
	ns, err := c.AttentionNotifications()
	if err != nil || len(ns) != 2 || ns[1].Unread {
		t.Fatal(ns, err)
	}
}
func TestReadRechecksVersionAndOnlyPatchesThread(t *testing.T) {
	for _, changed := range []bool{false, true} {
		calls := 0
		c := Client{runOverride: func(_ []byte, args ...string) (string, error) {
			calls++
			if calls == 1 {
				version := "v1"
				if changed {
					version = "v2"
				}
				return fmt.Sprintf(`{"id":"1","unread":true,"updated_at":%q,"repository":{"full_name":"o/r"},"subject":{"type":"PullRequest","url":"https://api.github.com/repos/o/r/pulls/1"}}`, version), nil
			}
			if strings.Join(args, " ") != "api --method PATCH notifications/threads/1" {
				t.Fatal(args)
			}
			return "", nil
		}}
		err := c.AttentionRead(attention.Notification{ID: "1", Repo: "o/r", Number: 1, Version: "v1"})
		if changed && (err == nil || calls != 1) {
			t.Fatal("new event was cleared")
		}
		if !changed && (err != nil || calls != 2) {
			t.Fatal(err, calls)
		}
	}
}
func TestEmptyPagesAreNotSuccessfulSnapshot(t *testing.T) {
	c := Client{runOverride: func([]byte, ...string) (string, error) { return "", nil }}
	if _, err := c.AttentionCandidates("o/r"); err == nil {
		t.Fatal("empty response accepted")
	}
}

func evidenceFixture(t *testing.T) (Client, map[string]json.RawMessage) {
	t.Helper()
	b, err := os.ReadFile("testdata/attention/base.json")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]json.RawMessage{}
	if err = json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	for key, raw := range fixtures {
		var b bytes.Buffer
		if err = json.Compact(&b, raw); err != nil {
			t.Fatal(err)
		}
		fixtures[key] = b.Bytes()
	}
	c := Client{runOverride: func(stdin []byte, args ...string) (string, error) {
		command := strings.Join(args, " ")
		key := ""
		switch {
		case strings.Contains(command, "graphql"):
			if len(stdin) > 0 {
				if strings.Contains(string(stdin), "ReviewDismissedEvent") {
					key = "dismissal"
				} else if strings.Contains(string(stdin), "reviewRequests") {
					key = "requests"
				} else {
					key = "merge"
				}
			} else {
				key = "threads"
			}
		case strings.Contains(command, "/issues/1/comments"):
			key = "comments"
		case strings.Contains(command, "/pulls/1/comments"):
			key = "reviewComments"
		case strings.Contains(command, "/pulls/1/reviews"):
			key = "reviews"
		case strings.Contains(command, "/check-runs"):
			key = "checks"
		case strings.Contains(command, "/statuses"):
			key = "statuses"
		case strings.HasSuffix(command, "repos/o/r/pulls/1"):
			key = "pr"
		default:
			t.Fatalf("unexpected GitHub operation: %s", command)
		}
		return string(fixtures[key]), nil
	}}
	return c, fixtures
}
func TestAttentionEvidenceDecisionTable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(map[string]json.RawMessage)
		want   []attention.Kind
		absent []attention.Kind
	}{
		{name: "joined thread reply", want: []attention.Kind{attention.Reply}, absent: []attention.Kind{attention.Direct}},
		{name: "resolved joined thread", edit: func(f map[string]json.RawMessage) {
			f["threads"] = []byte(strings.ReplaceAll(string(f["threads"]), `"isResolved":false`, `"isResolved":true`))
		}, absent: []attention.Kind{attention.Reply}},
		{name: "direct and team independent", edit: func(f map[string]json.RawMessage) {
			f["requests"] = []byte(`{"data":{"repository":{"pullRequest":{"reviewRequests":{"nodes":[{"id":"request-user-1","requestedReviewer":{"__typename":"User","login":"me"}},{"id":"request-team-1","requestedReviewer":{"__typename":"Team","databaseId":1,"slug":"team"}}],"pageInfo":{"hasNextPage":false}}}}}}`)

			f["pr"] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(f["pr"]), `"requested_reviewers":[]`, `"requested_reviewers":[{"login":"me"}]`), `"requested_teams":[]`, `"requested_teams":[{"id":1,"slug":"team"}]`))
		}, want: []attention.Kind{attention.Direct, attention.Team}},
		{name: "approval followed by push", edit: func(f map[string]json.RawMessage) {
			f["reviews"] = []byte(`[{"id":20,"state":"APPROVED","user":{"login":"me"},"submitted_at":"2026-09-01T09:00:00Z"}]`)
		}, absent: []attention.Kind{attention.Direct, attention.Invalidated}},
		{name: "approval dismissed", edit: func(f map[string]json.RawMessage) {
			f["reviews"] = []byte(`[{"id":20,"state":"DISMISSED","user":{"login":"me"},"submitted_at":"2026-09-01T09:00:00Z"}]`)
		}, want: []attention.Kind{attention.Invalidated}},
		{name: "author approved clean PR", edit: func(f map[string]json.RawMessage) {
			f["pr"] = []byte(strings.ReplaceAll(string(f["pr"]), `"login":"author"`, `"login":"me"`))
		}, want: []attention.Kind{attention.Merge, attention.Feedback}},
		{name: "auto merge suppresses work", edit: func(f map[string]json.RawMessage) {
			f["pr"] = []byte(strings.ReplaceAll(string(f["pr"]), `"login":"author"`, `"login":"me"`))
			f["merge"] = []byte(strings.ReplaceAll(string(f["merge"]), `"autoMergeRequest":null`, `"autoMergeRequest":{"enabledAt":"2026-09-01T00:00:00Z"}`))
		}, absent: []attention.Kind{attention.Merge}},
		{name: "required check pending", edit: func(f map[string]json.RawMessage) {
			f["pr"] = []byte(strings.ReplaceAll(string(f["pr"]), `"login":"author"`, `"login":"me"`))
			f["merge"] = []byte(strings.ReplaceAll(string(f["merge"]), `"COMPLETED"`, `"IN_PROGRESS"`))
		}, absent: []attention.Kind{attention.Merge}},
		{name: "latest head failure", edit: func(f map[string]json.RawMessage) {
			f["pr"] = []byte(strings.ReplaceAll(string(f["pr"]), `"login":"author"`, `"login":"me"`))
			f["checks"] = []byte(`{"check_runs":[{"id":1,"name":"test","status":"completed","conclusion":"failure"}]}`)
			f["merge"] = []byte(strings.ReplaceAll(string(f["merge"]), `"CLEAN"`, `"BLOCKED"`))
		}, want: []attention.Kind{attention.CI}, absent: []attention.Kind{attention.Merge}},
		{name: "draft has no CI task", edit: func(f map[string]json.RawMessage) {
			f["pr"] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(f["pr"]), `"login":"author"`, `"login":"me"`), `"draft":false`, `"draft":true`))
			f["checks"] = []byte(`{"check_runs":[{"name":"test","conclusion":"failure"}]}`)
		}, absent: []attention.Kind{attention.CI, attention.Merge}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := evidenceFixture(t)
			if tc.edit != nil {
				tc.edit(f)
			}
			ev, err := c.AttentionEvidence("o/r", 1, "me", map[int64]bool{1: true})
			if err != nil || !ev.Complete {
				t.Fatal(ev, err)
			}
			kinds := map[attention.Kind]bool{}
			for _, r := range ev.Reasons {
				kinds[r.Kind] = true
			}
			for _, kind := range tc.want {
				if !kinds[kind] {
					t.Errorf("missing %s: %+v", kind, ev)
				}
			}
			for _, kind := range tc.absent {
				if kinds[kind] {
					t.Errorf("unexpected %s", kind)
				}
			}
		})
	}
}
func TestNotificationPollHintAcrossPages(t *testing.T) {
	ns, hint, err := parseAttentionNotifications("HTTP/2.0 200 OK\r\nX-Poll-Interval: 120\r\n\r\n[]\nHTTP/2.0 200 OK\r\nX-Poll-Interval: 180\r\n\r\n[]")
	if err != nil || len(ns) != 0 || hint != 3*time.Minute {
		t.Fatal(ns, hint, err)
	}
}
