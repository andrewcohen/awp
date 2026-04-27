package deckui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
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
	ActionLastSession
	ActionReview
	ActionCustom
)

type UserAction struct {
	Name    string
	Command string
	Alias   string
}

type ActionRequest struct {
	Item     Item
	Action   Action
	Arg      string
	Reporter Reporter
}

type Handler func(ActionRequest) error

// Reporter lets a handler push progress updates to the deck UI.
// Step marks the previous step done and starts a new running step.
// Log appends a line to the scrolling tail.
// Calls are safe from any goroutine.
type Reporter interface {
	Step(label string)
	Log(line string)
}

type ProgressStepState int

const (
	StepRunning ProgressStepState = iota
	StepDone
	StepError
)

type ProgressStep struct {
	Label string
	State ProgressStepState
}

type progressEventKind int

const (
	progressEventStep progressEventKind = iota
	progressEventLog
	progressEventDone
)

type progressEvent struct {
	kind   progressEventKind
	label  string
	line   string
	err    error
	action Action
	arg    string
	item   Item
}

type chanReporter struct {
	ch chan progressEvent
}

func (r *chanReporter) Step(label string) {
	r.ch <- progressEvent{kind: progressEventStep, label: label}
}

func (r *chanReporter) Log(line string) {
	r.ch <- progressEvent{kind: progressEventLog, line: line}
}

type progressEventMsg struct {
	ev progressEvent
	ok bool
}

// NewWorkspaceLauncher returns a tea.Cmd that suspends the deck, runs the
// interactive new-workspace flow in the same terminal, and emits a result msg.
type NewWorkspaceLauncher func(repoRoot string) tea.Cmd

type Refresher func() tea.Cmd

// PRItem is a lightweight PR summary for the review picker.
type PRItem struct {
	Number  int
	Title   string
	HeadRef string
	Author  string
	IsDraft bool
}

// PRFetcher returns a tea.Cmd that fetches PRs and emits a PRFetchDoneMsg.
// repoRoot scopes the fetch to the selected item's repository.
type PRFetcher func(repoRoot string) tea.Cmd

// PRFetchDoneMsg carries the result of an async PR list fetch.
type PRFetchDoneMsg struct {
	PRs []PRItem
	Err error
}

type findStage int

const (
	findStageProject findStage = iota
	findStageWorkspace
)

const findHintAlphabet = "asdfghjklqwertyuiopzxcvbnm"

type Model struct {
	itemsCurrent      []Item
	itemsAll          []Item
	showAll           bool
	currentRepo       string
	cursor            int
	width             int
	height            int
	status            string
	handler           Handler
	filterInput       textinput.Model
	filtering         bool
	filter            string
	confirmDelete     bool
	deleteTarget      Item
	pendingSelect     Item // after next refresh, cursor jumps to this (project, workspace) if present
	findMode          bool
	findStage         findStage
	findProject       string
	findProjectHints  map[string]string
	findProjectLookup map[string]string
	findProjectPrefix map[rune]bool
	findRowHints      map[int]string
	findRowLookup     map[string]int
	findRowPrefix     map[rune]bool
	findPendingPrefix rune
	refresher         Refresher
	newLauncher       NewWorkspaceLauncher
	prFetcher         PRFetcher
	reviewMode        bool
	reviewLoading     bool
	reviewPRs         []PRItem
	reviewCursor      int
	userActions       []UserAction
	actionMode        bool
	actionAliasLookup map[string]UserAction
	spinner           spinner.Model
	busy              bool
	progressMode      bool
	progressTitle     string
	progressSteps     []ProgressStep
	progressLog       []string
	progressErr       error
	progressDone       bool
	progressDoneAction Action
	progressChan       chan progressEvent
}

const progressLogMax = 50

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
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	m := Model{
		itemsCurrent:      append([]Item(nil), itemsCurrent...),
		itemsAll:          append([]Item(nil), itemsAll...),
		currentRepo:       currentRepo,
		showAll:           len(itemsAll) > 0,
		status:            "↑/↓ move · enter summon · f find · n new · r review · x action · / filter · a agent · e editor · c review · v vcs · s shell · i ci · L last · D delete · R relink · P scope · q quit",
		findProjectHints:  map[string]string{},
		findProjectLookup: map[string]string{},
		findProjectPrefix: map[rune]bool{},
		findRowHints:      map[int]string{},
		findRowLookup:     map[string]int{},
		findRowPrefix:     map[rune]bool{},
		handler:           handler,
		filterInput:       fi,
		spinner:           sp,
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

func (m Model) WithPRFetcher(f PRFetcher) Model {
	m.prFetcher = f
	return m
}

func (m Model) WithUserActions(actions []UserAction) Model {
	m.userActions = actions
	m.actionAliasLookup = make(map[string]UserAction, len(actions))
	for _, a := range actions {
		if a.Alias != "" {
			m.actionAliasLookup[a.Alias] = a
		}
	}
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
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
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
	case progressEventMsg:
		if !msg.ok {
			return m, nil
		}
		switch msg.ev.kind {
		case progressEventStep:
			if n := len(m.progressSteps); n > 0 && m.progressSteps[n-1].State == StepRunning {
				m.progressSteps[n-1].State = StepDone
			}
			m.progressSteps = append(m.progressSteps, ProgressStep{Label: msg.ev.label, State: StepRunning})
		case progressEventLog:
			m.progressLog = append(m.progressLog, msg.ev.line)
			if len(m.progressLog) > progressLogMax {
				m.progressLog = m.progressLog[len(m.progressLog)-progressLogMax:]
			}
		case progressEventDone:
			return m.Update(actionResultMsg{action: msg.ev.action, arg: msg.ev.arg, item: msg.ev.item, err: msg.ev.err})
		}
		return m, waitForProgress(m.progressChan)
	case actionResultMsg:
		m.busy = false
		m.progressDone = true
		m.progressDoneAction = msg.action
		if msg.err != nil {
			m.progressErr = msg.err
			if n := len(m.progressSteps); n > 0 && m.progressSteps[n-1].State == StepRunning {
				m.progressSteps[n-1].State = StepError
			}
			m.status = "error: " + msg.err.Error()
			return m, nil
		}
		if n := len(m.progressSteps); n > 0 && m.progressSteps[n-1].State == StepRunning {
			m.progressSteps[n-1].State = StepDone
		}
		m.status = fmt.Sprintf("%s: %s", actionLabel(msg.action, msg.arg), msg.item.WorkspaceName)
		if msg.action == ActionDelete {
			return m, nil
		}
		return m, tea.Quit
	case PRFetchDoneMsg:
		m.busy = false
		if !m.reviewMode {
			return m, nil
		}
		m.reviewLoading = false
		if msg.Err != nil {
			m.reviewMode = false
			m.status = "review: " + msg.Err.Error()
			return m, nil
		}
		if len(msg.PRs) == 0 {
			m.reviewMode = false
			m.status = "review: no open PRs"
			return m, nil
		}
		m.reviewPRs = msg.PRs
		m.reviewCursor = 0
		m.status = "review: select PR (enter confirm, esc cancel)"
		return m, nil
	case refreshDoneMsg:
		if msg.err != nil {
			m.status = "refresh: " + msg.err.Error()
			return m, nil
		}
		m.itemsCurrent = append([]Item(nil), msg.itemsCurrent...)
		m.itemsAll = append([]Item(nil), msg.itemsAll...)
		items := m.items()
		if pending := m.pendingSelect; pending.WorkspaceName != "" {
			for i, it := range items {
				if it.WorkspaceName == pending.WorkspaceName && (pending.ProjectName == "" || it.ProjectName == pending.ProjectName) {
					m.cursor = i
					break
				}
			}
			m.pendingSelect = Item{}
		}
		if len(items) == 0 {
			m.cursor = 0
		} else if m.cursor >= len(items) {
			m.cursor = len(items) - 1
		}
		return m, nil
	case tea.KeyMsg:
		if m.progressMode {
			if !m.progressDone {
				return m, nil
			}
			switch msg.String() {
			case "esc", "q", "enter", "ctrl+c":
				m.progressMode = false
				m.progressSteps = nil
				m.progressLog = nil
				m.progressErr = nil
				m.progressDone = false
				if m.progressDoneAction == ActionDelete && m.refresher != nil {
					m.pendingSelect = Item{ProjectName: m.deleteTarget.ProjectName, WorkspaceName: "default"}
					return m, m.refresher()
				}
				return m, nil
			}
			return m, nil
		}
		if m.confirmDelete {
			switch strings.ToLower(msg.String()) {
			case "y", "enter":
				m.confirmDelete = false
				if m.handler == nil {
					m.status = "delete: handler not configured"
					return m, nil
				}
				return m.startAction(ActionDelete, m.deleteTarget, "")
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
		if m.reviewMode {
			if m.reviewLoading {
				switch msg.String() {
				case "esc", "q", "ctrl+c":
					m.reviewMode = false
					m.reviewLoading = false
					m.status = "review: cancelled"
				}
				return m, nil
			}
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				m.reviewMode = false
				m.reviewPRs = nil
				m.reviewCursor = 0
				m.status = "review: cancelled"
				return m, nil
			case "j", "down":
				if m.reviewCursor < len(m.reviewPRs)-1 {
					m.reviewCursor++
				}
				return m, nil
			case "k", "up":
				if m.reviewCursor > 0 {
					m.reviewCursor--
				}
				return m, nil
			case "enter":
				if len(m.reviewPRs) == 0 || m.handler == nil {
					return m, nil
				}
				pr := m.reviewPRs[m.reviewCursor]
				item, _ := m.selected()
				m.reviewMode = false
				return m.startAction(ActionReview, item, strconv.Itoa(pr.Number))
			}
			return m, nil
		}
		if m.findMode {
			switch msg.String() {
			case "esc", "ctrl+c":
				if m.findPendingPrefix != 0 {
					m.findPendingPrefix = 0
					m.status = stageStatus(m.findStage)
					return m, nil
				}
				m.cancelFind("find: cancelled")
				return m, nil
			case "q":
				if m.findPendingPrefix != 0 {
					return m, nil
				}
				m.cancelFind("find: cancelled")
				return m, nil
			case "backspace", "ctrl+h":
				if m.findPendingPrefix != 0 {
					m.findPendingPrefix = 0
					m.status = stageStatus(m.findStage)
					return m, nil
				}
				if m.findStage == findStageWorkspace {
					m.findStage = findStageProject
					m.findProject = ""
					m.findRowHints = map[int]string{}
					m.findRowLookup = map[string]int{}
					m.findRowPrefix = map[rune]bool{}
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
				return m.handleFindRune(r)
			}
			return m, nil
		}
		if m.actionMode {
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				m.actionMode = false
				m.status = "action: cancelled"
				return m, nil
			}
			if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
				alias := string(msg.Runes[0])
				if ua, ok := m.actionAliasLookup[alias]; ok {
					m.actionMode = false
					return m.trigger(ActionCustom, ua.Name)
				}
				m.actionMode = false
				m.status = fmt.Sprintf("action: unknown alias %q", alias)
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
			return m.trigger(ActionOpenWindow, "review")
		case "C":
			return m.trigger(ActionOpenWindow, "review:tuicr -r main..@")
		case "v":
			return m.trigger(ActionOpenWindow, "vcs")
		case "s":
			return m.trigger(ActionOpenWindow, "")
		case "i":
			return m.trigger(ActionCI, "")
		case "L":
			if m.handler == nil {
				return m, nil
			}
			return m.startAction(ActionLastSession, Item{}, "")
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
		case "r":
			if m.prFetcher == nil {
				m.status = "review: not configured"
				return m, nil
			}
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.RepoRoot) == "" {
				m.status = "review: select a row with a known repo"
				return m, nil
			}
			m.reviewMode = true
			m.reviewLoading = true
			m.reviewPRs = nil
			m.reviewCursor = 0
			m.busy = true
			m.status = "review: loading PRs..."
			return m, tea.Batch(m.spinner.Tick, m.prFetcher(item.RepoRoot))
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
		case "x":
			if len(m.userActions) == 0 {
				m.status = "no user actions configured"
				return m, nil
			}
			m.actionMode = true
			m.status = m.actionModeStatus()
			return m, nil
		}
	}
	return m, nil
}

func (m Model) trigger(a Action, arg string) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || m.handler == nil {
		return m, nil
	}
	return m.startAction(a, item, arg)
}

func (m *Model) startAction(a Action, item Item, arg string) (tea.Model, tea.Cmd) {
	m.busy = true
	m.progressMode = true
	m.progressTitle = fmt.Sprintf("%s · %s", actionLabel(a, arg), item.WorkspaceName)
	m.progressSteps = nil
	m.progressLog = nil
	m.progressErr = nil
	m.progressDone = false
	m.progressChan = make(chan progressEvent, 32)
	m.status = fmt.Sprintf("%s %s...", actionLabel(a, arg), item.WorkspaceName)
	return *m, tea.Batch(m.spinner.Tick, m.dispatch(a, item, arg), waitForProgress(m.progressChan))
}

func (m Model) dispatch(a Action, item Item, arg string) tea.Cmd {
	ch := m.progressChan
	handler := m.handler
	return func() tea.Msg {
		reporter := &chanReporter{ch: ch}
		err := handler(ActionRequest{Item: item, Action: a, Arg: arg, Reporter: reporter})
		if ch != nil {
			ch <- progressEvent{kind: progressEventDone, err: err, action: a, arg: arg, item: item}
			close(ch)
		}
		return nil
	}
}

func waitForProgress(ch chan progressEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		return progressEventMsg{ev: ev, ok: ok}
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
	case ActionLastSession:
		return "last session"
	case ActionReview:
		return "review"
	case ActionCustom:
		if arg != "" {
			return "run " + arg
		}
		return "run action"
	}
	return "action"
}

func (m Model) actionModeStatus() string {
	parts := make([]string, 0, len(m.userActions))
	for _, a := range m.userActions {
		if a.Alias != "" {
			parts = append(parts, fmt.Sprintf("%s:%s", a.Alias, a.Name))
		}
	}
	return "action: " + strings.Join(parts, " · ")
}

func (m Model) View() string {
	if m.width == 0 {
		m.width = 100
	}
	if m.height == 0 {
		m.height = 24
	}
	if m.progressMode {
		body := m.renderProgress(m.width)
		footer := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.progressFooter())
		return lipgloss.JoinVertical(lipgloss.Left, body, footer)
	}

	leftWidth := max(32, m.width/2)
	if leftWidth > m.width-24 {
		leftWidth = m.width - 24
	}
	rightWidth := max(20, m.width-leftWidth-3)

	var left, right string
	if m.reviewMode {
		left = m.renderReviewList(leftWidth)
		right = m.renderReviewDetails(rightWidth)
	} else {
		left = m.renderList(leftWidth)
		right = m.renderDetails(rightWidth)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "\n", right)

	statusText := m.status
	if m.busy {
		statusText = m.spinner.View() + " " + m.status
	}
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(statusText)
	if m.filtering {
		footer = "/" + m.filterInput.View()
	} else if m.findMode || m.actionMode {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(m.status + " (esc cancel)")
	} else if m.filter != "" {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
			fmt.Sprintf("filter: %q (esc to clear) · %s", m.filter, statusText),
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
		dim := m.findMode && m.findStage == findStageWorkspace && item.ProjectName != m.findProject
		if item.ProjectName != lastProject {
			headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
			header := item.ProjectName
			if m.findMode && m.findStage == findStageWorkspace && item.ProjectName == m.findProject {
				headerStyle = headerStyle.Bold(true).Foreground(lipgloss.Color("117"))
			} else if dim {
				headerStyle = headerStyle.Foreground(lipgloss.Color("238"))
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
		} else if dim {
			style = style.Foreground(lipgloss.Color("238"))
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
		promptColor := lipgloss.Color("245")
		if dim {
			promptColor = lipgloss.Color("238")
		}
		rows = append(rows, lipgloss.NewStyle().Width(width).Foreground(promptColor).Render("   "+truncate(prompt, max(8, width-4))))
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
		"r      review PR",
		"x      user actions",
		"a      open agent window",
		"e      open editor window ($EDITOR)",
		"c      open review window (tuicr -r @)",
		"C      open review window (tuicr -r main..@)",
		"v      open vcs window (jjui)",
		"s      open shell window",
		"i      watch CI run for branch",
		"D      delete workspace",
		"R      relink/recover session",
		"q      quit deck",
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderReviewList(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render("review: select PR")
	rows := []string{title, ""}
	if m.reviewLoading {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("Loading PRs..."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	if len(m.reviewPRs) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("No open PRs."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	for i, pr := range m.reviewPRs {
		prefix := "  "
		style := lipgloss.NewStyle().Width(width - 1)
		if i == m.reviewCursor {
			prefix = "› "
			style = style.Bold(true).Foreground(lipgloss.Color("230"))
		}
		draft := ""
		if pr.IsDraft {
			draft = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(" [draft]")
		}
		number := lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Render(fmt.Sprintf("#%d", pr.Number))
		line := fmt.Sprintf("%s%s%s %s", prefix, number, draft, truncate(pr.Title, max(10, width-20)))
		rows = append(rows, style.Render(line))
		meta := fmt.Sprintf("   @%s  %s", pr.Author, pr.HeadRef)
		rows = append(rows, lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("245")).Render(truncate(meta, max(8, width-4))))
	}
	return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
}

func (m Model) renderReviewDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("PR review")
	if m.reviewLoading || len(m.reviewPRs) == 0 {
		lines := []string{title, "", "Waiting for PR list..."}
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	if m.reviewCursor < 0 || m.reviewCursor >= len(m.reviewPRs) {
		return lipgloss.NewStyle().Width(width).Render(title + "\n\nSelect a PR.")
	}
	pr := m.reviewPRs[m.reviewCursor]
	draft := "no"
	if pr.IsDraft {
		draft = "yes"
	}
	lines := []string{
		title,
		"",
		fmt.Sprintf("PR:     #%d", pr.Number),
		fmt.Sprintf("Title:  %s", pr.Title),
		fmt.Sprintf("Author: @%s", pr.Author),
		fmt.Sprintf("Branch: %s", pr.HeadRef),
		fmt.Sprintf("Draft:  %s", draft),
		"",
		"Actions:",
		"enter  start review",
		"esc    cancel",
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderProgress(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render(m.progressTitle)
	rows := []string{title, ""}
	if len(m.progressSteps) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(m.spinner.View()+" starting..."))
	}
	for _, step := range m.progressSteps {
		var glyph, color string
		switch step.State {
		case StepDone:
			glyph, color = "✓", "82"
		case StepError:
			glyph, color = "✗", "203"
		case StepRunning:
			glyph, color = m.spinner.View(), "117"
		default:
			glyph, color = "○", "245"
		}
		line := fmt.Sprintf("%s %s", lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph), step.Label)
		rows = append(rows, line)
	}
	if m.progressErr != nil {
		rows = append(rows, "")
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("error: "+m.progressErr.Error()))
	}
	if len(m.progressLog) > 0 {
		rows = append(rows, "")
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Bold(true).Render("log"))
		tail := m.progressLog
		maxLines := max(4, m.height-len(m.progressSteps)-10)
		if maxLines > 0 && len(tail) > maxLines {
			tail = tail[len(tail)-maxLines:]
		}
		logStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Width(width)
		for _, line := range tail {
			rows = append(rows, logStyle.Render(truncate(line, max(8, width-2))))
		}
	}
	return lipgloss.NewStyle().Width(width).Padding(1, 2).Render(strings.Join(rows, "\n"))
}

func (m Model) progressFooter() string {
	if m.progressDone {
		if m.progressErr != nil {
			return "press esc to dismiss"
		}
		return "done · press esc to dismiss"
	}
	return "running... (no cancel)"
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
	m.findPendingPrefix = 0
	m.findProjectHints, m.findProjectLookup, m.findProjectPrefix = m.buildProjectHints()
	m.findRowHints = map[int]string{}
	m.findRowLookup = map[string]int{}
	m.findRowPrefix = map[rune]bool{}
	m.status = "find: project"
}

func (m *Model) cancelFind(status string) {
	m.findMode = false
	m.findStage = findStageProject
	m.findProject = ""
	m.findPendingPrefix = 0
	m.findProjectHints = map[string]string{}
	m.findProjectLookup = map[string]string{}
	m.findProjectPrefix = map[rune]bool{}
	m.findRowHints = map[int]string{}
	m.findRowLookup = map[string]int{}
	m.findRowPrefix = map[rune]bool{}
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) handleFindRune(r rune) (tea.Model, tea.Cmd) {
	if m.findPendingPrefix != 0 {
		hint := string(m.findPendingPrefix) + string(r)
		m.findPendingPrefix = 0
		if m.findStage == findStageProject {
			project, ok := m.findProjectLookup[hint]
			if !ok {
				m.status = stageStatus(m.findStage)
				return m, nil
			}
			return m.enterWorkspaceStage(project), nil
		}
		idx, ok := m.findRowLookup[hint]
		if !ok {
			m.status = stageStatus(m.findStage)
			return m, nil
		}
		m.cursor = idx
		m.cancelFind("")
		if item, ok := m.selected(); ok {
			m.status = "find: " + item.WorkspaceName
		}
		return m, nil
	}

	hint := string(r)
	if m.findStage == findStageProject {
		if project, ok := m.findProjectLookup[hint]; ok {
			return m.enterWorkspaceStage(project), nil
		}
		if m.findProjectPrefix[r] {
			m.findPendingPrefix = r
			m.status = fmt.Sprintf("find: project %c…", r)
		}
		return m, nil
	}
	if idx, ok := m.findRowLookup[hint]; ok {
		m.cursor = idx
		m.cancelFind("")
		if item, ok := m.selected(); ok {
			m.status = "find: " + item.WorkspaceName
		}
		return m, nil
	}
	if m.findRowPrefix[r] {
		m.findPendingPrefix = r
		m.status = fmt.Sprintf("find: workspace %c…", r)
	}
	return m, nil
}

func (m Model) enterWorkspaceStage(project string) Model {
	m.findProject = project
	m.findStage = findStageWorkspace
	items := m.items()
	matches := []int{}
	for i, item := range items {
		if item.ProjectName == project {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		m.cancelFind("find: cancelled")
		return m
	}
	if len(matches) == 1 {
		m.cursor = matches[0]
		m.cancelFind("find: " + items[matches[0]].WorkspaceName)
		return m
	}
	m.findRowHints, m.findRowLookup, m.findRowPrefix = m.buildRowHints(project)
	m.status = "find: workspace"
	if len(m.findRowLookup) == 0 {
		m.cancelFind("find: cancelled")
	}
	return m
}

func stageStatus(stage findStage) string {
	if stage == findStageWorkspace {
		return "find: workspace"
	}
	return "find: project"
}

func (m Model) buildProjectHints() (map[string]string, map[string]string, map[rune]bool) {
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
	hintByName := assignHints(projects)
	lookup := map[string]string{}
	prefix := map[rune]bool{}
	forward := map[string]string{}
	for name, hint := range hintByName {
		forward[name] = hint
		lookup[hint] = name
		if len([]rune(hint)) == 2 {
			prefix[[]rune(hint)[0]] = true
		}
	}
	return forward, lookup, prefix
}

func (m Model) buildRowHints(project string) (map[int]string, map[string]int, map[rune]bool) {
	items := m.items()
	rowIdx := []int{}
	names := []string{}
	for i, item := range items {
		if item.ProjectName != project {
			continue
		}
		rowIdx = append(rowIdx, i)
		names = append(names, item.WorkspaceName)
	}
	hintByName := assignHints(names)
	forward := map[int]string{}
	lookup := map[string]int{}
	prefix := map[rune]bool{}
	for i, name := range names {
		hint, ok := hintByName[name]
		if !ok {
			continue
		}
		forward[rowIdx[i]] = hint
		lookup[hint] = rowIdx[i]
		if len([]rune(hint)) == 2 {
			prefix[[]rune(hint)[0]] = true
		}
	}
	return forward, lookup, prefix
}

func (m Model) findHints() (map[string]string, map[int]string) {
	if !m.findMode {
		return map[string]string{}, map[int]string{}
	}
	projectHints := map[string]string{}
	if m.findStage == findStageProject {
		for name, hint := range m.findProjectHints {
			projectHints[name] = hint
		}
	}
	rowHints := map[int]string{}
	for idx, hint := range m.findRowHints {
		rowHints[idx] = hint
	}
	return projectHints, rowHints
}

// assignHints picks EasyMotion-style hints for the given ordered list of
// target names. Unique first letters become single-key hints; collisions
// promote all sharing targets to two-key hints (preferred first letter +
// home-row disambiguator). Names whose first rune is not [a-z] fall through
// to the disambiguator pool for their first char. If smart assignment cannot
// cover every target, the function falls back to sequential home-row hints.
func assignHints(names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 {
		return out
	}
	type bucket struct {
		key   rune
		names []string
	}
	var ordered []*bucket
	byKey := map[rune]*bucket{}
	firstRune := func(s string) rune {
		rs := []rune(s)
		if len(rs) == 0 {
			return 0
		}
		r := unicode.ToLower(rs[0])
		if r >= 'a' && r <= 'z' {
			return r
		}
		return 0
	}
	for _, name := range names {
		k := firstRune(name)
		b, ok := byKey[k]
		if !ok {
			b = &bucket{key: k}
			byKey[k] = b
			ordered = append(ordered, b)
		}
		b.names = append(b.names, name)
	}

	reservedSingle := map[rune]bool{}
	for _, b := range ordered {
		if b.key != 0 && len(b.names) == 1 {
			reservedSingle[b.key] = true
			out[b.names[0]] = string(b.key)
		}
	}

	secondPool := make([]rune, 0, len(findHintAlphabet))
	for _, c := range findHintAlphabet {
		if !reservedSingle[c] {
			secondPool = append(secondPool, c)
		}
	}

	used := map[string]bool{}
	for _, hint := range out {
		used[hint] = true
	}

	assignDouble := func(name string, first rune) bool {
		for _, second := range secondPool {
			hint := string(first) + string(second)
			if used[hint] {
				continue
			}
			used[hint] = true
			out[name] = hint
			return true
		}
		return false
	}

	for _, b := range ordered {
		if b.key == 0 || len(b.names) <= 1 {
			continue
		}
		for _, name := range b.names {
			assignDouble(name, b.key)
		}
	}

	if fallback, ok := byKey[0]; ok {
		firstPool := make([]rune, 0, len(findHintAlphabet))
		for _, c := range findHintAlphabet {
			if reservedSingle[c] {
				continue
			}
			firstPool = append(firstPool, c)
		}
		for _, name := range fallback.names {
			for _, first := range firstPool {
				if assignDouble(name, first) {
					break
				}
			}
		}
	}

	for _, name := range names {
		if _, ok := out[name]; ok {
			continue
		}
		legacy := map[string]string{}
		for i, n := range names {
			if i >= len(findHintAlphabet) {
				break
			}
			legacy[n] = string(findHintAlphabet[i])
		}
		return legacy
	}
	return out
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

func renderFindHint(hint string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("62")).
		Render("[" + hint + "]")
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
