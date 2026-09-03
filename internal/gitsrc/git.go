// Package gitsrc produces diffs by shelling out to git. Shelling out (rather
// than a pure-Go git implementation) keeps behaviour identical to what the
// user sees in their own terminal, including their diff config.
package gitsrc

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
)

// diffArgs are shared by every invocation: rename detection on, colour and
// external diff drivers off, so the parser sees plain unified output.
var diffArgs = []string{"-c", "core.quotepath=false", "diff", "--no-color", "--no-ext-diff", "-M", "--find-copies"}

type Repo struct {
	Root string
}

// Open locates the repository containing dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %s", dir)
	}
	return &Repo{Root: strings.TrimSpace(out)}, nil
}

// Branch is the current branch name, or the short commit when detached.
// Reviews of local work are keyed by branch, so notes survive an agent
// rewriting files and committing underneath them.
func (r *Repo) Branch() (string, error) {
	out, err := run(r.Root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name != "HEAD" {
		return name, nil
	}
	out, err = run(r.Root, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(out), err
}

// WorkingTree diffs HEAD against the working tree (staged and unstaged
// together), plus every untracked file rendered as an addition.
//
// Untracked files are included deliberately: when reviewing generated code,
// the new files an agent created are usually the most important part of the
// change, and a plain `git diff` would hide them entirely.
func (r *Repo) WorkingTree(includeUntracked bool) ([]*diffparse.FileDiff, error) {
	base := "HEAD"
	if _, err := run(r.Root, "rev-parse", "--verify", "HEAD"); err != nil {
		base = emptyTree // repo with no commits yet
	}
	raw, err := r.diff(base)
	if err != nil {
		return nil, err
	}
	files := diffparse.Parse(raw)
	if err := r.attachStats(files, base); err != nil {
		return nil, err
	}
	if includeUntracked {
		extra, err := r.untracked()
		if err != nil {
			return nil, err
		}
		files = append(files, extra...)
	}
	return files, nil
}

// Range diffs a revision range. "a...b" (merge-base) and "a..b" both work, as
// does a single revision.
func (r *Repo) Range(spec string) ([]*diffparse.FileDiff, error) {
	raw, err := r.diff(spec)
	if err != nil {
		return nil, err
	}
	files := diffparse.Parse(raw)
	if err := r.attachStats(files, spec); err != nil {
		return nil, err
	}
	return files, nil
}

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func (r *Repo) diff(rev string) (string, error) {
	args := append(append([]string{}, diffArgs...), rev)
	return run(r.Root, args...)
}

// attachStats fills Additions/Deletions from --numstat so the file list can
// show them without parsing a single hunk body.
//
// -z is used because it reports a rename as two separate NUL-terminated
// fields; the default format collapses them into "old => new", which cannot
// be parsed unambiguously when a path contains " => ".
func (r *Repo) attachStats(files []*diffparse.FileDiff, rev string) error {
	args := append(append([]string{}, diffArgs...), "--numstat", "-z", rev)
	out, err := run(r.Root, args...)
	if err != nil {
		return err
	}
	byPath := make(map[string]*diffparse.FileDiff, len(files))
	for _, f := range files {
		byPath[f.Path()] = f
	}

	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		head := fields[i]
		if head == "" {
			continue
		}
		parts := strings.SplitN(head, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" {
			// rename or copy: the old and new paths follow as their own fields
			if i+2 >= len(fields) {
				break
			}
			path = fields[i+2]
			i += 2
		}
		f, ok := byPath[path]
		if !ok {
			continue
		}
		// binary files report "-" for both counts, which parses to 0
		f.Additions, _ = strconv.Atoi(parts[0])
		f.Deletions, _ = strconv.Atoi(parts[1])
	}
	return nil
}

func (r *Repo) untracked() ([]*diffparse.FileDiff, error) {
	out, err := run(r.Root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []*diffparse.FileDiff
	for _, name := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		if name == "" {
			continue
		}
		// --no-index exits 1 when files differ, which is the normal case here.
		raw, _ := runAllowExit(r.Root, "-c", "core.quotepath=false", "diff", "--no-color",
			"--no-ext-diff", "--no-index", "--", devNull, filepath.Join(r.Root, name))
		parsed := diffparse.Parse(raw)
		for _, f := range parsed {
			f.NewPath = name
			f.OldPath = name
			for _, h := range f.Hunks() {
				f.Additions += len(h.Lines)
			}
			files = append(files, f)
		}
	}
	return files, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func runAllowExit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if _, ok := err.(*exec.ExitError); ok {
		err = nil
	}
	return stdout.String(), err
}
