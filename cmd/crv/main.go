// Command crv reviews diffs in the terminal.
//
// Step 1 scope: render a working-tree or range diff and navigate it. Notes,
// GitHub PRs and review submission come later.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/gitsrc"
	"github.com/tobiasbernting/code-review-cli/internal/render"
	"github.com/tobiasbernting/code-review-cli/internal/tui"
)

const usage = `crv — review code in the terminal

usage:
  crv .              review uncommitted work (including untracked files)
  crv <range>        review a range, e.g. main...feature or HEAD~3..HEAD
  crv --help

flags:
  --theme <name>     chroma syntax theme (default catppuccin-mocha)
  --no-color         disable colour (also honours NO_COLOR)
  --no-untracked     exclude untracked files from the working-tree diff
  --width <n>        output width when not attached to a terminal
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "crv: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		theme     = flag.String("theme", "", "chroma syntax theme")
		noColor   = flag.Bool("no-color", false, "disable colour")
		noUntrack = flag.Bool("no-untracked", false, "exclude untracked files")
		widthFlag = flag.Int("width", 0, "output width when not a terminal")
	)
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	target := "."
	if flag.NArg() > 0 {
		target = flag.Arg(0)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := gitsrc.Open(cwd)
	if err != nil {
		return err
	}

	var files []*diffparse.FileDiff
	var title string
	if isWorkingTree(target) {
		files, err = repo.WorkingTree(!*noUntrack)
		title = "working tree"
	} else {
		files, err = repo.Range(target)
		title = target
	}
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("no changes")
		return nil
	}

	th := render.DefaultTheme()
	if *theme != "" {
		th.Syntax = *theme
	}
	color := !*noColor && os.Getenv("NO_COLOR") == ""
	doc := render.Build(files, render.NewHighlighter(th.Syntax, color))

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return printPlain(doc, th, *widthFlag)
	}
	prog := tea.NewProgram(tui.New(doc, th, title), tea.WithAltScreen())
	_, err = prog.Run()
	return err
}

// isWorkingTree reports whether the target means "uncommitted work" rather
// than a revision. A bare "." is the documented spelling; an empty argument
// list lands here too.
func isWorkingTree(target string) bool {
	return target == "" || target == "."
}

// printPlain is the non-TTY path: same rows, printed once and exited, so
// `crv . | less` and `crv . > review.txt` work.
func printPlain(doc *render.Document, th render.Theme, width int) error {
	if width <= 0 {
		width = 120
	}
	r := render.NewRenderer(th, doc)
	var b strings.Builder
	for _, row := range doc.Rows {
		b.WriteString(r.Render(row, width, 0, false))
		b.WriteString("\n")
	}
	_, err := os.Stdout.WriteString(b.String())
	return err
}
