package ghsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/attention"
)

type AttentionIdentity struct {
	ID    int64
	Login string
}

func (c Client) AttentionIdentity() (AttentionIdentity, error) {
	var v AttentionIdentity
	err := c.attentionGet("user", &v)
	if err == nil && (v.ID == 0 || v.Login == "") {
		err = errors.New("GitHub returned no account identity")
	}
	return v, err
}
func (c Client) attentionGet(path string, v any) error {
	out, err := c.run("api", "--method", "GET", path)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), v)
}
func attentionPages[T any](c Client, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	out, err := c.run("api", "--paginate", path+sep+"per_page=100")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var all []T
	pages := 0
	for {
		var page []T
		err = dec.Decode(&page)
		if errors.Is(err, io.EOF) {
			if pages == 0 {
				return nil, errors.New("empty paginated GitHub response")
			}
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		if page == nil {
			return nil, errors.New("missing paginated GitHub collection")
		}
		pages++
		all = append(all, page...)
	}
}

type attentionNotification struct {
	ID         string
	Unread     bool
	UpdatedAt  string `json:"updated_at"`
	Repository struct {
		FullName string `json:"full_name"`
	}
	Subject struct{ Type, URL string }
}

func notification(n attentionNotification) (attention.Notification, bool) {
	u, err := url.Parse(n.Subject.URL)
	if err != nil || n.Subject.Type != "PullRequest" {
		return attention.Notification{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 5 || parts[len(parts)-2] != "pulls" {
		return attention.Notification{}, false
	}
	number, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || number <= 0 {
		return attention.Notification{}, false
	}
	return attention.Notification{ID: n.ID, Repo: n.Repository.FullName, Number: number, Version: n.UpdatedAt, Unread: n.Unread}, true
}
func (c Client) AttentionNotifications() ([]attention.Notification, error) {
	out, err := c.run("api", "--include", "--paginate", "notifications?all=true&per_page=50")
	if err != nil {
		return nil, err
	}
	all, hint, err := parseAttentionNotifications(out)
	if c.AttentionPollHint != nil && hint > 0 {
		c.AttentionPollHint(hint)
	}
	return all, err
}
func parseAttentionNotifications(out string) ([]attention.Notification, time.Duration, error) {
	var all []attention.Notification
	var hint time.Duration
	pages := 0
	for strings.TrimSpace(out) != "" {
		out = strings.TrimSpace(out)
		if strings.HasPrefix(out, "HTTP/") {
			end := strings.Index(out, "\r\n\r\n")
			skip := 4
			if end < 0 {
				end = strings.Index(out, "\n\n")
				skip = 2
			}
			if end < 0 {
				return nil, hint, errors.New("incomplete notification headers")
			}
			for _, line := range strings.Split(out[:end], "\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && strings.EqualFold(strings.TrimSpace(key), "X-Poll-Interval") {
					seconds, err := strconv.Atoi(strings.TrimSpace(value))
					if err != nil || seconds < 0 {
						return nil, hint, errors.New("invalid GitHub polling interval")
					}
					hint = max(hint, time.Duration(seconds)*time.Second)
				}
			}
			out = out[end+skip:]
		}
		dec := json.NewDecoder(strings.NewReader(out))
		var page []attentionNotification
		if err := dec.Decode(&page); err != nil {
			return nil, hint, err
		}
		if page == nil {
			return nil, hint, errors.New("missing notification collection")
		}
		pages++
		out = out[dec.InputOffset():]
		for _, n := range page {
			if v, ok := notification(n); ok {
				all = append(all, v)
			}
		}
	}
	if pages == 0 {
		return nil, hint, errors.New("empty notification response")
	}
	return all, hint, nil
}
func (c Client) AttentionRead(n attention.Notification) error {
	var current attentionNotification
	if err := c.attentionGet("notifications/threads/"+url.PathEscape(n.ID), &current); err != nil {
		return err
	}
	v, ok := notification(current)
	if !ok || !strings.EqualFold(v.Repo, n.Repo) || v.Number != n.Number || v.Version != n.Version {
		return errors.New("notification changed; awaiting reconciliation")
	}
	if !current.Unread {
		return nil
	}
	_, err := c.run("api", "--method", "PATCH", "notifications/threads/"+url.PathEscape(n.ID))
	return err
}

type attentionPull struct {
	Base               struct{ Repo struct{ ID int64 } }
	Number             int
	Title, Body, State string
	HTMLURL            string `json:"html_url"`
	Draft              bool
	User               struct{ Login string }
	Head               struct{ SHA string }
	RequestedReviewers []struct{ Login string } `json:"requested_reviewers"`
	RequestedTeams     []struct {
		ID   int64
		Slug string
	} `json:"requested_teams"`
}

func (p attentionPull) normalized(repo string) attention.PR {
	return attention.PR{Repo: repo, RepoID: p.Base.Repo.ID, Number: p.Number, Title: p.Title, URL: p.HTMLURL, Head: p.Head.SHA, Author: p.User.Login, Draft: p.Draft, Closed: p.State != "open"}
}

// Enumerating repository PRs avoids search's 1,000-result cap and discovers
// activity even when its notification has already been read or expired.
func (c Client) AttentionCandidates(repo string) ([]attention.PR, error) {
	pulls, err := attentionPages[attentionPull](c, "repos/"+repo+"/pulls?state=open")
	if err != nil {
		return nil, err
	}
	out := make([]attention.PR, 0, len(pulls))
	for _, p := range pulls {
		out = append(out, p.normalized(repo))
	}
	return out, nil
}
func (c Client) AttentionTeams() (map[int64]bool, error) {
	teams, err := attentionPages[struct{ ID int64 }](c, "user/teams")
	out := map[int64]bool{}
	for _, t := range teams {
		out[t.ID] = true
	}
	return out, err
}

type attentionComment struct {
	ID          int64
	Body        string
	HTMLURL     string    `json:"html_url"`
	CreatedAt   time.Time `json:"created_at"`
	User        struct{ Login string }
	State       string
	SubmittedAt time.Time `json:"submitted_at"`
}

func (c Client) AttentionEvidence(repo string, number int, viewer string, teams map[int64]bool) (attention.Evidence, error) {
	e := attention.Evidence{PR: attention.PR{Repo: repo, Number: number}}
	var p attentionPull
	path := fmt.Sprintf("repos/%s/pulls/%d", repo, number)
	if err := c.attentionGet(path, &p); err != nil {
		return e, err
	}
	if p.Number != number || p.Head.SHA == "" || p.Base.Repo.ID == 0 {
		return e, errors.New("incomplete pull request response")
	}
	e.PR = p.normalized(repo)
	add := func(k attention.Kind, source, generation, text, target string, at time.Time) {
		e.Reasons = append(e.Reasons, attention.Reason{Kind: k, Source: source, Generation: generation, Text: text, URL: target, At: at})
	}
	if !e.PR.Closed {
		requests, err := c.attentionRequests(e.PR, viewer, teams)
		if err != nil {
			return e, err
		}
		e.Reasons = append(e.Reasons, requests...)
	}
	incoming := func(k attention.Kind, id, body, target, actor string, at time.Time) {
		if !strings.EqualFold(actor, viewer) {
			add(k, id, attention.Generation(body), body, target, at)
		}
	}
	if attention.Mentioned(p.Body, viewer) {
		incoming(attention.Mention, "body", p.Body, p.HTMLURL, p.User.Login, time.Time{})
	}
	comments, err := attentionPages[attentionComment](c, fmt.Sprintf("repos/%s/issues/%d/comments", repo, number))
	if err != nil {
		return e, err
	}
	for _, v := range comments {
		if attention.Mentioned(v.Body, viewer) {
			incoming(attention.Mention, fmt.Sprint(v.ID), v.Body, v.HTMLURL, v.User.Login, v.CreatedAt)
		}
	}
	reviews, err := attentionPages[attentionComment](c, path+"/reviews")
	if err != nil {
		return e, err
	}
	for _, v := range reviews {
		if attention.Mentioned(v.Body, viewer) {
			incoming(attention.Mention, "review:"+fmt.Sprint(v.ID), v.Body, v.HTMLURL, v.User.Login, v.SubmittedAt)
		}
		if strings.EqualFold(p.User.Login, viewer) && (v.State == "CHANGES_REQUESTED" || strings.TrimSpace(v.Body) != "") {
			incoming(attention.Feedback, fmt.Sprint(v.ID), v.Body+"\n"+v.State, v.HTMLURL, v.User.Login, v.SubmittedAt)
		}
	}
	// A dismissed review alone does not prove a dismissed approval. Preserve it
	// as triage until timeline evidence identifies the original review state.
	var latest *attentionComment
	for i := range reviews {
		v := &reviews[i]
		if strings.EqualFold(v.User.Login, viewer) && v.State != "PENDING" && (latest == nil || v.SubmittedAt.After(latest.SubmittedAt)) {
			latest = v
		}
	}
	if !e.PR.Closed && latest != nil && latest.State == "DISMISSED" {
		r, err := c.attentionDismissal(repo, number, latest.ID)
		if err != nil {
			return e, err
		}
		if r != nil {
			e.Reasons = append(e.Reasons, *r)
		}
	}
	threads, err := c.Threads(repo, number)
	if err != nil {
		return e, err
	}
	if !threads.ResolutionKnown {
		return e, errors.New("review thread resolution unavailable")
	}
	for _, t := range threads.Threads {
		if !t.ResolutionKnown {
			return e, errors.New("incomplete review thread resolution evidence")
		}
		joinedAt := ""
		for _, v := range t.Comments {
			if strings.EqualFold(v.User.Login, viewer) && (joinedAt == "" || v.CreatedAt < joinedAt) {
				joinedAt = v.CreatedAt
			}
		}
		for _, v := range t.Comments {
			at, _ := time.Parse(time.RFC3339, v.CreatedAt)
			if attention.Mentioned(v.Body, viewer) {
				incoming(attention.Mention, fmt.Sprint(v.ID), v.Body, v.URL, v.User.Login, at)
			}
			if !t.Resolved && joinedAt != "" && v.CreatedAt > joinedAt {
				incoming(attention.Reply, fmt.Sprint(v.ID), v.Body, v.URL, v.User.Login, at)
			}
			if strings.EqualFold(p.User.Login, viewer) {
				incoming(attention.Feedback, "comment:"+fmt.Sprint(v.ID), v.Body, v.URL, v.User.Login, at)
			}
		}
	}
	if !e.PR.Closed && !p.Draft && strings.EqualFold(p.User.Login, viewer) {
		if err = c.attentionChecks(&e, add); err != nil {
			return e, err
		}
		ready, mergeErr := c.attentionMerge(e.PR)
		if mergeErr != nil {
			return e, mergeErr
		}
		for _, reason := range e.Reasons {
			if reason.Kind == attention.CI {
				ready = false
			}
		}
		if ready {
			add(attention.Merge, "merge", e.PR.Head, "Approved and eligible to merge", e.PR.URL, time.Time{})
		}
	}
	var after attentionPull
	if err = c.attentionGet(path, &after); err != nil {
		return e, err
	}
	if after.Head.SHA != p.Head.SHA || after.State != p.State {
		return e, errors.New("pull request changed during evidence fetch")
	}
	e.Complete = true
	return e, nil
}
func (c Client) attentionChecks(e *attention.Evidence, add func(attention.Kind, string, string, string, string, time.Time)) error {
	type check struct {
		App                      struct{ ID int64 }
		ID                       int64
		Name, Status, Conclusion string
		HTMLURL                  string `json:"html_url"`
	}
	out, err := c.run("api", "--paginate", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100&filter=latest", e.PR.Repo, e.PR.Head))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	pages := 0
	for {
		var page struct {
			CheckRuns []check `json:"check_runs"`
		}
		err = dec.Decode(&page)
		if errors.Is(err, io.EOF) {
			if pages == 0 {
				return errors.New("empty check runs response")
			}
			break
		}
		if err != nil {
			return err
		}
		if page.CheckRuns == nil {
			return errors.New("missing check runs collection")
		}
		pages++
		for _, v := range page.CheckRuns {
			source := fmt.Sprintf("check:%d:%s", v.App.ID, v.Name)
			if v.Status != "completed" {
				e.PendingCI = append(e.PendingCI, source)
			}
			switch v.Conclusion {
			case "failure", "timed_out", "action_required", "startup_failure":
				add(attention.CI, source, e.PR.Head, v.Name+": "+v.Conclusion, v.HTMLURL, time.Time{})
			}
		}
	}
	statuses, err := attentionPages[struct {
		Context, State string
		TargetURL      string `json:"target_url"`
	}](c, fmt.Sprintf("repos/%s/commits/%s/statuses", e.PR.Repo, e.PR.Head))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, v := range statuses {
		if seen[v.Context] {
			continue
		}
		seen[v.Context] = true
		if v.State == "pending" {
			e.PendingCI = append(e.PendingCI, "status:"+v.Context)
		}
		if v.State == "failure" || v.State == "error" {
			add(attention.CI, "status:"+v.Context, e.PR.Head, v.Context+": "+v.State, v.TargetURL, time.Time{})
		}
	}
	return nil
}
