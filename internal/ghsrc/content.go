package ghsrc

import (
	"fmt"
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
	if strings.ContainsRune(string(data), 0) {
		return "", fmt.Errorf("binary file; textual context is unavailable")
	}
	return string(data), nil
}
