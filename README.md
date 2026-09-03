# crv

Review code without leaving the terminal.

**Status: step 1 of the plan.** `crv` renders and navigates diffs. Notes,
GitHub PR review and comment posting are not built yet.

## Install

```sh
go install github.com/tobiasbernting/code-review-cli/cmd/crv@latest
```

Or download a binary for macOS, Linux or Windows from the
[releases](https://github.com/tobiasbernting/code-review-cli/releases) page and
put it on your `PATH`. From a clone: `go build -o crv ./cmd/crv`.

## Use

```sh
crv .                  # uncommitted work, including untracked files
crv main...feature     # a revision range
crv HEAD~3..HEAD
crv . | less -R        # non-interactive: prints and exits
```

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

### Flags

| flag | effect |
| --- | --- |
| `--theme <name>` | chroma syntax theme (default `catppuccin-mocha`) |
| `--no-color` | disable colour; `NO_COLOR` is honoured too |
| `--no-untracked` | exclude untracked files |
| `--width <n>` | output width when stdout is not a terminal |
| `--version` | print version and exit |

## Layout

| package | role |
| --- | --- |
| `internal/diffparse` | unified diff → structs; hunk bodies parsed lazily |
| `internal/gitsrc` | shells out to `git` for diffs and stats |
| `internal/render` | diffs → styled rows; syntax and word-level highlighting |
| `internal/tui` | bubbletea viewport over those rows |

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
requests into a release pull request with a generated changelog; merging that
tags the version, and the same workflow run cross-compiles for macOS, Linux and
Windows and attaches the archives and checksums to the release.

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

2. Local review notes, `--format=markdown` export
3. `crv <pr>` — PR diffs via `gh`, teammates' comments rendered inline
4. Submit a review (pending drafts → one GitHub review)
5. `crv` with no argument — your review queue
6. Mouse and OSC 52 yank
