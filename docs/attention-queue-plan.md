# Attention queue and GitHub notification cleanup

Status: implemented on `feat/attention-queue`; live macOS/login-session and scoped
GitHub mutation smoke tests remain operator validation. See [implementation and
usage notes](attention-queue.md).
Date: 2026-09-08. Repository inspected at commit `974607d`.

## Objective

Extend `crv` into the user's trusted place to see GitHub work that needs them.
A background worker discovers actionable PR activity, saves tasks locally,
clears handled GitHub notifications, and sends desktop alerts. An empty queue
means no known outstanding work, provided synchronization is healthy. Reading
a notification is independent of completing its task.

This document captures the completed interview. Do not restart that interview.
The current request authorizes writing this plan only. A future request to
implement it should proceed through the milestones below. Actual repository
names must come from the user's configuration; do not guess them or install a
running service as a side effect of building or testing.

## Agreed product decisions

- Extend this repository, rather than introduce a separate product.
- Manage PR notifications only, within an explicit configurable repository list.
  Issues and notifications outside that list are untouched.
- Direct and team review requests have equal priority and both produce alerts.
  A team task remains while GitHub requests that team's review; another person's
  review alone does not complete it. A direct request is independent.
- After approval, a push alone does not require another review. An explicit
  re-review request or invalidation of the user's approval does.
- Direct mentions and replies in unresolved review threads the user participated
  in create conversation tasks. Participation includes replying, not just
  starting the thread. General unrelated discussion is noise.
- Include the user's authored PRs: review feedback, failed CI on the latest
  commit of non-draft PRs, and readiness to merge.
- CI tasks clear when checks pass or a newer commit supersedes the failure.
- Ready-to-merge tasks require approval, passing required checks, and the user
  being the author responsible for merging. Auto-merge and merge-queue PRs do
  not need an additional merge task.
- Conversation tasks require explicit local Done. Opening, reading, or replying
  does not implicitly complete them. Later relevant activity reopens work.
- Persist actionable work before automatically marking its GitHub notification
  read. Automatically mark confidently non-actionable notifications read too.
- Reading a notification in GitHub never silently completes a local task.
- Keep browsable cleanup history explaining what was cleared and why.
- Use an embedded SQLite database for local persistence. The user explicitly
  approved bundling SQLite; no separate database server is needed.
- Uncertain classification becomes Needs triage; leave its notification unread
  until the user decides. Failed fetches must never be classified as noise.
- Alert when new work needs attention, explaining the reason. Group activity by
  PR and suppress repeated alerts for unchanged work.
- Run continuously in the background, with launch/refresh synchronization too.
- No bespoke initial-backlog preview or onboarding workflow: ordinary processing
  handles whatever exists on first run. The user already keeps GitHub clean.
- macOS is the first supported service/notification integration. Keep the core
  portable for Linux/WSL; WSL installation and native Windows toast bridging are
  deferred, not first-release acceptance requirements.

## Existing implementation and integration points

| Location | Current behavior and intended use |
| --- | --- |
| `cmd/crv/main.go` | Standard-library flag dispatch; bare invocation opens queue. Add command routing without breaking local diff/range/PR inputs. |
| `internal/ghsrc/gh.go` | `gh` subprocess client, host/repo routing, auth preflight and test seam. Reuse credentials; add context cancellation/timeouts and structured response handling where needed. |
| `internal/ghsrc/queue.go` | Search for `review-requested:@me` or `author:@me`; defaults to 30 results, five-minute JSON cache. This cache cannot serve as durable task storage or complete discovery. |
| `internal/ghsrc/history.go` | Submitted reviews, latest-review selection, replies and thread resolution. Reuse data access, but do not equate latest review with effective approval. |
| `internal/ghsrc/threads.go` | Review threads and snapshots. Extend pagination and evidence as necessary. |
| `internal/followup/session.go` | Own-started threads and changes since latest review. Add navigation for attention involving threads merely joined, without silently changing the existing own-thread view. |
| `internal/tui/queue.go` | Queue directly calls `CachedQueue`; selection exits queue into review viewer. Introduce an attention source and retain authored browsing/draft counts. |
| `internal/tui/followup.go`, `sync.go`, `submit.go` | Existing review workflow; integrate task refresh after actions without making opening/submitting complete unrelated reasons. |
| `internal/config/config.go`, `dir.go`, `template.go` | TOML, strict unknown-key checks, flags/env/repo/user precedence; storage under `config.Dir()`. Nested attention settings need deliberate parser support. |
| `config.example.toml`, `README.md`, `docs/comments-and-sync.md` | Update configuration, queue behavior, service usage and read/done semantics during implementation. |

Repository currently uses Go and Bubble Tea. Existing tests use a client runner
override. Preserve local review behavior without GitHub or a running worker.
No applicable `AGENTS.md` was found during this inspection; check again when
starting implementation. Inspect the current tree and changes before editing.

## Proposed implementation defaults

These are engineering recommendations, not additional user requirements. Adjust
them when evidence warrants it, preserving the decisions above.

### Configuration and commands

Add a user-level section such as:

```toml
[attention]
enabled = true
repositories = ["owner/repository", "owner/another-repository"]
poll_interval = "60s"
desktop_notifications = true
```

The example repository names are placeholders. Default the feature to disabled
for existing users; enabling it requires a nonempty validated allowlist. Retain
the old queue when disabled. Scope the first worker to one configured host and
authenticated account; key all data by both. Use existing host configuration,
but resolve it explicitly at startup, independently of the current directory.

Attention configuration must come from the user config/explicit worker options,
not an arbitrary checkout's `.crv.toml`: worker scope cannot change with cwd.
Document this exception to normal per-repository UI configuration precedence.
Handle nested TOML tables and strict validation, and keep generated template and
example tests consistent. Treat the UI limit as presentation only, never a
discovery limit. Pause side effects on unexpected authenticated-account changes.

Suggested command surface:

- `crv`: attention queue when configured; existing queue otherwise.
- `crv attention sync`: one synchronization cycle, usable without installation.
- `crv attention history`: inspect cleanup decisions and failures.
- `crv service run`: foreground worker, portable and suitable for debugging.
- `crv service install|uninstall|start|stop|status`: macOS lifecycle adapters.

Keep exact names flexible if existing CLI constraints suggest better ones.
Do not let subcommands be mistaken for git revision ranges. Service status must
show identity, scope, last successful sync, stale/error state and alert support.

### Module responsibilities

- `internal/attention`: normalized evidence, deterministic classification,
  task lifecycle, reconciliation and public operations for UI/worker.
- `internal/attentionstore` (or a private store within attention): transactional
  persistence, acknowledgements, sync checkpoints, cleanup/alert outbox and history.
- Extend `internal/ghsrc`: discovery, notification operations and PR evidence.
  Keep GitHub response types and subprocess details out of the classifier.
- `internal/service`: scheduling and platform-specific lifecycle support.
- `internal/desktop`: small notification adapter; macOS implementation and an
  explicit unsupported/no-op result on other platforms.

Use a pure classification seam with a supplied clock and fixture evidence. Keep
interfaces limited to real boundaries: GitHub, store, clock and alert delivery.
Avoid a generic plugin framework or a second GitHub authentication stack.

### Persistent state and concurrency

Use SQLite transactions with an embedded, portable driver after checking current
Go, release and cross-compilation compatibility. Record the driver dependency
choice in the implementation PR. SQLite is an agreed choice, not an outstanding
permission question. Create/manage the database locally; do not commit a runtime
database or require a separately installed database server. The existing JSON
queue cache is not durable task storage.

Minimum durable concepts:

- Account identity: host plus stable viewer ID/login.
- PR identity: host, repository identity/name and PR number.
- Task reason: kind, source identifier, activity generation, evidence timestamp,
  active/done state, completion source and target URL/thread/check details.
- Observed evidence and per-source cursors; completeness and last successful fetch.
- Conversation acknowledgements tied to event IDs/versions, not PR `updatedAt`.
- Notification thread ID, observed version/time, classification and read status.
- Outbox entries for pending read operations and grouped desktop alerts.
- Cleanup history with decision explanation, attempt/result and timestamps.

Render one PR row with multiple reasons. Completing a conversation reason must
not clear review, CI or merge work on the same row. Persist Done for the exact
observed generation so concurrently arriving activity remains active.

Use short transactions, schema migrations and an explicit busy/lock policy.
Run only one reconciler per account/store at a time across worker and foreground
sync. UI reads and Done writes may run concurrently. Do not hold a database
transaction across network calls. Refresh must not start competing reconcilers.
Store permissions should match existing private local state; never store tokens.

## Classification and lifecycle specification

| Reason | Creates/reopens work | Completes or removes work |
| --- | --- | --- |
| Direct review | Outstanding request to viewer, including a new request after review | Request fulfilled/withdrawn or PR closed/merged |
| Team review | Outstanding request to a team containing viewer | That team's request fulfilled/withdrawn or PR closed/merged; preserve other requests |
| Approval invalidated | Evidence that viewer's prior effective approval was dismissed/invalidated | Subsequent qualifying review or PR closed/merged |
| Mention | New direct mention addressed to viewer in PR body/comment/review content | Explicit Done for observed activity; later relevant activity reopens |
| Thread reply | Another actor adds relevant activity in an unresolved review thread viewer started or joined | Explicit Done; later relevant activity reopens |
| Author feedback | Actionable submitted review feedback/comments on viewer's PR | Explicit Done for observed feedback; later feedback reopens |
| CI failed | Terminal failure/error on latest head of viewer's non-draft PR | Same check reruns successfully, newer head supersedes it, draft conversion or PR closes/merges |
| Ready to merge | Viewer's non-draft PR is approved and currently eligible to merge with required checks satisfied | Eligibility lost, auto-merge/queue enabled, or PR closes/merges |
| Needs triage | Evidence incomplete or ambiguous for an in-scope notification | User retains as a task or explicitly dismisses; alternatively later complete evidence classifies it |

Details to encode explicitly:

- Do not use a PR's aggregate `reviewDecision` as proof the viewer's approval
  was invalidated. Examine viewer review state/history and relevant events.
- Team membership/request semantics require API verification; the existing
  `review-requested:@me` query is not sufficient evidence of complete team coverage.
- Use review threads for unresolved-state checks. Top-level PR comments use the
  issues-comments API despite this feature excluding GitHub Issues themselves.
- Ignore the viewer's own comments as new incoming work. A plain approval with
  no actionable feedback should not create an author conversation task; it may
  instead make the PR ready to merge. Ambiguous feedback stays in triage.
- Match actual mention targets, avoiding login substrings and quoted/code-only
  false positives; use API evidence when available. Do not infer all current
  activity from the notification's subscription `reason`.
- Track relevant comment edits as new generations when content affecting the
  task changes; metadata-only changes must not reopen completed work.
- A resolved thread does not create a reply task merely because it is inspected.
  Proposed default: existing conversation tasks remain until explicit Done,
  even after resolution or PR closure; label them closed/resolved for context.
- Treat failed checks individually and support both checks and commit statuses.
  Define mappings for timed-out/action-required/cancelled/skipped/neutral from
  verified API semantics; do not call every non-success result a failure.
- Rerun-in-progress is not passing. Proposed default: retain a prior failure
  task labelled rerunning until success, supersession or terminal new result.
- Merge readiness needs authoritative eligibility and required-check evidence,
  not just a green aggregate badge. Unknown/computing state cannot create a
  ready-to-merge task. This feature never merges PRs automatically.
- A new reason on an already-active PR can alert once; unrelated pushes and
  timestamps do not. Reopening a completed reason with new evidence can alert.

## Discovery, reconciliation and notification cleanup

Notifications are an input, not the authoritative task ledger. Periodically
discover requested reviews (direct and team), authored PRs, tracked PRs and
relevant recent activity even when notifications have already been read.
Use paginated notification reads including read entries over overlapping windows
where supported, plus PR evidence reconciliation. Never restrict discovery to
only unread entries. Document any server retention limits rather than claiming
unbounded recovery after long downtime.

Normal cycle:

1. Resolve pinned account and allowlist; acquire reconciler lease/lock.
2. Discover candidates and notifications with complete pagination. Apply scope
   before fetching unrelated content or scheduling mutations.
3. Fetch evidence needed for classification. Reconcile previously tracked tasks
   even when they disappear from open-PR search, to distinguish closure from
   partial search results or permission failures.
4. Classify against saved evidence and acknowledgements. On missing/ambiguous
   evidence preserve tasks and create/update triage as needed.
5. Atomically save task changes, evidence, checkpoints for completed sources,
   cleanup decisions and outgoing operations. A failed transaction means no
   corresponding remote read operation or desktop alert can run.
6. Drain read outbox only for confidently classified, in-scope PR notification
   threads. Retain uncertain notifications unread. Record successful and failed
   attempts; retry transient failures with backoff.
7. Deliver grouped new-work alerts from durable state. Record delivery results;
   failures must not lose tasks or make sync report a healthy alert channel.
8. Publish health/last-sync status; release lock. Poll with cancellation, bounded
   requests, rate-limit awareness, jitter and exponential backoff on failures.

Use the individual notification-thread read operation. Never use global or
repository-wide mark-all-read: those could clear issues, uncertain items or
other notifications outside the classified set. Do not unsubscribe, delete,
mark Done on GitHub or modify other people's notification state.

The per-thread read endpoint does not expose a documented event-version
conditional write. Re-fetch/check for newer activity before marking read, then
schedule an overlapping post-write reconciliation. A new event can still arrive
between these calls: independent activity discovery is what prevents this race
from losing a task. Do not promise atomic event-level GitHub read semantics.
An uncertain newer event may briefly be marked read in this unavoidable race;
surface it locally as triage and document the limitation honestly.

Do not advance a failed source's cursor, infer completion from an incomplete
page, or fall back to an empty successful snapshot. Use overlap and stable IDs
for deduplication. Search limits must trigger partitioning/fallback, not silent
truncation. Capture GraphQL errors even when HTTP status is successful.

Outbox delivery should tolerate a crash after the remote read succeeded but
before local acknowledgement. Desktop delivery cannot generally guarantee
exactly-once semantics; prefer stable notification identifiers where supported,
bounded retries and no repeated alert on every poll.

## TUI behavior

- Default attention view shows PR, reason(s), age, draft/check context and local
  draft count. Preserve authored browsing and existing review navigation.
- Opening selects the relevant thread/diff when possible. For unsupported
  content such as a top-level mention, show content and a direct GitHub link;
  never pretend an own-thread filter has shown a thread it excludes.
- Add explicit Done for conversation/triage items with clear scope. Triage
  supports Keep as task or Dismiss; save that decision before clearing GitHub.
- Keep outstanding review/CI/merge work controlled by current evidence. Done
  must not provide an accidental blanket acknowledgement of every PR reason.
- Display worker health and snapshot age. Cached work remains usable offline,
  but zero tasks plus failed sync must not claim that everything is clear.
- Refresh requests shared synchronization; opening `crv` works without a daemon
  using the same reconciler. Surface cleanup failures separately from task state.
- Provide browsable history of automatic clearing and its reason, with PR links.
  Provide a local way to retain/reopen an item if classification was wrong;
  do not promise restoration of GitHub unread state.

## macOS service and portability

Use a per-user LaunchAgent and absolute executable/config paths. Installation
must account for launchd's environment differing from an interactive shell,
including locating `gh` and access to its existing credentials. Avoid shell
interpolation of repository names, PR titles, paths or notification content.
Generate valid plist data with proper escaping. Install/start/stop/uninstall
operate only on crv's own service; uninstall preserves tasks, config and history.

Choose and verify a macOS desktop mechanism in a small prototype: notification
permission behavior, terminal versus LaunchAgent execution, Unicode/quotes,
grouping and deduplication. Prefer a bounded OS adapter over a new UI application.
Do not assume an alert API that works interactively works unattended. Clicking
an alert may open the PR URL; launching a terminal at a thread is optional.

`service run` and the entire classifier/store/sync pipeline must compile and
run on Linux independently of launchd or AppleScript. Unsupported lifecycle and
desktop commands should report that limitation clearly. Defer Linux service
installation and Windows toast bridging; WSL can later reuse the foreground
worker and receive dedicated adapters. Keep existing Windows build support.

## Delivery milestones

### 1. Verify API contracts and establish fixtures

Read current code/tests and current primary API documentation. Verify team
discovery, individual approval dismissal, required checks/merge eligibility,
notification auth support, pagination and per-thread read races. Use read-only
account checks only when needed; never clear real notifications as a probe.
Create representative redacted fixtures and record limitations. Prototype the
macOS alert adapter without installing the background service.

Exit: concrete evidence structures, supported capabilities and fixture cases;
no guessed API field names or unsupported credential assumptions.

### 2. Persistent task engine and configuration

Implement validated user-level attention settings, durable store/migrations,
pure lifecycle/classification logic and per-generation Done. Include outbox and
concurrency design now so remote cleanup cannot precede task persistence later.

Exit: deterministic tests cover the decision table, multiple reasons, restart,
concurrent completion/new activity and failed transactions. Existing config
templates and defaults remain consistent.

### 3. GitHub discovery and one-shot sync

Add paginated evidence gathering and account/scope validation. Wire classification
and persistence through `attention sync`. Implement per-thread cleanup with
durable outbox retries and audit history. Provide a non-mutating diagnostic mode
for fixture/live comparison, without a special first-run onboarding flow.

Exit: integration tests prove scope isolation, saved-before-read ordering,
external-read independence, race recovery and no completion on partial failures.

### 4. Attention UI

Connect bare `crv` when enabled; retain legacy disabled behavior. Add reasons,
Done, triage, history, health and navigation to relevant content. Refresh after
review actions through shared reconciliation, preserving drafts and existing
follow-up behavior.

Exit: queue tests verify reading versus Done, independent reasons, stale state,
authored browsing and late asynchronous refreshes. Existing review tests pass.

### 5. Worker, alerts and macOS lifecycle

Build cancellation-aware scheduler around the proven one-shot pipeline, then
desktop grouping/deduplication and LaunchAgent lifecycle commands. Ensure no
duplicate worker or competing foreground reconciler can run for the same store.

Exit: fake-clock/outbox tests pass; a deliberately configured local smoke test
demonstrates login/background execution, stop/start and durable recovery.
Installation is an explicit operator action, not part of ordinary tests.

### 6. Documentation and release readiness

Update README, example/generated configuration and comments/sync documentation.
Explain repository scope, task ownership, notification cleanup, health states,
credential requirements, macOS setup, uninstall and WSL limits. Remove obsolete
claims that nothing is sent until review submission: the enabled attention
worker changes notification read state automatically, while code-review writes
still require the existing explicit actions.

Exit: acceptance scenarios below pass, applicable existing suite passes, and
cross-platform builds remain valid. Summarize any API/OS limitations in handoff.

## Acceptance scenarios and validation

Use fixtures/fakes for GitHub writes and OS lifecycle tests. Meaningful scenarios:

1. New personal or team request creates one PR task and one grouped alert; its
   notification clears only after persistence. Restart/poll does not duplicate it.
2. Approve, then ordinary push: no review task or alert; notification clears.
   Explicit re-request or actual viewer approval invalidation creates work.
3. Teammate review leaves a still-outstanding team request active. Removing that
   request completes only that reason, preserving any direct request.
4. Unrelated comment clears silently; direct mention or reply in an unresolved
   joined thread creates work. Resolved-thread noise does not create reply work.
5. Open a task or read it in GitHub: task remains. Done suppresses the same
   generation; a later reply reopens it, including a reply arriving during Done.
6. Authored PR feedback creates conversation work. Latest-head CI failure adds
   another reason. Passing checks/new head clears only CI; drafts do not create CI.
7. Approved, eligible authored PR creates merge work; auto-merge, merge queue,
   blocked checks or unknown eligibility prevent it. Never perform a merge.
8. Out-of-scope PRs and actual Issues never get read mutations. Ambiguous in-scope
   activity remains unread and visible in triage until a decision is persisted.
9. Network/auth/GraphQL partial error preserves prior work and marks sync stale.
   Account switch cannot reuse another account's tasks or mutation outbox.
10. Crash before transaction commit performs no read; crash after read retries
    harmlessly. Concurrent notification update is recovered by overlapping scans.
11. More than one page/30 PRs, read notifications, same-time events, deleted
    comments and lost permissions do not silently drop work or reset cursors.
12. Foreground refresh, daemon sync and UI Done coexist without duplicate reads,
    lost acknowledgement or corrupted state. Sleep/wake catches up normally.
13. Desktop delivery failure preserves tasks and reports degraded status; PR
    content cannot inject shell commands. No recurring unchanged-work alerts.
14. Removing a repository from config stops new cleanup/alerts for it, cancels
    pending outbox side effects and retains historical data outside active view.
15. Disabled/unconfigured attention preserves current CLI/local review behavior.
    Uninstall stops service and retains all user state.

Run targeted package tests during development, then `go test ./...` and
appropriate race tests for store/sync concurrency. Build the CLI for current
release target platforms; follow the actual GoReleaser matrix and avoid replacing
the checked-in/local `crv` binary during verification. Do not regenerate rendering
goldens unless rendering changes require it. Real notification cleanup tests need
an explicitly scoped test target or the user's configured operational rollout.

## External references and remaining technical checks

Consult current docs again at implementation time; these are reference links,
not guarantees that the user's credentials/host support every field.

- [GitHub notification REST API](https://docs.github.com/en/rest/activity/notifications):
  list/pagination/read endpoints and token support. The per-thread read operation
  is equivalent to reading a notification, not completing a local task. Current
  documentation excludes fine-grained PATs and GitHub App tokens for this operation;
  do not assume `gh auth status` alone proves cleanup capability.
- [GitHub GraphQL pull-request types](https://docs.github.com/en/graphql/reference/pulls):
  verify review requests, reviews, merge state and related evidence against the
  active schema, including pagination and null/unknown states.
- [GitHub PR dashboard and search additions](https://github.blog/changelog/2026-07-09-new-pull-requests-dashboard-is-now-generally-available/):
  documents team-review-requested-user and review-involves filters. Verify API
  search behavior before relying on these for worker discovery.

Technical uncertainties should be resolved by documentation, fixtures and bounded
prototypes. Escalate only a discovered limitation that requires changing an agreed
product decision; do not ask the user to repeat already settled preferences.
