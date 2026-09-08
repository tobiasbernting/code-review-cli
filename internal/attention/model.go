// Package attention owns local work independently of GitHub notification state.
package attention

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Kind string

const (
	Direct      Kind = "review requested"
	Team        Kind = "team review requested"
	Invalidated Kind = "approval invalidated"
	Mention     Kind = "mention"
	Reply       Kind = "thread reply"
	Feedback    Kind = "author feedback"
	CI          Kind = "CI failed"
	Merge       Kind = "ready to merge"
	Triage      Kind = "needs triage"
	Retained    Kind = "retained task"
)

type PR struct {
	RepoID                   int64
	Repo                     string
	Number                   int
	Title, URL, Head, Author string
	Draft, Closed            bool
}

func (p PR) Key() string { return fmt.Sprintf("%s#%d", strings.ToLower(p.Repo), p.Number) }

type Reason struct {
	Fingerprint                   string
	Kind                          Kind
	Source, Generation, Text, URL string
	At                            time.Time
}

func (r Reason) Key() string { return string(r.Kind) + ":" + r.Source }
func (r Reason) Manual() bool {
	return r.Kind == Mention || r.Kind == Reply || r.Kind == Feedback || r.Kind == Triage || r.Kind == Retained
}

type Evidence struct {
	ObservedAt         time.Time
	PR                 PR
	Complete           bool
	Problem            string
	Reasons            []Reason
	PendingCI          []string
	UncertaintyVersion string
}
type Task struct {
	PR         PR
	Reason     Reason
	Active     bool
	Completion string
}
type Notification struct {
	ID, Repo, Version string
	Number            int
	Unread            bool
}
type Snapshot struct {
	Tasks             []Task
	LastSync          time.Time
	Error, AlertError string
	Identity          string
}

// Reconcile only removes evidence-controlled reasons after a complete fetch.
// Acknowledgements belong to a reason generation, never a PR timestamp.
func Reconcile(old []Task, e Evidence, acks map[string]bool) []Task {
	byKey := map[string]Task{}
	for _, t := range old {
		pending := false
		for _, source := range e.PendingCI {
			if t.Reason.Kind == CI && t.Reason.Source == source && t.PR.Head == e.PR.Head {
				pending = true
				t.Reason.Text = strings.TrimSuffix(t.Reason.Text, " (rerunning)") + " (rerunning)"
				break
			}
		}
		if !e.Complete || t.Reason.Manual() || pending {
			t.PR = e.PR
			byKey[t.Reason.Key()] = t
		}
	}
	reasons := e.Reasons
	if !e.Complete {
		reasons = append(reasons, Reason{Kind: Triage, Source: "evidence", Generation: Generation(e.Problem + e.UncertaintyVersion), Text: e.Problem, URL: e.PR.URL})
	} else {
		delete(byKey, string(Triage)+":evidence")
	}
	for _, r := range reasons {
		if r.Fingerprint == "" {
			r.Fingerprint = r.Generation
		}
		for _, prior := range old {
			if prior.Reason.Key() != r.Key() {
				continue
			}
			fingerprint := prior.Reason.Fingerprint
			if fingerprint == "" {
				fingerprint = prior.Reason.Generation
			}
			if fingerprint == r.Fingerprint {
				r.Generation = prior.Reason.Generation
			} else if r.Manual() {
				r.Generation = Generation(prior.Reason.Generation + "/" + r.Fingerprint)
			}
			break
		}
		if r.At.IsZero() {
			r.At = e.ObservedAt
			for _, prior := range old {
				if prior.Reason.Key() == r.Key() && prior.Reason.Generation == r.Generation && !prior.Reason.At.IsZero() {
					r.At = prior.Reason.At
					break
				}
			}
		}

		t := Task{PR: e.PR, Reason: r, Active: !acks[AckKey(e.PR, r)]}
		if !t.Active {
			t.Completion = "local Done"
		}
		byKey[r.Key()] = t
	}
	out := make([]Task, 0, len(byKey))
	for _, t := range byKey {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reason.Key() < out[j].Reason.Key() })
	return out
}
func AckKey(p PR, r Reason) string { return p.Key() + "/" + r.Key() + "/" + r.Generation }
func Generation(s string) string   { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// Mentioned ignores quoted lines and fenced/inline code and matches full logins.
func Mentioned(body, login string) bool {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, ">") {
			continue
		}
		line = stripCodeSpans(line)
		line = strings.ReplaceAll(line, `\@`, "")
		pattern := `(?i)(^|[^a-z0-9_@])@` + regexp.QuoteMeta(login) + `([^a-z0-9_-]|$)`
		if regexp.MustCompile(pattern).MatchString(line) {
			return true
		}
	}
	return false
}

func stripCodeSpans(line string) string {
	var out strings.Builder
	for len(line) > 0 {
		i := strings.IndexByte(line, '`')
		if i < 0 {
			out.WriteString(line)
			break
		}
		out.WriteString(line[:i])
		line = line[i:]
		n := 0
		for n < len(line) && line[n] == '`' {
			n++
		}
		delimiter := line[:n]
		end := strings.Index(line[n:], delimiter)
		if end < 0 {
			out.WriteString(line)
			break
		}
		line = line[n+end+n:]
	}
	return out.String()
}
