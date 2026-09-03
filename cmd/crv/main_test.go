package main

import (
	"flag"
	"strings"
	"testing"

	"github.com/tobiasbernting/code-review-cli/internal/config"
)

// The help text is where someone finds out configuration exists at all, so it
// has to name the real files, the precedence, and every key.
func TestUsageExplainsConfiguration(t *testing.T) {
	got := usage()

	userPath, err := config.UserPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"configuration:",
		config.RepoFile,
		userPath,
		"CRV_HOST", "NO_COLOR",
		"host =", "theme =", "editor =", "untracked =", "color =", "width =",
		"crv --config",
		"crv --init-config",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help text does not mention %q", want)
		}
	}
}

// Every flag the help lists must actually exist, or the help is a lie.
func TestHelpFlagsExist(t *testing.T) {
	fs := flag.NewFlagSet("crv", flag.ContinueOnError)
	registerFlags(fs)

	for _, line := range strings.Split(usage(), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "--") {
			continue
		}
		name := strings.TrimPrefix(strings.Fields(trimmed)[0], "--")
		if fs.Lookup(name) == nil {
			t.Errorf("help lists --%s, which is not a registered flag", name)
		}
	}
}

// And the reverse: a flag nobody is told about may as well not exist.
func TestEveryFlagIsDocumented(t *testing.T) {
	help := usage()
	fs := flag.NewFlagSet("crv", flag.ContinueOnError)
	registerFlags(fs)

	fs.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(help, "--"+f.Name) {
			t.Errorf("flag --%s is not mentioned in the help text", f.Name)
		}
	})
}
