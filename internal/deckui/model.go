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

// refreshInterval is how often the deck polls for live tmux state
// (sessions, agent pane command). Status updates pushed in by agent
// hooks come through the state-file watcher much sooner than this, so
// the tick is just a backstop for tmux-only transitions.
const refreshInterval = 5 * time.Second

type refreshTickMsg time.Time

func scheduleRefreshTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

type Item struct {
	ProjectName   string
	WorkspaceName string
	Path          string
	RepoRoot      string
	Bookmark      string // jj bookmark associated with this workspace (matches PR headRefName)
	Status        string
	Unread        bool
	PromptPreview string
	HeadDesc      string
	TmuxWindow    string
	SessionName   string
	Active        bool
	Current       bool
}

type Action int

const (
	ActionSummon Action = iota
	ActionRelink
	ActionOpenWindow
	ActionDelete
	ActionDeleteProject
	ActionCI
	ActionLastSession
	ActionReview
	ActionCustom
	ActionCreateWorkspace
)

type UserAction struct {
	Name       string
	Command    string
	Alias      string
	Background bool
	Focus      bool
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

// BookmarkFetcher returns a tea.Cmd that lists deduped bookmarks and emits a
// BookmarksDoneMsg.
type BookmarkFetcher func(repoRoot string) tea.Cmd

// StateEditorLauncher returns a tea.Cmd that suspends the deck and opens the
// global workspace-state.json in $EDITOR.
type StateEditorLauncher func() tea.Cmd

// StateEditDoneMsg is emitted when the state editor exits.
type StateEditDoneMsg struct{ Err error }

// bookmarkPurpose disambiguates the two flows that share the picker: the
// new-workspace form's bookmark seed vs. linking a bookmark to an existing
// workspace (used by the B key in row mode).
type bookmarkPurpose int

const (
	bookmarkPurposeNewWorkspace bookmarkPurpose = iota
	bookmarkPurposeLinkExisting
)

// BookmarkLinkHandler is called when the user picks a bookmark in the
// "link to existing workspace" flow. The handler must persist the chosen
// bookmark to the workspace's stored Entry.Bookmark; the deck then refreshes
// items so the per-row PR glyph picks up the new association on the next
// paint without any gh call (the in-memory PR cache is keyed by repo+headRef,
// not by workspace, so changing the workspace's bookmark is a local lookup).
type BookmarkLinkHandler func(item Item, bookmark string) error

// BookmarksDoneMsg carries the result of an async bookmark fetch.
type BookmarksDoneMsg struct {
	Bookmarks []string
	Err       error
}

type Refresher func() tea.Cmd

// StateChangeWatcher returns a command that emits StateChangedMsg when the
// persisted workspace state file changes. It is an optimization layered on
// top of the periodic refresh tick; callers may leave it nil.
type StateChangeWatcher func() tea.Cmd

// StateChangedMsg is emitted when workspace-state.json is created, replaced,
// renamed, or written. The deck treats it as an immediate refresh hint and
// keeps polling as the correctness fallback.
type StateChangedMsg struct{}

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

// ProjectItem describes a discovered project directory shown in the open
// picker. Path is the absolute repo root.
type ProjectItem struct {
	Path string
	Name string // display label, usually filepath.Base(Path)
}

// ProjectFinder returns a tea.Cmd that discovers projects from configured
// roots and emits a ProjectsDoneMsg.
type ProjectFinder func() tea.Cmd

// ProjectsDoneMsg carries the result of an async project search.
type ProjectsDoneMsg struct {
	Projects []ProjectItem
	Err      error
}

// ProjectOpener is invoked when the user picks a project from the open
// picker. The deck quits afterwards (handler is expected to switch tmux).
type ProjectOpener func(ProjectItem) error

// PRFetcher returns a tea.Cmd that fetches PRs and emits a PRFetchDoneMsg.
// repoRoot scopes the fetch to the selected item's repository.
type PRFetcher func(repoRoot string) tea.Cmd

// PRFetchDoneMsg carries the result of an async PR list fetch.
type PRFetchDoneMsg struct {
	PRs []PRItem
	Err error
}

// PRState mirrors gh's pr.state field for the row-glyph projection.
type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
)

// PRReviewDecision mirrors gh's reviewDecision; "" means no review yet.
type PRReviewDecision string

const (
	PRReviewApproved         PRReviewDecision = "APPROVED"
	PRReviewChangesRequested PRReviewDecision = "CHANGES_REQUESTED"
	PRReviewRequired         PRReviewDecision = "REVIEW_REQUIRED"
)

// PRCIState rolls up statusCheckRollup into one signal. NONE = no checks yet.
type PRCIState string

const (
	PRCINone    PRCIState = "NONE"
	PRCIPending PRCIState = "PENDING"
	PRCIPassing PRCIState = "PASSING"
	PRCIFailing PRCIState = "FAILING"
)

// PRStatus is the per-PR projection consumed by the row glyph.
type PRStatus struct {
	Number         int
	HeadRefName    string
	State          PRState
	IsDraft        bool
	ReviewDecision PRReviewDecision
	CIState        PRCIState
}

// PRStatusFetcher returns a tea.Cmd that fetches PR status for one or more
// repos (one gh call per repo, parallel). The result is delivered as a
// PRStatusDoneMsg keyed by repoRoot.
type PRStatusFetcher func(repoRoots []string) tea.Cmd

// PRStatusDoneMsg carries the result of an async PR-status fetch. ByRepo maps
// repoRoot → headRefName → PRStatus. Errs maps repoRoot → error for any repo
// whose fetch failed; the deck surfaces a non-blocking status-line message,
// but rows for successful repos still render their glyphs.
type PRStatusDoneMsg struct {
	ByRepo    map[string]map[string]PRStatus
	Errs      map[string]error
	FetchedAt time.Time
}

// prStatusMinInterval is the minimum time between consecutive gh fetches for
// the same repo. The throttle guards every entry point that might trigger a
// fetch (cold Init, future refresh keys, future polling).
const prStatusMinInterval = 1 * time.Minute

type findStage int

const (
	findStageProject findStage = iota
	findStageWorkspace
)

const findHintAlphabet = "asdfghjklqwertyuiopzxcvbnm"

type Model struct {
	itemsCurrent       []Item
	itemsAll           []Item
	scope              Scope
	scopeChanged       ScopeChangedHandler
	currentRepo        string
	cursor             int
	width              int
	height             int
	status             string
	handler            Handler
	filterInput        textinput.Model
	filtering          bool
	filter             string
	confirmDelete      bool
	deleteIsProject    bool // confirmDelete branch: project-level delete (typed confirmation)
	deleteInput        textinput.Model
	deleteErr          string
	helpMode           bool
	deleteTarget       Item
	pendingSelect      Item // after next refresh, cursor jumps to this (project, workspace) if present
	findMode           bool
	findStage          findStage
	findProject        string
	findProjectHints   map[string]string
	findProjectLookup  map[string]string
	findProjectPrefix  map[rune]bool
	findRowHints       map[int]string
	findRowLookup      map[string]int
	findRowPrefix      map[rune]bool
	findPendingPrefix  rune
	refresher          Refresher
	refreshing         bool // true while a m.refresher() command is in flight
	stateWatcher       StateChangeWatcher
	prFetcher          PRFetcher
	prStatusFetcher    PRStatusFetcher
	prStatusByRepo     map[string]map[string]PRStatus // repoRoot → headRefName → status
	prStatusFetchedAt  map[string]time.Time           // repoRoot → wall clock of last successful fetch
	bookmarkFetcher    BookmarkFetcher
	stateEditor        StateEditorLauncher
	reviewMode         bool
	reviewLoading      bool
	reviewPRs          []PRItem
	reviewCursor       int
	reviewFiltering    bool
	reviewFilter       string
	newMenuMode        bool
	newMenuCursor      int
	newMenuRepo        string
	bookmarkMode       bool
	bookmarkLoading    bool
	bookmarks          []string
	bookmarkCursor     int
	bookmarkFilter     textinput.Model
	bookmarkFiltering  bool
	bookmarkPurpose    bookmarkPurpose
	bookmarkLinkTarget Item
	bookmarkLinkHandler BookmarkLinkHandler
	userActions        []UserAction
	actionMode         bool
	actionAliasLookup  map[string]UserAction
	spinner            spinner.Model
	busy               bool
	progressMode       bool
	progressTitle      string
	progressSteps      []ProgressStep
	progressLog        []string
	progressErr        error
	progressDone       bool
	progressDoneAction Action
	progressChan       chan progressEvent
	openMode           bool
	openLoading        bool
	openProjects       []ProjectItem
	openCursor         int
	openFilter         textinput.Model
	projectFinder      ProjectFinder
	projectOpener      ProjectOpener
	asyncJobLauncher   AsyncJobLauncher
	jobsListRefresher  JobsListRefresher
	jobCancelHandler   JobCancelHandler
	jobDismissHandler  JobDismissHandler
	jobLogOpener       JobLogOpener
	jobRetryHandler    JobRetryHandler
	jobs               []Job
	jobCounts          JobCounts
	jobsOverlay        bool
	jobsOverlayCursor  int

	// New-workspace form. When newWorkspaceMode is true the deck's
	// View renders the form in place of the row list and Update
	// delegates key handling to the form. See doc.go for the
	// "modal-state inside Model, never a nested tea.Program"
	// architectural constraint.
	newWorkspaceMode bool
	newWorkspaceForm newWorkspaceForm
	newWorkspaceRepo string
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
	of := textinput.New()
	of.Placeholder = "filter projects..."
	of.CharLimit = 96
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	m := Model{
		itemsCurrent:      append([]Item(nil), itemsCurrent...),
		itemsAll:          append([]Item(nil), itemsAll...),
		currentRepo:       currentRepo,
		scope:             defaultInitialScope(itemsAll),
		status:            "",
		findProjectHints:  map[string]string{},
		findProjectLookup: map[string]string{},
		findProjectPrefix: map[rune]bool{},
		findRowHints:      map[int]string{},
		findRowLookup:     map[string]int{},
		findRowPrefix:     map[rune]bool{},
		handler:           handler,
		filterInput:       fi,
		bookmarkFilter:    bf,
		openFilter:        of,
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

func (m Model) WithRefresher(r Refresher) Model {
	m.refresher = r
	return m
}

func (m Model) WithStateChangeWatcher(w StateChangeWatcher) Model {
	m.stateWatcher = w
	return m
}

func (m Model) WithPRFetcher(f PRFetcher) Model {
	m.prFetcher = f
	return m
}

// WithPRStatusFetcher installs the async fetcher used to populate the per-row
// PR glyph. Without it, no PR glyph is rendered.
func (m Model) WithPRStatusFetcher(f PRStatusFetcher) Model {
	m.prStatusFetcher = f
	return m
}

// WithPRStatusSeed primes the per-row PR cache and last-fetched timestamps,
// usually from a persisted ~/.awp/pr-status-cache.json read at startup. The
// 60s refresh throttle uses the seeded timestamps, so a deck reopened within
// a minute of the last fetch will reuse the cached glyphs without re-running
// gh. Pass nil maps to leave the cache empty.
func (m Model) WithPRStatusSeed(byRepo map[string]map[string]PRStatus, fetchedAt map[string]time.Time) Model {
	if byRepo != nil {
		m.prStatusByRepo = byRepo
	}
	if fetchedAt != nil {
		m.prStatusFetchedAt = fetchedAt
	}
	return m
}

func (m Model) WithBookmarkFetcher(f BookmarkFetcher) Model {
	m.bookmarkFetcher = f
	return m
}

// WithBookmarkLinkHandler installs the persistence callback used by the B-key
// bookmark linker. Without it, the linker shows a "not configured" status.
func (m Model) WithBookmarkLinkHandler(h BookmarkLinkHandler) Model {
	m.bookmarkLinkHandler = h
	return m
}

func (m Model) WithStateEditor(l StateEditorLauncher) Model {
	m.stateEditor = l
	return m
}

// WithProjectFinder installs the async finder that discovers projects from
// configured roots. Without it, the `o` key shows an error.
func (m Model) WithProjectFinder(f ProjectFinder) Model {
	m.projectFinder = f
	return m
}

// WithProjectOpener installs the handler invoked when the user picks a
// project from the open screen. The deck quits after a successful pick.
func (m Model) WithProjectOpener(o ProjectOpener) Model {
	m.projectOpener = o
	return m
}

// WithAsyncJobLauncher installs the launcher used for async progress
// actions (today: workspace creation). When set, ActionCreateWorkspace
// dispatches via the launcher instead of running the handler inline,
// and the deck stays interactive throughout.
func (m Model) WithAsyncJobLauncher(l AsyncJobLauncher) Model {
	m.asyncJobLauncher = l
	return m
}

// WithJobsListRefresher installs the function that returns the
// current async job list. Called on every refresh tick to keep the
// tray and overlay up to date.
func (m Model) WithJobsListRefresher(r JobsListRefresher) Model {
	m.jobsListRefresher = r
	return m
}

// WithJobCancelHandler installs the cancel handler used by `c` in
// the jobs overlay.
func (m Model) WithJobCancelHandler(h JobCancelHandler) Model {
	m.jobCancelHandler = h
	return m
}

// WithJobDismissHandler installs the dismiss handler used by `x` in
// the jobs overlay (deletes the record + log file).
func (m Model) WithJobDismissHandler(h JobDismissHandler) Model {
	m.jobDismissHandler = h
	return m
}

// WithJobLogOpener installs the log-opener used by `o` in the jobs
// overlay.
func (m Model) WithJobLogOpener(o JobLogOpener) Model {
	m.jobLogOpener = o
	return m
}

// WithJobRetryHandler installs the retry handler used by `r` in the
// jobs overlay (re-spawns a failed/cancelled/orphaned job from its
// original Spec).
func (m Model) WithJobRetryHandler(h JobRetryHandler) Model {
	m.jobRetryHandler = h
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

func (m Model) findUserAction(name string) (UserAction, bool) {
	for _, ua := range m.userActions {
		if ua.Name == name {
			return ua, true
		}
	}
	return UserAction{}, false
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

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{scheduleRefreshTick()}
	if m.stateWatcher != nil {
		cmds = append(cmds, m.stateWatcher())
	}
	// Kick the first enrichment refresh immediately so tmux-derived
	// decorations (Active/Current/Stale, "exited" status) land within
	// a few tens of ms of first paint instead of waiting refreshInterval.
	if m.refresher != nil {
		cmds = append(cmds, m.refresher())
	}
	if cmd := m.prStatusRefreshCmd(time.Now()); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// prStatusRefreshCmd returns a tea.Cmd that fetches PR status for every repo
// that has at least one non-default workspace AND has not been fetched within
// prStatusMinInterval of now. Returns nil if no repos are due (or no fetcher
// is configured).
func (m Model) prStatusRefreshCmd(now time.Time) tea.Cmd {
	if m.prStatusFetcher == nil {
		return nil
	}
	repos := m.prStatusRepos(now)
	if len(repos) == 0 {
		return nil
	}
	return m.prStatusFetcher(repos)
}

// prStatusRepos returns the deduplicated, throttled list of repo roots that
// should be fetched for PR status: at least one workspace whose Path differs
// from RepoRoot (i.e. not a default-only repo), and the last fetch (if any)
// was at least prStatusMinInterval ago.
func (m Model) prStatusRepos(now time.Time) []string {
	// Prefer itemsAll so we cover every repo the deck knows about, not just
	// the currently-scoped one. The Init pass on a freshly scoped deck still
	// hydrates all visible scopes' glyphs.
	src := m.itemsAll
	if len(src) == 0 {
		src = m.itemsCurrent
	}
	seen := make(map[string]bool)
	nonDefault := make(map[string]bool)
	for _, it := range src {
		repo := strings.TrimSpace(it.RepoRoot)
		if repo == "" {
			continue
		}
		seen[repo] = true
		if strings.TrimSpace(it.Path) != "" && it.Path != repo {
			nonDefault[repo] = true
		}
	}
	out := make([]string, 0, len(nonDefault))
	for repo := range nonDefault {
		if last, ok := m.prStatusFetchedAt[repo]; ok && now.Sub(last) < prStatusMinInterval {
			continue
		}
		out = append(out, repo)
	}
	return out
}

func (m Model) canBackgroundRefresh() bool {
	return m.refresher != nil && !m.busy && !m.progressMode &&
		!m.confirmDelete && !m.filtering &&
		!m.findMode && !m.actionMode &&
		!m.newMenuMode && !m.bookmarkMode && !m.reviewMode &&
		!m.openMode && !m.helpMode && !m.newWorkspaceMode
}

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
		// race with their own state. Jobs are only re-polled while
		// something is in flight — terminal records don't change, so
		// continuing to read disk every refreshInterval is wasted work.
		// Explicit paths (dispatch, dismiss, opening the J overlay)
		// still force a fresh pull. Skip when a previous refresh is
		// still in flight so fsnotify-driven bursts don't pile up.
		var jobsCmd tea.Cmd
		if hasActiveJobs(m.jobs) {
			jobsCmd = refreshJobsListCmd(m.jobsListRefresher)
		}
		if m.canBackgroundRefresh() && !m.refreshing {
			m.refreshing = true
			cmds := []tea.Cmd{m.refresher(), scheduleRefreshTick()}
			if jobsCmd != nil {
				cmds = append(cmds, jobsCmd)
			}
			return m, tea.Batch(cmds...)
		}
		if jobsCmd != nil {
			return m, tea.Batch(jobsCmd, scheduleRefreshTick())
		}
		return m, scheduleRefreshTick()
	case StateChangedMsg:
		cmds := []tea.Cmd{}
		if m.stateWatcher != nil {
			cmds = append(cmds, m.stateWatcher())
		}
		if m.canBackgroundRefresh() && !m.refreshing {
			m.refreshing = true
			cmds = append(cmds, m.refresher())
		}
		return m, tea.Batch(cmds...)
	case jobsListMsg:
		m.jobs = msg.jobs
		m.jobCounts = countsFromJobs(msg.jobs)
		// Keep overlay cursor in range as jobs come and go.
		if m.jobsOverlayCursor >= len(m.jobs) {
			m.jobsOverlayCursor = len(m.jobs) - 1
		}
		if m.jobsOverlayCursor < 0 {
			m.jobsOverlayCursor = 0
		}
		return m, nil
	case JobActionDoneMsg:
		if msg.Err != nil {
			m.status = msg.Kind + ": " + msg.Err.Error()
		} else {
			m.status = msg.Kind + ": " + msg.JobID
		}
		// Refresh the jobs list immediately so the overlay reflects
		// the action without waiting for the next tick.
		return m, refreshJobsListCmd(m.jobsListRefresher)
	case asyncJobDispatchedMsg:
		if msg.err != nil {
			m.status = "create: " + msg.err.Error()
			return m, nil
		}
		// Kick off a fresh tray refresh so the user sees the new
		// "running" count immediately rather than waiting up to 2 s
		// for the next tick.
		return m, refreshJobsListCmd(m.jobsListRefresher)
	case promptEditedMsg:
		// Editor exec returns here after the user finishes editing the
		// new-workspace prompt in $EDITOR. Route to the form so it can
		// stash the edited value (or surface the error). When the form
		// isn't open we drop the message — the editor exec only fires
		// from inside the form, so a stale message here means the user
		// cancelled out before the editor returned.
		if !m.newWorkspaceMode {
			return m, nil
		}
		return m.dispatchNewWorkspaceForm(msg)
	case NewWorkspaceDoneMsg:
		// Legacy message from the now-removed nested-tea.Program form.
		// Kept for any in-flight callers; the inline form bypasses this
		// path entirely.
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
			m.refreshing = true
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
	case ProjectsDoneMsg:
		m.busy = false
		if !m.openMode {
			return m, nil
		}
		m.openLoading = false
		if msg.Err != nil {
			m.openMode = false
			m.status = "open: " + msg.Err.Error()
			return m, nil
		}
		if len(msg.Projects) == 0 {
			m.openMode = false
			m.status = "open: no projects found (configure deck.project_roots)"
			return m, nil
		}
		m.openProjects = msg.Projects
		m.openCursor = 0
		m.openFilter.Focus()
		m.status = "open: type to filter · enter pick · esc cancel"
		return m, textinput.Blink
	case PRStatusDoneMsg:
		if m.prStatusByRepo == nil {
			m.prStatusByRepo = make(map[string]map[string]PRStatus)
		}
		if m.prStatusFetchedAt == nil {
			m.prStatusFetchedAt = make(map[string]time.Time)
		}
		fetchedAt := msg.FetchedAt
		if fetchedAt.IsZero() {
			fetchedAt = time.Now()
		}
		for repo, byHead := range msg.ByRepo {
			m.prStatusByRepo[repo] = byHead
			m.prStatusFetchedAt[repo] = fetchedAt
		}
		if n := len(msg.Errs); n > 0 {
			m.status = fmt.Sprintf("PR status: %d repos failed", n)
		}
		return m, nil
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
		m.reviewPRs = msg.PRs
		m.reviewCursor = 0
		if len(msg.PRs) == 0 {
			m.status = "review: no open PRs (esc to cancel)"
		} else {
			m.status = "review: select PR (enter confirm, / filter, esc cancel)"
		}
		return m, nil
	case refreshDoneMsg:
		m.refreshing = false
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
					m.refreshing = true
					return m, m.refresher()
				}
				return m, nil
			}
			return m, nil
		}
		if m.newWorkspaceMode {
			return m.dispatchNewWorkspaceForm(msg)
		}
		if m.confirmDelete {
			if m.deleteIsProject {
				switch msg.String() {
				case "esc", "ctrl+c":
					m.confirmDelete = false
					m.deleteIsProject = false
					m.deleteInput.Blur()
					m.deleteInput.SetValue("")
					m.deleteErr = ""
					m.status = "delete project: cancelled"
					return m, tea.ClearScreen
				case "enter":
					typed := strings.TrimSpace(m.deleteInput.Value())
					if typed != m.deleteTarget.ProjectName {
						m.deleteErr = "project name didn't match"
						return m, nil
					}
					m.confirmDelete = false
					m.deleteIsProject = false
					m.deleteInput.Blur()
					m.deleteInput.SetValue("")
					m.deleteErr = ""
					if m.handler == nil {
						m.status = "delete project: handler not configured"
						return m, tea.ClearScreen
					}
					updated, cmd := m.startAction(ActionDeleteProject, m.deleteTarget, "")
					return updated, batchCmds(cmd, tea.ClearScreen)
				}
				var cmd tea.Cmd
				m.deleteInput, cmd = m.deleteInput.Update(msg)
				m.deleteErr = ""
				return m, cmd
			}
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
				// tea.ClearScreen on modal exit so the row list's
				// first frame after filtering closes overwrites every
				// cell, not just lines the renderer's per-line diff
				// thinks changed. See doc.go.
				return m, tea.ClearScreen
			case "enter":
				m.filtering = false
				m.filterInput.Blur()
				m.filter = m.filterInput.Value()
				m.cursor = 0
				return m, tea.ClearScreen
			}
			beforeCount := len(m.items())
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.filter = m.filterInput.Value()
			afterCount := len(m.items())
			if m.cursor >= afterCount {
				m.cursor = 0
			}
			// Only force a full repaint when the visible row count
			// actually changes — clearing on every keystroke flickers.
			// When the row list shrinks, rows that fall out the bottom
			// otherwise bleed through the renderer's per-line diff.
			if beforeCount != afterCount {
				return m, batchCmds(cmd, tea.ClearScreen)
			}
			return m, cmd
		}
		if m.newMenuMode {
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				m.newMenuMode = false
				m.status = "new: cancelled"
				return m, tea.ClearScreen
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
		if m.openMode {
			if m.openLoading {
				switch msg.String() {
				case "esc", "ctrl+c":
					m.openMode = false
					m.openLoading = false
					m.status = "open: cancelled"
				}
				return m, nil
			}
			switch msg.String() {
			case "esc", "ctrl+c":
				m.openMode = false
				m.openProjects = nil
				m.openCursor = 0
				m.openFilter.Blur()
				m.openFilter.SetValue("")
				m.status = "open: cancelled"
				return m, nil
			case "down", "ctrl+n":
				if m.openCursor < len(m.filteredProjects())-1 {
					m.openCursor++
				}
				return m, nil
			case "up", "ctrl+p":
				if m.openCursor > 0 {
					m.openCursor--
				}
				return m, nil
			case "enter":
				picks := m.filteredProjects()
				if len(picks) == 0 || m.projectOpener == nil {
					return m, nil
				}
				pick := picks[m.openCursor]
				err := m.projectOpener(pick)
				if err != nil {
					m.status = "open: " + err.Error()
					return m, nil
				}
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.openFilter, cmd = m.openFilter.Update(msg)
			if m.openCursor >= len(m.filteredProjects()) {
				m.openCursor = 0
			}
			return m, cmd
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
			// Filter-mode loop: keys flow into the textinput so the user can
			// type freely. Arrows are intercepted before the input sees them
			// so list navigation still works while filtering (fzf-style),
			// since the textinput would otherwise treat them as in-string
			// cursor moves. Enter commits the filter; esc clears it.
			if m.bookmarkFiltering {
				switch msg.String() {
				case "esc":
					m.bookmarkFiltering = false
					m.bookmarkFilter.Blur()
					m.bookmarkFilter.SetValue("")
					m.bookmarkCursor = 0
					return m, nil
				case "enter":
					// Enter while filtering selects the highlighted row
					// rather than just committing — that's the behavior
					// users expect from fuzzy pickers and avoids the
					// double-enter "commit, then pick" friction.
					picks := m.filteredBookmarks()
					if len(picks) == 0 {
						return m, nil
					}
					return m.acceptBookmarkSelection(picks[m.bookmarkCursor])
				case "up", "ctrl+p", "ctrl+k":
					if m.bookmarkCursor > 0 {
						m.bookmarkCursor--
					}
					return m, nil
				case "down", "ctrl+n", "ctrl+j":
					if m.bookmarkCursor < len(m.filteredBookmarks())-1 {
						m.bookmarkCursor++
					}
					return m, nil
				}
				var cmd tea.Cmd
				m.bookmarkFilter, cmd = m.bookmarkFilter.Update(msg)
				if m.bookmarkCursor >= len(m.filteredBookmarks()) {
					m.bookmarkCursor = 0
				}
				return m, cmd
			}
			// Navigation loop.
			switch msg.String() {
			case "esc", "ctrl+c":
				// First esc clears a committed filter; second esc closes
				// the picker. Matches the review picker.
				if strings.TrimSpace(m.bookmarkFilter.Value()) != "" && msg.String() == "esc" {
					m.bookmarkFilter.SetValue("")
					m.bookmarkCursor = 0
					return m, nil
				}
				m.bookmarkMode = false
				m.bookmarks = nil
				m.bookmarkCursor = 0
				m.bookmarkFilter.Blur()
				m.bookmarkFilter.SetValue("")
				m.bookmarkFiltering = false
				m.bookmarkPurpose = bookmarkPurposeNewWorkspace
				m.bookmarkLinkTarget = Item{}
				m.status = "bookmark: cancelled"
				return m, nil
			case "/":
				m.bookmarkFiltering = true
				m.bookmarkFilter.Focus()
				m.bookmarkFilter.SetCursor(len(m.bookmarkFilter.Value()))
				return m, textinput.Blink
			case "j", "down":
				if m.bookmarkCursor < len(m.filteredBookmarks())-1 {
					m.bookmarkCursor++
				}
				return m, nil
			case "k", "up":
				if m.bookmarkCursor > 0 {
					m.bookmarkCursor--
				}
				return m, nil
			case "enter":
				picks := m.filteredBookmarks()
				if len(picks) == 0 {
					return m, nil
				}
				return m.acceptBookmarkSelection(picks[m.bookmarkCursor])
			}
			return m, nil
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
			if m.reviewFiltering {
				switch msg.String() {
				case "esc":
					m.reviewFiltering = false
					m.filterInput.Blur()
					m.reviewFilter = ""
					m.filterInput.SetValue("")
					m.reviewCursor = 0
					return m, nil
				case "enter":
					m.reviewFiltering = false
					m.filterInput.Blur()
					m.reviewFilter = m.filterInput.Value()
					m.reviewCursor = 0
					return m, nil
				}
				var cmd tea.Cmd
				m.filterInput, cmd = m.filterInput.Update(msg)
				m.reviewFilter = m.filterInput.Value()
				if m.reviewCursor >= len(m.filteredReviewPRs()) {
					m.reviewCursor = 0
				}
				return m, cmd
			}
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				if m.reviewFilter != "" && msg.String() == "esc" {
					m.reviewFilter = ""
					m.filterInput.SetValue("")
					m.reviewCursor = 0
					return m, nil
				}
				m.reviewMode = false
				m.reviewPRs = nil
				m.reviewCursor = 0
				m.reviewFilter = ""
				m.filterInput.SetValue("")
				m.status = "review: cancelled"
				return m, nil
			case "/":
				m.reviewFiltering = true
				m.filterInput.Focus()
				m.filterInput.SetValue(m.reviewFilter)
				m.filterInput.SetCursor(len(m.reviewFilter))
				return m, textinput.Blink
			case "j", "down":
				prs := m.filteredReviewPRs()
				if m.reviewCursor < len(prs)-1 {
					m.reviewCursor++
				}
				return m, nil
			case "k", "up":
				if m.reviewCursor > 0 {
					m.reviewCursor--
				}
				return m, nil
			case "enter":
				prs := m.filteredReviewPRs()
				if len(prs) == 0 || m.handler == nil {
					return m, nil
				}
				if m.reviewCursor < 0 || m.reviewCursor >= len(prs) {
					return m, nil
				}
				pr := prs[m.reviewCursor]
				item, _ := m.selected()
				m.reviewMode = false
				m.reviewFilter = ""
				m.filterInput.SetValue("")
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
		if m.jobsOverlay {
			return m.updateJobsOverlay(msg)
		}
		switch msg.String() {
		case "?":
			m.helpMode = true
			return m, nil
		case "J":
			m.jobsOverlay = true
			m.jobsOverlayCursor = 0
			// Force an immediate refresh so the overlay isn't blank
			// for up to 2 s after opening.
			return m, refreshJobsListCmd(m.jobsListRefresher)
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
			// tea.ClearScreen on modal entry; see doc.go and the
			// matching tea.ClearScreen on exit above.
			return m, tea.ClearScreen
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
			return m, tea.ClearScreen
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
			m.deleteErr = ""
			if strings.TrimSpace(item.WorkspaceName) == "default" {
				// "Deleting" the default workspace is reinterpreted as
				// deleting the whole project from the deck: every
				// non-default workspace under this repo is removed and
				// the project is dropped from workspace state. The
				// default jj workspace itself stays. Require typing
				// the project name to confirm — it's a bigger blast
				// radius than a single-workspace delete.
				m.deleteIsProject = true
				ti := textinput.New()
				ti.Placeholder = item.ProjectName
				ti.CharLimit = 128
				ti.Focus()
				m.deleteInput = ti
				m.status = fmt.Sprintf("delete project %q?", item.ProjectName)
				return m, textinput.Blink
			}
			m.deleteIsProject = false
			m.status = fmt.Sprintf("delete %s? [y/N]", item.WorkspaceName)
			return m, nil
		case "R":
			return m.trigger(ActionRelink, "")
		case "B":
			item, ok := m.selected()
			if !ok {
				m.status = "link: select a workspace row"
				return m, nil
			}
			return m.startBookmarkLinker(item)
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
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.RepoRoot) == "" {
				m.status = "new: select a row with a known repo"
				return m, nil
			}
			m.newMenuMode = true
			m.newMenuCursor = 0
			m.newMenuRepo = item.RepoRoot
			m.status = "new: choose start (↑/↓ enter · b bookmark · r review · esc cancel)"
			// tea.ClearScreen forces Bubble Tea's renderer to drop its
			// previous-frame buffer and rewrite every cell on the next
			// View. Required when entering a modal whose layout is
			// narrower than the row list — otherwise the renderer's
			// per-line diff skips redrawing rows whose left columns are
			// occupied by stale row-list content. See doc.go for the
			// "modal-state, full repaint on entry" pattern.
			return m, tea.ClearScreen
		case "o":
			if m.projectFinder == nil {
				m.status = "open: not configured (set deck.project_roots in config)"
				return m, nil
			}
			m.openMode = true
			m.openLoading = true
			m.openProjects = nil
			m.openCursor = 0
			m.openFilter.SetValue("")
			m.busy = true
			m.status = "open: scanning project roots..."
			return m, tea.Batch(m.spinner.Tick, m.projectFinder())
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

// launchNewForm enters inline new-workspace-form mode. The form is a
// state of this Model (see doc.go); we do not nest a tea.Program. The
// repo root collected from the row selection is stashed so submit can
// dispatch a create job through the existing async-job path.
func (m *Model) launchNewForm(initial NewWorkspaceInitial) (tea.Model, tea.Cmd) {
	repo := m.newMenuRepo
	m.newMenuMode = false
	m.bookmarkMode = false
	m.bookmarks = nil
	m.bookmarkCursor = 0
	m.bookmarkFilter.SetValue("")
	if strings.TrimSpace(repo) == "" {
		m.status = "new: select a row with a known repo"
		return *m, nil
	}
	m.newWorkspaceMode = true
	m.newWorkspaceRepo = repo
	m.newWorkspaceForm = newNewWorkspaceForm(initial)
	m.status = "new workspace..."
	// tea.ClearScreen so the renderer drops its previous-frame buffer
	// and the form's first paint overwrites every cell, including
	// columns the deck row list (or the new-menu) wrote that the form
	// doesn't. See doc.go.
	return *m, tea.Batch(textinput.Blink, tea.ClearScreen)
}

// dispatchNewWorkspaceForm forwards a message to the inline form and
// acts on the form's returned action. Submit dispatches a create job;
// cancel and editor exec leave the form open with the form's own
// state updated.
func (m Model) dispatchNewWorkspaceForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd, action := m.newWorkspaceForm.update(msg)
	m.newWorkspaceForm = form
	switch action {
	case newFormActionCancel:
		m.newWorkspaceMode = false
		m.newWorkspaceRepo = ""
		m.newWorkspaceForm = newWorkspaceForm{}
		m.status = "new: cancelled"
		// tea.ClearScreen on every modal exit so the row list's first
		// frame after the modal closes overwrites every cell, not just
		// the lines the renderer thinks changed.
		return m, batchCmds(cmd, tea.ClearScreen)
	case newFormActionSubmit:
		req := form.request()
		repo := m.newWorkspaceRepo
		m.newWorkspaceMode = false
		m.newWorkspaceRepo = ""
		m.newWorkspaceForm = newWorkspaceForm{}
		updated, dispatchCmd := m.startCreateAction(req, repo)
		return updated, batchCmds(cmd, dispatchCmd, tea.ClearScreen)
	}
	return m, cmd
}

// batchCmds combines non-nil tea.Cmds. tea.Batch(nil, ...) panics in
// some Bubble Tea versions, so we filter first.
func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	out := cmds[:0]
	for _, c := range cmds {
		if c != nil {
			out = append(out, c)
		}
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	default:
		return tea.Batch(out...)
	}
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
	m.bookmarkPurpose = bookmarkPurposeNewWorkspace
	m.bookmarkLoading = true
	m.bookmarks = nil
	m.bookmarkCursor = 0
	m.bookmarkFilter.Blur()
	m.bookmarkFilter.SetValue("")
	m.bookmarkFiltering = false
	m.busy = true
	m.status = "bookmark: loading..."
	return *m, tea.Batch(m.spinner.Tick, m.bookmarkFetcher(m.newMenuRepo))
}

// acceptBookmarkSelection branches on bookmarkPurpose to either feed the
// chosen name to the new-workspace form or persist it via BookmarkLinkHandler.
// Shared between filter-mode (enter selects directly) and nav-mode (enter
// after committing a filter) so the two paths can't diverge.
func (m *Model) acceptBookmarkSelection(name string) (tea.Model, tea.Cmd) {
	switch m.bookmarkPurpose {
	case bookmarkPurposeLinkExisting:
		target := m.bookmarkLinkTarget
		m.bookmarkMode = false
		m.bookmarks = nil
		m.bookmarkCursor = 0
		m.bookmarkFilter.Blur()
		m.bookmarkFilter.SetValue("")
		m.bookmarkFiltering = false
		m.bookmarkPurpose = bookmarkPurposeNewWorkspace
		m.bookmarkLinkTarget = Item{}
		if m.bookmarkLinkHandler == nil {
			m.status = "link: not configured"
			return *m, nil
		}
		if err := m.bookmarkLinkHandler(target, name); err != nil {
			m.status = "link: " + err.Error()
			return *m, nil
		}
		m.status = fmt.Sprintf("linked %s → %s", target.WorkspaceName, name)
		// Explicit link is a "tell me now" action — bypass the 60s
		// throttle so a freshly-opened PR shows up without making the
		// user wait or close+reopen the deck. Drop just this repo's
		// fetchedAt so other repos are unaffected.
		if m.prStatusFetchedAt != nil && strings.TrimSpace(target.RepoRoot) != "" {
			delete(m.prStatusFetchedAt, target.RepoRoot)
		}
		cmds := []tea.Cmd{}
		if m.refresher != nil {
			cmds = append(cmds, m.refresher())
		}
		if cmd := m.prStatusRefreshCmd(time.Now()); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if len(cmds) == 0 {
			return *m, nil
		}
		return *m, tea.Batch(cmds...)
	}
	return m.launchNewForm(NewWorkspaceInitial{Bookmark: name})
}

// startBookmarkLinker opens the same fuzzy picker but routes the selection to
// the BookmarkLinkHandler (writing Entry.Bookmark) instead of the new-workspace
// form. Used by the row-mode `B` key to backfill workspaces whose bookmark
// isn't already on file.
func (m *Model) startBookmarkLinker(target Item) (tea.Model, tea.Cmd) {
	if m.bookmarkFetcher == nil {
		m.status = "link: bookmark fetcher not configured"
		return *m, nil
	}
	if strings.TrimSpace(target.RepoRoot) == "" {
		m.status = "link: select a row with a known repo"
		return *m, nil
	}
	m.bookmarkMode = true
	m.bookmarkPurpose = bookmarkPurposeLinkExisting
	m.bookmarkLinkTarget = target
	m.bookmarkLoading = true
	m.bookmarks = nil
	m.bookmarkCursor = 0
	m.bookmarkFilter.Blur()
	m.bookmarkFilter.SetValue("")
	m.bookmarkFiltering = false
	m.busy = true
	m.status = "link: loading bookmarks..."
	return *m, tea.Batch(m.spinner.Tick, m.bookmarkFetcher(target.RepoRoot))
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

func (m Model) filteredProjects() []ProjectItem {
	q := strings.ToLower(strings.TrimSpace(m.openFilter.Value()))
	if q == "" {
		return append([]ProjectItem(nil), m.openProjects...)
	}
	out := make([]ProjectItem, 0, len(m.openProjects))
	for _, p := range m.openProjects {
		if fuzzyMatch(strings.ToLower(p.Name), q) || fuzzyMatch(strings.ToLower(p.Path), q) {
			out = append(out, p)
		}
	}
	return out
}

// fuzzyMatch returns true if every rune of needle appears in haystack in
// order (subsequence match). Used by the project picker so typing
// "myrepo" matches "github.com/me/myrepo".
func fuzzyMatch(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	hi := 0
	for _, nr := range needle {
		found := false
		for hi < len(haystack) {
			hr, size := utf8DecodeRune(haystack[hi:])
			hi += size
			if hr == nr {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func utf8DecodeRune(s string) (rune, int) {
	for i, r := range s {
		_ = i
		return r, len(string(r))
	}
	return 0, 0
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
	case ActionDelete, ActionDeleteProject, ActionReview, ActionCreateWorkspace, ActionCI, ActionCustom:
		return true
	}
	return false
}

func (m *Model) startAction(a Action, item Item, arg string) (tea.Model, tea.Cmd) {
	// Workspace lifecycle actions (create, delete) run async via the
	// jobs subsystem. Window-summoning actions (review, ci, custom
	// user actions) stay in-process so they remain interactive and
	// don't surprise the user with a detached subprocess opening tmux
	// windows behind their back.
	if m.asyncJobLauncher != nil {
		switch a {
		case ActionDelete:
			return m.startAsyncAction(AsyncJobSpec{
				Action:        "delete",
				Title:         "delete · " + item.WorkspaceName,
				RepoRoot:      item.RepoRoot,
				WorkspaceName: item.WorkspaceName,
				WorkspacePath: item.Path,
				Arg:           arg,
			})
		case ActionDeleteProject:
			return m.startAsyncAction(AsyncJobSpec{
				Action:        "delete-project",
				Title:         "delete project · " + item.ProjectName,
				RepoRoot:      item.RepoRoot,
				WorkspaceName: item.WorkspaceName,
				WorkspacePath: item.Path,
				Arg:           arg,
			})
		case ActionReview:
			return m.startAsyncAction(AsyncJobSpec{
				Action:        "review",
				Title:         "review · PR " + arg,
				RepoRoot:      item.RepoRoot,
				WorkspaceName: item.WorkspaceName,
				WorkspacePath: item.Path,
				Arg:           arg,
			})
		case ActionCustom:
			if ua, ok := m.findUserAction(arg); ok && ua.Background {
				return m.startAsyncAction(AsyncJobSpec{
					Action:        "custom",
					Title:         arg + " · " + item.WorkspaceName,
					RepoRoot:      item.RepoRoot,
					WorkspaceName: item.WorkspaceName,
					WorkspacePath: item.Path,
					Arg:           arg,
				})
			}
		}
	}
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

// startAsyncAction dispatches a non-create progress action through
// the async launcher. No modal progress mode, no tea.Quit.
func (m *Model) startAsyncAction(spec AsyncJobSpec) (tea.Model, tea.Cmd) {
	launcher := m.asyncJobLauncher
	m.status = statusToastFor(spec)
	dispatch := func() tea.Msg {
		return asyncJobDispatchedMsg{spec: spec, err: launcher(spec)}
	}
	return *m, dispatch
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
	if m.asyncJobLauncher != nil {
		return m.startAsyncCreateAction(req, repoRoot)
	}
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

// startAsyncCreateAction dispatches a workspace-create job to the
// detached subprocess via the async launcher. The deck stays
// interactive: no modal progress mode, no tea.Quit. The new
// workspace appears in the deck list once the subprocess finishes
// via the existing 2 s refresher.
func (m *Model) startAsyncCreateAction(req NewWorkspaceRequest, repoRoot string) (tea.Model, tea.Cmd) {
	title := "create workspace"
	if n := strings.TrimSpace(req.Name); n != "" {
		title = "create · " + n
	} else if b := strings.TrimSpace(req.Bookmark); b != "" {
		title = "create · " + b
	}
	spec := AsyncJobSpec{
		Action:   "create-workspace",
		RepoRoot: repoRoot,
		Title:    title,
		Name:     strings.TrimSpace(req.Name),
		Bookmark: strings.TrimSpace(req.Bookmark),
		Prompt:   strings.TrimSpace(req.Prompt),
	}
	launcher := m.asyncJobLauncher
	dispatch := func() tea.Msg {
		err := launcher(spec)
		return asyncJobDispatchedMsg{spec: spec, err: err}
	}
	m.status = statusToastFor(spec)
	return *m, dispatch
}

// asyncJobDispatchedMsg is emitted once the async launcher returns
// (the subprocess has been forked, or spawn failed before it could
// be).
type asyncJobDispatchedMsg struct {
	spec AsyncJobSpec
	err  error
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
	case ActionDeleteProject:
		return "delete project"
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
	if m.newWorkspaceMode {
		// Render the inline new-workspace form across the entire
		// viewport. Same pattern as progressMode above; both replace
		// the deck body wholesale when the modal owns the screen.
		return m.newWorkspaceForm.view(m.width, m.height)
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
	case m.openMode:
		left = m.renderOpenList(leftWidth)
		right = m.renderOpenDetails(rightWidth)
	case m.bookmarkMode:
		// Full-width like the review picker. JoinHorizontal between a
		// short loading-state left pane and a tall static right pane
		// caused painting bleed during load (lipgloss pads with empty
		// rows, not space-filled rows, and JoinVertical's pad newlines
		// don't clear residue). Single-column avoids the issue and
		// gives the list more room.
		left = m.renderBookmarkList(m.width)
		right = ""
	case m.reviewMode:
		left = m.renderReviewList(m.width)
		right = ""
	default:
		left = m.renderList(leftWidth)
		right = m.renderDetails(rightWidth)
	}
	var body string
	if right == "" {
		body = left
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, "\n", right)
	}

	statusText := m.status
	if m.busy {
		statusText = m.spinner.View() + " " + m.status
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	var rightSeg string
	switch {
	case m.filtering:
		rightSeg = "/" + m.filterInput.View()
	case m.findMode || m.actionMode:
		rightSeg = dim.Render(m.status + " (esc cancel)")
	case m.filter != "":
		rightSeg = dim.Render(fmt.Sprintf("filter: %q · %s", m.filter, statusText))
	default:
		rightSeg = dim.Render(statusText)
	}
	footer := composeStatusBar(m.jobCounts, rightSeg, m.width)
	footerHeight := lipgloss.Height(footer)
	bodyHeight := lipgloss.Height(body)
	pad := m.height - bodyHeight - footerHeight
	if pad < 0 {
		pad = 0
	}
	// Pad rows must be space-filled (width chars wide) rather than bare
	// "\n"s. Bare newlines don't overwrite columns from the previous frame,
	// so when a modal shrinks the body (e.g. the bookmark picker's short
	// loading state) the prior frame's tall content bleeds through. Padding
	// with explicit blank rows of full width gives the diff renderer
	// something to clear with.
	blankRow := strings.Repeat(" ", m.width)
	padBlock := ""
	if pad > 0 {
		blanks := make([]string, pad)
		for i := range blanks {
			blanks[i] = blankRow
		}
		padBlock = strings.Join(blanks, "\n")
	}
	parts := []string{body, padBlock, footer}
	view := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if m.confirmDelete {
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderDeleteConfirm())
	}
	if m.helpMode {
		// Center the help box over the existing view as a popover.
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderHelp(m.width))
	}
	if m.jobsOverlay {
		view = renderJobsOverlay(m.jobs, m.jobsOverlayCursor, m.width, m.height)
	}
	return view
}

// updateJobsOverlay handles keypresses while the J overlay is active.
// Closes on esc/J/q, navigates with j/k/arrows, dispatches c (cancel)
// / x (dismiss) / o (open log) via the configured handlers.
func (m Model) updateJobsOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "J":
		m.jobsOverlay = false
		return m, nil
	case "j", "down":
		if m.jobsOverlayCursor < len(m.jobs)-1 {
			m.jobsOverlayCursor++
		}
		return m, nil
	case "k", "up":
		if m.jobsOverlayCursor > 0 {
			m.jobsOverlayCursor--
		}
		return m, nil
	case "g":
		m.jobsOverlayCursor = 0
		return m, nil
	case "G":
		m.jobsOverlayCursor = len(m.jobs) - 1
		if m.jobsOverlayCursor < 0 {
			m.jobsOverlayCursor = 0
		}
		return m, nil
	case "c":
		if len(m.jobs) == 0 || m.jobCancelHandler == nil {
			return m, nil
		}
		j := m.jobs[m.jobsOverlayCursor]
		if j.Status.IsTerminal() {
			m.status = "cancel: job already finished"
			return m, nil
		}
		handler := m.jobCancelHandler
		id := j.ID
		return m, func() tea.Msg {
			return JobActionDoneMsg{JobID: id, Kind: "cancel", Err: handler(id)}
		}
	case "x":
		if len(m.jobs) == 0 || m.jobDismissHandler == nil {
			return m, nil
		}
		j := m.jobs[m.jobsOverlayCursor]
		if !j.Status.IsTerminal() {
			m.status = "dismiss: cancel a running job first"
			return m, nil
		}
		handler := m.jobDismissHandler
		id := j.ID
		return m, func() tea.Msg {
			return JobActionDoneMsg{JobID: id, Kind: "dismiss", Err: handler(id)}
		}
	case "r":
		if len(m.jobs) == 0 || m.jobRetryHandler == nil {
			return m, nil
		}
		j := m.jobs[m.jobsOverlayCursor]
		if !j.Status.IsTerminal() {
			m.status = "retry: job is still running"
			return m, nil
		}
		if j.Status == JobDone {
			m.status = "retry: job already succeeded"
			return m, nil
		}
		handler := m.jobRetryHandler
		id := j.ID
		return m, func() tea.Msg {
			return JobActionDoneMsg{JobID: id, Kind: "retry", Err: handler(id)}
		}
	case "o":
		if len(m.jobs) == 0 || m.jobLogOpener == nil {
			return m, nil
		}
		j := m.jobs[m.jobsOverlayCursor]
		return m, m.jobLogOpener(j.ID)
	case "y":
		if len(m.jobs) == 0 {
			return m, nil
		}
		j := m.jobs[m.jobsOverlayCursor]
		text := jobDetailsForCopy(j)
		if err := writeSystemClipboard(text); err != nil {
			m.status = "copy: " + err.Error()
		} else {
			m.status = fmt.Sprintf("copied %d bytes to clipboard", len(text))
		}
		return m, nil
	}
	return m, nil
}

func (m Model) renderHelp(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render("awp deck — help (?, esc, or enter to close)")

	dot := func(state string, unread bool, label string) string {
		return statusGlyph(state, false, unread) + "  " + label
	}
	statusLines := []string{
		lipgloss.NewStyle().Bold(true).Render("Agent status (left dot)"),
		dot("working", false, "working — actively producing output / running a tool"),
		dot("waiting", true, "waiting — paused on a permission prompt"),
		dot("idle", true, "notified — agent finished a turn (clears on summon)"),
		dot("exited", true, "exited — process gone, pane back at a shell"),
	}

	prDot := func(g, color, label string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(g) + "  " + label
	}
	prLines := []string{
		lipgloss.NewStyle().Bold(true).Render("PR status (right glyph)"),
		prDot(prGlyphOpen, "117", "open — PR is open, no review yet"),
		prDot(prGlyphDraft, "245", "draft — PR is in draft"),
		prDot(prGlyphApproved, "82", "approved — at least one approving review"),
		prDot(prGlyphCIPend, "214", "CI pending — checks in flight"),
		prDot(prGlyphCIFail, "203", "CI failed — at least one check failing"),
		prDot(prGlyphMerged, "245", "merged — safe to delete this workspace"),
		prDot(prGlyphClosed, "244", "closed — PR closed without merging"),
	}

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	keyLines := []string{lipgloss.NewStyle().Bold(true).Render("Keys")}
	for i, g := range deckKeyGroups() {
		if i > 0 {
			keyLines = append(keyLines, "")
		}
		keyLines = append(keyLines, headerStyle.Render(g.Title))
		for _, kr := range g.Keys {
			keyLines = append(keyLines, fmt.Sprintf("  %s  %s", keyStyle.Width(8).Render(kr[0]), descStyle.Render(kr[1])))
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		strings.Join(statusLines, "\n"),
		"",
		strings.Join(prLines, "\n"),
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
	// Reserve a fixed-width prefix slot at all times so workspace rows
	// and project headers don't shift horizontally between modes (no
	// find / 1-char hint / 2-char hint).
	const prefixWidth = 4
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
		// Style the label segment directly. The dot is rendered with its
		// own ANSI color sequence ending in a reset, which would otherwise
		// truncate any outer Foreground/Bold applied to the whole row —
		// that's why selected rows containing a status dot weren't
		// highlighting past the dot.
		labelStyle := lipgloss.NewStyle()
		if i == m.cursor {
			prefix = "› "
			labelStyle = labelStyle.Bold(true).Foreground(lipgloss.Color("230"))
		} else if dim {
			labelStyle = labelStyle.Foreground(lipgloss.Color("238"))
		}
		label := truncate(item.WorkspaceName, max(10, width-20))
		// Status is canonical in JSON, so render the stored glyph
		// immediately on the fast first paint. The only tmux-derived
		// override is `working` → `exited` (agent shell death — Claude
		// has no exit hook), which arrives a frame later from the
		// enrichment pass and is rare enough that a brief flash is
		// preferable to a blank glyph slot.
		glyph := statusGlyph(item.Status, dim, item.Unread)
		prGlyph := m.prGlyphForItem(item)
		line := fmt.Sprintf("%s %s %s", prefixSlot.Render(prefix), glyph, labelStyle.Render(label))
		if prGlyph != "" {
			line += " " + prGlyph
		}
		rows = append(rows, lipgloss.NewStyle().Width(width-1).Render(line))
	}
	return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
}

// keyGroup is a labeled group of (key, description) rows shown in both the
// right details panel and the `?` help overlay. One source of truth so the
// two surfaces never drift.
type keyGroup struct {
	Title string
	Keys  [][2]string
}

func deckKeyGroups() []keyGroup {
	return []keyGroup{
		{
			Title: "Navigate",
			Keys: [][2]string{
				{"↑/↓ j/k", "move cursor"},
				{"/", "filter rows · esc clears"},
				{"f", "find: project → workspace easymotion jump"},
				{"P", "cycle scope (current → all → awaiting)"},
				{"L", "switch to last tmux session"},
			},
		},
		{
			Title: "Open / create",
			Keys: [][2]string{
				{"enter", "summon (create or focus the workspace tmux session)"},
				{"n", "new workspace"},
				{"o", "open: fuzzy-pick a project from configured roots"},
			},
		},
		{
			Title: "Windows",
			Keys: [][2]string{
				{"a", "agent window (re-attach without re-prompting)"},
				{"e", "editor window ($EDITOR)"},
				{"c / C", "review window: tuicr -r @  /  tuicr -r main..@"},
				{"v", "vcs window (jjui)"},
				{"s", "shell window"},
				{"i", "ci window (gh run watch)"},
				{"x", "user actions menu"},
			},
		},
		{
			Title: "Workspace",
			Keys: [][2]string{
				{"r", "review a PR"},
				{"D", "delete workspace (or default → delete project)"},
				{"R", "relink session"},
				{"B", "link bookmark to workspace (drives PR glyph)"},
				{",", "edit global state file in $EDITOR"},
			},
		},
		{
			Title: "Async jobs",
			Keys: [][2]string{
				{"J", "jobs overlay (list, cancel, retry, dismiss, open log)"},
			},
		},
		{
			Title: "View",
			Keys: [][2]string{
				{"?", "this help"},
				{"q / esc", "quit"},
			},
		},
	}
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
	lines := []string{
		title,
		"",
		fmt.Sprintf("Project:   %s", item.ProjectName),
		fmt.Sprintf("Workspace: %s", item.WorkspaceName),
		fmt.Sprintf("Status:    %s", normalizeStatus(item.Status)),
		fmt.Sprintf("Session:   %s", sess),
		fmt.Sprintf("Live:      %s", active),
		fmt.Sprintf("Path:      %s", item.Path),
	}
	if head := strings.TrimSpace(item.HeadDesc); head != "" {
		lines = append(lines, fmt.Sprintf("Head:      %s", head))
	}
	bm := strings.TrimSpace(item.Bookmark)
	if bm == "" {
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("(none — press B to link)")
		lines = append(lines, fmt.Sprintf("Bookmark:  %s", hint))
	} else {
		lines = append(lines, fmt.Sprintf("Bookmark:  %s", bm))
	}
	if pr, label, ok := m.prStatusLabelForItem(item); ok {
		colored := lipgloss.NewStyle().Foreground(lipgloss.Color(prGlyphColor(pr))).Render(label)
		lines = append(lines, fmt.Sprintf("PR:        #%d  %s", pr.Number, colored))
	}
	lines = append(lines,
		"",
		"Prompt:",
		truncatePrompt(prompt, width, 4),
	)
	if act := renderActivityBlock(m.jobs, item, width); act != "" {
		lines = append(lines, "", act)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// renderActivityBlock renders the per-workspace "Recent activity" list:
// up to 5 most-recent jobs whose Spec ties them to the selected
// workspace. Returns "" when no jobs match — the caller decides whether
// to show a placeholder.
func renderActivityBlock(allJobs []Job, item Item, width int) string {
	matching := make([]Job, 0, 8)
	for _, j := range allJobs {
		if !jobMatchesWorkspace(j, item) {
			continue
		}
		matching = append(matching, j)
	}
	if len(matching) == 0 {
		return ""
	}
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	const maxRows = 5
	rows := []string{headerStyle.Render("Recent activity")}
	for i, j := range matching {
		if i >= maxRows {
			rows = append(rows, dimStyle.Render(fmt.Sprintf("  … %d more (J)", len(matching)-maxRows)))
			break
		}
		rows = append(rows, "  "+formatActivityRow(j, width-2))
	}
	return strings.Join(rows, "\n")
}

func jobMatchesWorkspace(j Job, item Item) bool {
	if strings.TrimSpace(item.Path) != "" && j.WorkspacePath == item.Path {
		return true
	}
	if strings.TrimSpace(item.WorkspaceName) != "" &&
		j.WorkspaceName == item.WorkspaceName &&
		j.RepoRoot == item.RepoRoot {
		return true
	}
	return false
}

func formatActivityRow(j Job, width int) string {
	glyph, color := activityGlyph(j.Status)
	glyphStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(glyph)
	label := j.Action
	if j.Action == "custom" && strings.TrimSpace(j.Title) != "" {
		// Title is "<name> · <workspace>"; take the leading component.
		if idx := strings.Index(j.Title, " · "); idx > 0 {
			label = j.Title[:idx]
		} else {
			label = j.Title
		}
	}
	rel := relativeTimeShort(j.StartedAt, j.EndedAt, j.Status)
	relStyled := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(rel)
	return fmt.Sprintf("%s %s   %s", glyphStyled, label, relStyled)
}

func activityGlyph(s JobStatus) (glyph, color string) {
	switch s {
	case JobPending, JobRunning:
		return "▶", "39"
	case JobDone:
		return "✓", "42"
	case JobError:
		return "⚠", "203"
	case JobCancelled:
		return "·", "245"
	case JobOrphaned:
		return "☠", "172"
	}
	return "·", "245"
}

func relativeTimeShort(started, ended time.Time, status JobStatus) string {
	ref := ended
	if ref.IsZero() {
		ref = started
	}
	if ref.IsZero() {
		return ""
	}
	d := time.Since(ref)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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

func (m Model) renderOpenList(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Render("open: pick a project")
	rows := []string{title, ""}
	if m.openLoading {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("Scanning project roots..."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	rows = append(rows, "/"+m.openFilter.View(), "")
	picks := m.filteredProjects()
	if len(picks) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("No projects match."))
		return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
	}
	for i, p := range picks {
		prefix := "  "
		style := lipgloss.NewStyle().Width(width - 1)
		if i == m.openCursor {
			prefix = "› "
			style = style.Bold(true).Foreground(lipgloss.Color("230"))
		}
		label := truncate(p.Name, max(10, width-4))
		rows = append(rows, style.Render(prefix+label))
	}
	return lipgloss.NewStyle().Width(width).PaddingRight(1).Render(strings.Join(rows, "\n"))
}

func (m Model) renderOpenDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("open")
	picks := m.filteredProjects()
	lines := []string{title, ""}
	if m.openCursor >= 0 && m.openCursor < len(picks) {
		p := picks[m.openCursor]
		lines = append(lines,
			"Selection: "+p.Name,
			"Path:      "+p.Path,
		)
	} else {
		lines = append(lines, "Pick a project to summon (or create) its default workspace.")
	}
	lines = append(lines, "",
		"Keys:",
		"type     fuzzy filter",
		"↑/↓      navigate",
		"enter    open",
		"esc      cancel",
	)
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) renderBookmarkList(width int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	subtitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	containerStyle := lipgloss.NewStyle().Width(width).PaddingRight(1)

	titleText := "bookmark: pick one"
	if m.bookmarkPurpose == bookmarkPurposeLinkExisting {
		titleText = "link bookmark → " + m.bookmarkLinkTarget.WorkspaceName
	}
	header := titleStyle.Render(titleText)

	if m.bookmarkLoading {
		return containerStyle.Render(strings.Join([]string{
			header,
			subtitleStyle.Render(m.spinner.View() + " loading bookmarks..."),
		}, "\n"))
	}

	picks := m.filteredBookmarks()
	filterValue := strings.TrimSpace(m.bookmarkFilter.Value())

	// Persistent subtitle: a single row that morphs between live filter
	// input, committed filter summary, and the default hint. Keeping it
	// always present means the list rows below never jump vertically.
	var subtitle string
	switch {
	case m.bookmarkFiltering:
		subtitle = "/" + m.bookmarkFilter.View()
	case filterValue != "":
		subtitle = subtitleStyle.Render(fmt.Sprintf("filter: %q · %d/%d  (esc clears)", filterValue, len(picks), len(m.bookmarks)))
	default:
		subtitle = subtitleStyle.Render(fmt.Sprintf("%d bookmarks  ·  / filter · enter select · esc cancel", len(m.bookmarks)))
	}

	rows := []string{header, subtitle, ""}

	if len(m.bookmarks) == 0 {
		rows = append(rows, mutedStyle.Render("No bookmarks."))
		return containerStyle.Render(strings.Join(rows, "\n"))
	}
	if len(picks) == 0 {
		rows = append(rows, mutedStyle.Render("No bookmarks match."))
		return containerStyle.Render(strings.Join(rows, "\n"))
	}

	// Bound the visible list to the terminal height. Rows we must reserve:
	//   1 header, 1 subtitle, 1 blank gap, 1 "… X more" hint, plus 2 for
	//   the deck's bottom status bar (job tray can take a row, plus the
	//   status line). 6 is conservative — better to under-fill than to
	//   push the search bar off the top of the screen when the list
	//   exceeds the viewport.
	reserved := 6
	avail := m.height - reserved
	if avail < 1 {
		avail = 1
	}
	capacity := avail
	if capacity > len(picks) {
		capacity = len(picks)
	}
	if m.bookmarkCursor >= len(picks) {
		m.bookmarkCursor = len(picks) - 1
	}
	if m.bookmarkCursor < 0 {
		m.bookmarkCursor = 0
	}
	offset := 0
	if m.bookmarkCursor >= capacity {
		offset = m.bookmarkCursor - capacity + 1
	}
	if offset+capacity > len(picks) {
		offset = len(picks) - capacity
	}
	if offset < 0 {
		offset = 0
	}

	for i := offset; i < offset+capacity; i++ {
		name := picks[i]
		prefix := "  "
		style := lipgloss.NewStyle().Width(width - 1)
		if i == m.bookmarkCursor {
			prefix = "› "
			style = style.Bold(true).Foreground(lipgloss.Color("230"))
		}
		rows = append(rows, style.Render(prefix+truncate(name, max(8, width-4))))
	}
	if offset+capacity < len(picks) {
		rows = append(rows, mutedStyle.Render(fmt.Sprintf("  … %d more", len(picks)-(offset+capacity))))
	}
	return containerStyle.Render(strings.Join(rows, "\n"))
}

func (m Model) filteredReviewPRs() []PRItem {
	f := strings.ToLower(strings.TrimSpace(m.reviewFilter))
	if f == "" {
		return m.reviewPRs
	}
	out := make([]PRItem, 0, len(m.reviewPRs))
	for _, pr := range m.reviewPRs {
		// Match only against fields the user actually sees in the row:
		// PR number, title, and author. Including the branch caused PRs
		// with the substring in their HeadRef to show up with no visible
		// reason why.
		hay := strings.ToLower(fmt.Sprintf("%d %s %s", pr.Number, pr.Title, pr.Author))
		if strings.Contains(hay, f) {
			out = append(out, pr)
		}
	}
	return out
}

func (m Model) renderReviewList(width int) string {
	titleStyle := lipgloss.NewStyle().Bold(true)
	subtitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	containerStyle := lipgloss.NewStyle().Width(width).PaddingRight(1)

	header := titleStyle.Render("review: select PR")

	if m.reviewLoading {
		return containerStyle.Render(strings.Join([]string{
			header,
			subtitleStyle.Render(m.spinner.View() + " loading PRs..."),
		}, "\n"))
	}

	prs := m.filteredReviewPRs()

	var subtitle string
	switch {
	case m.reviewFiltering:
		subtitle = "/" + m.filterInput.View()
	case m.reviewFilter != "":
		subtitle = subtitleStyle.Render(fmt.Sprintf("filter: %q · %d/%d  (esc clears)", m.reviewFilter, len(prs), len(m.reviewPRs)))
	default:
		subtitle = subtitleStyle.Render(fmt.Sprintf("%d open  ·  / filter · enter open · esc cancel", len(m.reviewPRs)))
	}

	rows := []string{header, subtitle, ""}

	if len(m.reviewPRs) == 0 {
		rows = append(rows, mutedStyle.Render("No open PRs."))
		return containerStyle.Render(strings.Join(rows, "\n"))
	}
	if len(prs) == 0 {
		rows = append(rows, mutedStyle.Render("No matching PRs."))
		return containerStyle.Render(strings.Join(rows, "\n"))
	}

	// One row per PR. Reserve header + subtitle + blank + scroll hint.
	reserved := 4
	avail := m.height - reserved
	if avail < 1 {
		avail = 1
	}
	capacity := avail
	if capacity > len(prs) {
		capacity = len(prs)
	}
	if m.reviewCursor >= len(prs) {
		m.reviewCursor = len(prs) - 1
	}
	if m.reviewCursor < 0 {
		m.reviewCursor = 0
	}
	offset := 0
	if m.reviewCursor >= capacity {
		offset = m.reviewCursor - capacity + 1
	}
	if offset+capacity > len(prs) {
		offset = len(prs) - capacity
	}
	if offset < 0 {
		offset = 0
	}

	// Right-align PR numbers within the widest number's width so titles align.
	numW := 0
	for _, pr := range m.reviewPRs {
		if w := len(fmt.Sprintf("#%d", pr.Number)); w > numW {
			numW = w
		}
	}

	const prefixWidth = 2
	prefixSlot := lipgloss.NewStyle().Width(prefixWidth)
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	numStyleSelected := lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	draftStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	authorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	titleSelected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	titleNormal := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	authorMutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	for i := offset; i < offset+capacity; i++ {
		pr := prs[i]
		selected := i == m.reviewCursor

		prefix := "  "
		if selected {
			prefix = "› "
		}

		numText := fmt.Sprintf("%*s", numW, fmt.Sprintf("#%d", pr.Number))
		var numRendered string
		if selected {
			numRendered = numStyleSelected.Render(numText)
		} else {
			numRendered = numStyle.Render(numText)
		}

		author := "@" + pr.Author
		if selected {
			author = authorStyle.Render(author)
		} else {
			author = authorMutedStyle.Render(author)
		}

		draft := ""
		if pr.IsDraft {
			draft = " " + draftStyle.Render("draft")
		}

		// width = prefix + num + space + title + space + author + draft
		fixed := prefixWidth + numW + 1 + 1 + lipgloss.Width("@"+pr.Author) + lipgloss.Width(draft)
		titleRoom := width - 1 - fixed
		if titleRoom < 10 {
			titleRoom = 10
		}
		titleText := truncate(pr.Title, titleRoom)
		var titleRendered string
		if selected {
			titleRendered = titleSelected.Render(titleText)
		} else {
			titleRendered = titleNormal.Render(titleText)
		}

		line := fmt.Sprintf("%s%s  %s  %s%s",
			prefixSlot.Render(prefix), numRendered, titleRendered, author, draft)
		rows = append(rows, line)
	}

	if len(prs) > capacity {
		hint := fmt.Sprintf("  %d–%d of %d", offset+1, offset+capacity, len(prs))
		rows = append(rows, mutedStyle.Render(hint))
	}

	return containerStyle.Render(strings.Join(rows, "\n"))
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
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("203")).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)

	if m.deleteIsProject {
		project := strings.TrimSpace(m.deleteTarget.ProjectName)
		if project == "" {
			project = "this project"
		}
		lines := []string{
			titleStyle.Render("Delete project " + project + "?"),
			"",
			mutedStyle.Render("Removes every non-default workspace under this repo and"),
			mutedStyle.Render("drops the project from the deck. The default workspace"),
			mutedStyle.Render("itself is left intact."),
			"",
			mutedStyle.Render("Type the project name to confirm:"),
			m.deleteInput.View(),
		}
		if m.deleteErr != "" {
			lines = append(lines, "", errStyle.Render(m.deleteErr))
		}
		lines = append(lines, "", hintStyle.Render("enter confirm · esc cancel"))
		return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	}

	name := strings.TrimSpace(m.deleteTarget.WorkspaceName)
	if name == "" {
		name = "this workspace"
	}
	lines := []string{
		titleStyle.Render("Delete workspace " + name + "?"),
		"",
		hintStyle.Render("y confirm · n / esc cancel"),
	}
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
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

// mnemonicSecondKeys returns candidate second-key runes for a two-letter hint
// drawn from the name itself, in priority order: first letter of each word
// after the leading one (split on '-', '_', '.', '/', ' ', or camelCase
// boundaries), then the remaining letters of the name in order. The shared
// first key is skipped so we don't produce hints like "bb" for "billing".
func mnemonicSecondKeys(name string, first rune) []rune {
	rs := []rune(name)
	if len(rs) == 0 {
		return nil
	}
	letters := make([]rune, 0, len(rs))
	for _, r := range rs {
		lr := unicode.ToLower(r)
		if lr >= 'a' && lr <= 'z' {
			letters = append(letters, lr)
		} else {
			letters = append(letters, 0)
		}
	}
	seen := map[rune]bool{}
	out := make([]rune, 0, len(letters))
	push := func(r rune) {
		if r == 0 || r == first || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	// Word starts after a separator or camelCase boundary.
	for i := 1; i < len(rs); i++ {
		prev := rs[i-1]
		curr := rs[i]
		isSep := prev == '-' || prev == '_' || prev == '.' || prev == '/' || prev == ' '
		isCamel := unicode.IsLower(prev) && unicode.IsUpper(curr)
		if isSep || isCamel {
			push(letters[i])
		}
	}
	// Then the remaining letters in order (skipping the leading char).
	for i := 1; i < len(letters); i++ {
		push(letters[i])
	}
	return out
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

	inSecondPool := map[rune]bool{}
	for _, c := range secondPool {
		inSecondPool[c] = true
	}

	assignDouble := func(name string, first rune, candidates []rune) bool {
		tryRune := func(second rune) bool {
			if !inSecondPool[second] {
				return false
			}
			hint := string(first) + string(second)
			if used[hint] {
				return false
			}
			used[hint] = true
			out[name] = hint
			return true
		}
		for _, second := range candidates {
			if tryRune(second) {
				return true
			}
		}
		for _, second := range secondPool {
			if tryRune(second) {
				return true
			}
		}
		return false
	}

	for _, b := range ordered {
		if b.key == 0 || len(b.names) <= 1 {
			continue
		}
		for _, name := range b.names {
			assignDouble(name, b.key, mnemonicSecondKeys(name, b.key))
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
				if assignDouble(name, first, mnemonicSecondKeys(name, first)) {
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

// Nerd Font Octicon codepoints used for the per-row PR status glyph. The
// deck assumes a patched font is available. Codepoints live in the Private
// Use Area, so they encode here as \u escapes that the Go compiler turns into
// the same UTF-8 bytes regardless of editor/rendering pipeline behavior.
const (
	prGlyphOpen     = "" // nf-oct-git_pull_request
	prGlyphDraft    = "" // nf-oct-git_pull_request_draft
	prGlyphClosed   = "" // nf-oct-git_pull_request_closed
	prGlyphMerged   = "" // nf-oct-git_merge
	prGlyphApproved = "" // nf-oct-check
	prGlyphCIFail   = "" // nf-oct-x
	prGlyphCIPend   = "" // nf-oct-hourglass
)

// prGlyphFor returns the single glyph for the given PR status per the locked
// priority order: merged → closed → CI failed → CI pending → approved → draft
// → open. Returns "" when no glyph should render (caller passes a zero/empty
// status when the workspace has no matching PR).
func prGlyphFor(s PRStatus) string {
	if s.State == PRStateMerged {
		return prGlyphMerged
	}
	if s.State == PRStateClosed {
		return prGlyphClosed
	}
	switch s.CIState {
	case PRCIFailing:
		return prGlyphCIFail
	case PRCIPending:
		return prGlyphCIPend
	}
	if s.ReviewDecision == PRReviewApproved {
		return prGlyphApproved
	}
	if s.IsDraft {
		return prGlyphDraft
	}
	if s.State == PRStateOpen {
		return prGlyphOpen
	}
	return ""
}

// prGlyphColor picks a foreground color from the existing statusColor palette.
// Mapping (closest existing entries; no new palette in v1):
//
//	merged   → 245 (muted grey, "settled / done")
//	closed   → 244 (also muted grey)
//	failed   → 203 (same red as `error`)
//	pending  → 214 (same amber as `waiting`)
//	approved → 82  (same green as `working`)
//	draft    → 245 (muted)
//	open     → 117 (same cyan as `starting`)
func prGlyphColor(s PRStatus) string {
	if s.State == PRStateMerged {
		return "245"
	}
	if s.State == PRStateClosed {
		return "244"
	}
	switch s.CIState {
	case PRCIFailing:
		return "203"
	case PRCIPending:
		return "214"
	}
	if s.ReviewDecision == PRReviewApproved {
		return "82"
	}
	if s.IsDraft {
		return "245"
	}
	return "117"
}

// prStatusLabel returns a short human-readable phrase matching the glyph
// priority order. Mirrors prGlyphFor so the words shown in the details panel
// always agree with the glyph drawn in the row.
func prStatusLabel(s PRStatus) string {
	if s.State == PRStateMerged {
		return "merged"
	}
	if s.State == PRStateClosed {
		return "closed"
	}
	switch s.CIState {
	case PRCIFailing:
		if s.IsDraft {
			return "draft · CI failing"
		}
		return "CI failing"
	case PRCIPending:
		if s.IsDraft {
			return "draft · CI pending"
		}
		return "CI pending"
	}
	if s.ReviewDecision == PRReviewApproved {
		return "approved"
	}
	if s.IsDraft {
		return "draft"
	}
	if s.State == PRStateOpen {
		if s.ReviewDecision == PRReviewChangesRequested {
			return "open · changes requested"
		}
		return "open"
	}
	return ""
}

// prStatusLabelForItem looks up the workspace's PR (by Bookmark → headRefName)
// and returns the matched status plus a human-readable label. ok is false
// when there is no matching PR (no bookmark, no match, fetcher not run).
func (m Model) prStatusLabelForItem(item Item) (PRStatus, string, bool) {
	bm := strings.TrimSpace(item.Bookmark)
	if bm == "" {
		return PRStatus{}, "", false
	}
	repo := strings.TrimSpace(item.RepoRoot)
	if repo == "" {
		return PRStatus{}, "", false
	}
	byHead, ok := m.prStatusByRepo[repo]
	if !ok {
		return PRStatus{}, "", false
	}
	status, ok := byHead[bm]
	if !ok {
		return PRStatus{}, "", false
	}
	label := prStatusLabel(status)
	if label == "" {
		return PRStatus{}, "", false
	}
	return status, label, true
}

// prGlyphForItem resolves the workspace's bookmark to a PR (if any) and
// returns the rendered glyph string (with ANSI color), or "" when no glyph
// applies (no bookmark, no PR match, fetcher not configured).
func (m Model) prGlyphForItem(item Item) string {
	bm := strings.TrimSpace(item.Bookmark)
	if bm == "" {
		return ""
	}
	repo := strings.TrimSpace(item.RepoRoot)
	if repo == "" {
		return ""
	}
	byHead, ok := m.prStatusByRepo[repo]
	if !ok {
		return ""
	}
	status, ok := byHead[bm]
	if !ok {
		return ""
	}
	g := prGlyphFor(status)
	if g == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(prGlyphColor(status))).Render(g)
}

// statusGlyph renders a colored ● for an agent status. Only "loud" states
// (working/in-progress/running) render a dot unconditionally — every other
// state requires unread=true. This makes the dot strictly an attention
// signal: when the user is viewing the session (or has summoned it since
// the last transition) report-status / the deck refresh clear Unread, and
// the row goes quiet regardless of whether the last hook to write was
// "waiting", "idle", or "exited" with stale data.
func statusGlyph(status string, dim bool, unread bool) string {
	if !alwaysShownStatus(status) && !unread {
		return " "
	}
	color := statusColor(status, dim, unread)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("●")
}

func alwaysShownStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return true
	default:
		return false
	}
}

func statusColor(status string, dim bool, unread bool) string {
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
	default: // idle / done / unknown — only rendered when unread (notified)
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

// truncatePrompt wraps prompt to width and keeps the first maxLines lines,
// appending an ellipsis when content was dropped. Long single-line prompts
// otherwise wrap into a vertical wall that overflows the details panel.
func truncatePrompt(prompt string, width, maxLines int) string {
	prompt = strings.TrimRight(prompt, "\n")
	if maxLines <= 0 || width <= 0 {
		return prompt
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(prompt)
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= maxLines {
		return prompt
	}
	kept := lines[:maxLines]
	last := strings.TrimRight(kept[maxLines-1], " ")
	if lipgloss.Width(last)+2 > width {
		last = truncate(last, max(1, width-1)) + "…"
	} else {
		last += " …"
	}
	kept[maxLines-1] = last
	return strings.Join(kept, "\n")
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
