package ghsrc

import (
	"strings"
	"testing"
)

func TestFileAtReadsThePinnedRevision(t *testing.T) {
	var asked string
	client := Client{runOverride: func(_ []byte, args ...string) (string, error) {
		asked = strings.Join(args, " ")
		return "package a\n", nil
	}}
	sha := strings.Repeat("a", 40)
	data, err := client.FileAt("acme/x", sha, "dir/my file.go")
	if err != nil || string(data) != "package a\n" {
		t.Fatalf("got %q, %v", data, err)
	}
	if !strings.Contains(asked, "repos/acme/x/contents/dir/my%20file.go?ref="+sha) || !strings.Contains(asked, "application/vnd.github.raw") {
		t.Errorf("asked gh %q", asked)
	}
	if _, err := client.FileAt("acme/x", "main", "a.go"); err == nil {
		t.Error("a branch name was followed; only a pinned commit may be")
	}
}
