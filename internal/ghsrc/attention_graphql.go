package ghsrc

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/attention"
)

// Dismissal events identify the original state; aggregate reviewDecision cannot.
func (c Client) attentionDismissal(repo string, number int, reviewID int64) (*attention.Reason, error) {
	parts := strings.Split(repo, "/")
	var cursor any
	for {
		var data struct {
			Repository *struct {
				PullRequest *struct {
					TimelineItems struct {
						Nodes []struct {
							ID, PreviousReviewState, URL string
							CreatedAt                    time.Time
							Review                       *struct{ DatabaseID int64 }
						}
						PageInfo struct {
							HasNextPage bool
							EndCursor   string
						}
					}
				}
			}
		}
		query := `query($owner:String!,$name:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){timelineItems(first:100,after:$cursor,itemTypes:[REVIEW_DISMISSED_EVENT]){nodes{... on ReviewDismissedEvent{id previousReviewState url createdAt review{databaseId}}}pageInfo{hasNextPage endCursor}}}}}`
		if err := c.graphql(query, map[string]any{"owner": parts[0], "name": parts[1], "number": number, "cursor": cursor}, &data); err != nil {
			return nil, err
		}
		if data.Repository == nil || data.Repository.PullRequest == nil {
			return nil, errors.New("dismissal evidence unavailable")
		}
		page := data.Repository.PullRequest.TimelineItems
		for _, v := range page.Nodes {
			if v.Review != nil && v.Review.DatabaseID == reviewID {
				if v.PreviousReviewState != "APPROVED" {
					return nil, nil
				}
				return &attention.Reason{Kind: attention.Invalidated, Source: fmt.Sprint(reviewID), Generation: v.ID, URL: v.URL, Text: "Your approval was dismissed", At: v.CreatedAt}, nil
			}
		}
		if !page.PageInfo.HasNextPage {
			return nil, errors.New("dismissed review has no accessible dismissal event")
		}
		if page.PageInfo.EndCursor == "" || cursor == page.PageInfo.EndCursor {
			return nil, errors.New("invalid dismissal pagination")
		}
		cursor = page.PageInfo.EndCursor
	}
}

// CLEAN is authoritative merge eligibility. Explicitly inspect required contexts
// too, since a computed/unknown or incomplete rollup must never produce work.
func (c Client) attentionMerge(p attention.PR) (bool, error) {
	parts := strings.Split(p.Repo, "/")
	var cursor any
	eligible := true
	for {
		var data struct {
			Repository *struct {
				PullRequest *struct {
					HeadRefOID, MergeStateStatus, Mergeable, ReviewDecision string
					ViewerCanUpdate                                         bool
					AutoMergeRequest, MergeQueueEntry                       any
					Commits                                                 struct {
						Nodes []struct {
							Commit struct {
								StatusCheckRollup *struct {
									Contexts struct {
										Nodes []struct {
											Typename                  string `json:"__typename"`
											IsRequired                bool
											Status, Conclusion, State string
										}
										PageInfo struct {
											HasNextPage bool
											EndCursor   string
										}
									}
								}
							}
						}
					}
				}
			}
		}
		query := `query($owner:String!,$name:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){headRefOid mergeStateStatus mergeable reviewDecision viewerCanUpdate autoMergeRequest{enabledAt} mergeQueueEntry{id} commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100,after:$cursor){nodes{__typename ... on CheckRun{isRequired(pullRequestNumber:$number) status conclusion} ... on StatusContext{isRequired(pullRequestNumber:$number) state}}pageInfo{hasNextPage endCursor}}}}}}}}}`
		if err := c.graphql(query, map[string]any{"owner": parts[0], "name": parts[1], "number": p.Number, "cursor": cursor}, &data); err != nil {
			return false, err
		}
		if data.Repository == nil || data.Repository.PullRequest == nil {
			return false, errors.New("merge eligibility unavailable")
		}
		pr := data.Repository.PullRequest
		if pr.HeadRefOID != p.Head {
			return false, errors.New("head changed during merge eligibility fetch")
		}
		eligible = eligible && pr.MergeStateStatus == "CLEAN" && pr.Mergeable == "MERGEABLE" && pr.ReviewDecision == "APPROVED" && pr.ViewerCanUpdate && pr.AutoMergeRequest == nil && pr.MergeQueueEntry == nil
		if len(pr.Commits.Nodes) != 1 {
			return false, errors.New("missing latest commit eligibility")
		}
		rollup := pr.Commits.Nodes[0].Commit.StatusCheckRollup
		if rollup == nil {
			return eligible, nil
		}
		for _, v := range rollup.Contexts.Nodes {
			if !v.IsRequired {
				continue
			}
			switch v.Typename {
			case "CheckRun":
				eligible = eligible && v.Status == "COMPLETED" && (v.Conclusion == "SUCCESS" || v.Conclusion == "NEUTRAL" || v.Conclusion == "SKIPPED")
			case "StatusContext":
				eligible = eligible && v.State == "SUCCESS"
			default:
				return false, errors.New("unknown required check type")
			}
		}
		page := rollup.Contexts.PageInfo
		if !page.HasNextPage {
			return eligible, nil
		}
		if page.EndCursor == "" || cursor == page.EndCursor {
			return false, errors.New("invalid required checks pagination")
		}
		cursor = page.EndCursor
	}
}

// Request node IDs distinguish re-requests even when fulfillment happened
// between polls and the PR/head timestamps did not change.
func (c Client) attentionRequests(p attention.PR, viewer string, teams map[int64]bool) ([]attention.Reason, error) {
	parts := strings.Split(p.Repo, "/")
	var cursor any
	var reasons []attention.Reason
	for {
		var data struct {
			Repository *struct {
				PullRequest *struct {
					ReviewRequests *struct {
						Nodes []struct {
							ID                string
							RequestedReviewer *struct {
								Typename    string `json:"__typename"`
								Login, Slug string
								DatabaseID  int64
							}
						}
						PageInfo struct {
							HasNextPage bool
							EndCursor   string
						}
					}
				}
			}
		}
		query := `query($owner:String!,$name:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewRequests(first:100,after:$cursor){nodes{id requestedReviewer{__typename ... on User{login} ... on Team{databaseId slug}}}pageInfo{hasNextPage endCursor}}}}}`
		if err := c.graphql(query, map[string]any{"owner": parts[0], "name": parts[1], "number": p.Number, "cursor": cursor}, &data); err != nil {
			return nil, err
		}
		if data.Repository == nil || data.Repository.PullRequest == nil || data.Repository.PullRequest.ReviewRequests == nil {
			return nil, errors.New("review request evidence unavailable")
		}
		page := data.Repository.PullRequest.ReviewRequests
		for _, node := range page.Nodes {
			r := node.RequestedReviewer
			if r == nil || node.ID == "" {
				return nil, errors.New("incomplete requested reviewer")
			}
			reason := attention.Reason{Generation: node.ID, URL: p.URL}
			switch r.Typename {
			case "User":
				if !strings.EqualFold(r.Login, viewer) {
					continue
				}
				reason.Kind = attention.Direct
				reason.Source = viewer
				reason.Text = "Your review is requested"
			case "Team":
				if r.DatabaseID == 0 {
					return nil, errors.New("team identity unavailable")
				}
				if !teams[r.DatabaseID] {
					continue
				}
				reason.Kind = attention.Team
				reason.Source = fmt.Sprint(r.DatabaseID)
				reason.Text = "Review requested from " + r.Slug
			default:
				return nil, errors.New("unsupported requested reviewer type")
			}
			reasons = append(reasons, reason)
		}
		if !page.PageInfo.HasNextPage {
			return reasons, nil
		}
		if page.PageInfo.EndCursor == "" || cursor == page.PageInfo.EndCursor {
			return nil, errors.New("invalid review request pagination")
		}
		cursor = page.PageInfo.EndCursor
	}
}
