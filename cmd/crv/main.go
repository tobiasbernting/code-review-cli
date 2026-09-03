// Command crv reviews diffs in the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

// usage is built at call time so it can name the actual configuration paths
// on this machine rather than describing where they might be.
func usage() string {
	userPath, err := config.UserPath()
	if err != nil {
		userPath = "(could not determine your config directory)"
	}
	notesDir, err := notes.Dir()
	if err != nil {
		notesDir = "(could not determine your config directory)"
	}

	return fmt.Sprintf(`crv — review code in the terminal

usage:
  crv                the pull requests waiting on your review
  crv .              review uncommitted work (including untracked files)
  crv <range>        review a range, e.g. main...feature or HEAD~3..HEAD
  crv <number>       review a pull request, e.g. crv 42

flags:
  --host <name>      GitHub hostname (default: whatever gh is configured with)
  --theme <name>     chroma syntax theme
  --no-color         disable colour (also honours NO_COLOR)
  --no-untracked     exclude untracked files from the working-tree diff
  --width <n>        output width when not attached to a terminal
  --limit <n>        how many pull requests the queue lists (default 30)
  --export markdown  print the saved notes for this review and exit
  --config           print the resolved configuration and exit
  --version          print version and exit

configuration:
  Entirely optional — crv works with no configuration at all. Settings are
  read from the following, and the first one that mentions a setting wins:

    1. the flags above
    2. environment: CRV_HOST, CRV_THEME, CRV_EDITOR, CRV_WIDTH,
       CRV_UNTRACKED, CRV_COLOR, NO_COLOR
    3. %s in the repository being reviewed
    4. %s

  To create the user-level file (the quotes matter — the path contains a
  space on macOS):

    mkdir -p %s
    $EDITOR %s

  Both files are TOML and every key is optional:

    host = "github.example.com"   # default: whatever gh is configured with
    theme = "catppuccin-mocha"    # any chroma style name
    editor = "hx"                 # default: $VISUAL, then $EDITOR, then vi
    untracked = true              # include untracked files in crv .
    color = true
    width = 120                   # used when output is piped

  Set host in a repository's %s to review on an enterprise host
  without changing anything globally.

  `+"`crv --config`"+` prints which settings are in effect and which files were
  read. Review notes are kept in:
    %s

`, config.RepoFile, userPath, shellQuote(filepath.Dir(userPath)), shellQuote(userPath),
		config.RepoFile, notesDir)
}

// shellQuote makes a path safe to paste into a shell. macOS puts the config
// directory under "Application Support", so a space is the common case rather
// than an edge one.
func shellQuote(path string) string {
	if !strings.ContainsAny(path, " \t'\"$`\\") {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crv: "+err.Error())
		os.Exit(1)
	}
}

// options are the command-line flags. They are registered on a FlagSet rather
// than the global one so the help text can be checked against the flags that
// actually exist.
type options struct {
	host        string
	theme       string
	export      string
	noColor     bool
	noUntracked bool
	showConfig  bool
	showVersion bool
	width       int
	limit       int
}

func registerFlags(fs *flag.FlagSet) *options {
	var o options
	fs.StringVar(&o.host, "host", "", "GitHub hostname")
	fs.StringVar(&o.theme, "theme", "", "chroma syntax theme")
	fs.StringVar(&o.export, "export", "", "print saved notes: markdown")
	fs.BoolVar(&o.noColor, "no-color", false, "disable colour")
	fs.BoolVar(&o.noUntracked, "no-untracked", false, "exclude untracked files")
	fs.BoolVar(&o.showConfig, "config", false, "print the resolved configuration")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	fs.IntVar(&o.width, "width", 0, "output width when not a terminal")
	fs.IntVar(&o.limit, "limit", 30, "how many pull requests the queue lists")
	return &o
}

func run() error {
	fs := flag.NewFlagSet("crv", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage()) }
	opts := registerFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if opts.showVersion {
		fmt.Printf("crv %s (%s, built %s)\n", version, commit, date)
		return nil
	}

	// Earlier versions stored everything under os.UserConfigDir, which on
	// macOS is ~/Library/Application Support. Move it once so saved notes
	// survive the change of location.
	if from, to, moved := config.Migrate(); moved {
		fmt.Fprintf(os.Stderr, "crv: moved your notes and settings\n     from %s\n     to   %s\n", from, to)
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
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			cfg.Host = opts.host
		case "theme":
			cfg.Theme = opts.theme
		case "no-color":
			cfg.Color = !opts.noColor
		case "no-untracked":
			cfg.Untracked = !opts.noUntracked
		case "width":
			cfg.Width = opts.width
		}
	})

	if opts.showConfig {
		return printConfig(cfg, repo.Root)
	}

	// A bare `crv` opens the queue: it is the one invocation with no natural
	// argument, and it is the thing that replaces opening github.com.
	if fs.NArg() == 0 && opts.export == "" {
		sel, err := runQueue(repo, cfg, opts.limit)
		if err != nil {
			return err
		}
		if !sel.Chosen {
			return nil
		}
		return reviewPR(repo, cfg, sel.Repo, sel.Number)
	}

	target := "."
	if fs.NArg() > 0 {
		target = fs.Arg(0)
	}

	src, files, err := resolve(repo, cfg, target)
	if err != nil {
		return err
	}

	return start(repo, cfg, src, files, opts.export)
}

// start loads the saved notes for a source and shows it, however it was
// reached: a target on the command line or a row in the queue.
func start(repo *gitsrc.Repo, cfg config.Config, src tui.Source, files []*diffparse.FileDiff, export string) error {
	branch, _ := repo.Branch()
	review, err := notes.Load(src.Scope(repo.Root, branch))
	if err != nil {
		return err
	}

	if export != "" {
		return printExport(export, review)
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
	_, err = tea.NewProgram(tui.New(tui.Options{
		Files:    files,
		Theme:    th,
		Config:   cfg,
		Source:   src,
		Review:   review,
		Comments: comments,
	}), tea.WithAltScreen()).Run()
	return err
}

// runQueue shows the review queue and returns what was chosen.
func runQueue(repo *gitsrc.Repo, cfg config.Config, limit int) (tui.Selection, error) {
	client := ghsrc.Client{Host: cfg.Host, Dir: repo.Root}
	if err := client.Preflight(); err != nil {
		return tui.Selection{}, fmt.Errorf("%w\n\nthe queue needs gh; local reviews (crv . and crv <range>) do not", err)
	}

	// Piped output gets the list as text: starting a full-screen program with
	// no terminal would fail, and `crv | grep` is a reasonable thing to want.
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return tui.Selection{}, printQueue(client, limit)
	}

	th := render.DefaultTheme()
	th.Syntax = cfg.Theme

	model, err := tea.NewProgram(tui.NewQueue(client, th, limit), tea.WithAltScreen()).Run()
	if err != nil {
		return tui.Selection{}, err
	}
	q, ok := model.(tui.QueueModel)
	if !ok {
		return tui.Selection{}, nil
	}
	return q.Selected, nil
}

// printQueue is the non-interactive queue: one line per pull request.
func printQueue(client ghsrc.Client, limit int) error {
	items, _, err := client.CachedQueue(ghsrc.FilterReviewRequested, limit, false)
	if err != nil && len(items) == 0 {
		return err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "crv: "+err.Error()+" — showing the cached list")
	}
	if len(items) == 0 {
		fmt.Println("nothing waiting on your review")
		return nil
	}
	for _, it := range items {
		check := " "
		switch it.Checks {
		case "SUCCESS":
			check = "✓"
		case "FAILURE", "ERROR":
			check = "✗"
		case "PENDING":
			check = "•"
		}
		fmt.Printf("%s %s#%-4d %-8s %-4s %s\n", check, it.Repo, it.Number, it.Author, it.Age(), it.Title)
	}
	return nil
}

// reviewPR opens a pull request chosen from the queue. It may live in another
// repository than the working directory, so the client is pointed at that
// repository by name rather than by path.
func reviewPR(repo *gitsrc.Repo, cfg config.Config, name string, number int) error {
	client := ghsrc.Client{Host: cfg.Host, Dir: repo.Root, Repo: name}

	pr, err := client.PR(number)
	if err != nil {
		return err
	}
	raw, err := client.Diff(number)
	if err != nil {
		return err
	}
	files := diffparse.Parse(raw)
	diffparse.FillStats(files)

	viewer, _ := client.Viewer()
	src := tui.Source{
		Kind:     tui.SourcePR,
		Title:    fmt.Sprintf("%s#%d %s", name, pr.Number, pr.Title),
		Repo:     name,
		PRNumber: pr.Number,
		Client:   client,
		Author:   pr.Author.Login,
		Viewer:   viewer,
	}
	return start(repo, cfg, src, files, "")
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

	name, err := client.CurrentRepo()
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

	// Knowing who you are lets the submit screen rule out approving your own
	// pull request, which GitHub rejects with a bare 422.
	viewer, err := client.Viewer()
	if err != nil {
		viewer = ""
	}

	src := tui.Source{
		Kind:     tui.SourcePR,
		Title:    fmt.Sprintf("%s#%d %s", name, pr.Number, pr.Title),
		Repo:     name,
		PRNumber: pr.Number,
		Client:   client,
		Author:   pr.Author.Login,
		Viewer:   viewer,
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
	fmt.Printf("\nuser file  %s%s\n", userPath, exists(userPath))
	repoFile := filepath.Join(repoRoot, config.RepoFile)
	fmt.Printf("repo file  %s%s\n", repoFile, exists(repoFile))
	if s := cfg.Sources(); len(s) > 0 {
		fmt.Printf("loaded     %s\n", strings.Join(s, ", "))
	} else {
		fmt.Printf("loaded     (none — all defaults; crv --help shows how to create one)\n")
	}
	dir, _ := notes.Dir()
	fmt.Printf("notes      %s\n", dir)
	return nil
}

// exists annotates a path with whether a file is actually there, so --config
// answers "is my file being picked up?" and not only "where would it go?".
func exists(path string) string {
	if _, err := os.Stat(path); err == nil {
		return ""
	}
	return "  (does not exist)"
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
