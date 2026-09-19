package ghsrc

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
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
	if len(data) > 1024*1024 {
		return "", fmt.Errorf("file exceeds the 1 MiB context limit; inspect it in your editor")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("binary file; textual context is unavailable")
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
