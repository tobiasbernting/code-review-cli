package gitsrc

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewSideRev(t *testing.T) {
	for spec, want := range map[string]string{
		"":           "",
		"main":       "",
		"HEAD~2":     "",
		"main..feat": "feat",
		"main...b/c": "b/c",
		"main..":     "HEAD",
		"main...":    "HEAD",
		"abc123^!":   "abc123",
	} {
		if got := newSideRev(spec); got != want {
			t.Errorf("newSideRev(%q) = %q, want %q", spec, got, want)
		}
	}
}

func testRepo(t *testing.T) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("one\n")
	git("add", "f.txt")
	git("commit", "-q", "-m", "one")
	write("two\n")
	git("commit", "-q", "-am", "two")
	write("three\n")
	return &Repo{Root: dir}
}

// A range reads the file where it ends; anything diffed against the working
// tree reads it from there.
func TestNewSideReadsWhereTheDiffEnds(t *testing.T) {
	r := testRepo(t)
	for spec, want := range map[string]string{
		"":          "three\n",
		"HEAD~1":    "three\n",
		"HEAD~1..":  "two\n",
		"HEAD~1..@": "two\n",
	} {
		got, err := r.NewSide(spec)("f.txt")
		if err != nil {
			t.Errorf("%q: %v", spec, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%q read %q, want %q", spec, got, want)
		}
	}
	if _, err := r.NewSide("HEAD~1..HEAD~1")("f.txt"); err != nil {
		t.Errorf("reading at a commit: %v", err)
	}
	if _, err := r.NewSide("")("../outside.txt"); err == nil {
		t.Error("read a path outside the repository")
	}
}

func TestRangeEnd(t *testing.T) {
	for spec, want := range map[string]string{
		"main...feature": "feature",
		"HEAD~3..HEAD":   "HEAD",
		"main..":         "HEAD",
		"main...":        "HEAD",
		"main":           "", // a single revision is diffed against the working tree
		"HEAD~3":         "",
	} {
		if got := rangeEnd(spec); got != want {
			t.Errorf("rangeEnd(%q) = %q, want %q", spec, got, want)
		}
	}
}
