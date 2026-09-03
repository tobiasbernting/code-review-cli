package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// Selection is what the queue returns: the pull request to open, if any.
type Selection struct {
	Repo   string
	Number int
	Chosen bool
}

// QueueModel is the list of pull requests waiting on you. It is a separate
// program from the diff viewer: choosing a row ends it, and the caller then
// loads that pull request.
type QueueModel struct {
	client ghsrc.Client
	theme  render.Theme
	limit  int

	filter   ghsrc.Filter
	items    []ghsrc.QueueItem
	drafts   map[string]int // "repo#number" -> unsent notes
	fetched  time.Time
	loading  bool
	err      string
	Selected Selection

	cursor        int
	top           int
	width, height int
}

func NewQueue(client ghsrc.Client, theme render.Theme, limit int) QueueModel {
	return QueueModel{
		client: client, theme: theme, limit: limit,
		filter: ghsrc.FilterReviewRequested,
		drafts: map[string]int{},
		width:  80, height: 24,
		loading: true,
	}
}

type queueLoadedMsg struct {
	filter  ghsrc.Filter
	items   []ghsrc.QueueItem
	fetched time.Time
	err     error
}

func (m QueueModel) Init() tea.Cmd { return m.load(false) }

func (m QueueModel) load(force bool) tea.Cmd {
	client, filter, limit := m.client, m.filter, m.limit
	return func() tea.Msg {
		items, fetched, err := client.CachedQueue(filter, limit, force)
		return queueLoadedMsg{filter: filter, items: items, fetched: fetched, err: err}
	}
}

func (m QueueModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case queueLoadedMsg:
		m.loading = false
		// A late reply for a filter the user has already switched away from
		// would otherwise overwrite the list they are looking at.
		if msg.filter != m.filter {
			return m, nil
		}
		m.items, m.fetched = msg.items, msg.fetched
		m.err = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			if len(msg.items) > 0 {
				m.err += " — showing the cached list"
			}
		}
		m.countDrafts()
		if m.cursor >= len(m.items) {
			m.cursor = maxInt(0, len(m.items)-1)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m QueueModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = maxInt(0, len(m.items)-1)
	case "r":
		m.loading = true
		return m, m.load(true)
	case "t", "tab":
		if m.filter == ghsrc.FilterReviewRequested {
			m.filter = ghsrc.FilterAuthored
		} else {
			m.filter = ghsrc.FilterReviewRequested
		}
		m.cursor, m.loading = 0, true
		return m, m.load(false)
	case "enter", " ":
		if m.cursor < len(m.items) {
			it := m.items[m.cursor]
			m.Selected = Selection{Repo: it.Repo, Number: it.Number, Chosen: true}
			return m, tea.Quit
		}
	}
	return m, nil
}

// countDrafts reports how many unsent notes each listed pull request has, so
// a half-finished review is visible from the queue rather than forgotten.
func (m *QueueModel) countDrafts() {
	m.drafts = map[string]int{}
	for _, it := range m.items {
		review, err := notes.Load(notes.PRScope(it.Repo, it.Number))
		if err != nil || len(review.Notes) == 0 {
			continue
		}
		m.drafts[fmt.Sprintf("%s#%d", it.Repo, it.Number)] = len(review.Notes)
	}
}

func (m QueueModel) View() string {
	t := m.theme
	var b strings.Builder

	header := lipgloss.NewStyle().Background(lipgloss.Color(t.FileBg)).
		Foreground(lipgloss.Color(t.FileFg)).Bold(true)
	title := fmt.Sprintf(" review queue — %s", m.filter.Label())
	if m.loading {
		title += "  ·  loading…"
	} else if !m.fetched.IsZero() {
		title += fmt.Sprintf("  ·  updated %s ago", shortAge(time.Since(m.fetched)))
	}
	b.WriteString(header.Render(pad(title, m.width)) + "\n")

	body := m.height - 2
	switch {
	case m.err != "" && len(m.items) == 0:
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(lipgloss.Color(t.DelFg)).Render(m.err) + "\n")
	case m.loading && len(m.items) == 0:
		b.WriteString("\n  loading…\n")
	case len(m.items) == 0:
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(lipgloss.Color(t.MetaFg)).
			Render("nothing waiting on you — press t for your own pull requests") + "\n")
	default:
		if m.cursor >= m.top+body {
			m.top = m.cursor - body + 1
		}
		if m.cursor < m.top {
			m.top = m.cursor
		}
		for i := 0; i < body; i++ {
			idx := m.top + i
			if idx >= len(m.items) {
				b.WriteString("\n")
				continue
			}
			b.WriteString(m.row(m.items[idx], idx == m.cursor) + "\n")
		}
	}

	hint := "enter open  t switch  r refresh  q quit"
	if m.err != "" && len(m.items) > 0 {
		hint = "r retry  " + hint
	}
	b.WriteString(header.Render(pad(" "+fmt.Sprintf("%d pull request%s", len(m.items), plural(len(m.items))), m.width-len(hint)-1) + hint + " "))
	return b.String()
}

func (m QueueModel) row(it ghsrc.QueueItem, selected bool) string {
	t := m.theme
	base := lipgloss.NewStyle()
	if selected {
		base = base.Background(lipgloss.Color(t.CursorBg)).Bold(true)
	}

	check, checkFg := checkMark(it.Checks)
	name := fmt.Sprintf("%s#%d", it.Repo, it.Number)

	// The title gets whatever is left, so the identifying columns survive a
	// narrow terminal.
	fixed := 2 + 2 + runewidth.StringWidth(name) + 12 + 6
	titleWidth := maxInt(10, m.width-fixed)
	title := runewidth.Truncate(it.Title, titleWidth, "…")
	if it.IsDraft {
		title = "[draft] " + title
	}

	line := " " + lipgloss.NewStyle().Foreground(lipgloss.Color(checkFg)).Render(check) + " " +
		lipgloss.NewStyle().Foreground(lipgloss.Color(t.HunkFg)).Render(name) + "  " +
		title

	meta := fmt.Sprintf("  %s  %s", it.Author, it.Age())
	if n := m.drafts[name]; n > 0 {
		meta = fmt.Sprintf("  %d draft%s%s", n, plural(n), meta)
	}
	line += lipgloss.NewStyle().Foreground(lipgloss.Color(t.MetaFg)).Render(meta)

	return base.Render(pad(line, m.width))
}

// checkMark renders the CI rollup. An empty state means the pull request has
// no checks at all, which is different from checks that have not finished.
func checkMark(state string) (string, string) {
	switch state {
	case "SUCCESS":
		return "✓", "#7fd88f"
	case "FAILURE", "ERROR":
		return "✗", "#f07178"
	case "PENDING":
		return "•", "#e5c07b"
	default:
		return " ", "#5c6370"
	}
}

func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
