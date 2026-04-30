package deckui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// refreshInterval is how often the deck polls workspace state for status
// updates pushed in by agent hooks. Short enough that color transitions
// feel live; long enough not to waste cycles.
const refreshInterval = 2 * time.Second

type refreshTickMsg time.Time

func scheduleRefreshTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

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
	ActionCreateWorkspace
)

type UserAction struct {
	Name    string
	Command string
	Alias   string
}

// NewWorkspaceRequest is the form result passed back from the launcher when
// the user submits the new-workspace form. It is consumed by the deck handler
// for ActionCreateWorkspace.
type NewWorkspaceRequest struct {
	Name     string
	Bookmark string
	Prompt   string
}

type ActionRequest struct {
	Item      Item
	Action    Action
	Arg       string
	Workspace NewWorkspaceRequest
	Reporter  Reporter
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

// NewWorkspaceInitial pre-fills the workspace form before it is shown.
// Bookmark, when set, becomes the bookmark field on the form (and the
// workspace name auto-derives from it if the user leaves the name blank).
type NewWorkspaceInitial struct {
	Bookmark string
}

// NewWorkspaceLauncher returns a tea.Cmd that suspends the deck, runs the
// interactive new-workspace flow in the same terminal, and emits a result msg.
type NewWorkspaceLauncher func(repoRoot string, initial NewWorkspaceInitial) tea.Cmd

// BookmarkFetcher returns a tea.Cmd that lists deduped bookmarks and emits a
// BookmarksDoneMsg.
type BookmarkFetcher func(repoRoot string) tea.Cmd

// StateEditorLauncher returns a tea.Cmd that suspends the deck and opens the
// global workspace-state.json in $EDITOR.
type StateEditorLauncher func() tea.Cmd

// StateEditDoneMsg is emitted when the state editor exits.
type StateEditDoneMsg struct{ Err error }

// BookmarksDoneMsg carries the result of an async bookmark fetch.
type BookmarksDoneMsg struct {
	Bookmarks []string
	Err       error
}

type Refresher func() tea.Cmd

// Scope controls which items are shown in the deck list.
type Scope int

const (
	ScopeCurrent  Scope = iota // workspaces in the current project
	ScopeAll                   // workspaces across all projects
	ScopeAwaiting              // only workspaces with status=waiting (any project)
)

// String returns the persisted token for a Scope.
func (s Scope) String() string {
	switch s {
	case ScopeAll:
		return "all"
	case ScopeAwaiting:
		return "awaiting"
	default:
		return "current"
	}
}

// ParseScope returns the Scope for a persisted token, defaulting to current.
func ParseScope(s string) Scope {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all":
		return ScopeAll
	case "awaiting":
		return ScopeAwaiting
	default:
		return ScopeCurrent
	}
}

// ScopeChangedHandler is invoked when the user toggles scope (P), so the
// caller can persist the choice across runs.
type ScopeChangedHandler func(Scope)

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
	scope             Scope
	scopeChanged      ScopeChangedHandler
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
	helpMode          bool
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
	bookmarkFetcher   BookmarkFetcher
	stateEditor       StateEditorLauncher
	reviewMode        bool
	reviewLoading     bool
	reviewPRs         []PRItem
	reviewCursor      int
	newMenuMode       bool
	newMenuCursor     int
	newMenuRepo       string
	bookmarkMode      bool
	bookmarkLoading   bool
	bookmarks         []string
	bookmarkCursor    int
	bookmarkFilter    textinput.Model
	bookmarkFiltering bool
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
	Request   *NewWorkspaceRequest
	RepoRoot  string
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
	bf := textinput.New()
	bf.Placeholder = "filter bookmarks..."
	bf.CharLimit = 64
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	m := Model{
		itemsCurrent:      append([]Item(nil), itemsCurrent...),
		itemsAll:          append([]Item(nil), itemsAll...),
		currentRepo:       currentRepo,
		scope:             defaultInitialScope(itemsAll),
		status:            "? help · ↑/↓ move · enter summon · f find · n new · r review · x action · / filter · a agent · e editor · c review · v vcs · s shell · i ci · L last · D delete · R relink · P scope · , edit state · q quit",
		findProjectHints:  map[string]string{},
		findProjectLookup: map[string]string{},
		findProjectPrefix: map[rune]bool{},
		findRowHints:      map[int]string{},
		findRowLookup:     map[string]int{},
		findRowPrefix:     map[rune]bool{},
		handler:           handler,
		filterInput:       fi,
		bookmarkFilter:    bf,
		spinner:           sp,
	}
	if idx := m.indexCurrent(); idx >= 0 {
		m.cursor = idx
	}
	return m
}

func defaultInitialScope(itemsAll []Item) Scope {
	if len(itemsAll) > 0 {
		return ScopeAll
	}
	return ScopeCurrent
}

func (m Model) indexCurrent() int {
	src := m.scopedSource()
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

func (m Model) WithBookmarkFetcher(f BookmarkFetcher) Model {
	m.bookmarkFetcher = f
	return m
}

func (m Model) WithStateEditor(l StateEditorLauncher) Model {
	m.stateEditor = l
	return m
}

// WithScope sets the initial scope (called by the deck launcher after loading
// the persisted preference). Has no effect if scope is invalid.
func (m Model) WithScope(scope Scope) Model {
	if scope == ScopeCurrent || scope == ScopeAll || scope == ScopeAwaiting {
		m.scope = scope
		if idx := m.indexCurrent(); idx >= 0 {
			m.cursor = idx
		}
	}
	return m
}

// WithScopeChanged installs a callback invoked whenever the user cycles scope.
func (m Model) WithScopeChanged(h ScopeChangedHandler) Model {
	m.scopeChanged = h
	return m
}

// Scope returns the active scope (used by tests).
func (m Model) Scope() Scope { return m.scope }

func scopeLabel(scope Scope, currentRepo string) string {
	switch scope {
	case ScopeAll:
		return "all projects"
	case ScopeAwaiting:
		return "awaiting input"
	default:
		if strings.TrimSpace(currentRepo) != "" {
			return currentRepo
		}
		return "current project"
	}
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

// scopedSource returns the unfiltered item slice for the active scope.
func (m Model) scopedSource() []Item {
	switch m.scope {
	case ScopeAll, ScopeAwaiting:
		if len(m.itemsAll) > 0 {
			return m.itemsAll
		}
		return m.itemsCurrent
	default:
		return m.itemsCurrent
	}
}

func (m Model) items() []Item {
	src := m.scopedSource()
	if m.scope == ScopeAwaiting {
		filtered := make([]Item, 0, len(src))
		for _, it := range src {
			if strings.EqualFold(strings.TrimSpace(it.Status), "waiting") {
				filtered = append(filtered, it)
			}
		}
		src = filtered
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

func (m Model) Init() tea.Cmd { return scheduleRefreshTick() }

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
	case refreshTickMsg:
		// Background poll: fetch fresh state for status updates from
		// agent hooks. Pause during interactive overlays so we don't
		// race with their own state.
		if m.refresher != nil && !m.busy && !m.progressMode &&
			!m.confirmDelete && !m.filtering &&
			!m.findMode && !m.actionMode &&
			!m.newMenuMode && !m.bookmarkMode && !m.reviewMode &&
			!m.helpMode {
			return m, tea.Batch(m.refresher(), scheduleRefreshTick())
		}
		return m, scheduleRefreshTick()
	case NewWorkspaceDoneMsg:
		if msg.Cancelled {
			m.status = "new: cancelled"
			return m, nil
		}
		if msg.Err != nil {
			m.status = "new: " + msg.Err.Error()
			return m, nil
		}
		if msg.Request != nil {
			return m.startCreateAction(*msg.Request, msg.RepoRoot)
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
	case StateEditDoneMsg:
		if msg.Err != nil {
			m.status = "edit state: " + msg.Err.Error()
		} else {
			m.status = "edit state: done"
		}
		if m.refresher != nil {
			return m, m.refresher()
		}
		return m, nil
	case BookmarksDoneMsg:
		m.busy = false
		if !m.bookmarkMode {
			return m, nil
		}
		m.bookmarkLoading = false
		if msg.Err != nil {
			m.bookmarkMode = false
			m.status = "bookmark: " + msg.Err.Error()
			return m, nil
		}
		if len(msg.Bookmarks) == 0 {
			m.bookmarkMode = false
			m.status = "bookmark: no bookmarks found"
			return m, nil
		}
		m.bookmarks = msg.Bookmarks
		m.bookmarkCursor = 0
		m.bookmarkFiltering = true
		m.bookmarkFilter.Focus()
		m.status = "bookmark: type to filter · enter pick · esc cancel"
		return m, textinput.Blink
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
					if m.deleteTarget.Current {
						m.pendingSelect = Item{ProjectName: m.deleteTarget.ProjectName, WorkspaceName: "default"}
					}
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
		if m.newMenuMode {
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				m.newMenuMode = false
				m.status = "new: cancelled"
				return m, nil
			case "j", "down":
				if m.newMenuCursor < 2 {
					m.newMenuCursor++
				}
				return m, nil
			case "k", "up":
				if m.newMenuCursor > 0 {
					m.newMenuCursor--
				}
				return m, nil
			case "b":
				return m.startBookmarkPicker()
			case "r":
				return m.startReviewFromMenu()
			case "enter":
				switch m.newMenuCursor {
				case 0:
					return m.launchNewForm(NewWorkspaceInitial{})
				case 1:
					return m.startBookmarkPicker()
				case 2:
					return m.startReviewFromMenu()
				}
				return m, nil
			}
			return m, nil
		}
		if m.bookmarkMode {
			if m.bookmarkLoading {
				switch msg.String() {
				case "esc", "ctrl+c":
					m.bookmarkMode = false
					m.bookmarkLoading = false
					m.status = "bookmark: cancelled"
				}
				return m, nil
			}
			switch msg.String() {
			case "esc", "ctrl+c":
				m.bookmarkMode = false
				m.bookmarks = nil
				m.bookmarkCursor = 0
				m.bookmarkFilter.Blur()
				m.bookmarkFilter.SetValue("")
				m.bookmarkFiltering = false
				m.status = "bookmark: cancelled"
				return m, nil
			case "down":
				if m.bookmarkCursor < len(m.filteredBookmarks())-1 {
					m.bookmarkCursor++
				}
				return m, nil
			case "up":
				if m.bookmarkCursor > 0 {
					m.bookmarkCursor--
				}
				return m, nil
			case "enter":
				picks := m.filteredBookmarks()
				if len(picks) == 0 {
					return m, nil
				}
				name := picks[m.bookmarkCursor]
				return m.launchNewForm(NewWorkspaceInitial{Bookmark: name})
			}
			var cmd tea.Cmd
			m.bookmarkFilter, cmd = m.bookmarkFilter.Update(msg)
			if m.bookmarkCursor >= len(m.filteredBookmarks()) {
				m.bookmarkCursor = 0
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
		if m.helpMode {
			switch msg.String() {
			case "?", "esc", "q", "enter":
				m.helpMode = false
			}
			return m, nil
		}
		switch msg.String() {
		case "?":
			m.helpMode = true
			return m, nil
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
			m.scope = (m.scope + 1) % 3
			m.cursor = 0
			m.status = "scope: " + scopeLabel(m.scope, m.currentRepo)
			if m.scopeChanged != nil {
				m.scopeChanged(m.scope)
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
			return m.startQuickAction(ActionLastSession, Item{}, "")
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
			m.newMenuMode = true
			m.newMenuCursor = 0
			m.newMenuRepo = item.RepoRoot
			m.status = "new: choose start (↑/↓ enter · b bookmark · r review · esc cancel)"
			return m, nil
		case ",":
			if m.stateEditor == nil {
				m.status = "edit state: not configured"
				return m, nil
			}
			m.status = "editing workspace-state.json..."
			return m, m.stateEditor()
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

func (m *Model) launchNewForm(initial NewWorkspaceInitial) (tea.Model, tea.Cmd) {
	repo := m.newMenuRepo
	m.newMenuMode = false
	m.bookmarkMode = false
	m.bookmarks = nil
	m.bookmarkCursor = 0
	m.bookmarkFilter.SetValue("")
	if m.newLauncher == nil || strings.TrimSpace(repo) == "" {
		m.status = "new: launcher not configured"
		return *m, nil
	}
	m.status = "new workspace..."
	return *m, m.newLauncher(repo, initial)
}

func (m *Model) startBookmarkPicker() (tea.Model, tea.Cmd) {
	if m.bookmarkFetcher == nil {
		m.status = "bookmark: not configured"
		return *m, nil
	}
	if strings.TrimSpace(m.newMenuRepo) == "" {
		m.status = "bookmark: no repo"
		return *m, nil
	}
	m.newMenuMode = false
	m.bookmarkMode = true
	m.bookmarkLoading = true
	m.bookmarks = nil
	m.bookmarkCursor = 0
	m.bookmarkFilter.SetValue("")
	m.busy = true
	m.status = "bookmark: loading..."
	return *m, tea.Batch(m.spinner.Tick, m.bookmarkFetcher(m.newMenuRepo))
}

func (m *Model) startReviewFromMenu() (tea.Model, tea.Cmd) {
	if m.prFetcher == nil {
		m.status = "review: not configured"
		return *m, nil
	}
	repo := m.newMenuRepo
	if strings.TrimSpace(repo) == "" {
		m.status = "review: no repo"
		return *m, nil
	}
	m.newMenuMode = false
	m.reviewMode = true
	m.reviewLoading = true
	m.reviewPRs = nil
	m.reviewCursor = 0
	m.busy = true
	m.status = "review: loading PRs..."
	return *m, tea.Batch(m.spinner.Tick, m.prFetcher(repo))
}

func (m Model) filteredBookmarks() []string {
	q := strings.ToLower(strings.TrimSpace(m.bookmarkFilter.Value()))
	if q == "" {
		return append([]string(nil), m.bookmarks...)
	}
	out := make([]string, 0, len(m.bookmarks))
	for _, b := range m.bookmarks {
		if strings.Contains(strings.ToLower(b), q) {
			out = append(out, b)
		}
	}
	return out
}

func (m Model) trigger(a Action, arg string) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || m.handler == nil {
		return m, nil
	}
	if isProgressAction(a) {
		return m.startAction(a, item, arg)
	}
	return m.startQuickAction(a, item, arg)
}

func isProgressAction(a Action) bool {
	switch a {
	case ActionDelete, ActionReview, ActionCreateWorkspace, ActionCI, ActionCustom:
		return true
	}
	return false
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

// startQuickAction runs an action without entering progress mode. Used for
// fast operations like summon/switch where a progress UI just causes flicker.
func (m *Model) startQuickAction(a Action, item Item, arg string) (tea.Model, tea.Cmd) {
	m.busy = true
	m.status = fmt.Sprintf("%s %s...", actionLabel(a, arg), item.WorkspaceName)
	handler := m.handler
	dispatch := func() tea.Msg {
		err := handler(ActionRequest{Item: item, Action: a, Arg: arg, Reporter: noopActionReporter{}})
		return actionResultMsg{action: a, arg: arg, item: item, err: err}
	}
	return *m, tea.Batch(m.spinner.Tick, dispatch)
}

type noopActionReporter struct{}

func (noopActionReporter) Step(string) {}
func (noopActionReporter) Log(string)  {}

func (m *Model) startCreateAction(req NewWorkspaceRequest, repoRoot string) (tea.Model, tea.Cmd) {
	if m.handler == nil {
		m.status = "new: handler not configured"
		return *m, nil
	}
	m.busy = true
	m.progressMode = true
	m.progressTitle = "create workspace"
	if strings.TrimSpace(req.Name) != "" {
		m.progressTitle = "create · " + req.Name
	} else if strings.TrimSpace(req.Bookmark) != "" {
		m.progressTitle = "create · " + req.Bookmark
	}
	m.progressSteps = nil
	m.progressLog = nil
	m.progressErr = nil
	m.progressDone = false
	m.progressChan = make(chan progressEvent, 32)
	m.status = "creating workspace..."
	ch := m.progressChan
	handler := m.handler
	item := Item{RepoRoot: repoRoot}
	dispatch := func() tea.Msg {
		reporter := &chanReporter{ch: ch}
		err := handler(ActionRequest{
			Item:      item,
			Action:    ActionCreateWorkspace,
			Workspace: req,
			Reporter:  reporter,
		})
		if ch != nil {
			ch <- progressEvent{kind: progressEventDone, err: err, action: ActionCreateWorkspace, item: item}
			close(ch)
		}
		return nil
	}
	return *m, tea.Batch(m.spinner.Tick, dispatch, waitForProgress(m.progressChan))
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
	case ActionCreateWorkspace:
		return "create"
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
	switch {
	case m.newMenuMode:
		left = m.renderNewMenu(leftWidth)
		right = m.renderNewMenuDetails(rightWidth)
	case m.bookmarkMode:
		left = m.renderBookmarkList(leftWidth)
		right = m.renderBookmarkDetails(rightWidth)
	case m.reviewMode:
		left = m.renderReviewList(leftWidth)
		right = m.renderReviewDetails(rightWidth)
	default:
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
	// Pin the footer to the bottom of the viewport: pad the body up to
	// (height - footer height) before stacking the footer below.
	footerHeight := lipgloss.Height(footer)
	bodyHeight := lipgloss.Height(body)
	pad := m.height - bodyHeight - footerHeight
	if pad < 0 {
		pad = 0
	}
	view := lipgloss.JoinVertical(lipgloss.Left, body, strings.Repeat("\n", pad), footer)
	if m.confirmDelete {
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", m.renderDeleteConfirm())
	}
	if m.helpMode {
		// Center the help box over the existing view as a popover.
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderHelp(m.width))
	}
	return view
}

func (m Model) renderHelp(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render("awp deck — help (?, esc, or enter to close)")

	dot := func(state string, label string) string {
		return statusGlyph(state, false) + "  " + label
	}
	statusLines := []string{
		lipgloss.NewStyle().Bold(true).Render("Agent status (left dot)"),
		dot("working", "working — actively producing output / running a tool"),
		dot("waiting", "waiting — paused on a permission prompt"),
		dot("idle", "idle — alive, sitting at its prompt"),
		dot("starting", "starting — session initializing"),
		dot("exited", "exited — process gone, pane back at a shell"),
	}

	keyRows := [][2]string{
		{"enter", "summon (create or focus the workspace tmux session)"},
		{"a", "open agent window (re-attach without re-prompting)"},
		{"e", "open editor window ($EDITOR)"},
		{"c / C", "review window: tuicr -r @  /  tuicr -r main..@"},
		{"v", "vcs window (jjui)"},
		{"s", "shell window"},
		{"i", "ci window (gh run watch)"},
		{"r", "review a PR"},
		{"x", "user actions menu"},
		{"f", "find: project → workspace easymotion jump"},
		{"/", "filter rows · esc clears"},
		{"P", "cycle scope (current project → all projects → awaiting input)"},
		{"L", "switch to last tmux session"},
		{"R", "relink session"},
		{"D", "delete workspace"},
		{",", "edit global state file in $EDITOR"},
		{"?", "this help"},
		{"q / esc", "quit"},
	}
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	keyLines := []string{lipgloss.NewStyle().Bold(true).Render("Keys")}
	for _, kr := range keyRows {
		keyLines = append(keyLines, fmt.Sprintf("  %s  %s", keyStyle.Width(8).Render(kr[0]), descStyle.Render(kr[1])))
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		strings.Join(statusLines, "\n"),
		"",
		strings.Join(keyLines, "\n"),
	)
	boxWidth := 70
	if width > 0 && width-8 < boxWidth {
		boxWidth = width - 8
	}
	if boxWidth < 40 {
		boxWidth = 40
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("117")).
		Background(lipgloss.Color("236")).
		Padding(1, 2).
		Width(boxWidth).
		Render(body)
}

func (m Model) renderList(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("awp deck")
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("scope: " + scopeLabel(m.scope, m.currentRepo) + "  (P to cycle)")
	rows := []string{title, subtitle, ""}
	items := m.items()
	if len(items) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("No workspaces found."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	projectHints, rowHints := m.findHints()
	// Reserve a fixed prefix slot so workspace rows and project headers
	// don't shift horizontally when find-mode hints (1- or 2-char) appear.
	prefixWidth := 2
	for _, h := range rowHints {
		if w := len(h) + 2; w > prefixWidth {
			prefixWidth = w
		}
	}
	for _, h := range projectHints {
		if w := len(h) + 2; w > prefixWidth {
			prefixWidth = w
		}
	}
	prefixSlot := lipgloss.NewStyle().Width(prefixWidth)
	lastProject := ""
	for i, item := range items {
		dim := m.findMode && m.findStage == findStageWorkspace && item.ProjectName != m.findProject
		if item.ProjectName != lastProject {
			headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
			// Add a top margin between projects, but not above the very first one.
			if lastProject != "" {
				headerStyle = headerStyle.MarginTop(1)
			}
			if m.findMode && m.findStage == findStageWorkspace && item.ProjectName == m.findProject {
				headerStyle = headerStyle.Bold(true).Foreground(lipgloss.Color("117"))
			} else if dim {
				headerStyle = headerStyle.Foreground(lipgloss.Color("238"))
			}
			hintStr := ""
			if hint, ok := projectHints[item.ProjectName]; ok {
				hintStr = renderFindHint(hint)
			}
			header := fmt.Sprintf("%s %s", prefixSlot.Render(hintStr), item.ProjectName)
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
		}
		line := fmt.Sprintf("%s %s %s", prefixSlot.Render(prefix), statusGlyph(item.Status, dim), label)
		rows = append(rows, style.Render(line))
		if prompt := strings.TrimSpace(item.PromptPreview); prompt != "" {
			promptColor := lipgloss.Color("245")
			if dim {
				promptColor = lipgloss.Color("238")
			}
			promptIndent := strings.Repeat(" ", prefixWidth+1)
			rows = append(rows, lipgloss.NewStyle().Width(width).Foreground(promptColor).Render(promptIndent+truncate(prompt, max(8, width-prefixWidth-3))))
		}
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
		",      edit ~/.awp/workspace-state.json in $EDITOR",
		"q      quit deck",
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderNewMenu(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render("new workspace: choose start")
	options := []struct {
		label string
		hint  string
	}{
		{"empty", "empty workspace from current revision"},
		{"bookmark", "pick a jj bookmark to base it on"},
		{"review", "review an open PR"},
	}
	rows := []string{title, ""}
	for i, opt := range options {
		prefix := "  "
		style := lipgloss.NewStyle().Width(width - 1)
		if i == m.newMenuCursor {
			prefix = "› "
			style = style.Bold(true).Foreground(lipgloss.Color("230"))
		}
		quick := ""
		switch i {
		case 1:
			quick = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(" [b]")
		case 2:
			quick = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(" [r]")
		}
		rows = append(rows, style.Render(fmt.Sprintf("%s%s%s", prefix, opt.label, quick)))
		rows = append(rows, lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("245")).Render("   "+opt.hint))
	}
	return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
}

func (m Model) renderNewMenuDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("new workspace")
	lines := []string{
		title, "",
		"Repo: " + m.newMenuRepo, "",
		"Keys:",
		"↑/↓ j/k  navigate",
		"enter    choose",
		"b        bookmark (quick)",
		"r        review   (quick)",
		"esc      cancel",
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderBookmarkList(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render("bookmark: pick one")
	rows := []string{title, ""}
	if m.bookmarkLoading {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("Loading bookmarks..."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	if m.bookmarkFiltering || strings.TrimSpace(m.bookmarkFilter.Value()) != "" {
		rows = append(rows, "/"+m.bookmarkFilter.View(), "")
	}
	picks := m.filteredBookmarks()
	if len(picks) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("No bookmarks match."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	for i, name := range picks {
		prefix := "  "
		style := lipgloss.NewStyle().Width(width - 1)
		if i == m.bookmarkCursor {
			prefix = "› "
			style = style.Bold(true).Foreground(lipgloss.Color("230"))
		}
		rows = append(rows, style.Render(prefix+truncate(name, max(8, width-4))))
	}
	return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
}

func (m Model) renderBookmarkDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("bookmark")
	picks := m.filteredBookmarks()
	current := ""
	if m.bookmarkCursor >= 0 && m.bookmarkCursor < len(picks) {
		current = picks[m.bookmarkCursor]
	}
	lines := []string{title, ""}
	if current != "" {
		lines = append(lines, "Selection: "+current)
	} else {
		lines = append(lines, "Pick a bookmark to base the new workspace on.")
	}
	lines = append(lines, "",
		"Keys:",
		"↑/↓ j/k  navigate",
		"/        filter",
		"enter    select",
		"esc      cancel",
	)
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

// statusGlyph renders a colored ● for an agent status. When dim is true, the
// row is being shown as dimmed (filtered/inactive) and we render a duller
// version of the same color so the glyph still shows but doesn't fight the
// row styling.
func statusGlyph(status string, dim bool) string {
	color := statusColor(status, dim)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("●")
}

func statusColor(status string, dim bool) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		if dim {
			return "65"
		}
		return "82"
	case "waiting":
		if dim {
			return "136"
		}
		return "214"
	case "exited", "error":
		if dim {
			return "131"
		}
		return "203"
	case "starting":
		if dim {
			return "67"
		}
		return "117"
	default: // idle / done / unknown
		if dim {
			return "238"
		}
		return "244"
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
