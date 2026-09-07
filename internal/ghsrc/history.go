package ghsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// SubmittedReview identifies the revision reviewed, including reviews made
// outside crv. Dismissal does not erase the fact that a revision was reviewed.
type SubmittedReview struct {
	ID          int64
	CommitID    string
	SubmittedAt time.Time
	Author      string
	State       string
}

func (c Client) Reviews(repo string, number int) ([]SubmittedReview, error) {
	out, err := c.run("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var reviews []SubmittedReview
	for {
		var page []struct {
			ID          int64                  `json:"id"`
			CommitID    string                 `json:"commit_id"`
			SubmittedAt time.Time              `json:"submitted_at"`
			User        struct{ Login string } `json:"user"`
			State       string                 `json:"state"`
		}
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				return reviews, nil
			}
			return nil, fmt.Errorf("could not read review history: %w", err)
		}
		for _, r := range page {
			reviews = append(reviews, SubmittedReview{r.ID, r.CommitID, r.SubmittedAt, r.User.Login, r.State})
		}
	}
}

func LatestReview(reviews []SubmittedReview, viewer string) *SubmittedReview {
	if viewer == "" {
		return nil
	}
	var latest *SubmittedReview
	for i := range reviews {
		r := &reviews[i]
		if !strings.EqualFold(r.Author, viewer) || r.SubmittedAt.IsZero() || strings.EqualFold(r.State, "PENDING") {
			continue
		}
		if latest == nil || r.SubmittedAt.After(latest.SubmittedAt) || (r.SubmittedAt.Equal(latest.SubmittedAt) && r.ID > latest.ID) {
			latest = r
		}
	}
	return latest
}

// graphql refuses partial data when GitHub reports errors; a partial list is
// unsafe to present as the complete set of outstanding review conversations.
func (c Client) graphql(query string, variables map[string]any, result any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	out, err := c.runInput(payload, "api", "graphql", "--input", "-")
	if err != nil {
		return err
	}
	var response struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		return fmt.Errorf("could not read GitHub GraphQL response: %w", err)
	}
	if len(response.Errors) > 0 {
		messages := make([]string, 0, len(response.Errors))
		for _, e := range response.Errors {
			messages = append(messages, e.Message)
		}
		return fmt.Errorf("GitHub GraphQL: %s", strings.Join(messages, "; "))
	}
	if len(response.Data) == 0 || string(response.Data) == "null" {
		return errors.New("GitHub returned no GraphQL data")
	}
	if err := json.Unmarshal(response.Data, result); err != nil {
		return fmt.Errorf("could not read GitHub GraphQL data: %w", err)
	}
	return nil
}

func (c Client) Reply(repo string, number int, rootCommentID int64, body string) (Comment, error) {
	if rootCommentID <= 0 || strings.TrimSpace(body) == "" {
		return Comment{}, errors.New("a reply requires a root comment and a nonempty body")
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return Comment{}, err
	}
	out, err := c.runInput(payload, "api", "--method", "POST", fmt.Sprintf("repos/%s/pulls/%d/comments/%d/replies", repo, number, rootCommentID), "--input", "-")
	if err != nil {
		return Comment{}, err
	}
	var comment Comment
	if err := json.Unmarshal([]byte(out), &comment); err != nil {
		return Comment{}, fmt.Errorf("could not read submitted reply: %w", err)
	}
	if comment.ID <= 0 {
		return Comment{}, errors.New("GitHub did not return the submitted reply")
	}
	return comment, nil
}

func (c Client) SetThreadResolved(threadID string, resolved bool) error {
	if threadID == "" {
		return errors.New("a thread ID is required")
	}
	mutation := "resolveReviewThread"
	if !resolved {
		mutation = "unresolveReviewThread"
	}
	query := `mutation($id: ID!) { result: ` + mutation + `(input: {threadId: $id}) { thread { id isResolved } } }`
	var data struct {
		Result *struct {
			Thread *struct {
				ID         string
				IsResolved bool
			}
		}
	}
	if err := c.graphql(query, map[string]any{"id": threadID}, &data); err != nil {
		return err
	}
	if data.Result == nil || data.Result.Thread == nil || data.Result.Thread.ID != threadID || data.Result.Thread.IsResolved != resolved {
		return errors.New("GitHub did not confirm the requested thread resolution")
	}
	return nil
}
