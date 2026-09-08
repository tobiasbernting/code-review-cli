package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Attention struct {
	Enabled              bool     `toml:"enabled"`
	Repositories         []string `toml:"repositories"`
	PollInterval         string   `toml:"poll_interval"`
	DesktopNotifications bool     `toml:"desktop_notifications"`
}

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (a Attention) Validate() error {
	if a.Enabled && len(a.Repositories) == 0 {
		return fmt.Errorf("attention.enabled requires attention.repositories")
	}
	seen := map[string]bool{}
	for _, repo := range a.Repositories {
		key := strings.ToLower(repo)
		if !repositoryName.MatchString(repo) || seen[key] {
			return fmt.Errorf("invalid or duplicate attention repository %q", repo)
		}
		seen[key] = true
	}
	d, err := time.ParseDuration(a.PollInterval)
	if err != nil || d < time.Minute {
		return fmt.Errorf("attention.poll_interval must be at least 60s")
	}
	return nil
}

// LoadAttention resolves worker configuration independently of the checkout.
func LoadAttention() (Config, error) { return Load("") }
