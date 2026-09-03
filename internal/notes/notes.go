// Package notes stores review notes and per-file review marks on disk.
//
// Notes are shaped like GitHub review comments from the start — path, side,
// line, optional start_line — so submitting them is a serialisation step
// rather than a translation.
package notes

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/config"
)

// Side mirrors GitHub's diff sides. Only RIGHT is written today; LEFT exists
// so that adding "you should not have deleted this" later is not a migration.
const (
	SideRight = "RIGHT"
	SideLeft  = "LEFT"
)

type Note struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Side string `json:"side"`

	// Line is the last line of the comment, matching GitHub's semantics.
	// StartLine is set only for a multi-line note.
	Line      int `json:"line"`
	StartLine int `json:"start_line,omitempty"`

	Body string `json:"body"`

	// Blob is the file's hash when the note was written. A mismatch means the
	// file moved underneath the note and its line number can no longer be
	// trusted.
	Blob    string    `json:"blob"`
	Created time.Time `json:"created"`
}

// Range reports the note's line span for display.
func (n Note) Range() (start, end int) {
	if n.StartLine > 0 {
		return n.StartLine, n.Line
	}
	return n.Line, n.Line
}

// FileMark records that a file was marked reviewed, and the version it was
// reviewed at.
type FileMark struct {
	Reviewed bool   `json:"reviewed"`
	Blob     string `json:"blob"`
}

// Review is every note and mark for one review scope.
type Review struct {
	Scope   string              `json:"scope"`
	Notes   []Note              `json:"notes"`
	Files   map[string]FileMark `json:"files"`
	Updated time.Time           `json:"updated"`

	path string
}

// Scope identifies a review across sessions. A pull request is identified by
// number; local work is identified by branch rather than commit, so that an
// agent rewriting files under you does not orphan your notes.
func PRScope(repo string, number int) string { return fmt.Sprintf("%s#%d", repo, number) }

func LocalScope(repoRoot, branch string) string {
	return fmt.Sprintf("%s@%s", filepath.ToSlash(repoRoot), branch)
}

// Dir is where reviews are stored: outside the repository, so notes never
// pollute a worktree that is shared or reset.
func Dir() (string, error) {
	base, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "reviews"), nil
}

func fileFor(scope string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(scope))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".json"), nil
}

// Load reads the review for a scope, returning an empty one if none exists.
func Load(scope string) (*Review, error) {
	path, err := fileFor(scope)
	if err != nil {
		return nil, err
	}
	return LoadAt(path, scope)
}

// LoadAt reads a review from an explicit path. It exists so tests and callers
// with their own storage layout do not have to go through UserConfigDir.
func LoadAt(path, scope string) (*Review, error) {
	r := &Review{Scope: scope, Files: map[string]FileMark{}, path: path}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %w", path, err)
	}
	r.path = path
	r.Scope = scope
	if r.Files == nil {
		r.Files = map[string]FileMark{}
	}
	return r, nil
}

// Save writes the review, or removes the file when nothing is left to store.
func (r *Review) Save() error {
	if len(r.Notes) == 0 && len(r.Files) == 0 {
		err := os.Remove(r.path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	r.Updated = time.Now().UTC()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	// Written via a temporary file so an interrupted save cannot truncate an
	// existing review.
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Add stores a note and returns it. start is 0 for a single-line note.
func (r *Review) Add(path string, start, end int, blob, body string) Note {
	n := Note{
		ID:      newID(),
		Path:    path,
		Side:    SideRight,
		Line:    end,
		Body:    body,
		Blob:    blob,
		Created: time.Now().UTC(),
	}
	if start > 0 && start != end {
		n.StartLine = start
	}
	r.Notes = append(r.Notes, n)
	r.sortNotes()
	return n
}

func (r *Review) Update(id, body string) bool {
	for i := range r.Notes {
		if r.Notes[i].ID == id {
			r.Notes[i].Body = body
			return true
		}
	}
	return false
}

func (r *Review) Delete(id string) bool {
	for i := range r.Notes {
		if r.Notes[i].ID == id {
			r.Notes = append(r.Notes[:i], r.Notes[i+1:]...)
			return true
		}
	}
	return false
}

// At returns the notes anchored to a line, in creation order.
func (r *Review) At(path string, line int) []Note {
	var out []Note
	for _, n := range r.Notes {
		if n.Path == path && n.Line == line {
			out = append(out, n)
		}
	}
	return out
}

// Stale reports whether a note was written against a different version of the
// file than the one now being displayed.
func Stale(n Note, currentBlob string) bool {
	return n.Blob != "" && currentBlob != "" && n.Blob != currentBlob
}

// SetReviewed marks or unmarks a file at the given blob.
func (r *Review) SetReviewed(path, blob string, reviewed bool) {
	if !reviewed {
		delete(r.Files, path)
		return
	}
	r.Files[path] = FileMark{Reviewed: true, Blob: blob}
}

// ReviewState reports whether a file is marked reviewed, and whether it has
// changed since. A changed file stays marked rather than silently unchecking,
// so the display can say "you reviewed this, but it moved".
func (r *Review) ReviewState(path, blob string) (reviewed, changed bool) {
	m, ok := r.Files[path]
	if !ok || !m.Reviewed {
		return false, false
	}
	return true, m.Blob != "" && blob != "" && m.Blob != blob
}

func (r *Review) sortNotes() {
	sort.SliceStable(r.Notes, func(i, j int) bool {
		if r.Notes[i].Path != r.Notes[j].Path {
			return r.Notes[i].Path < r.Notes[j].Path
		}
		return r.Notes[i].Line < r.Notes[j].Line
	})
}

// Markdown renders the notes for pasting into a pull request or a chat.
func (r *Review) Markdown() string {
	if len(r.Notes) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Review notes — %s\n\n", r.Scope)

	current := ""
	for _, n := range r.Notes {
		if n.Path != current {
			current = n.Path
			fmt.Fprintf(&b, "### %s\n\n", current)
		}
		start, end := n.Range()
		loc := fmt.Sprintf("L%d", end)
		if start != end {
			loc = fmt.Sprintf("L%d-L%d", start, end)
		}
		fmt.Fprintf(&b, "- **%s** — %s\n", loc, strings.ReplaceAll(strings.TrimSpace(n.Body), "\n", "\n  "))
	}
	return b.String()
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only reachable if the system entropy source fails; a timestamp is
		// still unique enough to address a note within one review.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
