# crv

Review code without leaving the terminal.

`crv` renders and navigates diffs, takes review notes on lines, shows what
your teammates already said, and submits the whole thing to GitHub as one
review — without leaving the terminal.

## Install

```sh
go install github.com/tobiasbernting/code-review-cli/cmd/crv@latest   # or @main
```

`@latest` is the newest tagged release; `@main` is the current main branch.
Re-run the same command to update. `crv --version` reports which one you have,
even for a `go install` build.

Or download a binary for macOS, Linux or Windows from the
[releases](https://github.com/tobiasbernting/code-review-cli/releases) page and
put it on your `PATH`. From a clone: `go build -o crv ./cmd/crv`.

## Use

```sh
crv                    # the pull requests waiting on your review
crv .                  # uncommitted work, including untracked files
crv main...feature     # a revision range
crv HEAD~3..HEAD
crv 42                 # pull request 42, via gh
crv . | less -R        # non-interactive: prints and exits
```

Local reviews need only git. The queue and pull requests need
[gh](https://cli.github.com), which already knows your host and credentials —
including an enterprise one.

### The queue

A bare `crv` lists what is waiting on you, across every repository, with CI
status, age, and how many unsent notes you already have on each. `enter` opens
one, `t` switches to your own pull requests, `r` refreshes.

The list is one GraphQL request and is cached for five minutes; a failed
refresh shows the cached list rather than an empty screen. Diffs are never
cached — reviewing a stale diff is the worst thing this tool could do.

Untracked files are shown as additions on purpose: when reviewing generated
code, the new files are usually the point of the change, and plain `git diff`
hides them.

### Keys

| key | action |
| --- | --- |
| `j` / `k`, arrows | move |
| `ctrl+d` / `ctrl+u` | half page |
| `tab` / `shift+tab` | next / previous file |
| `n` / `p` | next / previous hunk |
| `J` / `K`, `]` / `[` | next / previous file (aliases) |
| `g` / `G` | top / bottom |
| `h` / `l` | scroll horizontally, `0` to reset |
| `f` | file list |
| `?` | help |
| `q` | quit |

Reviewing:

| key | action |
| --- | --- |
| `c` | comment on this line |
| `v` | start a multi-line selection, then move and press `c` |
| `e` / `d` | edit / delete the note under the cursor |
| `ctrl+e` | finish a note in `$EDITOR` instead |
| `x` | mark this file reviewed |
| `S` | submit the review to GitHub |

### Flags

| flag | effect |
| --- | --- |
| `--theme <name>` | chroma syntax theme (default `catppuccin-mocha`) |
| `--no-color` | disable colour; `NO_COLOR` is honoured too |
| `--no-untracked` | exclude untracked files |
| `--width <n>` | output width when stdout is not a terminal |
| `--host <name>` | GitHub hostname; defaults to gh's own configuration |
| `--export markdown` | print this review's notes and exit |
| `--limit <n>` | how many pull requests the queue lists (default 30) |
| `--config` | print the resolved configuration and exit |
| `--init-config` | write a starter configuration file and exit |
| `--version` | print version and exit |

## Notes and reviews

Notes are stored outside the repository — under `~/.config/crv`, or
`$XDG_CONFIG_HOME/crv` if that is set — so they never pollute a worktree that
is shared or reset. They are keyed by pull
request number, or by branch for local work, so an agent rewriting files
underneath you does not orphan them.

Each note records the blob hash of the file it was written against. When the
file changes, the note is shown as **stale** and detached from its line rather
than pointing at a line that has since moved. The same applies to a file marked
reviewed: it keeps its tick and gains a `~`, because silently unticking would
hide that you had already read it.

Nothing is sent anywhere until you press `S`. GitHub reviews are atomic, so
every note is posted as a single review with one event — comment, approve, or
request changes — rather than as a stream of separate comments. Once submitted,
the local copies are dropped: GitHub owns them from then on, which is what stops
two versions of the same review from disagreeing.

For a local review with no pull request to post to, `crv --export markdown`
prints the notes for pasting wherever they need to go.

## Configuration

Optional — crv works with none. To start from a documented file:

```sh
crv --init-config          # writes ~/.config/crv/config.toml
```

Every setting in it is commented out, so nothing is overridden until you
uncomment it. That is deliberate: a starter file listing real values would pin
today's defaults forever, and a later change to one would never reach you.
[`config.example.toml`](config.example.toml) is the same file, for reading here.

Settings are resolved from, highest priority first: command-line flags, `CRV_*`
environment variables, `.crv.toml` in the repository, and
`~/.config/crv/config.toml`. `crv --config` prints what won and whether each
file exists.

On Windows the directory is `%AppData%\crv`, where that convention applies
instead.

```toml
# .crv.toml — checked in, or not, as you prefer
host = "github.example.com"   # default: whatever gh is configured with
theme = "catppuccin-mocha"
editor = "hx"
untracked = true
color = true
width = 120
```

`host` is empty by default on purpose: gh already knows whether you are on
github.com or an enterprise host, and a repository-local file is a better place
to override that than global state you forget you set.

## Layout

| package | role |
| --- | --- |
| `internal/diffparse` | unified diff → structs; hunk bodies parsed lazily |
| `internal/gitsrc` | shells out to `git` for diffs and stats |
| `internal/render` | diffs → styled rows; syntax and word-level highlighting |
| `internal/tui` | bubbletea viewport over those rows |
| `internal/notes` | review notes and per-file marks on disk |
| `internal/ghsrc` | pull requests, comments and review submission, via `gh` |
| `internal/config` | settings resolution |

`render` has no dependency on the TUI, which is what lets the same rows serve
the interactive view, the piped output, and the golden-file tests.

## Tests

Run them before pushing — CI only checks that the project builds on all three
platforms, it does not run the suite.

```sh
go test ./...
go test ./internal/render -update   # rewrite golden files
```

## Releasing

Merging to `main` is the whole process. release-please gathers merged pull
requests into a release pull request with a generated changelog. Merging that
pull request makes the workflow tag the version, and the tag makes GoReleaser
publish the release with binaries for macOS, Linux and Windows.

The tag is the handover point on purpose: immutable releases are enabled on
this repository, so a release cannot gain assets after it is published.
Whoever creates it must create it complete, which has to be GoReleaser.

If release-please's bookkeeping ever gets stuck, pushing a `v*` tag by hand
builds and publishes that tag directly.

Pull request titles must be conventional commits (`feat:`, `fix:`, `feat!:`)
— the squash-merge title becomes the commit message release-please reads, and
`pr-title.yml` rejects anything else before merge.

One secret is required: `RELEASE_PLEASE_TOKEN`, a fine-grained personal access
token for this repository with **Contents: read and write** and **Pull
requests: read and write**. GitHub deliberately does not trigger workflows for
anything pushed with the built-in `GITHUB_TOKEN`, so a release pull request it
opened would carry no check runs and could never satisfy the branch ruleset.

To check a change to the release setup without tagging:

```sh
goreleaser check
goreleaser build --snapshot --clean
```

## Planned

- Mouse support and OSC 52 yank
- Replying to a teammate's comment thread
- `LEFT`-side comments on deleted lines
