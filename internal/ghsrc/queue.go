package ghsrc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/config"
)

// Filter selects which pull requests the queue shows.
type Filter string

const (
	// FilterReviewRequested is the morning list: what is waiting on you.
	FilterReviewRequested Filter = "review-requested:@me"
	// FilterAuthored is your own open pull requests.
	FilterAuthored Filter = "author:@me"
)

func (f Filter) Label() string {
	if f == FilterAuthored {
		return "mine"
	}
	return "to review"
}

// QueueItem is one row of the review queue.
type QueueItem struct {
	Repo      string
	Number    int
	Title     string
	Author    string
	URL       string
	IsDraft   bool
	Checks    string // SUCCESS, FAILURE, PENDING, ERROR, or "" when there are none
	UpdatedAt time.Time
}

// Age is a compact "how long since this last moved".
func (q QueueItem) Age() string {
	d := time.Since(q.UpdatedAt)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// queueQuery asks for everything the list shows in one request. Fetching the
// check status per pull request would be one API call each; the last commit's
// rollup gives it for all of them at once.
const queueQuery = `query($q: String!, $limit: Int!) {
  search(query: $q, type: ISSUE, first: $limit) {
    nodes {
      ... on PullRequest {
        number title url isDraft updatedAt
        author { login }
        repository { nameWithOwner }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
      }
    }
  }
}`

// Queue lists the pull requests matching a filter, newest activity first.
func (c Client) Queue(filter Filter, limit int) ([]QueueItem, error) {
	if limit <= 0 {
		limit = 30
	}
	out, err := c.run("api", "graphql",
		"-f", "query="+queueQuery,
		"-F", "q=is:open is:pr archived:false "+string(filter),
		"-F", "limit="+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	return parseQueue(out)
}

func parseQueue(out string) ([]QueueItem, error) {
	var resp struct {
		Data struct {
			Search struct {
				Nodes []struct {
					Number     int                    `json:"number"`
					Title      string                 `json:"title"`
					URL        string                 `json:"url"`
					IsDraft    bool                   `json:"isDraft"`
					UpdatedAt  time.Time              `json:"updatedAt"`
					Author     struct{ Login string } `json:"author"`
					Repository struct {
						NameWithOwner string `json:"nameWithOwner"`
					} `json:"repository"`
					Commits struct {
						Nodes []struct {
							Commit struct {
								StatusCheckRollup *struct {
									State string `json:"state"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
				} `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("could not read the review queue: %w", err)
	}

	items := make([]QueueItem, 0, len(resp.Data.Search.Nodes))
	for _, n := range resp.Data.Search.Nodes {
		// An empty node is a search result that is not a pull request.
		if n.Repository.NameWithOwner == "" {
			continue
		}
		item := QueueItem{
			Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title,
			Author: n.Author.Login, URL: n.URL, IsDraft: n.IsDraft, UpdatedAt: n.UpdatedAt,
		}
		if len(n.Commits.Nodes) > 0 {
			if r := n.Commits.Nodes[0].Commit.StatusCheckRollup; r != nil {
				item.Checks = r.State
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// queueCache holds the last fetched list. The diff of a pull request is never
// cached — reviewing a stale diff is the worst thing this tool could do — but
// the list is several API calls and staleness there is harmless.
type queueCache struct {
	Fetched time.Time              `json:"fetched"`
	Items   map[string][]QueueItem `json:"items"`
}

// CacheTTL is how long a cached queue is considered fresh.
const CacheTTL = 5 * time.Minute

func cachePath() (string, error) {
	base, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "queue.json"), nil
}

// CachedQueue returns the cached list when it is fresh, and fetches otherwise.
// A fetch failure falls back to whatever was cached, so a flaky network shows
// a stale list rather than nothing.
func (c Client) CachedQueue(filter Filter, limit int, force bool) (items []QueueItem, cachedAt time.Time, err error) {
	path, perr := cachePath()
	cache := queueCache{Items: map[string][]QueueItem{}}
	if perr == nil {
		if data, rerr := os.ReadFile(path); rerr == nil {
			_ = json.Unmarshal(data, &cache)
			if cache.Items == nil {
				cache.Items = map[string][]QueueItem{}
			}
		}
	}

	key := string(filter)
	if !force {
		if got, ok := cache.Items[key]; ok && time.Since(cache.Fetched) < CacheTTL {
			return got, cache.Fetched, nil
		}
	}

	fresh, ferr := c.Queue(filter, limit)
	if ferr != nil {
		if got, ok := cache.Items[key]; ok {
			return got, cache.Fetched, ferr
		}
		return nil, time.Time{}, ferr
	}

	cache.Fetched = time.Now()
	cache.Items[key] = fresh
	if perr == nil {
		if data, merr := json.Marshal(cache); merr == nil {
			_ = os.MkdirAll(filepath.Dir(path), 0o700)
			_ = os.WriteFile(path, data, 0o600)
		}
	}
	return fresh, cache.Fetched, nil
}
