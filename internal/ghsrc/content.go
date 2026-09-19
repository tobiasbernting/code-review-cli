package ghsrc

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// maxText is the most file content krv shows as context.
const maxText = 1024 * 1024

// The reasons file content is not shown as text. Callers that phrase their
// own message test for them with errors.Is.
var (
	ErrTooLarge = errors.New("file exceeds the 1 MiB context limit")
	ErrBinary   = errors.New("binary file")
)

// BlobText reads pinned file content for a thread whose lines no longer occur
// in a diff. It never follows a moving branch name or executes repository code.
func (c Client) BlobText(repo, sha string) (string, error) {
	if !revisionObjectID(sha) {
		return "", fmt.Errorf("missing or invalid file revision")
	}
	data, err := c.revisionBlob(repo, sha)
	if err != nil {
		return "", err
	}
	return Text(data)
}

// Text is file content as text, or why it can't be shown as text: over the
// 1 MiB cap, or binary.
func Text(data []byte) (string, error) {
	if len(data) > maxText {
		return "", fmt.Errorf("%w; inspect it in your editor", ErrTooLarge)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%w; textual context is unavailable", ErrBinary)
	}
	return string(data), nil
}

// FileAt reads a file as it is at a commit, for opening in an editor. Like
// BlobText it only follows a pinned commit, never a branch name.
func (c Client) FileAt(repo, sha, path string) ([]byte, error) {
	if !revisionObjectID(sha) {
		return nil, fmt.Errorf("missing or invalid file revision")
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	out, err := c.run("api", "-H", "Accept: application/vnd.github.raw",
		fmt.Sprintf("repos/%s/contents/%s?ref=%s", repo, strings.Join(segs, "/"), sha))
	return []byte(out), err
}

// FileText reads a file as it is at commit, to show the unchanged lines a
// diff leaves out. The path is looked up to its blob, and the blob is read by
// SHA through BlobText: a diff names blobs by abbreviated hashes, which the
// blob endpoint does not take. Both are addressed by immutable IDs, so what
// is read can be kept for the session.
func (c Client) FileText(repo, commit, path string) (string, error) {
	if !revisionObjectID(commit) {
		return "", fmt.Errorf("missing or invalid commit")
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return "", fmt.Errorf("invalid repository %q", repo)
	}
	query := `query($owner: String!, $name: String!, $expression: String!) {
  repository(owner: $owner, name: $name) {
    object(expression: $expression) { __typename ... on Blob { oid byteSize isBinary } }
  }
}`
	var data struct {
		Repository *struct {
			Object *struct {
				Typename string `json:"__typename"`
				OID      string `json:"oid"`
				ByteSize int    `json:"byteSize"`
				IsBinary bool   `json:"isBinary"`
			}
		}
	}
	vars := map[string]any{"owner": owner, "name": name, "expression": commit + ":" + path}
	if err := c.graphql(query, vars, &data); err != nil {
		return "", err
	}
	if data.Repository == nil || data.Repository.Object == nil {
		return "", fmt.Errorf("%s is not in %s", path, commit[:7])
	}
	obj := data.Repository.Object
	switch {
	case obj.Typename != "Blob":
		return "", fmt.Errorf("%s is not a file", path)
	case obj.ByteSize > maxText:
		return "", fmt.Errorf("%w; inspect it in your editor", ErrTooLarge)
	case obj.IsBinary:
		return "", fmt.Errorf("%w; textual context is unavailable", ErrBinary)
	}
	return c.BlobText(repo, obj.OID)
}
