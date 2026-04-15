package deckui

import (
	"fmt"
	"strings"
	"unicode"

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
	Current       bool
	Stale         bool
}

type Action int

const (
	ActionSummon Action = iota
	ActionRelink
	ActionOpenWindow
	ActionDelete
	ActionCI
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

type Refresher func() tea.Cmd

type findStage int

const (
	findStageProject findStage = iota
	findStageWorkspace
)

const findHintAlphabet = "asdfghjklqwertyuiopzxcvbnm"

type Model struct {
	itemsCurrent   []Item
	itemsAll       []Item
	showAll        bool
	currentRepo    string
	cursor         int
	width          int
	height         int
	status         string
	handler        Handler
	filterInput    textinput.Model
	filtering      bool
	filter         string
	confirmDelete  bool
	deleteTarget   Item
	findMode       bool
	findStage      findStage
	findProject    string
	findProjectMap map[rune]string
	findRowMap     map[rune]int
	refresher      Refresher
	newLauncher    NewWorkspaceLauncher
}

type NewWorkspaceDoneMsg struct {
	Err       error
	Cancelled bool
}

type actionResultMsg struct {
	action Action
	arg    string
	item   Item
	err    error
}

type refreshDoneMsg struct {
	itemsCurrent []Item
	itemsAll     []Item
	err          error
}

func RefreshDoneMsg(itemsCurrent, itemsAll []Item, err error) tea.Msg {
	return refreshDoneMsg{itemsCurrent: itemsCurrent, itemsAll: itemsAll, err: err}
}

func New(items []Item, handler Handler) Model {
	return NewScoped(items, nil, "", handler)
}

// NewScoped builds a model with both scopes and a toggle key (P).
func NewScoped(itemsCurrent, itemsAll []Item, currentRepo string, handler Handler) Model {
	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 64
	m := Model{
		itemsCurrent: append([]Item(nil), itemsCurrent...),
		itemsAll:     append([]Item(nil), itemsAll...),
		currentRepo:  currentRepo,
		showAll:      len(itemsAll) > 0,
		status:       "↑/↓ move · enter summon · f find · n new · / filter · a agent · e editor · c review · v vcs · s shell · i ci · D delete · R relink · P scope · q quit",
		findProjectMap: map[rune]string{},
		findRowMap:     map[rune]int{},
		handler:      handler,
		filterInput:  fi,
	}
	if idx := m.indexCurrent(); idx >= 0 {
		m.cursor = idx
	}
	return m
}

func (m Model) indexCurrent() int {
	src := m.itemsCurrent
	if m.showAll && len(m.itemsAll) > 0 {
		src = m.itemsAll
	}
	for i, it := range src {
		if it.Current {
			return i
		}
	}
	return -1
}

// WithNewWorkspaceLauncher installs a launcher used by the `n` key.
func (m Model) WithNewWorkspaceLauncher(l NewWorkspaceLauncher) Model {
	m.newLauncher = l
	return m
}

func (m Model) WithRefresher(r Refresher) Model {
	m.refresher = r
	return m
}

func (m Model) items() []Item {
	src := m.itemsCurrent
	if m.showAll && len(m.itemsAll) > 0 {
		src = m.itemsAll
	}
	f := strings.ToLower(strings.TrimSpace(m.filter))
	if f == "" {
		return src
	}
	out := make([]Item, 0, len(src))
	for _, it := range src {
		if strings.Contains(strings.ToLower(it.WorkspaceName), f) ||
			strings.Contains(strings.ToLower(it.ProjectName), f) {
			out = append(out, it)
		}
	}
	return out
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case NewWorkspaceDoneMsg:
		if msg.Cancelled {
			m.status = "new: cancelled"
			return m, nil
		}
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
		m.status = fmt.Sprintf("%s: %s", actionLabel(msg.action, msg.arg), msg.item.WorkspaceName)
		if msg.action == ActionDelete {
			if m.refresher != nil {
				return m, m.refresher()
			}
			return m, nil
		}
		return m, tea.Quit
	case refreshDoneMsg:
		if msg.err != nil {
			m.status = "refresh: " + msg.err.Error()
			return m, nil
		}
		m.itemsCurrent = append([]Item(nil), msg.itemsCurrent...)
		m.itemsAll = append([]Item(nil), msg.itemsAll...)
		if items := m.items(); len(items) == 0 {
			m.cursor = 0
		} else if m.cursor >= len(items) {
			m.cursor = len(items) - 1
		}
		return m, nil
	case tea.KeyMsg:
		if m.confirmDelete {
			switch strings.ToLower(msg.String()) {
			case "y":
				m.confirmDelete = false
				if m.handler == nil {
					m.status = "delete: handler not configured"
					return m, nil
				}
				m.status = fmt.Sprintf("delete %s...", m.deleteTarget.WorkspaceName)
				return m, m.dispatch(ActionDelete, m.deleteTarget, "")
			case "n", "esc", "q":
				m.confirmDelete = false
				m.status = "delete: cancelled"
				return m, nil
			}
			return m, nil
		}
		if m.filtering {
			switch msg.String() {
			case "esc":
				m.filtering = false
				m.filterInput.Blur()
				m.filter = ""
				m.filterInput.SetValue("")
				m.cursor = 0
				return m, nil
			case "enter":
				m.filtering = false
				m.filterInput.Blur()
				m.filter = m.filterInput.Value()
				m.cursor = 0
				return m, nil
			}
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.filter = m.filterInput.Value()
			if m.cursor >= len(m.items()) {
				m.cursor = 0
			}
			return m, cmd
		}
		if m.findMode {
			switch msg.String() {
			case "esc", "ctrl+c", "q":
				m.cancelFind("find: cancelled")
				return m, nil
			case "backspace", "ctrl+h":
				if m.findStage == findStageWorkspace {
					m.findStage = findStageProject
					m.findProject = ""
					m.findRowMap = map[rune]int{}
					m.status = "find: project"
					return m, nil
				}
				m.cancelFind("find: cancelled")
				return m, nil
			}
			if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
				r := unicode.ToLower(msg.Runes[0])
				if !strings.ContainsRune(findHintAlphabet, r) {
					return m, nil
				}
				if m.findStage == findStageProject {
					project, ok := m.findProjectMap[r]
					if !ok {
						return m, nil
					}
					m.findProject = project
					m.findStage = findStageWorkspace
					m.findRowMap = m.buildRowHintMap(project)
					m.status = "find: workspace"
					if len(m.findRowMap) == 0 {
						m.cancelFind("find: cancelled")
					}
					return m, nil
				}
				idx, ok := m.findRowMap[r]
				if !ok {
					return m, nil
				}
				m.cursor = idx
				m.cancelFind("")
				if item, ok := m.selected(); ok {
					m.status = "find: " + item.WorkspaceName
				}
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if m.filter != "" && msg.String() == "esc" {
				m.filter = ""
				m.filterInput.SetValue("")
				m.cursor = 0
				return m, nil
			}
			return m, tea.Quit
		case "/":
			m.filtering = true
			m.filterInput.Focus()
			m.filterInput.SetValue(m.filter)
			return m, nil
		case "f", "F":
			if len(m.items()) == 0 {
				return m, nil
			}
			m.startFind()
			return m, nil
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
			return m.trigger(ActionSummon, "")
		case "a":
			return m.trigger(ActionOpenWindow, "agent")
		case "e":
			return m.trigger(ActionOpenWindow, "editor")
		case "c":
			return m.trigger(ActionOpenWindow, "tuicr")
		case "v":
			return m.trigger(ActionOpenWindow, "vcs")
		case "s":
			return m.trigger(ActionOpenWindow, "")
		case "i":
			return m.trigger(ActionCI, "")
		case "D":
			item, ok := m.selected()
			if !ok {
				return m, nil
			}
			m.confirmDelete = true
			m.deleteTarget = item
			m.status = fmt.Sprintf("delete %s? [y/N]", item.WorkspaceName)
			return m, nil
		case "R":
			return m.trigger(ActionRelink, "")
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

func (m Model) trigger(a Action, arg string) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || m.handler == nil {
		return m, nil
	}
	m.status = fmt.Sprintf("%s %s...", actionLabel(a, arg), item.WorkspaceName)
	return m, m.dispatch(a, item, arg)
}

func (m Model) dispatch(a Action, item Item, arg string) tea.Cmd {
	return func() tea.Msg {
		err := m.handler(ActionRequest{Item: item, Action: a, Arg: arg})
		return actionResultMsg{action: a, arg: arg, item: item, err: err}
	}
}

func actionLabel(a Action, arg string) string {
	switch a {
	case ActionSummon:
		return "summon"
	case ActionRelink:
		return "relink"
	case ActionOpenWindow:
		if arg != "" {
			return "open " + arg
		}
		return "open shell"
	case ActionDelete:
		return "delete"
	case ActionCI:
		return "ci"
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
	if m.filtering {
		footer = "/" + m.filterInput.View()
	} else if m.findMode {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.status + " (esc/q cancel)")
	} else if m.filter != "" {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
			fmt.Sprintf("filter: %q (esc to clear) · %s", m.filter, m.status),
		)
	}
	view := lipgloss.JoinVertical(lipgloss.Left, body, footer)
	if m.confirmDelete {
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", m.renderDeleteConfirm())
	}
	return view
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
	projectHints, rowHints := m.findHints()
	lastProject := ""
	for i, item := range items {
		if item.ProjectName != lastProject {
			headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
			header := item.ProjectName
			if m.findMode && m.findStage == findStageWorkspace && item.ProjectName == m.findProject {
				headerStyle = headerStyle.Bold(true).Foreground(lipgloss.Color("117"))
			}
			if hint, ok := projectHints[item.ProjectName]; ok {
				header = fmt.Sprintf("%s %s", renderFindHint(hint), header)
			}
			rows = append(rows, headerStyle.Render(header))
			lastProject = item.ProjectName
		}
		prefix := "  "
		if hint, ok := rowHints[i]; ok {
			prefix = renderFindHint(hint)
		}
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
		"f      find jump (project → workspace)",
		"a      open agent window",
		"e      open editor window ($EDITOR)",
		"c      open code review window (tuicr)",
		"v      open vcs window (jjui)",
		"s      open shell window",
		"i      watch CI run for branch",
		"D      delete workspace",
		"R      relink/recover session",
		"q      quit deck",
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderDeleteConfirm() string {
	name := m.deleteTarget.WorkspaceName
	if strings.TrimSpace(name) == "" {
		name = "this workspace"
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("203")).
		Padding(1, 2).
		Render("Delete workspace \"" + name + "\"?\n\nPress y to confirm, n to cancel.")
	return box
}

func (m Model) selected() (Item, bool) {
	items := m.items()
	if len(items) == 0 || m.cursor < 0 || m.cursor >= len(items) {
		return Item{}, false
	}
	return items[m.cursor], true
}

func (m *Model) startFind() {
	m.findMode = true
	m.findStage = findStageProject
	m.findProject = ""
	m.findProjectMap = m.buildProjectHintMap()
	m.findRowMap = map[rune]int{}
	m.status = "find: project"
}

func (m *Model) cancelFind(status string) {
	m.findMode = false
	m.findStage = findStageProject
	m.findProject = ""
	m.findProjectMap = map[rune]string{}
	m.findRowMap = map[rune]int{}
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) buildProjectHintMap() map[rune]string {
	items := m.items()
	projects := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if _, ok := seen[item.ProjectName]; ok {
			continue
		}
		seen[item.ProjectName] = struct{}{}
		projects = append(projects, item.ProjectName)
	}
	hints := map[rune]string{}
	for i, project := range projects {
		if i >= len(findHintAlphabet) {
			break
		}
		hints[rune(findHintAlphabet[i])] = project
	}
	return hints
}

func (m Model) buildRowHintMap(project string) map[rune]int {
	items := m.items()
	hints := map[rune]int{}
	n := 0
	for i, item := range items {
		if item.ProjectName != project {
			continue
		}
		if n >= len(findHintAlphabet) {
			break
		}
		hints[rune(findHintAlphabet[n])] = i
		n++
	}
	return hints
}

func (m Model) findHints() (map[string]rune, map[int]rune) {
	if !m.findMode {
		return map[string]rune{}, map[int]rune{}
	}
	projectHints := map[string]rune{}
	for hint, project := range m.findProjectMap {
		projectHints[project] = hint
	}
	rowHints := map[int]rune{}
	for hint, idx := range m.findRowMap {
		rowHints[idx] = hint
	}
	return projectHints, rowHints
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

func renderFindHint(hint rune) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("62")).
		Render(fmt.Sprintf("[%c]", hint))
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
