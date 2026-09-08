// Package attentionstore persists reconciliation and outgoing operations atomically.
package attentionstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tobiasbernting/code-review-cli/internal/attention"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }
type Outgoing struct {
	ID                                int64
	Kind, Repo, Key, Version, Payload string
	Attempts                          int
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "_txlock=immediate"}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 1 {
		db.Close()
		return nil, fmt.Errorf("attention database schema %d is newer than this binary supports", version)
	}
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS tasks(pr TEXT, reason TEXT, payload TEXT NOT NULL, PRIMARY KEY(pr,reason));
 CREATE TABLE IF NOT EXISTS acknowledgements(key TEXT PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS notifications(id TEXT PRIMARY KEY,pr TEXT,payload TEXT);
 CREATE TABLE IF NOT EXISTS decisions(id TEXT,version TEXT,PRIMARY KEY(id,version));
 CREATE TABLE IF NOT EXISTS activations(key TEXT PRIMARY KEY,count INTEGER);
 CREATE TABLE IF NOT EXISTS evidence(pr TEXT PRIMARY KEY,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS outbox(id INTEGER PRIMARY KEY,kind TEXT,repo TEXT,key TEXT,version TEXT,payload TEXT,attempts INTEGER DEFAULT 0,next_attempt INTEGER DEFAULT 0, UNIQUE(kind,key,version));
 CREATE TABLE IF NOT EXISTS delivered(kind TEXT,key TEXT,version TEXT,PRIMARY KEY(kind,key,version));
 CREATE TABLE IF NOT EXISTS history(id INTEGER PRIMARY KEY,at TEXT,repo TEXT,url TEXT,message TEXT);
 CREATE TABLE IF NOT EXISTS worker_lease(id INTEGER PRIMARY KEY CHECK(id=1),owner TEXT,expires INTEGER);
 CREATE TABLE IF NOT EXISTS lease(id INTEGER PRIMARY KEY CHECK(id=1),owner TEXT,expires INTEGER);
 PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Pin(identity string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO meta VALUES('identity',?)`, identity)
	if err != nil {
		return err
	}
	if s.Meta("identity") != identity {
		return errors.New("authenticated account changed; attention side effects paused")
	}
	return nil
}
func (s *Store) Meta(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// Lease is renewed by the reconciler. No database transaction spans network I/O.
func (s *Store) Acquire(owner string, now time.Time) error {
	r, err := s.db.Exec(`INSERT INTO lease VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET owner=excluded.owner,expires=excluded.expires WHERE lease.expires<?`, owner, now.Add(2*time.Minute).Unix(), now.Unix())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return errors.New("attention synchronization already running")
	}
	return nil
}
func (s *Store) Renew(owner string, now time.Time) error {
	r, err := s.db.Exec(`UPDATE lease SET expires=? WHERE owner=? AND expires>=?`, now.Add(2*time.Minute).Unix(), owner, now.Unix())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("attention synchronization lease lost")
	}
	return nil
}
func (s *Store) Release(owner string) { s.db.Exec(`DELETE FROM lease WHERE owner=?`, owner) }
func (s *Store) Tasks() ([]attention.Task, error) {
	rows, err := s.db.Query(`SELECT payload FROM tasks ORDER BY pr,reason`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []attention.Task
	for rows.Next() {
		var data string
		var t attention.Task
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Snapshot(scope []string) (attention.Snapshot, error) {
	all, err := s.Tasks()
	v := attention.Snapshot{Identity: s.Meta("identity"), Error: s.Meta("error"), AlertError: s.Meta("alert_error")}
	v.LastSync, _ = time.Parse(time.RFC3339, s.Meta("last_sync"))
	for _, t := range all {
		if t.Active && InScope(scope, t.PR.Repo) {
			v.Tasks = append(v.Tasks, t)
		}
	}
	return v, err
}
func InScope(scope []string, repo string) bool {
	for _, r := range scope {
		if strings.EqualFold(r, repo) {
			return true
		}
	}
	return false
}
func (s *Store) Save(e attention.Evidence, notifications []attention.Notification, alerts bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousEvidence string
	identityErr := tx.QueryRow(`SELECT payload FROM evidence WHERE pr=?`, e.PR.Key()).Scan(&previousEvidence)
	if identityErr != nil && !errors.Is(identityErr, sql.ErrNoRows) {
		return identityErr
	}
	if identityErr == nil {
		var previous attention.Evidence
		if err = json.Unmarshal([]byte(previousEvidence), &previous); err != nil {
			return err
		}
		if previous.PR.RepoID != 0 && e.PR.RepoID != 0 && previous.PR.RepoID != e.PR.RepoID {
			return errors.New("repository identity changed; reconciliation paused")
		}
	}
	rows, err := tx.Query(`SELECT payload FROM tasks WHERE pr=?`, e.PR.Key())
	if err != nil {
		return err
	}
	var old []attention.Task
	for rows.Next() {
		var data string
		var t attention.Task
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(data), &t); err != nil {
			rows.Close()
			return err
		}
		old = append(old, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	acks := map[string]bool{}
	rows, err = tx.Query(`SELECT key FROM acknowledgements`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		acks[key] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	next := attention.Reconcile(old, e, acks)
	previous := map[string]bool{}
	for _, t := range old {
		if t.Active {
			previous[attention.AckKey(t.PR, t.Reason)] = true
		}
	}
	if _, err = tx.Exec(`DELETE FROM tasks WHERE pr=?`, e.PR.Key()); err != nil {
		return err
	}
	var fresh []string
	var generations []string
	for _, t := range next {
		b, _ := json.Marshal(t)
		if _, err = tx.Exec(`INSERT INTO tasks VALUES(?,?,?)`, t.PR.Key(), t.Reason.Key(), string(b)); err != nil {
			return err
		}
		if t.Active && !previous[attention.AckKey(t.PR, t.Reason)] {
			fresh = append(fresh, string(t.Reason.Kind))
			key := attention.AckKey(t.PR, t.Reason)
			if _, err = tx.Exec(`INSERT INTO activations VALUES(?,1) ON CONFLICT(key) DO UPDATE SET count=count+1`, key); err != nil {
				return err
			}
			var count int
			if err = tx.QueryRow(`SELECT count FROM activations WHERE key=?`, key).Scan(&count); err != nil {
				return err
			}
			generations = append(generations, fmt.Sprintf("%s/%d", key, count))
		}
	}
	for _, n := range notifications {
		b, _ := json.Marshal(n)
		if _, err = tx.Exec(`INSERT INTO notifications VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET pr=excluded.pr,payload=excluded.payload`, n.ID, e.PR.Key(), string(b)); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(e)
	if _, err = tx.Exec(`INSERT INTO evidence VALUES(?,?) ON CONFLICT(pr) DO UPDATE SET payload=excluded.payload`, e.PR.Key(), string(b)); err != nil {
		return err
	}
	queued := false
	enqueue := func(kind, key, version, payload string) error {
		result, er := tx.Exec(`INSERT OR IGNORE INTO outbox(kind,repo,key,version,payload) SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM delivered WHERE kind=? AND key=? AND version=?)`, kind, e.PR.Repo, key, version, payload, kind, key, version)
		if er == nil {
			n, _ := result.RowsAffected()
			queued = n > 0
		}
		return er
	}
	if alerts && len(fresh) > 0 {
		// Coalesce pending activity into one delivery per PR, including retrying work.
		if _, err = tx.Exec(`DELETE FROM outbox WHERE kind='alert' AND key=?`, e.PR.Key()); err != nil {
			return err
		}
		labels := []string{}
		seenLabels := map[attention.Kind]bool{}
		for _, task := range next {
			if task.Active && !seenLabels[task.Reason.Kind] {
				labels = append(labels, string(task.Reason.Kind))
				seenLabels[task.Reason.Kind] = true
			}
		}
		if err = enqueue("alert", e.PR.Key(), attention.Generation(strings.Join(generations, "\n")), e.PR.Title+"\n"+strings.Join(labels, ", ")+"\n"+e.PR.URL); err != nil {
			return err
		}
	}
	if e.Complete {
		for _, n := range notifications {
			if !n.Unread {
				continue
			}
			message := "no outstanding actionable activity"
			if len(next) > 0 {
				message = "classified work saved locally"
			}
			b, _ := json.Marshal(n)
			if err = enqueue("read", n.ID, n.Version, string(b)); err != nil {
				return err
			}
			if !queued {
				continue
			}
			if _, err = tx.Exec(`INSERT INTO history(at,repo,url,message) VALUES(?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), e.PR.Repo, e.PR.URL, message); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Done records exactly what the UI observed even if a newer generation was saved.
func (s *Store) Done(t attention.Task) error {
	if !t.Reason.Manual() {
		return errors.New("this reason is controlled by GitHub evidence")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO acknowledgements VALUES(?)`, attention.AckKey(t.PR, t.Reason)); err != nil {
		return err
	}
	var data string
	err = tx.QueryRow(`SELECT payload FROM tasks WHERE pr=? AND reason=?`, t.PR.Key(), t.Reason.Key()).Scan(&data)
	if err != nil {
		return err
	}
	var current attention.Task
	if err = json.Unmarshal([]byte(data), &current); err != nil {
		return err
	}
	if current.Reason.Generation == t.Reason.Generation {
		current.Active = false
		current.Completion = "local Done"
		b, _ := json.Marshal(current)
		if _, err = tx.Exec(`UPDATE tasks SET payload=? WHERE pr=? AND reason=?`, string(b), t.PR.Key(), t.Reason.Key()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Pending(scope []string, now time.Time) ([]Outgoing, error) {
	rows, err := s.db.Query(`SELECT id,kind,repo,key,version,payload,attempts FROM outbox WHERE next_attempt<=? ORDER BY id`, now.Unix())
	if err != nil {
		return nil, err
	}
	var out []Outgoing
	var cancel []int64
	for rows.Next() {
		var o Outgoing
		if err = rows.Scan(&o.ID, &o.Kind, &o.Repo, &o.Key, &o.Version, &o.Payload, &o.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		if InScope(scope, o.Repo) {
			out = append(out, o)
		} else {
			cancel = append(cancel, o.ID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range cancel {
		if _, err = s.db.Exec(`DELETE FROM outbox WHERE id=?`, id); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) Finish(o Outgoing, delivery error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	message := o.Kind + " succeeded"
	if delivery != nil {
		message = o.Kind + " failed: " + delivery.Error()
		delay := time.Minute * time.Duration(1<<min(o.Attempts, 8))
		if o.Kind == "alert" && o.Attempts >= 2 {
			delay = 100 * 365 * 24 * time.Hour
		}
		_, err = tx.Exec(`UPDATE outbox SET attempts=attempts+1,next_attempt=? WHERE id=?`, time.Now().Add(delay).Unix(), o.ID)
	} else {
		_, err = tx.Exec(`INSERT OR IGNORE INTO delivered VALUES(?,?,?)`, o.Kind, o.Key, o.Version)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM outbox WHERE id=?`, o.ID)
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO history(at,repo,url,message) VALUES(?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), o.Repo, "", message); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) History() ([]string, error) {
	rows, err := s.db.Query(`SELECT at,repo,url,message FROM history ORDER BY id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var at, repo, url, msg string
		if err = rows.Scan(&at, &repo, &url, &msg); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s %s %s %s", at, repo, msg, url))
	}
	return out, rows.Err()
}

// DecideTriage explicitly authorizes cleanup for the exact stored notification
// versions. Keep creates a separate manual task before scheduling any read.
func (s *Store) DecideTriage(t attention.Task, keep bool) error {
	if t.Reason.Kind != attention.Triage {
		return errors.New("select a Needs triage reason")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data string
	if err = tx.QueryRow(`SELECT payload FROM tasks WHERE pr=? AND reason=?`, t.PR.Key(), t.Reason.Key()).Scan(&data); err != nil {
		return err
	}
	var current attention.Task
	if err = json.Unmarshal([]byte(data), &current); err != nil {
		return err
	}
	if current.Reason.Generation != t.Reason.Generation {
		return errors.New("triage changed; refresh before deciding")
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO acknowledgements VALUES(?)`, attention.AckKey(t.PR, t.Reason)); err != nil {
		return err
	}
	current.Active = false
	current.Completion = "local triage decision"
	b, _ := json.Marshal(current)
	if _, err = tx.Exec(`UPDATE tasks SET payload=? WHERE pr=? AND reason=?`, string(b), t.PR.Key(), t.Reason.Key()); err != nil {
		return err
	}
	if keep {
		retained := t
		retained.Reason.Kind = attention.Retained
		retained.Active = true
		b, _ = json.Marshal(retained)
		if _, err = tx.Exec(`INSERT INTO tasks VALUES(?,?,?) ON CONFLICT(pr,reason) DO UPDATE SET payload=excluded.payload`, t.PR.Key(), retained.Reason.Key(), string(b)); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT payload FROM notifications WHERE pr=?`, t.PR.Key())
	if err != nil {
		return err
	}
	var ns []attention.Notification
	for rows.Next() {
		var raw string
		var n attention.Notification
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &n); err != nil {
			rows.Close()
			return err
		}
		ns = append(ns, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, n := range ns {
		if !n.Unread {
			continue
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO decisions VALUES(?,?)`, n.ID, n.Version); err != nil {
			return err
		}
		raw, _ := json.Marshal(n)
		if _, err = tx.Exec(`INSERT OR IGNORE INTO outbox(kind,repo,key,version,payload) VALUES('read',?,?,?,?)`, n.Repo, n.ID, n.Version, string(raw)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO history(at,repo,url,message) VALUES(?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), t.PR.Repo, t.PR.URL, fmt.Sprintf("triage decision saved; keep=%t", keep)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ReadDecided(n attention.Notification) bool {
	var count int
	return s.db.QueryRow(`SELECT count(*) FROM decisions WHERE id=? AND version=?`, n.ID, n.Version).Scan(&count) == nil && count > 0
}

// Retain is a local correction, independent of GitHub unread state.
func (s *Store) Retain(p attention.PR, text string) error {
	t := attention.Task{PR: p, Reason: attention.Reason{Kind: attention.Retained, Source: "local", Generation: fmt.Sprint(time.Now().UnixNano()), Text: text, URL: p.URL, At: time.Now()}, Active: true}
	b, _ := json.Marshal(t)
	_, err := s.db.Exec(`INSERT INTO tasks VALUES(?,?,?) ON CONFLICT(pr,reason) DO UPDATE SET payload=excluded.payload`, p.Key(), t.Reason.Key(), string(b))
	return err
}

func (s *Store) FailedAlerts() bool {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM outbox WHERE kind='alert' AND attempts>0`).Scan(&n)
	return err != nil || n > 0
}
func (s *Store) Cancel(o Outgoing, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM outbox WHERE id=?`, o.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO history(at,repo,url,message) VALUES(?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), o.Repo, "", o.Kind+" cancelled: "+reason); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AcquireWorker(owner string, now time.Time) error {
	r, err := s.db.Exec(`INSERT INTO worker_lease VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET owner=excluded.owner,expires=excluded.expires WHERE worker_lease.expires<?`, owner, now.Add(2*time.Minute).Unix(), now.Unix())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("attention worker already running")
	}
	return nil
}
func (s *Store) RenewWorker(owner string, now time.Time) error {
	r, err := s.db.Exec(`UPDATE worker_lease SET expires=? WHERE owner=? AND expires>=?`, now.Add(2*time.Minute).Unix(), owner, now.Unix())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("attention worker lease lost")
	}
	return nil
}
func (s *Store) ReleaseWorker(owner string) {
	s.db.Exec(`DELETE FROM worker_lease WHERE owner=?`, owner)
}
