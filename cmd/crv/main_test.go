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

// go install sets none of the ldflags, so the version has to come from the
// build info Go embeds. Without it every installed copy reports "dev".
func TestBuildInfoFillsInVersion(t *testing.T) {
	v, c, d := buildInfo()

	if v == "" || c == "" || d == "" {
		t.Fatalf("buildInfo returned empty fields: %q %q %q", v, c, d)
	}
	// Under `go test` the module version is "(devel)", so the fallback should
	// leave the version alone but still find the revision from VCS settings.
	if v != "dev" && strings.HasPrefix(v, "v") {
		t.Errorf("version %q keeps its v prefix; the ldflags form does not", v)
	}
}

// The ldflags GoReleaser injects must win over anything inferred.
func TestBuildInfoPrefersLdflags(t *testing.T) {
	oldV, oldC, oldD := version, commit, date
	t.Cleanup(func() { version, commit, date = oldV, oldC, oldD })

	version, commit, date = "1.2.3", "abcdef1", "2026-01-01T00:00:00Z"
	v, c, d := buildInfo()
	if v != "1.2.3" || c != "abcdef1" || d != "2026-01-01T00:00:00Z" {
		t.Errorf("build info overrode the injected values: %q %q %q", v, c, d)
	}
}

// Only a version that came from a real tag should be reported; Go's
// pseudo-versions are accurate but say less than "dev" does.
func TestReleaseVersionRejectsPseudoVersions(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"v1.1.0", "1.1.0", true},
		{"v2.0.0-rc.1", "2.0.0-rc.1", true},
		{"(devel)", "", false},
		{"", "", false},
		{"1.1.1-0.20260903201147-848cbb466d36+dirty", "", false},
		{"v1.1.1-0.20260903201147-848cbb466d36", "", false},
	}
	for _, tc := range cases {
		got, ok := releaseVersion(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("releaseVersion(%q) = %q,%v; want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
