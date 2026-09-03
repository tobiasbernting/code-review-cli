# crv

Review code without leaving the terminal.

**Status: step 1 of the plan.** `crv` renders and navigates diffs. Notes,
GitHub PR review and comment posting are not built yet.

## Install

```sh
go build -o crv ./cmd/crv
```

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
| `n` / `p` | next / previous hunk |
| `]` / `[` | next / previous file |
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

## Planned

2. Local review notes, `--format=markdown` export
3. `crv <pr>` — PR diffs via `gh`, teammates' comments rendered inline
4. Submit a review (pending drafts → one GitHub review)
5. `crv` with no argument — your review queue
6. Mouse, OSC 52 yank, GoReleaser releases and a Homebrew tap
