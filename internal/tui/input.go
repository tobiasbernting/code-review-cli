package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// input is a one-line editor drawn at the bottom of the screen. Most review
// notes are a single sentence; anything longer goes to $EDITOR instead.
type input struct {
	prompt string
	value  string
	cursor int
	active bool
}

func (i *input) start(prompt, value string) {
	i.prompt, i.value, i.cursor, i.active = prompt, value, len(value), true
}

func (i *input) stop() {
	i.active, i.value, i.cursor, i.prompt = false, "", 0, ""
}

// handle applies a keypress. It returns done when the value was accepted, and
// cancelled when the edit was abandoned.
func (i *input) handle(msg tea.KeyMsg) (done, cancelled bool) {
	switch msg.String() {
	case "enter":
		return true, false
	case "esc", "ctrl+c":
		return false, true
	case "backspace":
		if i.cursor > 0 {
			r := []rune(i.value)
			i.value = string(append(r[:i.cursor-1], r[i.cursor:]...))
			i.cursor--
		}
	case "delete":
		r := []rune(i.value)
		if i.cursor < len(r) {
			i.value = string(append(r[:i.cursor], r[i.cursor+1:]...))
		}
	case "left":
		if i.cursor > 0 {
			i.cursor--
		}
	case "right":
		if i.cursor < len([]rune(i.value)) {
			i.cursor++
		}
	case "home", "ctrl+a":
		i.cursor = 0
	case "end", "ctrl+e":
		i.cursor = len([]rune(i.value))
	case "ctrl+u":
		i.value, i.cursor = "", 0
	case "ctrl+w":
		i.deleteWord()
	default:
		if msg.Type == tea.KeyRunes {
			r := []rune(i.value)
			r = append(r[:i.cursor], append([]rune(string(msg.Runes)), r[i.cursor:]...)...)
			i.value = string(r)
			i.cursor += len(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			r := []rune(i.value)
			r = append(r[:i.cursor], append([]rune(" "), r[i.cursor:]...)...)
			i.value = string(r)
			i.cursor++
		}
	}
	return false, false
}

func (i *input) deleteWord() {
	r := []rune(i.value)
	end := i.cursor
	for end > 0 && r[end-1] == ' ' {
		end--
	}
	for end > 0 && r[end-1] != ' ' {
		end--
	}
	i.value = string(append(r[:end], r[i.cursor:]...))
	i.cursor = end
}

// render draws the prompt and value with a block cursor.
func (i input) render(width int, promptFg, bg string) string {
	r := []rune(i.value)
	before, at, after := string(r[:i.cursor]), " ", ""
	if i.cursor < len(r) {
		at, after = string(r[i.cursor]), string(r[i.cursor+1:])
	}

	base := lipgloss.NewStyle().Background(lipgloss.Color(bg))
	line := base.Foreground(lipgloss.Color(promptFg)).Bold(true).Render(" "+i.prompt+" ") +
		base.Render(before) +
		base.Reverse(true).Render(at) +
		base.Render(after)

	if w := lipgloss.Width(line); w < width {
		line += base.Render(strings.Repeat(" ", width-w))
	}
	return line
}
