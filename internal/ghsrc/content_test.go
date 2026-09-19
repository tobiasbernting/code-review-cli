package ghsrc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const headBlob = "3333333333333333333333333333333333333333"

// fileTextClient answers the lookup of a path at a commit with object, and
// serves headBlob's content as body. calls records what was asked.
func fileTextClient(t *testing.T, object string, body string, calls *[]string) Client {
	t.Helper()
	return Client{runOverride: func(stdin []byte, args ...string) (string, error) {
		*calls = append(*calls, strings.Join(args, " "))
		switch {
		case len(args) > 1 && args[1] == "graphql":
			var req struct{ Variables map[string]any }
			if err := json.Unmarshal(stdin, &req); err != nil {
				t.Fatal(err)
			}
			if got := req.Variables["expression"]; got != revisionHead+":src/a.go" {
				t.Errorf("looked up %v, want the path at the head commit", got)
			}
			if req.Variables["owner"] != "acme" || req.Variables["name"] != "repo" {
				t.Errorf("looked in %v/%v, want acme/repo", req.Variables["owner"], req.Variables["name"])
			}
			return `{"data":{"repository":{"object":` + object + `}}}`, nil
		case len(args) > 1 && args[1] == "repos/acme/repo/git/blobs/"+headBlob:
			return fmt.Sprintf(`{"sha":%q,"encoding":"base64","content":%q,"size":%d}`,
				headBlob, base64.StdEncoding.EncodeToString([]byte(body)), len(body)), nil
		}
		return "", fmt.Errorf("unexpected gh %v", args)
	}}
}

func TestFileTextReadsTheHeadBlobBySHA(t *testing.T) {
	var calls []string
	object := fmt.Sprintf(`{"__typename":"Blob","oid":%q,"byteSize":6,"isBinary":false}`, headBlob)
	client := fileTextClient(t, object, "a\nb\nc\n", &calls)
	text, err := client.FileText("acme/repo", revisionHead, "src/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if text != "a\nb\nc\n" {
		t.Errorf("text = %q", text)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "git/blobs/"+headBlob) {
		t.Errorf("calls = %q, want a lookup and then the blob by its SHA", calls)
	}
}

// What can be told from the lookup is told without downloading the blob.
func TestFileTextRefusesBeforeDownloading(t *testing.T) {
	for _, tc := range []struct {
		name, object string
		want         error
	}{
		{"too large", fmt.Sprintf(`{"__typename":"Blob","oid":%q,"byteSize":%d,"isBinary":false}`, headBlob, 2<<20), ErrTooLarge},
		{"binary", fmt.Sprintf(`{"__typename":"Blob","oid":%q,"byteSize":6,"isBinary":true}`, headBlob), ErrBinary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			_, err := fileTextClient(t, tc.object, "", &calls).FileText("acme/repo", revisionHead, "src/a.go")
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(calls) != 1 {
				t.Errorf("calls = %q, want the lookup only", calls)
			}
		})
	}
}

func TestFileTextNeedsAFileAtTheCommit(t *testing.T) {
	for name, object := range map[string]string{
		"missing":   `null`,
		"submodule": `{"__typename":"Commit"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			if _, err := fileTextClient(t, object, "", &calls).FileText("acme/repo", revisionHead, "src/a.go"); err == nil {
				t.Error("no error for a path that is not a file at the commit")
			}
		})
	}
	var calls []string
	if _, err := fileTextClient(t, `null`, "", &calls).FileText("acme/repo", "main", "src/a.go"); err == nil || len(calls) != 0 {
		t.Errorf("a branch name was followed: err %v, calls %q", err, calls)
	}
}

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
