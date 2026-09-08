// Package service coordinates the shared foreground/background reconciler.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"github.com/tobiasbernting/code-review-cli/internal/attentionstore"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
)

type GitHub interface {
	AttentionIdentity() (ghsrc.AttentionIdentity, error)
	AttentionTeams() (map[int64]bool, error)
	AttentionNotifications() ([]attention.Notification, error)
	AttentionCandidates(string) ([]attention.PR, error)
	AttentionEvidence(string, int, string, map[int64]bool) (attention.Evidence, error)
	AttentionRead(attention.Notification) error
}
type Engine struct {
	Reload       func() error
	Store        *attentionstore.Store
	GitHub       GitHub
	Host         string
	Repositories []string
	Alerts       bool
	Notify       func(context.Context, string, string) error
}

func (e *Engine) Sync(ctx context.Context) (result error) {
	owner := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := e.Store.Acquire(owner, time.Now()); err != nil {
		return err
	}
	defer e.Store.Release(owner)
	defer func() {
		if result != nil {
			e.Store.SetMeta("error", result.Error())
		} else {
			e.Store.SetMeta("error", "")
			e.Store.SetMeta("last_sync", time.Now().UTC().Format(time.RFC3339))
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.Store.Renew(owner, time.Now()) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-renewDone }()
	identity, err := e.GitHub.AttentionIdentity()
	if err != nil {
		return err
	}
	pin := fmt.Sprintf("%s/%d/%s", e.Host, identity.ID, identity.Login)
	if err = e.Store.Pin(pin); err != nil {
		return err
	}
	teams, err := e.GitHub.AttentionTeams()
	if err != nil {
		return err
	}
	notifications, err := e.GitHub.AttentionNotifications()
	if err != nil {
		return err
	}
	candidates := map[string]attention.PR{}
	byPR := map[string][]attention.Notification{}
	for _, n := range notifications {
		if attentionstore.InScope(e.Repositories, n.Repo) {
			p := attention.PR{Repo: n.Repo, Number: n.Number}
			candidates[p.Key()] = p
			byPR[p.Key()] = append(byPR[p.Key()], n)
		}
	}
	old, err := e.Store.Tasks()
	if err != nil {
		return err
	}
	for _, t := range old {
		if attentionstore.InScope(e.Repositories, t.PR.Repo) {
			candidates[t.PR.Key()] = t.PR
		}
	}
	var failures []error
	for _, repo := range e.Repositories {
		prs, fetchErr := e.GitHub.AttentionCandidates(repo)
		if fetchErr != nil {
			failures = append(failures, fetchErr)
			continue
		}
		for _, p := range prs {
			candidates[p.Key()] = p
		}
	}
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	confident := map[string]bool{}
	for _, key := range keys {
		if err = ctx.Err(); err != nil {
			return err
		}
		p := candidates[key]
		ev, fetchErr := e.GitHub.AttentionEvidence(p.Repo, p.Number, identity.Login, teams)
		ev.ObservedAt = time.Now().UTC()
		if fetchErr != nil {
			ev.Complete = false
			ev.Problem = fetchErr.Error()
			if ev.PR.Title == "" {
				ev.PR = p
			}
			failures = append(failures, fmt.Errorf("%s: %w", key, fetchErr))
		}
		for _, n := range byPR[key] {
			ev.UncertaintyVersion += n.ID + ":" + n.Version + ";"
		}
		if err = e.Store.Renew(owner, time.Now()); err != nil {
			return err
		}
		if err = e.Store.Save(ev, byPR[key], e.Alerts); err != nil {
			return err
		}
		confident[key] = ev.Complete
	}
	pending, err := e.Store.Pending(e.Repositories, time.Now())
	if err != nil {
		return err
	}
	var alertErrors []error
	for _, o := range pending {
		if err = ctx.Err(); err != nil {
			return err
		}
		current, identityErr := e.GitHub.AttentionIdentity()
		if identityErr != nil {
			return identityErr
		}
		if current.ID != identity.ID || current.Login != identity.Login {
			return errors.New("authenticated account changed before delivery")
		}
		if err = e.Store.Renew(owner, time.Now()); err != nil {
			return err
		}
		var delivery error
		switch o.Kind {
		case "read":
			var n attention.Notification
			if err = json.Unmarshal([]byte(o.Payload), &n); err != nil {
				return err
			}
			p := attention.PR{Repo: n.Repo, Number: n.Number}
			superseded := false
			for _, observed := range byPR[p.Key()] {
				if observed.ID == n.ID && observed.Version != n.Version {
					superseded = true
					break
				}
			}
			if superseded {
				if err = e.Store.Cancel(o, "superseded by newer notification activity"); err != nil {
					return err
				}
				continue
			}
			if !confident[p.Key()] && !e.Store.ReadDecided(n) {
				continue
			}
			delivery = e.GitHub.AttentionRead(n)
		case "alert":
			if !e.Alerts {
				if err = e.Store.Finish(o, nil); err != nil {
					return err
				}
				continue
			}
			snapshot, snapshotErr := e.Store.Snapshot(e.Repositories)
			if snapshotErr != nil {
				return snapshotErr
			}
			active := false
			for _, task := range snapshot.Tasks {
				if task.PR.Key() == o.Key {
					active = true
				}
			}
			if !active {
				if err = e.Store.Finish(o, nil); err != nil {
					return err
				}
				continue
			}
			if e.Notify == nil {
				delivery = errors.New("desktop alerts unsupported")
			} else {
				delivery = e.Notify(ctx, o.Key, o.Payload)
			}
			if delivery != nil {
				alertErrors = append(alertErrors, delivery)
			}
		default:
			return fmt.Errorf("unknown outbox operation %q", o.Kind)
		}
		if err = e.Store.Finish(o, delivery); err != nil {
			return err
		}
		if delivery != nil {
			failures = append(failures, delivery)
		}
	}
	if len(alertErrors) > 0 {
		e.Store.SetMeta("alert_error", errors.Join(alertErrors...).Error())
	} else {
		if !e.Store.FailedAlerts() {
			e.Store.SetMeta("alert_error", "")
		}
	}
	return errors.Join(failures...)
}

// Run catches up on every wake; cancellation interrupts both waiting and gh.
func (e *Engine) Run(ctx context.Context, interval time.Duration) error {
	owner := fmt.Sprint(time.Now().UnixNano())
	if err := e.Store.AcquireWorker(owner, time.Now()); err != nil {
		return err
	}
	defer e.Store.ReleaseWorker(owner)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.Store.RenewWorker(owner, time.Now()) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()

	delay := interval
	for {
		var err error
		if e.Reload != nil {
			err = e.Reload()
		}
		if err == nil {
			err = e.Sync(ctx)
		} else {
			e.Store.SetMeta("error", err.Error())
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			delay = min(max(interval, delay*2), 30*time.Minute)
		} else {
			delay = interval
		}
		if hint, parseErr := time.ParseDuration(e.Store.Meta("poll_hint")); parseErr == nil {
			delay = max(delay, hint)
		}
		jitter := time.Duration(time.Now().UnixNano() % int64(max(time.Second, delay/10)))
		timer := time.NewTimer(delay + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
