package tui

import (
	"fmt"
	"sort"

	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/followup"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type OverlayOptions struct {
	Expanded        map[string]bool
	NewComments     map[int64]bool
	UpdatedComments map[int64]bool
	Plain           bool
}

// Overlay adapts a note store and fetched comments to what the renderer
// draws, without either side knowing about the other. It is exported so the
// non-interactive path renders notes and comments too: piping a pull request
// review that silently omitted every comment would be a lie.
func Overlay(review *notes.Review, threads []ghsrc.Thread, files []*diffparse.FileDiff, opts OverlayOptions) render.Overlay {
	if review == nil {
		review = &notes.Review{Files: map[string]notes.FileMark{}}
	}
	blobs := Blobs(files)
	paths := make(map[string]bool, len(files))
	lines := make(map[string]map[int]bool, len(files))
	for _, file := range files {
		path := file.Path()
		paths[path] = true
		lines[path] = map[int]bool{}
		for _, hunk := range file.Hunks() {
			for _, line := range hunk.Lines {
				if line.NewNum > 0 {
					lines[path][line.NewNum] = true
				}
			}
		}
	}

	return render.Overlay{
		At: func(path string, line int) []render.Annotation {
			var out []render.Annotation
			for _, n := range review.Notes {
				if n.Path == path && n.Line == line && !noteNeedsReanchor(n, blobs, paths) {
					out = append(out, noteAnnotation(n, false))
				}
			}
			for _, thread := range threads {
				if thread.Path == path && thread.Line == line && !thread.Outdated && thread.Side != "LEFT" && lines[path][line] {
					out = append(out, threadAnnotations(thread, opts)...)
				}
			}
			return out
		},
		Detached: func(path string) []render.AnnotationGroup {
			var drafts []render.Annotation
			for _, n := range review.Notes {
				if n.Path == path && noteNeedsReanchor(n, blobs, paths) {
					drafts = append(drafts, noteAnnotation(n, true))
				}
			}
			groups := make([]render.AnnotationGroup, 0, 4)
			if len(drafts) > 0 {
				groups = append(groups, render.AnnotationGroup{Title: "Needs re-anchor", Items: drafts})
			}

			buckets := map[string][]render.Annotation{}
			for _, thread := range threads {
				if thread.Path != path || (!thread.Outdated && thread.Side != "LEFT" && thread.Line > 0 && lines[path][thread.Line]) {
					continue
				}
				title := threadSection(thread)
				buckets[title] = append(buckets[title], threadAnnotations(thread, opts)...)
			}
			for _, title := range []string{"Review threads", "Outdated, unresolved", "Outdated comments", "Outdated, resolved"} {
				if len(buckets[title]) > 0 {
					groups = append(groups, render.AnnotationGroup{Title: title, Items: buckets[title]})
				}
			}
			return groups
		},
		Orphaned: func() []render.AnnotationGroup {
			groups := map[string][]render.Annotation{}
			for _, n := range review.Notes {
				if !paths[n.Path] {
					title := "Needs re-anchor · " + n.Path
					groups[title] = append(groups[title], noteAnnotation(n, true))
				}
			}
			for _, thread := range threads {
				if !paths[thread.Path] {
					title := threadSection(thread) + " · " + thread.Path
					groups[title] = append(groups[title], threadAnnotations(thread, opts)...)
				}
			}
			titles := make([]string, 0, len(groups))
			for title := range groups {
				titles = append(titles, title)
			}
			sort.Strings(titles)
			out := make([]render.AnnotationGroup, 0, len(titles))
			for _, title := range titles {
				out = append(out, render.AnnotationGroup{Title: title, Items: groups[title]})
			}
			return out
		},
		FileState: func(path string) (bool, bool) {
			return review.ReviewState(path, blobs[path])
		},
	}
}

func noteNeedsReanchor(n notes.Note, blobs map[string]string, paths map[string]bool) bool {
	return !paths[n.Path] || notes.Stale(n, blobs[n.Path])
}

func noteAnnotation(n notes.Note, needsReanchor bool) render.Annotation {
	return render.Annotation{
		Kind: render.AnnNote, ID: n.ID, Body: n.Body,
		StartLine: n.StartLine, Line: n.Line, NeedsReanchor: needsReanchor,
	}
}

func threadSection(thread ghsrc.Thread) string {
	if !thread.Outdated {
		return "Review threads"
	}
	if !thread.ResolutionKnown {
		return "Outdated comments"
	}
	if thread.Resolved {
		return "Outdated, resolved"
	}
	return "Outdated, unresolved"
}

func threadAnnotations(thread ghsrc.Thread, opts OverlayOptions) []render.Annotation {
	if len(thread.Comments) == 0 {
		return nil
	}
	hasNew, hasUpdated := false, false
	for _, comment := range thread.Comments {
		hasNew = hasNew || opts.NewComments[comment.ID]
		hasUpdated = hasUpdated || opts.UpdatedComments[comment.ID]
	}
	expanded := opts.Plain || hasNew || hasUpdated
	// An explicit toggle wins over the activity default, so a thread opened by
	// new activity can still be collapsed by hand.
	if override, ok := opts.Expanded[thread.ID]; ok && !opts.Plain {
		expanded = override
	} else if !expanded {
		expanded = !thread.Resolved && (!thread.Outdated || thread.ResolutionKnown)
		if thread.Outdated && !thread.ResolutionKnown {
			expanded = true
		}
	}

	root := thread.Comments[0]
	rootAuthor := root.User.Login
	if rootAuthor == "" {
		rootAuthor = "unknown"
	}
	out := []render.Annotation{{
		Kind: render.AnnThread, ID: thread.ID, ThreadID: thread.ID,
		Author: rootAuthor, Body: root.Body, StartLine: thread.StartLine,
		Line: thread.Line, Outdated: thread.Outdated, Resolved: thread.Resolved,
		ResolutionKnown: thread.ResolutionKnown, New: hasNew, Updated: hasUpdated,
		Collapsed: !expanded, ReplyCount: len(thread.Comments) - 1,
	}}
	if !expanded {
		return out
	}
	for _, comment := range thread.Comments {
		out = append(out, render.Annotation{
			Kind: render.AnnComment, ID: fmt.Sprint(comment.ID), ThreadID: thread.ID,
			Author: comment.User.Login, Body: comment.Body, StartLine: thread.StartLine,
			Line: thread.Line, Outdated: thread.Outdated, Resolved: thread.Resolved,
			ResolutionKnown: thread.ResolutionKnown, New: opts.NewComments[comment.ID],
			Updated: opts.UpdatedComments[comment.ID],
		})
	}
	return out
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
	AttentionURL string
	HeadSHA      string
	FollowUp     *followup.Session
	Kind         SourceKind
	Title        string
	Repo         string // "owner/name", pull requests only
	PRNumber     int
	Client       ghsrc.Client

	// Author is the pull request's author and Viewer is you. GitHub rejects
	// approving or requesting changes on your own pull request with a bare
	// 422, so it is worth catching before the request is made.
	Author string
	Viewer string
}

// OwnPR reports whether you are the author of the pull request under review.
func (s Source) OwnPR() bool {
	return s.Author != "" && s.Viewer != "" && s.Author == s.Viewer
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
