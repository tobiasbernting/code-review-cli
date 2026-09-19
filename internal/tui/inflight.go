package tui

import (
	"strings"

	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
)

// inflight names the GitHub requests running now. A request is keyed by what
// it does and to what, so pressing a key again while its request runs does
// nothing instead of sending it twice, while unrelated requests are not held
// up. Starting a request records its key; its result message clears it.
type inflight map[string]bool

const (
	reqSync   = "sync"
	reqSubmit = "submit"
)

func reqReply(threadID string) string     { return "reply:" + threadID }
func reqResolve(threadID string) string   { return "resolve:" + threadID }
func reqQueue(filter ghsrc.Filter) string { return "queue:" + string(filter) }

// start records key as running. It reports false, and records nothing, when
// that request is already running.
func (f inflight) start(key string) bool {
	if f[key] {
		return false
	}
	f[key] = true
	return true
}

func (f inflight) done(key string) { delete(f, key) }

func (f inflight) has(key string) bool { return f[key] }

// mutating reports whether a request that changes GitHub is running. The
// review holds still while one does: the result decides what the screen shows
// next, and whether it landed must not be lost to a keypress.
func (f inflight) mutating() bool {
	for key := range f {
		if key == reqSubmit || strings.HasPrefix(key, "reply:") || strings.HasPrefix(key, "resolve:") {
			return true
		}
	}
	return false
}
