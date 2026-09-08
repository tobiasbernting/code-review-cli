package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
	"github.com/tobiasbernting/code-review-cli/internal/service"
)

type AttentionQueue struct {
	historyCursor                 int
	request                       int
	engine                        *service.Engine
	legacy                        QueueModel
	snapshot                      attention.Snapshot
	groups                        [][]attention.Task
	cursor, reason, width, height int
	loading, authored, history    bool
	historyLines                  []string
	err                           string
	Selected                      Selection
}
type attentionLoaded struct {
	request  int
	snapshot attention.Snapshot
	err      error
}
type attentionAction struct{ err error }

func NewAttentionQueue(e *service.Engine, c ghsrc.Client, th render.Theme, limit int) AttentionQueue {
	m := AttentionQueue{engine: e, legacy: NewQueue(c, th, limit), width: 80, height: 24, loading: true}
	snapshot, err := e.Store.Snapshot(e.Repositories)
	m.snapshot = snapshot
	if err != nil {
		m.err = err.Error()
	}
	m.regroup()
	return m
}
func (m AttentionQueue) Init() tea.Cmd { return m.load(true) }
func (m AttentionQueue) load(sync bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if sync {
			err = m.engine.Sync(context.Background())
		}
		s, readErr := m.engine.Store.Snapshot(m.engine.Repositories)
		if readErr != nil {
			err = readErr
		}
		return attentionLoaded{request: m.request, snapshot: s, err: err}
	}
}
func (m *AttentionQueue) regroup() {
	m.groups = nil
	index := map[string]int{}
	for _, t := range m.snapshot.Tasks {
		key := t.PR.Key()
		i, ok := index[key]
		if !ok {
			i = len(m.groups)
			index[key] = i
			m.groups = append(m.groups, nil)
		}
		m.groups[i] = append(m.groups[i], t)
	}
	m.cursor = min(m.cursor, max(0, len(m.groups)-1))
	m.reason = 0
}
func (m AttentionQueue) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.legacy.width, m.legacy.height = v.Width, v.Height
	case attentionLoaded:
		if v.request != m.request {
			return m, m.load(false)
		}
		m.loading = false
		m.snapshot = v.snapshot
		m.err = ""
		if v.err != nil {
			m.err = v.err.Error()
		}
		m.regroup()
		return m, nil
	case attentionAction:
		m.err = ""
		if v.err != nil {
			m.err = v.err.Error()
			return m, nil
		}
		return m, m.load(false)
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "t":
			m.authored = !m.authored
			m.history = false
			if m.authored {
				m.legacy.filter = ghsrc.FilterAuthored
				return m, m.legacy.load(false)
			}
			return m, nil
		case "h":
			m.history = !m.history
			lines, err := m.engine.Store.History()
			m.historyLines = lines
			if err != nil {
				m.err = err.Error()
			}
			return m, nil
		}
		if m.history {
			switch v.String() {
			case "j", "down":
				m.historyCursor = min(m.historyCursor+1, max(0, len(m.historyLines)-1))
			case "k", "up":
				m.historyCursor = max(0, m.historyCursor-1)
			case "esc":
				m.history = false
			}
			return m, nil
		}
		if m.authored {
			updated, cmd := m.legacy.Update(msg)
			m.legacy = updated.(QueueModel)
			m.Selected = m.legacy.Selected
			return m, cmd
		}
		switch v.String() {
		case "esc":
			if m.history {
				m.history = false
				return m, nil
			}
			return m, tea.Quit
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.groups)-1))
			m.reason = 0
		case "k", "up":
			m.cursor = max(0, m.cursor-1)
			m.reason = 0
		case "tab":
			if len(m.groups) > 0 {
				m.reason = (m.reason + 1) % len(m.groups[m.cursor])
			}
		case "r":
			if !m.loading {
				m.loading = true
				m.request++
				return m, m.load(true)
			}
		case "enter":
			if len(m.groups) > 0 {
				p := m.groups[m.cursor][0].PR
				m.Selected = Selection{Repo: p.Repo, Number: p.Number, Chosen: true, AttentionURL: m.groups[m.cursor][m.reason].Reason.URL}
				return m, tea.Quit
			}
		case "K", "D":
			m.request++
			m.loading = false
			if len(m.groups) > 0 {
				task := m.groups[m.cursor][m.reason]
				keep := v.String() == "K"
				return m, func() tea.Msg { return attentionAction{m.engine.Store.DecideTriage(task, keep)} }
			}
		case "u":
			m.request++
			m.loading = false
			if len(m.groups) > 0 {
				p := m.groups[m.cursor][0].PR
				return m, func() tea.Msg { return attentionAction{m.engine.Store.Retain(p, "Retained locally")} }
			}
		case "d":
			m.request++
			m.loading = false
			if len(m.groups) > 0 {
				task := m.groups[m.cursor][m.reason]
				if task.Reason.Kind == attention.Triage {
					m.err = "Use K Keep or D Dismiss for triage"
					return m, nil
				}
				return m, func() tea.Msg { return attentionAction{m.engine.Store.Done(task)} }
			}
		}
	default:
		if m.authored {
			updated, cmd := m.legacy.Update(msg)
			m.legacy = updated.(QueueModel)
			return m, cmd
		}
	}
	return m, nil
}
func AttentionText(s attention.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Attention — %s\nLast successful sync: %s\n", s.Identity, s.LastSync.Format(time.RFC3339))
	if s.LastSync.IsZero() || time.Since(s.LastSync) > 3*time.Minute {
		b.WriteString("Snapshot stale; outstanding work may be missing.\n")
	}
	if s.Error != "" {
		fmt.Fprintf(&b, "Sync error: %s\n", s.Error)
	}
	if s.AlertError != "" {
		fmt.Fprintf(&b, "Alert error: %s\n", s.AlertError)
	}
	for _, t := range s.Tasks {
		fmt.Fprintf(&b, "%s  %s  %s\n  %s\n", t.PR.Key(), t.Reason.Kind, t.PR.Title, t.Reason.URL)
	}
	if len(s.Tasks) == 0 {
		b.WriteString("No known outstanding tasks.\n")
	}
	return b.String()
}
func (m AttentionQueue) content() string {
	if m.authored {
		return m.legacy.View()
	}
	var b strings.Builder
	b.WriteString("Attention queue  ·  " + m.snapshot.Identity + "\n")
	if m.loading {
		b.WriteString("Synchronizing…\n")
	}
	if m.snapshot.LastSync.IsZero() {
		b.WriteString("Not synchronized; outstanding work may be missing.\n")
	} else {
		fmt.Fprintf(&b, "Snapshot %s ago", shortAge(time.Since(m.snapshot.LastSync)))
		if time.Since(m.snapshot.LastSync) > 3*time.Minute {
			b.WriteString(" — stale")
		}
		b.WriteString("\n")
	}
	if m.err != "" {
		b.WriteString("Sync/action error: " + m.err + "\n")
	}
	if m.snapshot.Error != "" {
		b.WriteString("Sync error: " + m.snapshot.Error + "\n")
	}
	if m.snapshot.AlertError != "" {
		b.WriteString("Alert error: " + m.snapshot.AlertError + "\n")
	}
	if m.history {
		for i, line := range m.historyLines {
			if i < m.historyCursor {
				continue
			}
			if i >= m.historyCursor+max(1, m.height-7) {
				break
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("h back · q quit")
		return b.String()
	}
	top := max(0, m.cursor-max(1, m.height/3)+1)
	for i := top; i < len(m.groups) && i < top+max(1, m.height/3); i++ {
		group := m.groups[i]
		marker := " "
		if i == m.cursor {
			marker = ">"
		}
		p := group[0].PR
		kinds := []string{}
		var oldest time.Time
		for _, t := range group {
			kinds = append(kinds, string(t.Reason.Kind))
			if !t.Reason.At.IsZero() && (oldest.IsZero() || t.Reason.At.Before(oldest)) {
				oldest = t.Reason.At
			}
		}
		draft := ""
		if !oldest.IsZero() {
			draft = " [" + shortAge(time.Since(oldest)) + " old]"
		}
		if p.Draft {
			draft += " [draft]"
		}
		if r, err := notes.Load(notes.PRScope(p.Repo, p.Number)); err == nil && len(r.Notes) > 0 {
			draft += fmt.Sprintf(" [%d local notes]", len(r.Notes))
		}
		fmt.Fprintf(&b, "%s %s %s%s — %s\n", marker, p.Key(), p.Title, draft, strings.Join(kinds, ", "))
	}
	if len(m.groups) > 0 {
		t := m.groups[m.cursor][m.reason]
		fmt.Fprintf(&b, "\nReason %d/%d: %s\n%s\n%s\n", m.reason+1, len(m.groups[m.cursor]), t.Reason.Kind, t.Reason.Text, t.Reason.URL)
		if t.PR.Closed {
			b.WriteString("PR closed; conversation remains until Done.\n")
		}
		if t.Reason.Kind == attention.Triage {
			b.WriteString("K Keep as task · D Dismiss (this observed activity)\n")
		} else if t.Reason.Manual() {
			b.WriteString("d Done for this reason and observed activity\n")
		}
	} else {
		b.WriteString("No known outstanding tasks.\n")
	}
	b.WriteString("enter review · tab reason · d Done · t authored · h history · r refresh · q quit")
	return b.String()
}

func (m AttentionQueue) View() string {
	if m.authored {
		return m.legacy.View()
	}
	lines := strings.Split(m.content(), "\n")
	height := max(8, m.height)
	width := max(20, m.width)
	if len(lines) > height {
		lines = append(lines[:height-1], lines[len(lines)-1])
	}
	style := lipgloss.NewStyle().Background(lipgloss.Color(m.legacy.theme.Bg)).Foreground(lipgloss.Color(m.legacy.theme.Fg))
	for i, line := range lines {
		line = ansi.Strip(line)
		line = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, line)
		lines[i] = style.Render(pad(ansi.Truncate(line, width, "…"), width))
	}
	return strings.Join(lines, "\n")
}
