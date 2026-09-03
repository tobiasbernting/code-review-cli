package tui

import (
	"fmt"

	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// Overlay adapts a note store and fetched comments to what the renderer
// draws, without either side knowing about the other. It is exported so the
// non-interactive path renders notes and comments too: piping a pull request
// review that silently omitted every comment would be a lie.
func Overlay(review *notes.Review, comments []ghsrc.Comment, blobs map[string]string) render.Overlay {
	if review == nil {
		review = &notes.Review{Files: map[string]notes.FileMark{}}
	}
	return render.Overlay{
		At: func(path string, line int) []render.Annotation {
			var out []render.Annotation
			for _, n := range review.Notes {
				if n.Path != path || n.Line != line || notes.Stale(n, blobs[path]) {
					continue
				}
				out = append(out, render.Annotation{
					Kind: render.AnnNote, ID: n.ID, Body: n.Body,
					StartLine: n.StartLine, Line: n.Line,
				})
			}
			for _, c := range comments {
				if c.Path != path || c.Line != line || c.Outdated() {
					continue
				}
				out = append(out, render.Annotation{
					Kind: render.AnnComment, ID: fmt.Sprint(c.ID), Author: c.User.Login,
					Body: c.Body, StartLine: c.StartLine, Line: c.Line,
				})
			}
			return out
		},
		Detached: func(path string) []render.Annotation {
			var out []render.Annotation
			for _, n := range review.Notes {
				if n.Path == path && notes.Stale(n, blobs[path]) {
					out = append(out, render.Annotation{
						Kind: render.AnnNote, ID: n.ID, Body: n.Body,
						StartLine: n.StartLine, Line: n.Line, Stale: true,
					})
				}
			}
			for _, c := range comments {
				if c.Path == path && c.Outdated() {
					out = append(out, render.Annotation{
						Kind: render.AnnComment, ID: fmt.Sprint(c.ID), Author: c.User.Login,
						Body: c.Body, Line: c.Line, Stale: true,
					})
				}
			}
			return out
		},
		FileState: func(path string) (bool, bool) {
			return review.ReviewState(path, blobs[path])
		},
	}
}

// Blobs indexes the new-side blob hash of every file, which is what notes
// anchor to.
func Blobs(files []*diffparse.FileDiff) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path()] = f.NewBlob
	}
	return out
}

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
