// Package ghsrc talks to GitHub by shelling out to the gh CLI.
//
// gh already solves authentication, enterprise hosts and rate limiting, and
// it is the tool the user is already logged into. Reimplementing that against
// the REST API would mean owning three problems for no gain.
package ghsrc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrNotInstalled is returned when gh is missing, so callers can tell the
// difference between "no GitHub" and "GitHub said no".
var ErrNotInstalled = errors.New("gh not found — install it: https://cli.github.com")

type Client struct {
	// Host overrides the GitHub hostname. Empty means gh's own configuration
	// decides, which is what makes a personal account and an enterprise host
	// both work without special-casing either.
	Host string
	Dir  string // repository directory, so gh resolves the right remote
}

// Preflight checks that gh exists and is authenticated. It is called once per
// session: a clear message here beats an exec error surfacing from deep
// inside a diff fetch.
func (c Client) Preflight() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return ErrNotInstalled
	}
	args := []string{"auth", "status"}
	if c.Host != "" {
		args = append(args, "--hostname", c.Host)
	}
	if _, err := c.run(args...); err != nil {
		host := c.Host
		if host == "" {
			host = "github.com"
		}
		return fmt.Errorf("gh is not authenticated for %s — run: gh auth login --hostname %s", host, host)
	}
	return nil
}

// Repo is the "owner/name" of the repository in the working directory.
func (c Client) Repo() (string, error) {
	out, err := c.run("repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", errors.New("could not determine the repository — is there a GitHub remote?")
	}
	return name, nil
}

type PR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	URL     string `json:"url"`
	BaseRef string `json:"baseRefName"`
	HeadRef string `json:"headRefName"`
	HeadSHA string `json:"headRefOid"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	IsDraft bool `json:"isDraft"`
}

func (c Client) PR(number int) (*PR, error) {
	out, err := c.run("pr", "view", strconv.Itoa(number), "--json",
		"number,title,body,state,url,baseRefName,headRefName,headRefOid,author,isDraft")
	if err != nil {
		return nil, err
	}
	var pr PR
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return nil, fmt.Errorf("could not read pull request %d: %w", number, err)
	}
	return &pr, nil
}

// Diff returns the pull request's unified diff.
func (c Client) Diff(number int) (string, error) {
	return c.run("pr", "diff", strconv.Itoa(number))
}

// Comment is one existing review comment written by a teammate.
type Comment struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Body      string `json:"body"`
	Side      string `json:"side"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line"`
	CreatedAt string `json:"created_at"`
	URL       string `json:"html_url"`
	InReplyTo int64  `json:"in_reply_to_id"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`

	// Position is null once the comment's diff hunk no longer exists, which
	// is GitHub's way of saying the comment is outdated after a force-push.
	Position *int `json:"position"`
}

// Outdated reports that GitHub can no longer anchor this comment to the diff.
func (c Comment) Outdated() bool { return c.Position == nil }

// Comments lists the review comments on a pull request.
func (c Client) Comments(repo string, number int) ([]Comment, error) {
	out, err := c.run("api", "--paginate",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number))
	if err != nil {
		return nil, err
	}
	return parseComments(out)
}

// parseComments decodes gh's --paginate output, which concatenates one JSON
// array per page rather than emitting a single array.
func parseComments(out string) ([]Comment, error) {
	dec := json.NewDecoder(strings.NewReader(out))
	var all []Comment
	for {
		var page []Comment
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("could not read review comments: %w", err)
		}
		all = append(all, page...)
	}
	return all, nil
}

// ReviewComment is one comment being submitted.
type ReviewComment struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line,omitempty"`
	Side      string `json:"side,omitempty"`
	StartSide string `json:"start_side,omitempty"`
}

// Review events accepted by the GitHub API.
const (
	EventComment        = "COMMENT"
	EventApprove        = "APPROVE"
	EventRequestChanges = "REQUEST_CHANGES"
)

// reviewPayload builds the request body for the reviews endpoint.
func reviewPayload(event, body string, comments []ReviewComment) ([]byte, error) {
	return json.Marshal(reviewRequest{Body: body, Event: event, Comments: comments})
}

type reviewRequest struct {
	Body     string          `json:"body,omitempty"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// SubmitReview posts every comment as one review. GitHub reviews are atomic,
// which is why notes are held locally until this is called: a half-written
// review never reaches the author, and one review sends one notification
// rather than one per comment.
func (c Client) SubmitReview(repo string, number int, event, body string, comments []ReviewComment) error {
	if event != EventComment && event != EventApprove && event != EventRequestChanges {
		return fmt.Errorf("unknown review event %q", event)
	}
	if event == EventComment && body == "" && len(comments) == 0 {
		return errors.New("nothing to submit")
	}
	payload, err := reviewPayload(event, body, comments)
	if err != nil {
		return err
	}
	_, err = c.runInput(payload, "api", "--method", "POST",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number), "--input", "-")
	return err
}

func (c Client) run(args ...string) (string, error) { return c.runInput(nil, args...) }

func (c Client) runInput(stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = c.Dir
	cmd.Env = os.Environ()
	if c.Host != "" {
		cmd.Env = append(cmd.Env, "GH_HOST="+c.Host)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}
