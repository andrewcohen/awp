package deckui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Item struct {
	ProjectName   string
	WorkspaceName string
	Path          string
	RepoRoot      string
	Status        string
	PromptPreview string
	TmuxWindow    string
	SessionName   string
	Active        bool
	Stale         bool
}

type Action int

const (
	ActionSummon Action = iota
	ActionLogs
	ActionTests
	ActionShell
	ActionRelink
	ActionSetPrompt
	ActionSetStatus
)

type ActionRequest struct {
	Item   Item
	Action Action
	Arg    string
}

type Handler func(ActionRequest) error

// NewWorkspaceLauncher returns a tea.Cmd that suspends the deck, runs the
// interactive new-workspace flow in the same terminal, and emits a result msg.
type NewWorkspaceLauncher func(repoRoot string) tea.Cmd

type Model struct {
	itemsCurrent []Item
	itemsAll     []Item
	showAll      bool
	currentRepo  string
	cursor       int
	width        int
	height       int
	status       string
	handler      Handler
	promptInput  textinput.Model
	editing      bool
	newLauncher  NewWorkspaceLauncher
}

type NewWorkspaceDoneMsg struct {
	Err error
}

type actionResultMsg struct {
	action Action
	item   Item
	err    error
}

func New(items []Item, handler Handler) Model {
	return NewScoped(items, nil, "", handler)
}

// NewScoped builds a model with both scopes and a toggle key (P).
// itemsCurrent = current repo only. itemsAll = every repo. currentRepo = active project name for header.
func NewScoped(itemsCurrent, itemsAll []Item, currentRepo string, handler Handler) Model {
	ti := textinput.New()
	ti.Placeholder = "prompt..."
	ti.CharLimit = 256
	return Model{
		itemsCurrent: append([]Item(nil), itemsCurrent...),
		itemsAll:     append([]Item(nil), itemsAll...),
		currentRepo:  currentRepo,
		showAll:      len(itemsAll) > 0,
		status:       "↑/↓ move · enter summon · n new · p prompt · l logs · t tests · s shell · R relink · P scope · q quit",
		handler:      handler,
		promptInput:  ti,
	}
}

// WithNewWorkspaceLauncher installs a launcher used by the `n` key to create
// a workspace in the selected row's project without leaving the popup.
func (m Model) WithNewWorkspaceLauncher(l NewWorkspaceLauncher) Model {
	m.newLauncher = l
	return m
}

func (m Model) items() []Item {
	if m.showAll && len(m.itemsAll) > 0 {
		return m.itemsAll
	}
	return m.itemsCurrent
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case NewWorkspaceDoneMsg:
		if msg.Err != nil {
			m.status = "new: " + msg.Err.Error()
			return m, nil
		}
		return m, tea.Quit
	case actionResultMsg:
		if msg.err != nil {
			m.status = "error: " + msg.err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("%s: %s", actionLabel(msg.action), msg.item.WorkspaceName)
		if msg.action != ActionSetPrompt && msg.action != ActionSetStatus {
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyMsg:
		if m.editing {
			switch msg.String() {
			case "esc":
				m.editing = false
				m.promptInput.Blur()
				return m, nil
			case "enter":
				item, ok := m.selected()
				m.editing = false
				m.promptInput.Blur()
				if !ok || m.handler == nil {
					return m, nil
				}
				arg := m.promptInput.Value()
				return m, m.dispatch(ActionSetPrompt, item, arg)
			}
			var cmd tea.Cmd
			m.promptInput, cmd = m.promptInput.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.items())-1 {
				m.cursor++
			}
			return m, nil
		case "P":
			m.showAll = !m.showAll
			m.cursor = 0
			if m.showAll {
				m.status = "scope: all projects"
			} else {
				m.status = "scope: current project"
			}
			return m, nil
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "enter":
			return m.trigger(ActionSummon)
		case "l":
			return m.trigger(ActionLogs)
		case "t":
			return m.trigger(ActionTests)
		case "s":
			return m.trigger(ActionShell)
		case "R":
			return m.trigger(ActionRelink)
		case "p":
			if item, ok := m.selected(); ok {
				m.editing = true
				m.promptInput.SetValue(item.PromptPreview)
				m.promptInput.Focus()
				m.status = "enter to save · esc cancel"
			}
			return m, nil
		case "n":
			if m.newLauncher == nil {
				m.status = "new: launcher not configured"
				return m, nil
			}
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.RepoRoot) == "" {
				m.status = "new: select a row with a known repo"
				return m, nil
			}
			m.status = "new workspace in " + item.ProjectName + "..."
			return m, m.newLauncher(item.RepoRoot)
		}
	}
	return m, nil
}

func (m Model) trigger(a Action) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || m.handler == nil {
		return m, nil
	}
	m.status = fmt.Sprintf("%s %s...", actionLabel(a), item.WorkspaceName)
	return m, m.dispatch(a, item, "")
}

func (m Model) dispatch(a Action, item Item, arg string) tea.Cmd {
	return func() tea.Msg {
		err := m.handler(ActionRequest{Item: item, Action: a, Arg: arg})
		return actionResultMsg{action: a, item: item, err: err}
	}
}

func actionLabel(a Action) string {
	switch a {
	case ActionSummon:
		return "summon"
	case ActionLogs:
		return "logs"
	case ActionTests:
		return "tests"
	case ActionShell:
		return "shell"
	case ActionRelink:
		return "relink"
	case ActionSetPrompt:
		return "prompt set"
	case ActionSetStatus:
		return "status set"
	}
	return "action"
}

func (m Model) View() string {
	if m.width == 0 {
		m.width = 100
	}
	if m.height == 0 {
		m.height = 24
	}
	leftWidth := max(32, m.width/2)
	if leftWidth > m.width-24 {
		leftWidth = m.width - 24
	}
	rightWidth := max(20, m.width-leftWidth-3)

	left := m.renderList(leftWidth)
	right := m.renderDetails(rightWidth)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "\n", right)

	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.status)
	if m.editing {
		footer = "prompt> " + m.promptInput.View()
	}
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

func (m Model) renderList(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("awp deck")
	scope := "current project"
	if m.currentRepo != "" {
		scope = m.currentRepo
	}
	if m.showAll {
		scope = "all projects"
	}
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("scope: " + scope + "  (P to toggle)")
	rows := []string{title, subtitle, ""}
	items := m.items()
	if len(items) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("No workspaces found."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	lastProject := ""
	for i, item := range items {
		if item.ProjectName != lastProject {
			rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(item.ProjectName))
			lastProject = item.ProjectName
		}
		prefix := "  "
		style := lipgloss.NewStyle().Width(width - 1)
		if i == m.cursor {
			prefix = "› "
			style = style.Bold(true).Foreground(lipgloss.Color("230"))
		}
		label := truncate(item.WorkspaceName, max(10, width-20))
		if item.Stale {
			label += " ⚠"
		} else if item.Active {
			label += " ●"
		}
		prompt := item.PromptPreview
		if strings.TrimSpace(prompt) == "" {
			prompt = "—"
		}
		line := fmt.Sprintf("%s %-4s %s", prefix, compactStatus(item.Status), label)
		rows = append(rows, style.Render(line))
		rows = append(rows, lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("245")).Render("   "+truncate(prompt, max(8, width-4))))
	}
	return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
}

func (m Model) renderDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("details")
	item, ok := m.selected()
	if !ok {
		return lipgloss.NewStyle().Width(width).Render(title + "\n\nSelect a workspace.")
	}
	prompt := item.PromptPreview
	if strings.TrimSpace(prompt) == "" {
		prompt = "No active prompt"
	}
	sess := item.SessionName
	if strings.TrimSpace(sess) == "" {
		sess = item.TmuxWindow
	}
	if strings.TrimSpace(sess) == "" {
		sess = "not linked"
	}
	active := "no"
	if item.Active {
		active = "yes"
	}
	if item.Stale {
		active = "stale"
	}
	lines := []string{
		title,
		"",
		fmt.Sprintf("Project:   %s", item.ProjectName),
		fmt.Sprintf("Workspace: %s", item.WorkspaceName),
		fmt.Sprintf("Status:    %s", normalizeStatus(item.Status)),
		fmt.Sprintf("Session:   %s", sess),
		fmt.Sprintf("Live:      %s", active),
		fmt.Sprintf("Path:      %s", item.Path),
		"",
		"Prompt:",
		prompt,
		"",
		"Actions:",
		"enter  summon (create/focus session)",
		"p      edit prompt",
		"l      open logs window",
		"t      open tests window",
		"s      split shell into agent window",
		"R      relink/recover session",
		"q      quit deck",
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) selected() (Item, bool) {
	items := m.items()
	if len(items) == 0 || m.cursor < 0 || m.cursor >= len(items) {
		return Item{}, false
	}
	return items[m.cursor], true
}

func compactStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in progress", "in_progress", "running":
		return "RUN"
	case "waiting":
		return "WAIT"
	case "error":
		return "ERR"
	case "starting":
		return "INIT"
	case "done":
		return "DONE"
	default:
		return "IDLE"
	}
}

func normalizeStatus(status string) string {
	s := strings.TrimSpace(strings.ToLower(status))
	if s == "" {
		return "idle"
	}
	s = strings.ReplaceAll(s, "_", " ")
	return s
}

func truncate(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= 0 || len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}
