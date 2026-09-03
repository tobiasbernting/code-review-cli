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

```sh
go test ./...
go test ./internal/render -update   # rewrite golden files
```

## Releasing

Tagging is the whole process — `.github/workflows/release.yml` runs GoReleaser,
which cross-compiles for macOS, Linux and Windows and attaches the archives
and checksums to the GitHub release.

```sh
git tag -a v0.1.0 -m v0.1.0 && git push origin v0.1.0
```

No secrets to configure — the workflow's built-in `GITHUB_TOKEN` is enough to
create the release.

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
