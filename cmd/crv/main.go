// Command crv reviews diffs in the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/gitsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
	"github.com/tobiasbernting/code-review-cli/internal/tui"
)

// Build metadata, injected by GoReleaser via -ldflags. The defaults are what
// a plain `go build` produces.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `crv — review code in the terminal

usage:
  crv .              review uncommitted work (including untracked files)
  crv <range>        review a range, e.g. main...feature or HEAD~3..HEAD
  crv <number>       review a pull request, e.g. crv 42
  crv --help
  crv --version

flags:
  --host <name>      GitHub hostname (default: whatever gh is configured with)
  --theme <name>     chroma syntax theme
  --no-color         disable colour (also honours NO_COLOR)
  --no-untracked     exclude untracked files from the working-tree diff
  --width <n>        output width when not attached to a terminal
  --export markdown  print the saved notes for this review and exit
  --config           print the resolved configuration and exit

notes are stored outside the repository, keyed by pull request number or by
branch, and are never sent anywhere until you submit them with S.
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crv: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		host        = flag.String("host", "", "GitHub hostname")
		theme       = flag.String("theme", "", "chroma syntax theme")
		noColor     = flag.Bool("no-color", false, "disable colour")
		noUntracked = flag.Bool("no-untracked", false, "exclude untracked files")
		widthFlag   = flag.Int("width", 0, "output width when not a terminal")
		export      = flag.String("export", "", "print saved notes: markdown")
		showConfig  = flag.Bool("config", false, "print the resolved configuration")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	if *showVersion {
		fmt.Printf("crv %s (%s, built %s)\n", version, commit, date)
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := gitsrc.Open(cwd)
	if err != nil {
		return err
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return err
	}
	// Flags are applied last: only here is it known which were actually set.
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			cfg.Host = *host
		case "theme":
			cfg.Theme = *theme
		case "no-color":
			cfg.Color = !*noColor
		case "no-untracked":
			cfg.Untracked = !*noUntracked
		case "width":
			cfg.Width = *widthFlag
		}
	})

	if *showConfig {
		return printConfig(cfg, repo.Root)
	}

	target := "."
	if flag.NArg() > 0 {
		target = flag.Arg(0)
	}

	src, files, err := resolve(repo, cfg, target)
	if err != nil {
		return err
	}

	branch, _ := repo.Branch()
	review, err := notes.Load(src.Scope(repo.Root, branch))
	if err != nil {
		return err
	}

	if *export != "" {
		return printExport(*export, review)
	}
	if len(files) == 0 {
		fmt.Println("no changes")
		return nil
	}

	var comments []ghsrc.Comment
	if src.Kind == tui.SourcePR {
		// A failure here must not block the review: the diff is the point,
		// and teammates' comments are additional context.
		comments, err = src.Client.Comments(src.Repo, src.PRNumber)
		if err != nil {
			fmt.Fprintln(os.Stderr, "crv: could not load existing comments: "+err.Error())
		}
	}

	th := render.DefaultTheme()
	th.Syntax = cfg.Theme

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return printPlain(files, th, cfg, tui.Overlay(review, comments, tui.Blobs(files)))
	}
	prog := tea.NewProgram(tui.New(tui.Options{
		Files:    files,
		Theme:    th,
		Config:   cfg,
		Source:   src,
		Review:   review,
		Comments: comments,
	}), tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

var prNumber = regexp.MustCompile(`^#?(\d+)$`)

// resolve turns the command-line target into a source and its diff. A bare
// number is a pull request, "." is the working tree, anything else is handed
// to git as a revision range.
func resolve(repo *gitsrc.Repo, cfg config.Config, target string) (tui.Source, []*diffparse.FileDiff, error) {
	if m := prNumber.FindStringSubmatch(target); m != nil {
		n, _ := strconv.Atoi(m[1])
		return resolvePR(repo, cfg, n)
	}

	if target == "" || target == "." {
		files, err := repo.WorkingTree(cfg.Untracked)
		return tui.Source{Kind: tui.SourceLocal, Title: "working tree"}, files, err
	}
	files, err := repo.Range(target)
	return tui.Source{Kind: tui.SourceLocal, Title: target}, files, err
}

func resolvePR(repo *gitsrc.Repo, cfg config.Config, number int) (tui.Source, []*diffparse.FileDiff, error) {
	client := ghsrc.Client{Host: cfg.Host, Dir: repo.Root}
	if err := client.Preflight(); err != nil {
		if errors.Is(err, ghsrc.ErrNotInstalled) {
			return tui.Source{}, nil, fmt.Errorf("%w\n\nlocal reviews (crv . and crv <range>) work without it", err)
		}
		return tui.Source{}, nil, err
	}

	name, err := client.Repo()
	if err != nil {
		return tui.Source{}, nil, err
	}
	pr, err := client.PR(number)
	if err != nil {
		return tui.Source{}, nil, err
	}
	raw, err := client.Diff(number)
	if err != nil {
		return tui.Source{}, nil, err
	}

	files := diffparse.Parse(raw)
	// gh pr diff has no --numstat companion, so the counts come from the
	// hunks instead.
	diffparse.FillStats(files)

	src := tui.Source{
		Kind:     tui.SourcePR,
		Title:    fmt.Sprintf("%s#%d %s", name, pr.Number, pr.Title),
		Repo:     name,
		PRNumber: pr.Number,
		Client:   client,
	}
	return src, files, nil
}

func printExport(format string, review *notes.Review) error {
	if format != "markdown" && format != "md" {
		return fmt.Errorf("unknown export format %q — only markdown is supported", format)
	}
	md := review.Markdown()
	if md == "" {
		fmt.Fprintln(os.Stderr, "crv: no notes for this review")
		return nil
	}
	_, err := os.Stdout.WriteString(md)
	return err
}

func printConfig(cfg config.Config, repoRoot string) error {
	userPath, _ := config.UserPath()
	fmt.Printf("host       %s\n", orDefault(cfg.Host, "(gh's own configuration)"))
	fmt.Printf("theme      %s\n", cfg.Theme)
	fmt.Printf("editor     %s\n", cfg.EditorCommand())
	fmt.Printf("untracked  %t\n", cfg.Untracked)
	fmt.Printf("color      %t\n", cfg.Color)
	fmt.Printf("width      %d\n", cfg.Width)
	fmt.Printf("\nuser file  %s\n", userPath)
	fmt.Printf("repo file  %s\n", repoRoot+"/"+config.RepoFile)
	if s := cfg.Sources(); len(s) > 0 {
		fmt.Printf("loaded     %s\n", strings.Join(s, ", "))
	} else {
		fmt.Printf("loaded     (none — all defaults)\n")
	}
	dir, _ := notes.Dir()
	fmt.Printf("notes      %s\n", dir)
	return nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// printPlain is the non-TTY path: same rows, printed once and exited, so
// `crv . | less` and `crv . > review.txt` work.
func printPlain(files []*diffparse.FileDiff, th render.Theme, cfg config.Config, ov render.Overlay) error {
	width := cfg.Width
	if width <= 0 {
		width = 120
	}
	doc := render.Build(files, render.NewHighlighter(th.Syntax, cfg.Color), ov)
	r := render.NewRenderer(th, doc)

	var b strings.Builder
	for _, row := range doc.Rows {
		b.WriteString(r.Render(row, width, 0, false))
		b.WriteString("\n")
	}
	_, err := os.Stdout.WriteString(b.String())
	return err
}
