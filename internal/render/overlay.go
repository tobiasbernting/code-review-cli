package render

// Annotation is something attached to a line of the diff: a local draft, a
// remote review comment, or its thread summary.
type Annotation struct {
	Kind      AnnotationKind
	ID        string // note id, or the GitHub comment id as a string
	ThreadID  string
	Author    string // empty for your own unsent notes
	Body      string
	StartLine int
	Line      int

	NeedsReanchor   bool
	Outdated        bool
	Resolved        bool
	ResolutionKnown bool
	New             bool
	Updated         bool
	Collapsed       bool
	ReplyCount      int
}

type AnnotationKind int

const (
	// AnnNote is a local, unsent note.
	AnnNote AnnotationKind = iota
	// AnnComment is an existing review comment from GitHub.
	AnnComment
	// AnnThread is the selectable summary row for a GitHub discussion.
	AnnThread
)

// AnnotationGroup labels annotations that belong to a file but no longer to
// a current diff line, or whose file is absent from the current diff.
type AnnotationGroup struct {
	Title string
	Items []Annotation
}

// Overlay supplies everything the renderer draws on top of the diff itself.
// Each field may be nil, which is how a plain local diff renders with no
// notes machinery involved at all.
type Overlay struct {
	// At returns the annotations anchored to a line of a file.
	At func(path string, line int) []Annotation

	// Detached returns annotations that no longer anchor anywhere in the
	// current diff. They are drawn under the file header rather than dropped,
	// because silently hiding a comment is worse than showing it out of place.
	Detached func(path string) []AnnotationGroup

	// Orphaned returns groups whose original path is not in the current diff.
	Orphaned func() []AnnotationGroup

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

func (o Overlay) detached(path string) []AnnotationGroup {
	if o.Detached == nil {
		return nil
	}
	return o.Detached(path)
}

func (o Overlay) orphaned() []AnnotationGroup {
	if o.Orphaned == nil {
		return nil
	}
	return o.Orphaned()
}

func (o Overlay) fileState(path string) (bool, bool) {
	if o.FileState == nil {
		return false, false
	}
	return o.FileState(path)
}
