package tui

import (
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
)

// SourceKind distinguishes a review of local work from a review of a pull
// request. Only the latter can be submitted to GitHub.
type SourceKind int

const (
	SourceLocal SourceKind = iota
	SourcePR
)

// Source describes what is being reviewed.
type Source struct {
	Kind     SourceKind
	Title    string
	Repo     string // "owner/name", pull requests only
	PRNumber int
	Client   ghsrc.Client
}

// CanSubmit reports whether this review can be sent to GitHub.
func (s Source) CanSubmit() bool { return s.Kind == SourcePR && s.Repo != "" }

// Scope is the key the review is stored under.
func (s Source) Scope(repoRoot, branch string) string {
	if s.Kind == SourcePR {
		return notes.PRScope(s.Repo, s.PRNumber)
	}
	return notes.LocalScope(repoRoot, branch)
}
