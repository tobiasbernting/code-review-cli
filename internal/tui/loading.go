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
	`  ___ _ ____   __`,
	` / __| '__\ \ / /`,
	`| (__| |   \ V / `,
	` \___|_|    \_/  `,
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

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	hunkWidth    = 36 // inside the box
	typedPerTick = 6  // the whole hunk in about the time a load takes
	holdFrames   = 15 // how long the finished hunk stays before it restarts
)

// loadingPage is what fills the screen while a pull request is fetched.
type loadingPage struct {
	item          ghsrc.QueueItem
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

	boxWidth := hunkWidth + 4
	var art []string
	for _, l := range loadingLogo {
		art = append(art, fg(t.Accent).Bold(true).Render(l))
	}
	art = append(art, "")
	art = append(art, p.box(fg, boxWidth)...)
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
		fg(t.Dim).Render("esc cancel"),
	)

	widest := 0
	for _, l := range art {
		widest = maxInt(widest, lipgloss.Width(l))
	}
	// Art that does not fit is not drawn at all: clipped or wrapped, it is
	// noise. One line still says what is happening.
	if widest+4 > p.width || len(art)+2 > p.height {
		// The title gives way first: which pull request, and how to get
		// out, are the parts that must survive.
		head, tail := "loading "+name+" ", " — esc cancel"
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

// box draws the hunk as typed so far, with a blinking cursor at the end.
func (p loadingPage) box(fg func(string) lipgloss.Style, width int) []string {
	t := p.theme
	border := fg(t.Dim)

	total := 0
	for _, l := range loadingHunk {
		total += len(l) + 1
	}
	typed := (p.frame * typedPerTick) % (total + holdFrames*typedPerTick)

	title := "─ reviewing "
	lines := []string{border.Render("╭" + title + strings.Repeat("─", width-2-runewidth.StringWidth(title)) + "╮")}
	cursorShown := false
	for _, l := range loadingHunk {
		shown := l
		if typed < len(l) {
			shown = l[:maxInt(0, typed)]
		}
		typed -= len(l) + 1

		colour := t.Fg
		switch {
		case strings.HasPrefix(l, "@@"):
			colour = t.Accent
		case strings.HasPrefix(l, "+"):
			colour = t.AddSign
		case strings.HasPrefix(l, "-"):
			colour = t.DelSign
		}
		text := fg(colour).Render(shown)
		used := len(shown)
		if !cursorShown && typed < 0 {
			cursorShown = true
			if p.frame%6 < 3 {
				text += fg(t.Accent).Render("▌")
				used++
			}
		}
		pad := maxInt(0, width-4-used)
		lines = append(lines, border.Render("│ ")+text+fg(t.Fg).Render(strings.Repeat(" ", pad))+border.Render(" │"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return lines
}

// centered pads a styled line to the middle of a surface-coloured row.
func centered(surface lipgloss.Style, s string, width int) string {
	w := lipgloss.Width(s)
	left := maxInt(0, (width-w)/2)
	right := maxInt(0, width-w-left)
	return surface.Render(strings.Repeat(" ", left)) + s + surface.Render(strings.Repeat(" ", right))
}
