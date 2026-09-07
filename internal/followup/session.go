// Package followup loads a revision-consistent review and keeps GitHub thread
// resolution separate from the evidence a reviewer has personally verified.
package followup

import (
	"strings"

	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
)

type Session struct {
	PR              *ghsrc.PR
	Viewer          string
	Baseline        *ghsrc.SubmittedReview
	Threads         []ghsrc.Thread
	Files           []*diffparse.FileDiff
	Comparison      *ghsrc.RevisionComparison
	ComparisonError string
	Warnings        []string

	// comparisonFiles caches the parsed comparison diff. ChangesFor runs once
	// per rendered frame, and reparsing the whole diff each time is visible as
	// input lag on a large pull request.
	comparisonFiles []*diffparse.FileDiff
}

// Load never labels a current PR diff as changes since an earlier review.
// History failures leave the ordinary diff available, with an explicit warning.
func FromSnapshot(snapshot ghsrc.Snapshot) *Session {
	files := diffparse.Parse(snapshot.RawDiff)
	diffparse.FillStats(files)
	return &Session{PR: snapshot.PR, Viewer: snapshot.Viewer, Baseline: snapshot.Baseline,
		Threads: snapshot.Threads.Threads, Files: files, Comparison: snapshot.Comparison,
		ComparisonError: snapshot.ComparisonError, Warnings: snapshot.Warnings}
}

func (s *Session) Comments() []ghsrc.Comment {
	var comments []ghsrc.Comment
	for _, t := range s.Threads {
		comments = append(comments, t.Comments...)
	}
	return comments
}

// OwnThreads includes resolved and outdated conversations and threads whose
// files disappeared. Visibility is independent of the current diff's file list.
func (s *Session) OwnThreads() []ghsrc.Thread {
	var threads []ghsrc.Thread
	if strings.TrimSpace(s.Viewer) == "" {
		return nil
	}
	for _, t := range s.Threads {
		if len(t.Comments) > 0 && strings.EqualFold(t.Comments[0].User.Login, s.Viewer) {
			threads = append(threads, t)
		}
	}
	return threads
}

// Evidence returns the current file path and a stable content/mode fingerprint.
// We conservatively invalidate verification for any change in that file, even
// outside the original comment's lines. An unavailable comparison is not proof.
func (s *Session) Evidence(t ghsrc.Thread) (path, fingerprint string) {
	path = t.Path
	if s.Comparison == nil {
		return path, ""
	}
	if blob, ok := s.Comparison.HeadFiles[path]; ok {
		return path, path + ":" + blob
	}
	for _, f := range s.Comparison.Files {
		if f.PreviousPath == path && f.Path != path {
			path = f.Path
			if blob, ok := s.Comparison.HeadFiles[path]; ok {
				return path, path + ":" + blob
			}
		}
	}
	// A thread can predate the selected review. Its rename may therefore be
	// present only in the full PR diff, not the latest-review comparison.
	for _, f := range s.Files {
		if f.OldPath == path && f.NewPath != path && f.Status == diffparse.Renamed {
			path = f.NewPath
			if blob, ok := s.Comparison.HeadFiles[path]; ok {
				return path, path + ":" + blob
			}
		}
	}
	// An absent historical path may have moved without enough similarity for
	// Git to recognize the rename. Bind this evidence to the whole revision so
	// a later edit to its unknown replacement cannot silently stay verified.
	head := s.Comparison.HeadSHA
	if s.PR != nil {
		head = s.PR.HeadSHA
	}
	return path, "absent:" + path + ":" + head
}

func (s *Session) ChangesFor(t ghsrc.Thread) []*diffparse.FileDiff {
	if s.Comparison == nil {
		return nil
	}
	path, _ := s.Evidence(t)
	var files []*diffparse.FileDiff
	for _, f := range s.comparisonDiff() {
		if f.Path() == path || f.OldPath == t.Path || f.NewPath == t.Path {
			files = append(files, f)
		}
	}
	return files
}

// comparisonDiff parses the comparison once and reuses it thereafter.
func (s *Session) comparisonDiff() []*diffparse.FileDiff {
	if s.comparisonFiles == nil && s.Comparison != nil {
		s.comparisonFiles = diffparse.Parse(s.Comparison.Diff)
		diffparse.FillStats(s.comparisonFiles)
	}
	return s.comparisonFiles
}
