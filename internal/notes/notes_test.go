package notes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newReview builds a review backed by a temp file, bypassing UserConfigDir.
func newReview(t *testing.T, scope string) *Review {
	t.Helper()
	return &Review{
		Scope: scope,
		Files: map[string]FileMark{},
		path:  filepath.Join(t.TempDir(), "review.json"),
	}
}

func TestAddSingleAndMultiLine(t *testing.T) {
	r := newReview(t, "acme/x#1")

	single := r.Add("a.go", 0, 12, "blob1", "nil check missing")
	if single.StartLine != 0 {
		t.Errorf("single-line note carries StartLine %d, want 0", single.StartLine)
	}
	if single.Side != SideRight {
		t.Errorf("side = %q, want RIGHT", single.Side)
	}
	if s, e := single.Range(); s != 12 || e != 12 {
		t.Errorf("range = %d-%d, want 12-12", s, e)
	}

	multi := r.Add("a.go", 20, 24, "blob1", "extract this")
	if multi.StartLine != 20 || multi.Line != 24 {
		t.Errorf("multi-line note = %d..%d, want 20..24", multi.StartLine, multi.Line)
	}

	// A range whose ends coincide is a single-line note, not a degenerate range.
	same := r.Add("a.go", 30, 30, "blob1", "x")
	if same.StartLine != 0 {
		t.Errorf("collapsed range kept StartLine %d, want 0", same.StartLine)
	}
}

func TestRoundTrip(t *testing.T) {
	r := newReview(t, "acme/x#1")
	r.Add("a.go", 0, 3, "blob1", "first")
	r.Add("b.go", 1, 4, "blob2", "second")
	r.SetReviewed("a.go", "blob1", true)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := &Review{path: r.path}
	if err := reload(loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(loaded.Notes))
	}
	if loaded.Notes[1].StartLine != 1 || loaded.Notes[1].Line != 4 {
		t.Errorf("range lost in round trip: %+v", loaded.Notes[1])
	}
	if m := loaded.Files["a.go"]; !m.Reviewed || m.Blob != "blob1" {
		t.Errorf("file mark lost: %+v", m)
	}
}

// Saving an empty review removes the file rather than leaving an empty husk.
func TestSaveRemovesEmptyReview(t *testing.T) {
	r := newReview(t, "acme/x#1")
	n := r.Add("a.go", 0, 1, "blob", "note")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r.Delete(n.ID)
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := reload(&Review{path: r.path}); err == nil {
		t.Error("expected the review file to be gone")
	}
}

func TestDeleteAndUpdate(t *testing.T) {
	r := newReview(t, "s")
	a := r.Add("a.go", 0, 1, "blob", "one")
	r.Add("a.go", 0, 2, "blob", "two")

	if !r.Update(a.ID, "edited") {
		t.Fatal("update reported no such note")
	}
	if got := r.At("a.go", 1); len(got) != 1 || got[0].Body != "edited" {
		t.Errorf("note not updated: %+v", got)
	}
	if !r.Delete(a.ID) || len(r.Notes) != 1 {
		t.Errorf("delete left %d notes, want 1", len(r.Notes))
	}
	if r.Delete("nope") {
		t.Error("deleting an unknown id reported success")
	}
}

func TestStaleTracksBlob(t *testing.T) {
	n := Note{Blob: "aaa"}
	if Stale(n, "aaa") {
		t.Error("same blob reported stale")
	}
	if !Stale(n, "bbb") {
		t.Error("changed blob not reported stale")
	}
	// Unknown blobs must not produce a false stale marking.
	if Stale(n, "") || Stale(Note{}, "bbb") {
		t.Error("missing blob information reported as stale")
	}
}

// A file that changed after being reviewed stays marked, but is reported as
// changed — silently unchecking would hide that you already read it.
func TestReviewStateReportsChangeWithoutUnmarking(t *testing.T) {
	r := newReview(t, "s")
	r.SetReviewed("a.go", "blob1", true)

	reviewed, changed := r.ReviewState("a.go", "blob1")
	if !reviewed || changed {
		t.Errorf("unchanged file: reviewed=%v changed=%v, want true/false", reviewed, changed)
	}
	reviewed, changed = r.ReviewState("a.go", "blob2")
	if !reviewed || !changed {
		t.Errorf("changed file: reviewed=%v changed=%v, want true/true", reviewed, changed)
	}
	if reviewed, _ := r.ReviewState("other.go", "x"); reviewed {
		t.Error("unmarked file reported as reviewed")
	}
}

func TestMarkdownGroupsByFile(t *testing.T) {
	r := newReview(t, "acme/x#7")
	r.Add("a.go", 0, 3, "b", "first")
	r.Add("a.go", 10, 12, "b", "spans lines")
	r.Add("b.go", 0, 1, "b", "other file")

	md := r.Markdown()
	for _, want := range []string{"acme/x#7", "### a.go", "### b.go", "**L3**", "**L10-L12**"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Count(md, "### a.go") != 1 {
		t.Errorf("a.go heading repeated:\n%s", md)
	}
	if newReview(t, "s").Markdown() != "" {
		t.Error("empty review produced markdown")
	}
}

func TestScopesAreDistinct(t *testing.T) {
	if PRScope("acme/x", 1) == PRScope("acme/x", 2) {
		t.Error("different pull requests share a scope")
	}
	if LocalScope("/repo", "main") == LocalScope("/repo", "feature") {
		t.Error("different branches share a scope")
	}
	if LocalScope("/repo", "main") == LocalScope("/other", "main") {
		t.Error("different repositories share a scope")
	}
}

// reload reads a saved review from its own path.
func reload(r *Review) error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, r)
}
