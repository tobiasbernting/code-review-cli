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

// loadingHint is the loading page's keys, on every scene.
const loadingHint = "esc cancel · tab next"

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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
	sc := scenes[p.scene%len(scenes)]
	bg := space
	if sc.themed {
		bg = t.Bg
	}
	c := newCanvas(p.width, p.height)
	if sc.draw(c, p) {
		return strings.Join(c.rows(bg), "\n")
	}

	// A scene that does not fit is not drawn at all: clipped or wrapped, it
	// is noise. One line still says what is happening.
	surface := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(t.Fg))
	name := fmt.Sprintf("%s#%d", p.item.Repo, p.item.Number)
	// The title gives way first: which pull request, and how to get out,
	// are the parts that must survive.
	head, tail := "loading "+name+" ", " — "+loadingHint
	room := p.width - 4 - runewidth.StringWidth(head) - runewidth.StringWidth(tail)
	text := head + tail
	if room > 3 {
		text = head + runewidth.Truncate(p.item.Title, room, "…") + tail
	}
	spin := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(t.Accent)).
		Render(spinner[p.frame%len(spinner)])
	line := spin + surface.Render(" ") + surface.Render(runewidth.Truncate(text, maxInt(1, p.width-4), "…"))

	top := maxInt(0, (p.height-1)/2)
	var b strings.Builder
	for i := 0; i < p.height; i++ {
		if i > 0 {
			b.WriteString("\n")
		}
		if i == top {
			b.WriteString(centered(surface, line, p.width))
			continue
		}
		b.WriteString(surface.Render(strings.Repeat(" ", p.width)))
	}
	return b.String()
}

// centered pads a styled line to the middle of a surface-coloured row.
func centered(surface lipgloss.Style, s string, width int) string {
	w := lipgloss.Width(s)
	left := maxInt(0, (width-w)/2)
	right := maxInt(0, width-w-left)
	return surface.Render(strings.Repeat(" ", left)) + s + surface.Render(strings.Repeat(" ", right))
}
