package followup

import (
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"strings"
	"testing"
)

func TestEvidenceSurvivesUnrelatedChangesAndTracksRenameAndDeletion(t *testing.T) {
	s := &Session{Comparison: &ghsrc.RevisionComparison{
		HeadFiles: map[string]string{"new.go": "100644:content", "other.go": "100644:one"},
		Files:     []ghsrc.RevisionFile{{Path: "new.go", PreviousPath: "old.go", Status: "renamed"}},
	}}
	thread := ghsrc.Thread{Path: "old.go"}
	path, before := s.Evidence(thread)
	if path != "new.go" || before != "new.go:100644:content" {
		t.Fatalf("rename lost: %q %q", path, before)
	}
	s.Comparison.HeadFiles["other.go"] = "100644:two"
	_, after := s.Evidence(thread)
	if before != after {
		t.Fatal("unrelated file invalidated evidence")
	}
	s.Comparison.HeadFiles["new.go"] = "100644:changed"
	_, after = s.Evidence(thread)
	if before == after {
		t.Fatal("changed file kept verification evidence")
	}
	delete(s.Comparison.HeadFiles, "new.go")
	_, after = s.Evidence(thread)
	if !strings.HasPrefix(after, "absent:") {
		t.Fatal("deletion lost")
	}
}

func TestOriginalPathCanBeRenamedBeforeLatestReview(t *testing.T) {
	s := &Session{Comparison: &ghsrc.RevisionComparison{HeadFiles: map[string]string{"new.go": "100644:blob"}}, Files: []*diffparse.FileDiff{{OldPath: "old.go", NewPath: "new.go", Status: diffparse.Renamed}}}
	path, fp := s.Evidence(ghsrc.Thread{Path: "old.go"})
	if path != "new.go" || fp != "new.go:100644:blob" {
		t.Fatalf("older rename lost: %s %s", path, fp)
	}
}

func TestUnmappedHistoricalPathRequiresVerificationOnNextPush(t *testing.T) {
	s := &Session{PR: &ghsrc.PR{HeadSHA: "first"}, Comparison: &ghsrc.RevisionComparison{HeadFiles: map[string]string{"replacement.go": "100644:old"}}}
	thread := ghsrc.Thread{Path: "original.go"}
	_, before := s.Evidence(thread)
	s.PR.HeadSHA = "second"
	s.Comparison.HeadFiles["replacement.go"] = "100644:new"
	_, after := s.Evidence(thread)
	if before == after {
		t.Fatal("unmapped path stayed verified after replacement changed")
	}
}

func TestUnknownViewerDoesNotOwnAnonymousThreads(t *testing.T) {
	s := &Session{Threads: []ghsrc.Thread{{Comments: []ghsrc.Comment{{ID: 1}}}}}
	if len(s.OwnThreads()) != 0 {
		t.Fatal("unknown identity matched anonymous author")
	}
}
