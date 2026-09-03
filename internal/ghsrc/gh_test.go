package ghsrc

import (
	"encoding/json"
	"strings"
	"testing"
)

// gh --paginate concatenates one array per page; a decoder that assumes a
// single array silently drops every page after the first.
func TestParseCommentsAcrossPages(t *testing.T) {
	out := `[{"id":1,"path":"a.go","line":10,"position":3,"user":{"login":"ann"},"body":"first"}]
[{"id":2,"path":"b.go","line":4,"position":null,"user":{"login":"bo"},"body":"second"}]`

	got, err := parseComments(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2 — later pages were dropped", len(got))
	}
	if got[0].User.Login != "ann" || got[0].Path != "a.go" {
		t.Errorf("first comment = %+v", got[0])
	}
	if got[0].Outdated() {
		t.Error("comment with a position reported as outdated")
	}
	// A null position is GitHub saying the comment no longer anchors to the
	// diff, which happens after a force-push.
	if !got[1].Outdated() {
		t.Error("comment with a null position not reported as outdated")
	}
}

func TestParseCommentsEmpty(t *testing.T) {
	got, err := parseComments("[]")
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no comments and no error", got, err)
	}
}

func TestReviewPayloadShape(t *testing.T) {
	payload, err := reviewPayload(EventRequestChanges, "needs work", []ReviewComment{
		{Path: "a.go", Line: 12, Body: "nil check", Side: "RIGHT"},
		{Path: "b.go", Line: 30, StartLine: 24, Body: "extract", Side: "RIGHT", StartSide: "RIGHT"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "REQUEST_CHANGES" || got["body"] != "needs work" {
		t.Errorf("payload = %s", payload)
	}
	comments, _ := got["comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("got %d comments in payload, want 2", len(comments))
	}

	// A single-line comment must omit start_line entirely: sending
	// start_line == line is rejected by the API.
	first, _ := comments[0].(map[string]any)
	if _, ok := first["start_line"]; ok {
		t.Errorf("single-line comment carries start_line: %s", payload)
	}
	second, _ := comments[1].(map[string]any)
	if second["start_line"] != float64(24) {
		t.Errorf("multi-line comment lost start_line: %s", payload)
	}
}

func TestSubmitRejectsUnknownEvent(t *testing.T) {
	err := Client{}.SubmitReview("acme/x", 1, "MERGE", "", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown review event") {
		t.Errorf("got %v, want an unknown-event error", err)
	}
}

func TestSubmitRejectsEmptyComment(t *testing.T) {
	if err := (Client{}).SubmitReview("acme/x", 1, EventComment, "", nil); err == nil {
		t.Error("expected an error when there is nothing to submit")
	}
	// Approving with no body and no comments is legitimate.
	if err := (Client{}).SubmitReview("acme/x", 1, EventApprove, "", nil); err != nil {
		if strings.Contains(err.Error(), "nothing to submit") {
			t.Error("a bare approval was rejected as empty")
		}
	}
}
