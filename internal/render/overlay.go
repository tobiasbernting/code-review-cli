package render

// Annotation is something attached to a line of the diff: a note you wrote, or
// a review comment a teammate already left on the pull request.
type Annotation struct {
	Kind      AnnotationKind
	ID        string // note id, or the GitHub comment id as a string
	Author    string // empty for your own unsent notes
	Body      string
	StartLine int
	Line      int

	// Stale marks a note written against a different version of the file, or
	// a GitHub comment the API can no longer anchor to the diff.
	Stale bool
}

type AnnotationKind int

const (
	// AnnNote is a local, unsent note.
	AnnNote AnnotationKind = iota
	// AnnComment is an existing review comment from GitHub.
	AnnComment
)

// Overlay supplies everything the renderer draws on top of the diff itself.
// Each field may be nil, which is how a plain local diff renders with no
// notes machinery involved at all.
type Overlay struct {
	// At returns the annotations anchored to a line of a file.
	At func(path string, line int) []Annotation

	// Detached returns annotations that no longer anchor anywhere in the
	// current diff. They are drawn under the file header rather than dropped,
	// because silently hiding a comment is worse than showing it out of place.
	Detached func(path string) []Annotation

	// FileState reports whether a file is marked reviewed, and whether it has
	// changed since it was marked.
	FileState func(path string) (reviewed, changed bool)
}

func (o Overlay) at(path string, line int) []Annotation {
	if o.At == nil || line == 0 {
		return nil
	}
	return o.At(path, line)
}

func (o Overlay) detached(path string) []Annotation {
	if o.Detached == nil {
		return nil
	}
	return o.Detached(path)
}

func (o Overlay) fileState(path string) (bool, bool) {
	if o.FileState == nil {
		return false, false
	}
	return o.FileState(path)
}
