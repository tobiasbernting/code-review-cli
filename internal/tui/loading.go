package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// frameMsg advances the loading page's animation. It carries the generation
// of the load it belongs to, so a cancelled load's ticks die out instead of
// piling onto the next one.
type frameMsg struct{ gen int }

const frameEvery = 100 * time.Millisecond

func nextFrame(gen int) tea.Cmd {
	return tea.Tick(frameEvery, func(time.Time) tea.Msg { return frameMsg{gen: gen} })
}

var loadingLogo = []string{
	` _               `,
	`| | ___ ____   __`,
	`| |/ / '__\ \ / /`,
	`|   <| |   \ V / `,
	`|_|\_\_|    \_/  `,
}

// loadingHunk is typed out, a few characters a frame, while the pull request
// loads: a review being written, in the theme's own diff colours.
var loadingHunk = []string{
	`@@ -1,4 +1,5 @@`,
	` func review(pr *PR) {`,
	`-    skim(pr)`,
	`+    read(pr.Diff)`,
	`+    think()`,
	`     comment(pr)`,
	` }`,
}

// loadingHint is the loading page's keys, on every scene.
const loadingHint = "esc cancel · tab next"

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	hunkWidth    = 36 // inside the box
	typedPerTick = 6  // the whole hunk in about the time a load takes
	holdFrames   = 15 // how long the finished hunk stays before it restarts
)

// loadingPage is what fills the screen while a pull request is fetched.
type loadingPage struct {
	item          ghsrc.QueueItem
	scene         int // index into scenes
	frame         int
	theme         render.Theme
	width, height int
}

func (p loadingPage) View() string {
	t := p.theme
	surface := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(t.Fg))
	fg := func(c string) lipgloss.Style {
		return lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(c))
	}

	name := fmt.Sprintf("%s#%d", p.item.Repo, p.item.Number)
	spin := spinner[p.frame%len(spinner)]
	sc := scenes[p.scene%len(scenes)]

	var art []string
	fits := true
	if sc.full {
		c := newCanvas(p.width, p.height)
		if sc.draw(c, p) {
			return strings.Join(c.rows(space), "\n")
		}
		fits = false
	} else {
		boxWidth := hunkWidth + 4
		for _, l := range loadingLogo {
			art = append(art, fg(t.Accent).Bold(true).Render(l))
		}
		art = append(art, "")
		art = append(art, p.box(sc, fg, boxWidth)...)
		art = append(art, "",
			fg(t.Accent).Bold(true).Render(name)+surface.Render("  ")+
				surface.Render(runewidth.Truncate(p.item.Title, maxInt(1, boxWidth-runewidth.StringWidth(name)-2), "…")),
		)
		if p.item.Author != "" {
			art = append(art, fg(t.Dim).Render("by "+p.item.Author))
		}
		art = append(art, "",
			fg(t.Accent).Render(spin)+fg(t.Dim).Render(" fetching the pull request…"),
			"",
			fg(t.Dim).Render(loadingHint),
		)
	}

	widest := 0
	for _, l := range art {
		widest = maxInt(widest, lipgloss.Width(l))
	}
	// Art that does not fit is not drawn at all: clipped or wrapped, it is
	// noise. One line still says what is happening.
	if !fits || widest+4 > p.width || len(art)+2 > p.height {
		// The title gives way first: which pull request, and how to get
		// out, are the parts that must survive.
		head, tail := "loading "+name+" ", " — "+loadingHint
		room := p.width - 4 - runewidth.StringWidth(head) - runewidth.StringWidth(tail)
		text := head + tail
		if room > 3 {
			text = head + runewidth.Truncate(p.item.Title, room, "…") + tail
		}
		line := fg(t.Accent).Render(spin) + surface.Render(" ") +
			surface.Render(runewidth.Truncate(text, maxInt(1, p.width-4), "…"))
		art = []string{line}
	}

	top := maxInt(0, (p.height-len(art))/2)
	var b strings.Builder
	for i := 0; i < p.height; i++ {
		if i > 0 {
			b.WriteString("\n")
		}
		j := i - top
		if j < 0 || j >= len(art) {
			b.WriteString(surface.Render(strings.Repeat(" ", p.width)))
			continue
		}
		b.WriteString(centered(surface, art[j], p.width))
	}
	return b.String()
}

// box frames a boxed scene's canvas, its title set into the top border.
func (p loadingPage) box(sc scene, fg func(string) lipgloss.Style, width int) []string {
	t := p.theme
	border := fg(t.Dim)
	c := newCanvas(width-4, boxRows)
	sc.draw(c, p)

	title := "─ " + sc.title + " "
	lines := []string{border.Render("╭" + title + strings.Repeat("─", width-2-runewidth.StringWidth(title)) + "╮")}
	for _, row := range c.rows(t.Bg) {
		lines = append(lines, border.Render("│ ")+row+border.Render(" │"))
	}
	return append(lines, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

// centered pads a styled line to the middle of a surface-coloured row.
func centered(surface lipgloss.Style, s string, width int) string {
	w := lipgloss.Width(s)
	left := maxInt(0, (width-w)/2)
	right := maxInt(0, width-w-left)
	return surface.Render(strings.Repeat(" ", left)) + s + surface.Render(strings.Repeat(" ", right))
}
