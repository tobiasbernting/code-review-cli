# Attention queue

Attention is opt-in. With it disabled, the existing review queue and local diff
commands behave as before. Enable it in your **user** config (`crv --config`
shows its location), using your actual repository names:

```toml
[attention]
enabled = true
repositories = ["your-owner/your-repository"]
poll_interval = "60s"
desktop_notifications = true
```

The name above is a placeholder. A nonempty allowlist is required when enabled.
Repository names are case insensitive; duplicates and malformed names are errors.
The minimum interval is one minute. GitHub's `X-Poll-Interval` can increase it.

Attention settings in `.crv.toml` are ignored. Worker host selection uses the
user config and `CRV_HOST`, then `GH_HOST`, then `github.com`, independently of the
current checkout. UI presentation settings retain normal precedence. One local
SQLite store is pinned to a host and authenticated account ID/login. An account
change pauses synchronization and delivery; switch back to the pinned account
before resuming. Tokens are never stored in the database.

## Commands

| Command | Effect |
| --- | --- |
| `crv` | Synchronize and open attention; works without a daemon or checkout |
| `crv attention sync` | Run one cycle, including notification cleanup and alerts |
| `crv attention diagnose` | Print evidence for open in-scope PRs; no database or GitHub writes |
| `crv attention history` | Print the latest 500 cleanup decisions and delivery attempts |
| `crv attention retain owner/repo#123` | Retain/reopen a local task, including from a history entry |
| `crv service run` | Run the portable foreground worker |
| `crv service install` | Write this user's macOS LaunchAgent; does not start it |
| `crv service start` / `stop` | Load/unload this LaunchAgent |
| `crv service status` | Show launchd state, identity, scope, sync and alert health; reports not installed or installed-but-not-loaded without failing |
| `crv service uninstall` | Unload/remove this LaunchAgent; preserve all user state |

`--limit` affects authored browsing only; it never limits attention discovery.
The attention view scrolls through every locally active PR.

## Reading and completing work

A row groups all reasons for a PR. Use `tab` to select a reason and `d` to mark
that conversation Done. Opening a PR, reading its notification in GitHub, replying,
or submitting a review does **not** complete conversation work. Done records the
observed activity generation; a later relevant comment or content edit creates
new work. A newer generation arriving concurrently with Done remains active.

Direct and team review requests are independent. A teammate's review does not
remove a team reason while that team's request remains outstanding. Ordinary
pushes do not invalidate a user's approval. Dismissal is checked against the
individual review's dismissal event and previous state.

Mentions exclude quoted lines and fenced/inline code and match complete logins.
Replies in unresolved threads the viewer started or joined create work. Existing
conversation work remains until Done, including after a thread resolves or a PR
closes. Author feedback is independent of failed latest-head checks and merge
readiness. Plain approvals without feedback do not create conversation work.
Failed checks/statuses are tracked independently; rerunning preserves a prior
failure until a terminal result or new head. Failure, timeout, action-required,
startup-failure and status error/failure create CI work; cancellation, skipped
and neutral are not treated as failures. Drafts do not create CI or merge tasks.

Merge work requires an authored, non-draft PR, approval, authoritative CLEAN and
MERGEABLE state, write eligibility, passing required contexts, and no auto-merge
or merge-queue entry. Unknown eligibility cannot produce merge work. crv never
merges PRs.

Needs triage means evidence is incomplete or ambiguous. Use `K` to Keep it as a
local task or `D` to Dismiss it. The decision is saved before the exact observed
notification versions become eligible for cleanup. Newer versions still require
classification. Network failures remain visible in health even after triage.

`enter` opens the selected reason's review thread when available, including a
thread you joined. Other content is displayed in the queue with its GitHub URL;
`enter` opens the PR diff. `t` switches to authored browsing, `h` shows cleanup
history, `u` retains a PR locally, and `r` refreshes. Returning from an attention
PR review refreshes the shared task state. Review workflow verification marks
remain separate from attention Done.

## Cleanup, persistence and health

The store is `attention.sqlite` beside user configuration and review notes.
SQLite is bundled through `modernc.org/sqlite` v1.39.1, a pure-Go driver compatible
with the CGO-disabled release matrix. State files are private; WAL, short immediate
transactions and a five-second busy timeout support concurrent UI writes. A
renewable two-minute lease prevents competing reconcilers. After a hard crash,
a replacement can acquire the expired lease. Migrations reject newer schemas.

A transaction saves task changes, evidence and outgoing operations before any
notification write or alert. Only the individual PR notification-thread PATCH is
used. Actual issues and out-of-scope notifications are never read automatically.
Removing repositories cancels pending operations and hides their tasks while
retaining historical state. The worker reloads scope and alert settings each
cycle; restart after changing the host or poll interval.

Discovery enumerates paginated open PRs in each configured repository rather than
using capped search results. It also revisits previously tracked work and all
available PR notifications, including read entries. This deliberately favors
coverage over API request volume; large repositories may need longer intervals.
GitHub retention still limits recovery of old, already-closed PR activity that
was never tracked locally. The queue does not claim unbounded historical recovery.

Before PATCH, crv re-fetches the individual notification and checks its version.
GitHub offers no conditional event-version PATCH: activity can still arrive
between that GET and PATCH. Subsequent full overlapping scans and independent
PR discovery recover work; an uncertain racing event can briefly be read on
GitHub before appearing locally as triage. No unread restoration is promised.

A failed source never becomes an empty successful result. Existing work remains
usable offline, with the last successful sync and errors displayed. “No known
outstanding tasks” with a stale or failed sync does not mean everything is clear.
Cleanup failures and alert failures are recorded independently of task completion.
Failed delivery retries use backoff. Alert attempts are limited to three; tasks
and failed delivery records survive an exhausted retry budget. Desktop delivery
cannot guarantee exactly once after a crash.

## Authentication and macOS setup

Use the existing `gh` credentials. GitHub's notification API requires compatible
classic-token authentication with `notifications` or `repo` scope; fine-grained
PATs and GitHub App tokens are not supported by these endpoints. Team discovery
also needs organization membership visibility (`read:org` for OAuth tokens).
Successful `gh auth status` alone does not prove these capabilities. Permission
or schema failures are surfaced, and uncertain notifications are left unread.

Install a stable executable, configure attention, then explicitly run:

```sh
crv attention diagnose
crv attention sync
crv service install
crv service start
crv service status
```

`sync` performs real cleanup; `diagnose` does not. Installation captures absolute
executable/config/gh paths and a minimal launchd environment, including `GH_HOST`
and an explicit `GH_CONFIG_DIR` when present. It does not copy token environment
variables. Credentials must be accessible to gh in the logged-in launchd session.
The service owns only `com.crv.attention`; its log lives beside the database.
`start` refuses to run before installation, `stop` on an unloaded or uninstalled
service succeeds quietly, and `install` reports an existing plist instead of a
raw file-exists error.

macOS alerts use bounded `osascript` calls with data passed as arguments, never
interpolated script text. Notification Center permission, Focus settings and the
login session determine visibility. A successful script invocation is not proof
that an alert was displayed. This implementation does not support click-through
or OS-level stable notification IDs. Unattended delivery needs an operator smoke
test on the target Mac; ordinary tests neither install a service nor change real
notifications.

The classifier/store/sync pipeline and `service run` build on Linux and Windows.
Desktop and lifecycle adapters explicitly report unsupported platforms. Native
Linux service installation and WSL/Windows toast bridging are deferred.

## API contracts and validation

The checked-in `internal/ghsrc/testdata/attention/base.json` uses entirely synthetic
identities and content. It covers joined threads, individual dismissal history,
required checks and merge eligibility; tests derive direct/team requests, drafts,
CI failures, auto-merge and resolved-thread variants. Additional fakes cover
paginated/read notifications, changed notification versions, transaction failure,
account switches, concurrent Done, restart, scope removal and delivery failure.

Contracts consulted during implementation:

- [GitHub notifications REST API](https://docs.github.com/en/rest/activity/notifications): token support, read entries, pagination, polling hint and individual read operation.
- [GitHub teams REST API](https://docs.github.com/en/rest/teams/teams): authenticated team membership and organization visibility.
- [GitHub pull request GraphQL types](https://docs.github.com/en/graphql/reference/pulls): dismissal previous state, merge state, queue membership and required-context semantics.
- [GitHub check runs REST API](https://docs.github.com/en/rest/checks/runs): latest runs, terminal conclusions and pagination.
- [SQLite driver documentation](https://pkg.go.dev/modernc.org/sqlite): embedded, CGO-free operation and transaction locking.
- [Apple notification scripting guide](https://developer.apple.com/library/archive/documentation/LanguagesUtilities/Conceptual/MacAutomationScriptingGuide/DisplayNotifications.html): AppleScript notification delivery and permission behavior.

Live notification mutation and unattended desktop/login-session acceptance are
operator validation steps. No service is installed or started by building or
running the tests.
