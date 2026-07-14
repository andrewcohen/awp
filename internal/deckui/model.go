package deckui

import (
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/deckdata"
	"github.com/andrewcohen/awp/internal/prstatus"
	"github.com/andrewcohen/awp/internal/workspace"
)

// refreshInterval is how often the deck polls for live tmux state
// (sessions, agent pane command). Status updates pushed in by agent
// hooks come through the state-file watcher much sooner than this, so
// the tick is just a backstop for tmux-only transitions.
const refreshInterval = 5 * time.Second

// devURLInterval is how often the deck polls for dev-server URLs.
// Tighter than refreshInterval because the "I just started pnpm dev"
// → "URL appears in the right panel" feedback loop is the whole point
// of the feature.
const devURLInterval = 2 * time.Second

type refreshTickMsg time.Time
type devURLTickMsg time.Time

func scheduleRefreshTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshTickMsg(t) })
}

func scheduleDevURLTick() tea.Cmd {
	return tea.Tick(devURLInterval, func(t time.Time) tea.Msg { return devURLTickMsg(t) })
}

// Item is the deck row type; it lives in internal/deckdata (the read
// model) so the selection logic can be tested without this Bubble Tea
// package. The alias keeps deckui's and cli's references compiling
// unchanged.
type Item = deckdata.Item

// DevLoopSummary is the row-sized dev-loop progress projection carried on
// an Item (see deckdata.DevLoopSummary); aliased here so deckui call sites
// and tests read it without importing deckdata directly.
type DevLoopSummary = deckdata.DevLoopSummary

type Action int

const (
	ActionSummon Action = iota
	ActionOpenWindow
	ActionDelete
	ActionDeleteProject
	ActionCI
	ActionLastSession
	ActionReview
	ActionCustom
	ActionCreateWorkspace
	ActionRename
	// ActionSendPrompt dispatches the prompt in Arg to the workspace's
	// agent. The handler is responsible for ensuring the session/agent
	// window exists — if the agent isn't running yet it should start
	// the agent with the prompt; if it is, it should paste the prompt
	// as a user message.
	ActionSendPrompt
	// ActionMergePR merges the workspace's PR via `gh pr merge`. It runs
	// in the foreground progress modal (not the async jobs subsystem) so
	// the modal stays open until gh reports success or failure.
	ActionMergePR
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
	Name             string
	Bookmark         string // anchor revision for the new workspace's @
	BookmarkToCreate string // new bookmark to create on @ (blank = skip)
	Prompt           string
	// PRNumber, when > 0, pins the created workspace to this PR (the
	// create handler calls RecordPROverride) so it links immediately.
	// Set when creating from a virtual "mine" inbox row; 0 otherwise.
	PRNumber int
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
// Name, when set, pre-fills the workspace-name field — used when the deck
// derives a sensible default from a picked bookmark (e.g. stripping the
// configured prefix from `andrew/foo` to propose `foo`).
type NewWorkspaceInitial struct {
	Bookmark string
	Name     string
	// PRNumber, when > 0, is carried through the form (not shown as a
	// field) and pins the created workspace to this PR. Set when the form
	// is opened from a virtual "mine" inbox row.
	PRNumber int
}

// BookmarkFetcher returns a tea.Cmd that lists deduped bookmarks and emits a
// BookmarksDoneMsg.
type BookmarkFetcher func(repoRoot string) tea.Cmd

// TrunkResolver returns the repo's `trunk()` revset bookmark name (e.g.
// "main", "master", "trunk"). Used by the new-workspace form's
// Start-from default. Return "" to let the form fall back to "main".
type TrunkResolver func(repoRoot string) string

// StateEditorLauncher returns a tea.Cmd that suspends the deck and opens the
// global workspace-state.json in $EDITOR.
type StateEditorLauncher func() tea.Cmd

// StateEditDoneMsg is emitted when the state editor exits.
type StateEditDoneMsg struct{ Err error }

// HookInstaller returns a tea.Cmd that (re)installs the global agent
// integrations (Claude Code hooks, pi.dev extension) and emits a
// HookInstallDoneMsg. The install is idempotent — it only writes when the
// on-disk hook block has drifted from what awp expects — so the deck can
// fire it unconditionally on startup. Without it, deck open never touches
// the hook config.
type HookInstaller func() tea.Cmd

// HookInstallDoneMsg reports the result of the startup hook install.
// ClaudeChanged / PiChanged are true only when that integration's on-disk
// config was actually rewritten (i.e. it had drifted); both false means
// everything was already up to date.
type HookInstallDoneMsg struct {
	ClaudeChanged bool
	PiChanged     bool
	Err           error
}

// bookmarkPurpose disambiguates the two flows that share the picker: the
// new-workspace form's bookmark seed vs. linking a bookmark to an existing
// workspace (used by the B key in row mode).
type bookmarkPurpose int

const (
	bookmarkPurposeNewWorkspace bookmarkPurpose = iota
	bookmarkPurposeLinkExisting
	// bookmarkPurposeNewWorkspaceStartFrom is the picker round-trip from
	// within the open workspace form: the form stays alive in the
	// background, the picker resolves a bookmark, then acceptBookmarkSelection
	// feeds the result back via SetPickedBookmark and re-shows the form.
	bookmarkPurposeNewWorkspaceStartFrom
)

// bookmarkItem is the list.Item shape for the bookmark picker. Only Title
// is rendered (delegate.ShowDescription is false); FilterValue feeds
// list.Model's built-in fuzzy filter.
type bookmarkItem struct{ name string }

func (b bookmarkItem) FilterValue() string { return b.name }
func (b bookmarkItem) Title() string       { return b.name }
func (b bookmarkItem) Description() string { return "" }

// loadingItem occupies the items area of a picker while its fetch is in
// flight. Without it bubbles/list renders the empty-state "No bookmarks"
// in both the status bar AND the items area — visible as a duplicate
// message. Slotting in a single placeholder keeps the items area
// non-empty so the list only renders the items-area branch.
type loadingItem struct{ label string }

func (l loadingItem) FilterValue() string { return "" }
func (l loadingItem) Title() string       { return l.label }
func (l loadingItem) Description() string { return "" }

// newBookmarkList mirrors the canonical list integration in
// internal/cli/picker.go: single-line items via DefaultDelegate with
// description hidden and zero spacing, themed via charm.ApplyListTheme so
// selection style (warning fg + ┃ left bar) matches every other picker.
func newBookmarkList() list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	l := list.New(nil, delegate, 0, 0)
	l.SetShowTitle(true)
	l.SetShowHelp(false)
	l.SetStatusBarItemName("bookmark", "bookmarks")
	l.DisableQuitKeybindings()
	charm.ApplyListTheme(&l, &delegate)
	l.SetDelegate(delegate)
	return l
}

func bookmarkPickerTitle(p bookmarkPurpose, target Item) string {
	switch p {
	case bookmarkPurposeLinkExisting:
		return "link bookmark → " + target.WorkspaceName
	case bookmarkPurposeNewWorkspaceStartFrom:
		return "start from: pick a bookmark"
	}
	return "bookmark: pick one"
}

// reviewItem wraps a PRItem for the review picker. FilterValue feeds
// list.Model's built-in fuzzy filter — it matches what the row visibly
// shows (number, title, author) and deliberately excludes HeadRef so PRs
// don't surface for invisible reasons.
type reviewItem struct{ pr PRItem }

func (r reviewItem) FilterValue() string {
	return fmt.Sprintf("%d %s %s", r.pr.Number, r.pr.Title, r.pr.Author)
}

// reviewItemDelegate renders the per-field-colored PR row (number, title,
// author, optional draft chip) instead of the DefaultDelegate's
// Title/Description layout. numW is the widest PR-number string so titles
// align across rows; refreshed by reviewItemDelegate.recompute when items
// change.
type reviewItemDelegate struct {
	numW int
}

func (d *reviewItemDelegate) Height() int  { return 1 }
func (d *reviewItemDelegate) Spacing() int { return 0 }
func (d *reviewItemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd {
	return nil
}

func (d *reviewItemDelegate) recompute(prs []list.Item) {
	d.numW = 0
	for _, it := range prs {
		ri, ok := it.(reviewItem)
		if !ok {
			continue
		}
		if w := len(fmt.Sprintf("#%d", ri.pr.Number)); w > d.numW {
			d.numW = w
		}
	}
}

func (d *reviewItemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	if li, ok := listItem.(loadingItem); ok {
		fmt.Fprint(w, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render("  "+li.label))
		return
	}
	item, ok := listItem.(reviewItem)
	if !ok {
		return
	}
	pr := item.pr
	selected := index == m.Index()
	width := m.Width()

	const prefixWidth = 2
	prefixSlot := lipgloss.NewStyle().Width(prefixWidth)
	prefix := "  "
	if selected {
		prefix = "┃ "
	}

	numText := fmt.Sprintf("%*s", d.numW, fmt.Sprintf("#%d", pr.Number))
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo))
	if selected {
		numStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true)
	}
	numRendered := numStyle.Render(numText)

	author := "@" + pr.Author
	authorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	if selected {
		authorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess))
	}
	authorRendered := authorStyle.Render(author)

	draft := ""
	if pr.IsDraft {
		draft = " " + lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Render("draft")
	}

	fixed := prefixWidth + d.numW + 1 + 1 + lipgloss.Width("@"+pr.Author) + lipgloss.Width(draft)
	titleRoom := width - 1 - fixed
	if titleRoom < 10 {
		titleRoom = 10
	}
	titleText := truncate(pr.Title, titleRoom)
	titleStyle := lipgloss.NewStyle()
	if selected {
		titleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true)
	}
	titleRendered := titleStyle.Render(titleText)

	fmt.Fprintf(w, "%s%s  %s  %s%s",
		prefixSlot.Render(prefix), numRendered, titleRendered, authorRendered, draft)
}

// newReviewList builds the review picker's list.Model with our custom
// delegate. List chrome (title, status bar, filter, pagination) goes
// through charm.ApplyListTheme; row rendering is owned by
// reviewItemDelegate.
func newReviewList() (list.Model, *reviewItemDelegate) {
	d := &reviewItemDelegate{}
	l := list.New(nil, d, 0, 0)
	l.SetShowTitle(true)
	l.SetShowHelp(false)
	l.Title = "review: select PR"
	l.SetStatusBarItemName("PR", "PRs")
	l.DisableQuitKeybindings()
	charm.ApplyListTheme(&l, nil)
	return l, d
}

// projectItem is the list.Item shape for the open / project picker.
// FilterValue concatenates Name and Path so the user can fuzzy-match on
// either; Title shows only the Name to keep rows compact.
type projectItem struct{ project ProjectItem }

func (p projectItem) FilterValue() string { return p.project.Name + " " + p.project.Path }
func (p projectItem) Title() string       { return p.project.Name }
func (p projectItem) Description() string { return "" }

func newOpenList() list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	l := list.New(nil, delegate, 0, 0)
	l.SetShowTitle(true)
	l.SetShowHelp(false)
	l.Title = "open: pick a project"
	l.SetStatusBarItemName("project", "projects")
	l.DisableQuitKeybindings()
	charm.ApplyListTheme(&l, &delegate)
	l.SetDelegate(delegate)
	return l
}

// newDeckViewport returns the viewport for the project-grouped workspace
// list. Its key bindings are wiped: the deck owns navigation via its own
// j/k handling (which mutates m.cursor and then calls clampDeckViewport
// to keep the cursor row in view).
func newDeckViewport() viewport.Model {
	v := viewport.New(0, 0)
	v.KeyMap = viewport.KeyMap{}
	return v
}

// newProgressViewport returns the viewport for the progress modal's
// streaming log. pgup/pgdn and ctrl+u/ctrl+d scroll back through
// history while syncProgressViewport auto-follows the tail when the
// user is already pinned to the bottom.
func newProgressViewport() viewport.Model {
	v := viewport.New(0, 0)
	v.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll log")),
		PageUp:       key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll log")),
		HalfPageDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ pg dn")),
		HalfPageUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ pg up")),
	}
	return v
}

// syncProgressViewport pushes the accumulated progressLog into the
// viewport. If the user was already at the bottom (auto-follow), the
// view scrolls to keep the tail visible; otherwise YOffset is left
// alone so the user can read older lines without the stream yanking
// them away.
func (m *Model) syncProgressViewport() {
	atBottom := m.progressViewport.AtBottom()
	m.progressViewport.SetContent(strings.Join(m.progressLog, "\n"))
	if atBottom {
		m.progressViewport.GotoBottom()
	}
}

// BookmarkLinkHandler is called when the user picks a bookmark in the
// "link to existing workspace" flow. The handler must persist the chosen
// bookmark to the workspace's stored Entry.Bookmark; the deck then refreshes
// items so the per-row PR glyph picks up the new association on the next
// paint without any gh call (the in-memory PR cache is keyed by repo+headRef,
// not by workspace, so changing the workspace's bookmark is a local lookup).
type BookmarkLinkHandler func(item Item, bookmark string) error

// PRNumberLinkHandler is called when the user pins (or clears) a PR
// number override via the `p s` chord. prNumber == 0 clears the
// override; positive values pin the workspace to that PR number,
// overriding bookmark-based PR-status resolution.
type PRNumberLinkHandler func(item Item, prNumber int) error

// PinGroupHandler is called when the user pins, moves, or unpins a
// workspace via the `m` chord. group == "" unpins; "default" is the
// mm register; otherwise a single lowercase letter a–z. The handler
// persists the register onto the workspace's stored Entry.PinGroup.
type PinGroupHandler func(item Item, group string) error

// PinGroupAliasHandler is called when the user renames a register's
// display alias via the `gR` chord. An empty alias clears it. The
// handler persists the register→alias map globally.
type PinGroupAliasHandler func(group, alias string) error

// BookmarksDoneMsg carries the result of an async bookmark fetch.
type BookmarksDoneMsg struct {
	Bookmarks []string
	Err       error
}

type Refresher func() tea.Cmd

// DevURLDiscoverer returns a tea.Cmd that performs port discovery for
// every active tmux session and emits a DevURLsMsg. The deck owns the
// 2s tick that drives the discoverer; the discoverer itself is
// stateless. Without it, the `u` key is a no-op.
type DevURLDiscoverer func() tea.Cmd

// DevURLsMsg carries one snapshot of discovered dev URLs, keyed by
// tmux session name. Missing keys mean "no URL discovered for that
// session this tick" — the model treats every snapshot as authoritative
// and replaces the cached map wholesale, so a URL that disappears
// (server stopped) drops on the next tick.
type DevURLsMsg struct {
	URLs map[string]string
}

// StateChangeWatcher returns a command that emits StateChangedMsg when the
// persisted workspace state file changes. It is an optimization layered on
// top of the periodic refresh tick; callers may leave it nil.
type StateChangeWatcher func() tea.Cmd

// StateChangedMsg is emitted when workspace-state.json is created, replaced,
// renamed, or written. The deck treats it as an immediate refresh hint and
// keeps polling as the correctness fallback.
type StateChangedMsg struct{}

// Scope, its values, and ParseScope live in internal/deckdata (the read
// model). These aliases keep deckui's and cli's references unchanged.
type Scope = deckdata.Scope

const (
	ScopeAll       = deckdata.ScopeAll
	ScopeAttention = deckdata.ScopeAttention
	ScopeInbox     = deckdata.ScopeInbox
	scopeCount     = deckdata.ScopeCount
)

// ParseScope maps the user-facing names accepted by `awp deck --scope`
// onto Scope values. See deckdata.ParseScope.
func ParseScope(s string) (Scope, bool) { return deckdata.ParseScope(s) }

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

// ReviewReloader reloads a stale tuicr review window onto a PR's current
// head, splices a session block into the repair prompt naming the new
// (and any prior-draft) session, and sends the augmented prompt to the
// workspace agent. It runs on PR-repair submit for someone else's PR; the
// wiring layer (internal/cli/deck.go) supplies the implementation. The
// returned tea.Cmd emits a reviewReloadedMsg.
//
// When the workspace has no live review window, or the window is already
// on prHeadSHA, no reload happens and the prompt is sent unchanged.
type ReviewReloader func(item Item, prNumber int, prHeadSHA, prURL, prompt string) tea.Cmd

// ReviewReloadedMsg reports the outcome of a ReviewReloader run: whether a
// stale window was actually reloaded (and onto which short head SHA), plus
// any error. The prompt was already sent by the reloader by the time this
// lands, so the handler only updates status and retires the activity.
type ReviewReloadedMsg struct {
	Item      Item
	Reloaded  bool
	ShortHead string
	Err       error
}

// The PR type cluster now lives in internal/prstatus so the pure read
// model (internal/deckdata) can reference it without importing this
// Bubble Tea package. These aliases keep the ~480 references in deckui and
// ~83 in internal/cli compiling unchanged; the underlying struct is
// identical, so the on-disk pr-status-cache.json format is unaffected.
type (
	PRState            = prstatus.PRState
	PRReviewDecision   = prstatus.PRReviewDecision
	PRCIState          = prstatus.PRCIState
	PRMergeStateStatus = prstatus.PRMergeStateStatus
	// PRStatus is the per-PR projection consumed by the row glyph.
	PRStatus = prstatus.PRStatus
)

const (
	PRStateOpen   = prstatus.PRStateOpen
	PRStateClosed = prstatus.PRStateClosed
	PRStateMerged = prstatus.PRStateMerged

	PRReviewApproved         = prstatus.PRReviewApproved
	PRReviewChangesRequested = prstatus.PRReviewChangesRequested
	PRReviewRequired         = prstatus.PRReviewRequired

	PRCINone    = prstatus.PRCINone
	PRCIPending = prstatus.PRCIPending
	PRCIPassing = prstatus.PRCIPassing
	PRCIFailing = prstatus.PRCIFailing

	PRMergeStateBehind   = prstatus.PRMergeStateBehind
	PRMergeStateBlocked  = prstatus.PRMergeStateBlocked
	PRMergeStateClean    = prstatus.PRMergeStateClean
	PRMergeStateDirty    = prstatus.PRMergeStateDirty
	PRMergeStateDraft    = prstatus.PRMergeStateDraft
	PRMergeStateHasHooks = prstatus.PRMergeStateHasHooks
	PRMergeStateUnknown  = prstatus.PRMergeStateUnknown
	PRMergeStateUnstable = prstatus.PRMergeStateUnstable
)

// inboxBucket, its values, and the label helpers live in
// internal/deckdata (the read model). These aliases keep deckui's
// references unchanged; the urgency coloring below stays here since it's
// presentation.
type inboxBucket = deckdata.InboxBucket

const (
	inboxNeedsYourReview = deckdata.InboxNeedsYourReview
	inboxNeedsAction     = deckdata.InboxNeedsAction
	inboxReadyToMerge    = deckdata.InboxReadyToMerge
	inboxOtherOpen       = deckdata.InboxOtherOpen
	inboxMine            = deckdata.InboxMine
	inboxBucketCount     = deckdata.InboxBucketCount
)

func inboxBucketLabel(b inboxBucket) string { return deckdata.InboxBucketLabel(b) }

// inboxBucketColor maps a bucket to a palette token so its header reads
// by urgency at a glance: teal = your move (review), red = something to
// fix, green = ready. The bottom two — other people's PRs and your own
// in-flight ones (waiting / drafts) — are muted; nothing's blocked on
// you there.
func inboxBucketColor(b inboxBucket) string {
	switch b {
	case inboxNeedsYourReview:
		return colAccent
	case inboxNeedsAction:
		return colDanger
	case inboxReadyToMerge:
		return colSuccess
	default:
		return colMuted
	}
}

func bucketFromHeaderLabel(label string) (inboxBucket, bool) {
	return deckdata.BucketFromHeaderLabel(label)
}

// headerStyle returns the style for a group header. Inbox bucket headers
// are urgency-colored; project headers (all / attention scopes) use the
// brightened ProjectHeader treatment. Find-mode's teal highlight is
// applied by the caller and wins over either.
func (m Model) headerStyle(label string) lipgloss.Style {
	if m.scope == ScopeInbox {
		if b, ok := bucketFromHeaderLabel(label); ok {
			return m.styles.BucketHeader[b]
		}
	}
	return m.styles.ProjectHeader
}

// pinGroupDefault is the register key for the gg chord — the "default"
// pinned register. Other registers are single lowercase letters a–z.
const pinGroupDefault = deckdata.PinGroupDefault

// pinGroupLabel is the display label for a register: its alias when one
// is set, otherwise "pinned" for the default register or the bare
// letter for a lettered register.
func (m Model) pinGroupLabel(key string) string {
	return deckdata.PinGroupLabel(m.pinGroupAliases, key)
}

// pinGroupChordLetter is the keystroke that targets a register in the
// `m` chord — "m" for the default register (mm), the letter otherwise.
// Shown as an emphasized [x] chip in the section header while the chord
// is pending.
func pinGroupChordLetter(key string) string {
	if key == pinGroupDefault {
		return "m"
	}
	return key
}

// pinnedCount returns how many leading items in a pinned-first ordering
// carry a register. items() sorts pinned rows ahead of unpinned ones in
// the all / attention scopes, so this is the length of that prefix.
func pinnedCount(items []Item) int { return deckdata.PinnedCount(items) }

// prInboxBucket classifies an OPEN PR into its inbox section. See
// deckdata.PRInboxBucket.
func prInboxBucket(s PRStatus) inboxBucket { return deckdata.PRInboxBucket(s) }

// PRStatusFetcher returns a tea.Cmd that fetches PR status for one or more
// repos (one gh call per repo, parallel). The fetcher streams one
// PRStatusRepoDoneMsg per repo as it completes (so the per-repo glyphs
// land incrementally and the activity counter ticks down), then emits a
// closing PRStatusDoneMsg when the fan-out completes (or times out).
type PRStatusFetcher func(repoRoots []string) tea.Cmd

// PRStatusRepoDoneMsg is emitted once per repo as its `gh pr list` call
// finishes. The model uses these to update per-row glyphs and tick the
// global pr-status activity incrementally. Err is set for the repo that
// failed; ByHead is non-nil on success.
type PRStatusRepoDoneMsg struct {
	Repo   string
	ByHead map[string]PRStatus
	Err    error
}

// PRStatusDoneMsg signals the end of a PR-status fan-out — every repo
// has either reported a PRStatusRepoDoneMsg or the 10s timeout fired.
// FetchedAt is the wall clock used to refresh the per-repo throttle
// timestamps for successful repos.
type PRStatusDoneMsg struct {
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

// findTargetKind distinguishes the two kinds of stage-1 find targets:
// an unpinned project section and a pinned register section. The deck
// groups pinned rows by register, so a register is a first-class jump
// target alongside projects.
type findTargetKind int

const (
	findTargetProject findTargetKind = iota
	findTargetPin
)

// findTarget is a stage-1 find target. For a project, key is the
// ProjectName; for a pin register, key is the register key
// ("default" or a letter).
type findTarget struct {
	kind findTargetKind
	key  string
}

const findHintAlphabet = "asdfghjklqwertyuiopzxcvbnm"

type Model struct {
	itemsAll []Item
	scope    Scope
	cursor   int
	// keymap is the single source of truth for the deck's row-mode key
	// bindings. Update routes through key.Matches against these
	// fields; renderHelp / deckKeyGroups derive their visible labels
	// from the same struct.
	keymap deckKeyMap
	// styles caches the deck's commonly-reached-for lipgloss styles
	// so hot render paths don't re-allocate them per frame. Initialized
	// in New(); see deckStyles in styles.go for the migration policy.
	styles deckStyles
	// deckViewport renders the windowed deck body each frame.
	// deckYOffset is the persistent first-visible row index (the
	// edge-triggered scroll position that survives across frames);
	// renderList syncs it into deckViewport.YOffset after content is
	// loaded so viewport's internal clamp doesn't reset it to zero
	// against an empty content buffer.
	deckViewport viewport.Model
	deckYOffset  int
	width        int
	height       int
	status       string
	// deckDeps groups the injected dependency callbacks (handler,
	// refresher, fetchers, job handlers, …). Embedded so m.handler etc.
	// still resolve via promotion; see deps.go.
	deckDeps
	// active is the current modal overlay, or nil for row mode. It is the
	// single-slot replacement for the deck's per-mode bool flags; modes are
	// migrated onto it incrementally (see modal.go). When set, Update
	// dispatches keys to it before the legacy flag handlers.
	active      modal
	filterInput textinput.Model
	filtering   bool
	filter      string
	// deleteTarget is the row a confirm-delete modal targets. It outlives
	// the modal: the progress-completion handler reads it to decide the
	// post-delete cursor selection, so it stays on the Model rather than
	// living only on confirmDeleteModal.
	deleteTarget  Item
	pendingSelect Item // after next refresh, cursor jumps to this (project, workspace) if present
	// optimisticCreates holds synthetic workspace rows for creates that
	// have been submitted but whose workspace-state.json entry hasn't
	// landed yet. Keyed by workspaceKey(repoRoot, name). Merged into the
	// view by items() (via rm's mergedItemsAll), reconciled away in
	// refreshDoneMsg once the real row appears, and dropped when the
	// backing create job fails or the spawn fails.
	optimisticCreates map[string]Item
	findMode          bool
	findStage         findStage
	findProject       string            // project name scoping the workspace stage ("" when a pin register scopes it instead)
	findPinGroup      string            // register key scoping the workspace stage ("" when a project scopes it, or in the project stage)
	findProjectHints  map[string]string // project name → stage-1 hint (rendered on project headers)
	findPinHints      map[string]string // register key → stage-1 hint (rendered on pinned section headers)
	findProjectLookup map[string]findTarget
	findProjectPrefix map[rune]bool
	findRowHints      map[int]string
	findRowLookup     map[string]int
	findRowPrefix     map[rune]bool
	findPendingPrefix rune
	refreshing        bool                           // true while a m.refresher() command is in flight
	refreshPending    bool                           // a change signal arrived mid-refresh; re-run on completion
	prStatusByRepo    map[string]map[string]PRStatus // repoRoot → headRefName → status
	prStatusFetchedAt map[string]time.Time           // repoRoot → wall clock of last successful fetch
	// pinChordMode is true after the user presses `m` and before the
	// second key of the pin chord (mm / m<letter> / mD / mR) arrives.
	// While pending, renderList highlights the register letter in each
	// pinned section header so the user can see which registers are in
	// use before choosing one.
	pinChordMode bool
	// gotoTopPending is true after the user presses `g` and before the
	// second `g` of the vim-style `gg` jump-to-top chord arrives. Any
	// other key cancels the chord.
	gotoTopPending  bool
	pinGroupAliases map[string]string // register key → display alias
	// bookmarkPrefix mirrors config.Deck.BookmarkPrefix. When non-empty
	// and a bookmark picked for the new-workspace flow begins with
	// "<prefix>/", the form's workspace-name field is pre-filled with the
	// stripped tail so the user gets a clean default ("andrew/foo" → "foo").
	bookmarkPrefix     string
	userActions        []UserAction
	actionMode         bool
	actionMenuActions  []UserAction
	actionAliasLookup  map[string]UserAction
	spinner            spinner.Model
	busy               bool
	progressViewport   viewport.Model
	progressMode       bool
	progressTitle      string
	progressSteps      []ProgressStep
	progressLog        []string
	progressErr        error
	progressDone       bool
	progressDoneAction Action
	progressChan       chan progressEvent
	jobs               []Job
	// reviewSetups tracks virtual-row review flows that have been
	// dispatched but whose async job hasn't finished setting up yet.
	// Keyed by reviewSetupKey (repoRoot + PR). Guards against a second
	// enter re-dispatching the same review while the first is still
	// creating the workspace / priming the reviewer. Cleared when the
	// backing job reaches a terminal state (or dispatch fails).
	reviewSetups map[string]bool

	// New-workspace form. When newWorkspaceMode is true the deck's
	// View renders the form in place of the row list and Update
	// delegates key handling to the form. See doc.go for the
	// "modal-state inside Model, never a nested tea.Program"
	// architectural constraint.
	newWorkspaceMode bool
	newWorkspaceForm newWorkspaceForm
	newWorkspaceRepo string
	// newWorkspacePR pins the just-created workspace to a PR when the form
	// was opened from a virtual "mine" inbox row, so the new workspace
	// links to the PR (glyph + status) without reopening the deck. Carried
	// alongside the form because it isn't an editable form field. Zero =
	// the ordinary create path (no PR link).
	newWorkspacePR int

	// Rename form. Same modal-state-inside-Model pattern as the
	// new-workspace form.
	renameMode bool
	renameForm renameWorkspaceForm

	// Send-prompt form: A on a workspace row opens a modal that lets
	// the user type a prompt and dispatch it to the workspace's agent.
	// Same modal-state-inside-Model pattern as the rename form.
	promptMode bool
	promptForm promptForm

	// activities is the ordered list of in-flight background
	// operations rendered in the bottom status bar. See activity.go.
	activities []Activity

	// devURLs holds the most recent dev-server URL discovered for each
	// tmux session, keyed by session name. Replaced wholesale on every
	// DevURLsMsg so disappearing servers clear automatically.
	devURLs map[string]string
}

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
	items []Item
	err   error
}

func RefreshDoneMsg(items []Item, err error) tea.Msg {
	return refreshDoneMsg{items: items, err: err}
}

func New(items []Item, handler Handler) Model {
	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 64
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colSpinner))
	m := Model{
		itemsAll:          append([]Item(nil), items...),
		scope:             ScopeAll,
		findProjectHints:  map[string]string{},
		findPinHints:      map[string]string{},
		findProjectLookup: map[string]findTarget{},
		findProjectPrefix: map[rune]bool{},
		findRowHints:      map[int]string{},
		findRowLookup:     map[string]int{},
		findRowPrefix:     map[rune]bool{},
		filterInput:       fi,
		deckViewport:      newDeckViewport(),
		progressViewport:  newProgressViewport(),
		keymap:            newDeckKeyMap(),
		styles:            newDeckStyles(),
		spinner:           sp,
	}
	m.handler = handler
	if idx := m.indexCurrent(); idx >= 0 {
		m.cursor = idx
	}
	return m
}

// indexCurrent returns the index in the visible items() list of the
// workspace whose tmux session the user is currently focused on, or -1
// when none. The cursor is indexed into items() (filtered + sorted), so
// callers that set m.cursor from this must walk the visible list, not
// itemsAll.
func (m Model) indexCurrent() int {
	for i, it := range m.items() {
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

// WithDevURLDiscoverer installs the callback that resolves tmux
// sessions to dev-server URLs (one per session, see
// internal/portcapture). When set, the deck schedules a 2s tick that
// dispatches the discoverer; the `u` key opens the URL of the
// selected workspace in the system browser.
func (m Model) WithDevURLDiscoverer(d DevURLDiscoverer) Model {
	m.devURLDiscoverer = d
	return m
}

func (m Model) WithStateChangeWatcher(w StateChangeWatcher) Model {
	m.stateWatcher = w
	return m
}

// WithHookInstaller installs the callback that (re)installs the global
// agent integrations on deck open. When set, Init fires it asynchronously
// so a first paint never blocks on filesystem writes; the result surfaces
// via HookInstallDoneMsg.
func (m Model) WithHookInstaller(h HookInstaller) Model {
	m.hookInstaller = h
	return m
}

func (m Model) WithPRFetcher(f PRFetcher) Model {
	m.prFetcher = f
	return m
}

// WithReviewReloader installs the callback that reloads a stale tuicr
// review window on PR-repair submit (see ReviewReloader). Without it,
// repair prompts send unchanged (the pre-reload behavior).
func (m Model) WithReviewReloader(r ReviewReloader) Model {
	m.reviewReloader = r
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
	// items() ordering depends on the PR cache (titles drive the sort).
	// New() set m.cursor against the pre-seed order, so once the seed
	// lands the cursor would point at a different row. Re-resolve to
	// the "current" workspace's new index so the deck opens onto the
	// workspace the user is actually in.
	if idx := m.indexCurrent(); idx >= 0 {
		m.cursor = idx
	}
	return m
}

func (m Model) WithBookmarkFetcher(f BookmarkFetcher) Model {
	m.bookmarkFetcher = f
	return m
}

// WithTrunkResolver installs the per-repo trunk-bookmark lookup used by
// the new-workspace form's Start-from default. Without it, the form
// falls back to literal "main".
func (m Model) WithTrunkResolver(r TrunkResolver) Model {
	m.trunkResolver = r
	return m
}

// WithBookmarkLinkHandler installs the persistence callback used by the B-key
// bookmark linker. Without it, the linker shows a "not configured" status.
func (m Model) WithBookmarkLinkHandler(h BookmarkLinkHandler) Model {
	m.bookmarkLinkHandler = h
	return m
}

// WithPRNumberLinkHandler installs the persistence callback used by the
// `p s` chord. Without it, the chord shows a "not configured" status.
func (m Model) WithPRNumberLinkHandler(h PRNumberLinkHandler) Model {
	m.prNumberLinkHandler = h
	return m
}

// WithPinGroupHandler installs the persistence callback used by the
// `g` pin chord. Without it, the chord shows a "not configured" status.
func (m Model) WithPinGroupHandler(h PinGroupHandler) Model {
	m.pinGroupHandler = h
	return m
}

// WithPinGroupAliasHandler installs the persistence callback used by
// the `gR` register-alias rename.
func (m Model) WithPinGroupAliasHandler(h PinGroupAliasHandler) Model {
	m.pinGroupAliasHandler = h
	return m
}

// WithPinGroupAliases seeds the register→alias display map loaded from
// the global pin-groups file at deck open.
func (m Model) WithPinGroupAliases(aliases map[string]string) Model {
	m.pinGroupAliases = aliases
	return m
}

// WithBookmarkPrefix installs the configured bookmark prefix so the deck can
// strip it when proposing a workspace name from a picked bookmark. Pass "" to
// disable the strip (default).
func (m Model) WithBookmarkPrefix(prefix string) Model {
	m.bookmarkPrefix = strings.TrimRight(strings.TrimSpace(prefix), "/")
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

// WithJobDeleteWorkspaceRetryHandler installs the recovery handler used
// by `D` in the jobs overlay for jobs whose ErrorKind is
// "stale_workspace". The handler deletes the workspace named in the
// spec and re-spawns the original job.
func (m Model) WithJobDeleteWorkspaceRetryHandler(h JobDeleteWorkspaceRetryHandler) Model {
	m.jobDeleteWorkspaceRetry = h
	return m
}

// Scope returns the active scope (used by tests).
func (m Model) Scope() Scope { return m.scope }

// WithInitialScope sets the scope the deck starts in. Cycling via `P`
// still moves through every scope; this only changes the entry point.
func (m Model) WithInitialScope(s Scope) Model {
	m.scope = s
	return m
}

func scopeLabel(scope Scope) string {
	switch scope {
	case ScopeInbox:
		return "inbox"
	case ScopeAttention:
		return "attention"
	default:
		return "all"
	}
}

// UserActionsResolver returns the user actions available for a given
// workspace repo root. When set, the deck consults it each time the
// action menu opens so cross-project decks resolve actions against the
// SELECTED workspace's config, not the deck-startup repo.
type UserActionsResolver func(repoRoot string) []UserAction

func (m Model) findUserAction(name string) (UserAction, bool) {
	for _, ua := range m.actionMenuActions {
		if ua.Name == name {
			return ua, true
		}
	}
	for _, ua := range m.userActions {
		if ua.Name == name {
			return ua, true
		}
	}
	return UserAction{}, false
}

func (m Model) WithUserActions(actions []UserAction) Model {
	m.userActions = actions
	m.actionAliasLookup = aliasLookup(actions)
	return m
}

func (m Model) WithUserActionsResolver(r UserActionsResolver) Model {
	m.userActionsResolver = r
	return m
}

// userActionsForRepo returns the action set the menu should show for
// the workspace at repoRoot. Falls back to the static list passed via
// WithUserActions when no resolver is configured or it yields nothing.
func (m Model) userActionsForRepo(repoRoot string) []UserAction {
	if m.userActionsResolver != nil && strings.TrimSpace(repoRoot) != "" {
		if list := m.userActionsResolver(repoRoot); len(list) > 0 {
			return list
		}
	}
	return m.userActions
}

func aliasLookup(actions []UserAction) map[string]UserAction {
	out := make(map[string]UserAction, len(actions))
	for _, a := range actions {
		if a.Alias != "" {
			out[a.Alias] = a
		}
	}
	return out
}

// rm builds the deckdata read-model view from the model's current raw
// inputs. It's a cheap value (maps/slices are reference types), so the
// read-model delegations below rebuild it per call rather than caching.
func (m Model) rm() deckdata.View {
	return deckdata.View{
		All:            m.mergedItemsAll(),
		Scope:          m.scope,
		Filter:         m.filter,
		PRStatusByRepo: m.prStatusByRepo,
		PinAliases:     m.pinGroupAliases,
		Attention:      AttentionIncluded,
	}
}

// mergedItemsAll is itemsAll plus any optimistic create rows whose real
// row hasn't landed yet, so a just-submitted create appears in the deck
// immediately. Optimistic rows superseded by a real row of the same
// (repo, name) are dropped so a just-persisted workspace isn't shown
// twice during the reconcile window.
func (m Model) mergedItemsAll() []Item {
	// Drop "unmanaged" (live-session-only) rows for workspaces a delete job
	// has already removed from state but whose tmux session kill is deferred
	// to popup exit. Without this, the row flips from the real workspace to an
	// adoptable "(live tmux session, not in store)" entry the moment the
	// delete job finishes, and lingers until the deck closes.
	base := make([]Item, 0, len(m.itemsAll)+len(m.optimisticCreates))
	for _, it := range m.itemsAll {
		if it.Status == "unmanaged" && m.hasDeleteJobFor(it.ProjectName, it.WorkspaceName) {
			continue
		}
		base = append(base, it)
	}
	if len(m.optimisticCreates) == 0 {
		return base
	}
	real := make(map[string]bool, len(base))
	for _, it := range base {
		real[workspaceKey(it.RepoRoot, it.WorkspaceName)] = true
	}
	for key, opt := range m.optimisticCreates {
		if real[key] {
			continue
		}
		base = append(base, opt)
	}
	return base
}

// hasDeleteJobFor reports whether any delete job (in-flight or already done)
// targets the given project + workspace. Unlike workspaceDeleteJob it counts
// terminal jobs too, because a completed delete removes the state row while
// its tmux session lives on until popup exit — so the job record is what
// tells the deck the leftover session is a teardown, not an adoptable
// workspace.
func (m Model) hasDeleteJobFor(projectName, workspaceName string) bool {
	ws := normalizeWorkspaceName(workspaceName)
	if ws == "" {
		return false
	}
	for _, j := range m.jobs {
		if j.Action != "delete" {
			continue
		}
		if filepath.Base(strings.TrimSpace(j.RepoRoot)) != projectName {
			continue
		}
		if normalizeWorkspaceName(j.WorkspaceName) == ws {
			return true
		}
	}
	return false
}

// items returns the visible, filtered, sorted deck rows for the current
// scope. See deckdata.View.Items.
func (m Model) items() []Item { return m.rm().Items() }

// itemInboxBucket classifies a workspace for the inbox scope. See
// deckdata.View.InboxBucket.
func (m Model) itemInboxBucket(it Item) inboxBucket { return m.rm().InboxBucket(it) }

// displayLabel returns the text that renders on a row: "#N title" when a
// PR is resolvable from the cache, falling back to the workspace name.
// See deckdata.View.DisplayLabel.
func (m Model) displayLabel(it Item) string { return m.rm().DisplayLabel(it) }

// Nerd Font glyphs used on the meta line. Both require a Nerd Font
// to render \u2014 they fall back to missing-glyph boxes otherwise.
const (
	glyphBranch   = "\uf418"     // nf-oct-git_branch
	glyphKeyboard = "\U000F030C" // nf-md-keyboard
	glyphReturn   = "\U000F0311" // nf-md-keyboard_return \u2014 leads the "to review" hint on virtual rows
	glyphBlocked  = "\uf023"     // nf-fa-lock \u2014 stacked PR blocked by an unready ancestor
)

// metaLine returns the secondary text for a workspace row in a dense
// glyph-prefixed format: "@author · <branch> · :port · <kbd> \"prompt\"".
// Glyphs disambiguate segments so labels can stay out:
//
//	@                 author (PR author)
//	nf-oct-git_branch branch (jj bookmark; HeadDesc fallback)
//	:                 port   (dev URL's port)
//	nf-md-keyboard    prompt (truncated PromptPreview, quoted)
//
// Each slot drops out when empty; falls back to the workspace name
// when none of the slots resolve.
func (m Model) metaLine(it Item) string {
	// When the agent is actively working through a dev loop, its progress
	// is the most useful thing to show — it replaces the port/branch meta
	// with "<done>/<total> · <phase> · ▶ <current unit>". Populated off the
	// render path by the deck refresher (internal/cli/deck.go) and only for
	// rows with real progress, so a nil DevLoop falls through to the
	// standard meta line below.
	if it.DevLoop != nil {
		if s := formatDevLoopMeta(*it.DevLoop); s != "" {
			return s
		}
	}
	var parts []string
	// Pinned rows are lifted out of their project group into a register
	// section, so the project context is otherwise lost. Lead the meta
	// line with it (all / attention scopes only — the inbox scope keeps
	// the project on the primary row as a chip and renders no pinned
	// region).
	if m.scope != ScopeInbox && strings.TrimSpace(it.PinGroup) != "" {
		if p := strings.TrimSpace(it.ProjectName); p != "" {
			parts = append(parts, "["+p+"]")
		}
	}
	pr, hasPR := m.resolvePRStatus(it)
	if hasPR {
		if author := strings.TrimSpace(pr.Author); author != "" {
			parts = append(parts, "@"+author)
		}
	}
	// Bookmark wins over HeadDesc — see note in the prior revision:
	// bookmarks load sync; HeadDesc arrives via the async jj enrichment
	// pass and would visibly "pop" the slot from branch name → commit
	// subject if preferred.
	branch := strings.TrimSpace(it.Bookmark)
	if branch == "" {
		branch = strings.TrimSpace(it.HeadDesc)
	}
	if branch != "" {
		parts = append(parts, glyphBranch+" "+branch)
	}
	if port := devURLPort(m.devURLs[it.SessionName]); port != "" {
		parts = append(parts, ":"+port)
	}
	if hasPR {
		if stale := prStaleSuffix(pr, it.BookmarkCommitID); stale != "" {
			parts = append(parts, stale)
		}
	}
	if prompt := promptPreviewSnippet(it.PromptPreview, 40); prompt != "" {
		parts = append(parts, glyphKeyboard+` "`+prompt+`"`)
	}
	if it.Virtual {
		// No local workspace — call out the one action that exists. A PR
		// awaiting your review is "to review"; everything else (your own
		// PR, or a stack-completion link that's someone else's) is "to
		// check out" — enter opens the new-workspace form for it.
		hint := "to check out"
		if hasPR && (pr.ReviewRequested || pr.ReviewRerequested) {
			hint = "to review"
		}
		parts = append(parts, glyphReturn+" "+hint)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	// Fallback when no PR/branch/dev/prompt slots resolve: show the
	// branch glyph + jj short change-id. Keep the shape constant from
	// first paint — render "<glyph> …" while the change-id is still
	// loading from the async enrichment pass, then fill it in. Avoids
	// the workspace-name → change-id swap that previously read as a
	// "pop" in the row.
	id := strings.TrimSpace(it.HeadChangeID)
	if id == "" {
		id = "…"
	}
	return glyphBranch + " " + id
}

// renderMetaText colors the semantic tokens of an already-truncated
// meta line — only :port is tinted (blue); everything else (author,
// branch, prompt, stale chip, the virtual-row "to review" hint,
// separators) stays muted. Operating on the truncated plain string
// keeps the width math in metaLine/truncate ANSI-free; coloring a token
// the truncation clipped is harmless since the prefix that selects its
// color survives.
func (m Model) renderMetaText(text string) string {
	segs := strings.Split(text, " · ")
	for i, seg := range segs {
		segs[i] = m.metaSegStyle(seg).Render(seg)
	}
	return strings.Join(segs, m.styles.Muted.Render(" · "))
}

// metaSegStyle picks the style for one meta-line segment: :port blue,
// everything else (author, branch, prompt, the "to review" hint) muted.
// Author (teal) and branch (green) tints were tried and read as too much
// color repeated on every row, so the meta line stays mostly muted with
// only the port token tinted for a touch of contrast.
func (m Model) metaSegStyle(seg string) lipgloss.Style {
	if strings.HasPrefix(seg, ":") {
		return m.styles.Port
	}
	return m.styles.Muted
}

// devLoopUnitGlyph marks the in-progress unit on the dev-loop meta line,
// matching the "▶ <current>" cursor the `w` watch overlay uses so the row
// and the overlay read as the same signal.
const devLoopUnitGlyph = "▶"

// formatDevLoopMeta renders a dev-loop progress summary as a meta-line
// string, progress-first: "<done>/<total> · <phase> · ▶ <task>". Each slot
// drops out when empty (no todo list → no count; no in-progress unit → no
// task), and the whole thing is empty when there is nothing to show —
// metaLine treats that as "fall through to the normal meta line". The
// result flows through renderMetaText like any other meta text, so it stays
// muted and its width math (split on " · ") is unchanged.
//
// A finished loop (all units done, e.g. 12/12) also renders empty: there's
// no in-progress work left to surface, so the row reverts to its normal
// branch/port meta rather than pinning a "done" count.
func formatDevLoopMeta(s DevLoopSummary) string {
	if s.Total > 0 && s.Done >= s.Total {
		return ""
	}
	var parts []string
	if s.Total > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", s.Done, s.Total))
	}
	if s.Phase != "" {
		parts = append(parts, s.Phase)
	}
	if task := strings.TrimSpace(s.Task); task != "" {
		parts = append(parts, devLoopUnitGlyph+" "+task)
	}
	return strings.Join(parts, " · ")
}

// devURLPort extracts the port from a dev URL like
// "http://localhost:5173" → "5173". Returns "" when the URL can't be
// parsed or has no port.
func devURLPort(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Port()
}

// promptPreviewSnippet sanitizes a multi-line prompt preview into a
// single-line snippet of at most n characters. Replaces newlines with
// spaces, collapses runs of whitespace, and truncates with an ellipsis
// — so even a long pasted prompt fits cleanly on the meta line.
func promptPreviewSnippet(p string, n int) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.Join(strings.Fields(p), " ")
	return truncate(p, n)
}

// initKickMsg drives the first-paint side effects (initial enrich
// refresh, PR-status fan-out) from Update so they can register
// activities on the model. Init can't mutate the model, so it dispatches
// this self-message and lets the Update path start activities + return
// the batched cmds.
type initKickMsg struct{}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{scheduleRefreshTick()}
	if m.stateWatcher != nil {
		cmds = append(cmds, m.stateWatcher())
	}
	if m.hookInstaller != nil {
		// Async, fire-and-forget: re-sync the global agent hooks in case
		// they've drifted (awp upgraded, settings.json hand-edited). The
		// install is idempotent and only writes on drift, so this is a
		// no-op most opens.
		cmds = append(cmds, m.hookInstaller())
	}
	if m.devURLDiscoverer != nil {
		// Kick off the dev-URL fan-out immediately so users don't wait
		// 2 s for the first paint when reopening a deck with running
		// servers, then enter the recurring tick from the result.
		cmds = append(cmds, m.devURLDiscoverer(), scheduleDevURLTick())
	}
	// Defer the enrichment and PR-status fan-out to Update via an
	// initKickMsg so the matching activities can be registered on the
	// model (Init has no way to mutate the model).
	cmds = append(cmds, func() tea.Msg { return initKickMsg{} })
	// Start the spinner tick loop once at boot. The tick handler keeps
	// the loop alive even when nothing is spinning (ticks return early
	// without advancing the frame), so new activities arriving mid-
	// session never need to re-bootstrap. Without this kickoff (and
	// the always-perpetuate handler), the spinner can freeze for a
	// window between "last activity expired" and "a foreground action
	// or jobsListMsg batched a fresh Tick."
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

// prStatusRefreshCmd returns a tea.Cmd that fetches PR status for every repo
// that has at least one non-default workspace AND has not been fetched within
// prStatusMinInterval of now, and updates the model to reflect a started
// pr-status activity. Returns the original model and a nil cmd if no repos
// are due (or no fetcher is configured).
//
// Skipped when a pr-status activity is already in flight: re-issuing the
// fetch while the previous batch is still running would refetch the same
// repos and (with startActivity's accumulate behavior) inflate Total in
// the activity bar — the user-visible symptom was "11/12 with 6
// projects." Force-refreshes via forcePRStatusRefresh bypass this guard
// because they're explicit user signals tied to a freshly-changed
// PR↔workspace mapping.
func (m Model) prStatusRefreshCmd(now time.Time) (Model, tea.Cmd) {
	if m.prStatusFetcher == nil {
		return m, nil
	}
	for _, a := range m.activities {
		if a.ID == "pr-status" && a.FinishedAt.IsZero() {
			return m, nil
		}
	}
	repos := m.prStatusRepos(now)
	if len(repos) == 0 {
		return m, nil
	}
	m = m.startActivity("pr-status", "pr-status", len(repos))
	return m, m.prStatusFetcher(repos)
}

// forcePRStatusRefresh dispatches an immediate fetch for the named
// repo, bypassing both the prStatusMinInterval cooldown AND the
// "must have a PR-bearing workspace" eligibility check that the
// periodic policy applies. Used by flows where the user just did
// something that materially affects which PR belongs to which
// workspace (new workspace from bookmark, review of a PR, `p s` save)
// — the trigger is a direct user signal, not a state observation, so
// the row needs to reflect the new association even before the
// downstream m.itemsAll picks up the PRNumber.
func (m Model) forcePRStatusRefresh(repo string) (Model, tea.Cmd) {
	repo = strings.TrimSpace(repo)
	if repo == "" || m.prStatusFetcher == nil {
		return m, nil
	}
	if m.prStatusFetchedAt != nil {
		delete(m.prStatusFetchedAt, repo)
	}
	m = m.startActivity("pr-status", "pr-status", 1)
	return m, m.prStatusFetcher([]string{repo})
}

// prStatusRepos returns the deduplicated, throttled list of repo roots
// that should be fetched for PR status. A repo is eligible iff at least
// one of its workspace items has PRNumber > 0 or a tracked Bookmark
// (see prStatusReposPolicy). The throttle (prStatusMinInterval)
// suppresses repos whose last successful fetch is too recent.
func (m Model) prStatusRepos(now time.Time) []string {
	return prStatusReposPolicy(m.itemsAll, m.prStatusFetchedAt, now)
}

// prStatusReposPolicy is the pure-function form of prStatusRepos. The
// model-level wrapper above plus forcePRStatusRefresh both go through
// here so the eligibility + throttle logic lives in one place. Splitting
// it out lets callers (and tests) reason about the policy without
// constructing a full Model.
//
// Eligibility: any workspace under the repo that either pins a PR
// explicitly (PRNumber > 0) OR has a tracked bookmark. The bookmark
// branch keeps legacy entries (created before the PROverride→PRNumber
// rename) in the fetch loop, which is the precondition the on-load
// migration in loadDeckItems needs in order to populate PRNumber.
// Without it, a repo whose entries all only have Bookmark set would be
// locked out of the cache forever — no fetch → no cache data → no
// migration match → no PRNumber → no eligibility, etc.
func prStatusReposPolicy(items []Item, lastFetch map[string]time.Time, now time.Time) []string {
	eligible := make(map[string]bool)
	for _, it := range items {
		repo := strings.TrimSpace(it.RepoRoot)
		if repo == "" {
			continue
		}
		if it.PRNumber > 0 || strings.TrimSpace(it.Bookmark) != "" {
			eligible[repo] = true
		}
	}
	out := make([]string, 0, len(eligible))
	for repo := range eligible {
		if last, ok := lastFetch[repo]; ok && now.Sub(last) < prStatusMinInterval {
			continue
		}
		out = append(out, repo)
	}
	return out
}

func (m Model) canBackgroundRefresh() bool {
	return m.refresher != nil && !m.busy && !m.progressMode &&
		m.active == nil &&
		!m.filtering &&
		!m.findMode && !m.actionMode &&
		!m.newWorkspaceMode
}

// requestRefresh starts a row refresh, coalescing concurrent requests.
//
// loadDeckItems snapshots workspace-state.json at the moment the
// refresher command *runs*, not when its result lands. If a change
// signal (StateChangedMsg from a new/delete, an explicit post-action
// refresh) arrived while an earlier refresh was still in flight, the
// earlier refresh's snapshot may predate the write — so naively
// dropping the new request (the old `if !m.refreshing` guard) left the
// deck showing stale items until the next 5s poll. Letting both run
// concurrently was no better: the two results land in nondeterministic
// order and a stale snapshot can overwrite a fresh one.
//
// So: only one refresh runs at a time. If one is already in flight,
// record that another is needed (refreshPending) and return a nil cmd;
// the refreshDoneMsg handler re-fires once the in-flight one lands,
// guaranteeing the final read happens strictly after the latest signal.
// withActivity registers the "enrich" spinner activity (explicit
// user-driven refreshes); background/coalesced refreshes pass false.
func (m Model) requestRefresh(withActivity bool) (Model, tea.Cmd) {
	if m.refresher == nil {
		return m, nil
	}
	if m.refreshing {
		m.refreshPending = true
		return m, nil
	}
	m.refreshing = true
	if withActivity {
		m = m.startActivity("enrich", "enrich", 0)
	}
	return m, m.refresher()
}

func (m Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	// After every Update, re-clamp the deck-list scroll offset so the
	// cursor stays in view if it moved (j/k/jump/etc.) and so the offset
	// stays sane when items shrink or the window resizes. Centralizing
	// this here means individual handlers don't have to remember to call
	// the helper — value-receiver Update means our mutation here flows
	// back to the caller via the named return.
	defer func() {
		mm, ok := model.(Model)
		if !ok {
			return
		}
		(&mm).clampDeckViewport()
		model = mm
	}()
	// huh-backed form modals are stateful tea.Models — they need to
	// receive non-KeyMsg messages too (nextFieldMsg, updateFieldMsg,
	// the Init sequence, etc.). Route every message through the
	// active form's update before falling into the main switch so the
	// form's state machine actually progresses. The dispatch helpers
	// return immediately, never falling through to the deck's normal
	// handlers, which is the right semantics while the form has modal
	// focus.
	// Route input to the active modal form — EXCEPT the self-perpetuating
	// background ticks. refreshTickMsg / devURLTickMsg each reschedule the
	// next tick from their own handler in the main switch below; the loop
	// only stays alive because every tick's handler fires the following
	// one. If a form dispatcher swallowed a tick (it forwards everything
	// to huh, which ignores these deck-private message types and returns
	// no follow-up cmd) the loop would die silently for the rest of the
	// session. Since the new-workspace form is typically open for several
	// seconds and refreshInterval is 5s, a tick almost always lands while
	// it's up — so the background refresh/job poll that reconciles a
	// freshly-created workspace never fires again, and the row stays stuck
	// on its optimistic "creating…" state until a manual deck reload. The
	// tick handlers already gate their actual work on canBackgroundRefresh()
	// (false while a form is open), so letting them through only keeps the
	// timer alive; it does not disturb the form.
	switch msg.(type) {
	case refreshTickMsg, devURLTickMsg:
		// fall through to the main switch, which reschedules the tick.
	default:
		if m.newWorkspaceMode {
			return m.dispatchNewWorkspaceForm(msg)
		}
		if m.renameMode {
			return m.dispatchRenameForm(msg)
		}
		if m.promptMode {
			return m.dispatchPromptForm(msg)
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case initKickMsg:
		cmds := []tea.Cmd{}
		// enrich: register the activity for the cold-start refresh, then
		// dispatch the fetch. The matching finishActivity runs on
		// refreshDoneMsg.
		var refreshCmd tea.Cmd
		m, refreshCmd = m.requestRefresh(true)
		if refreshCmd != nil {
			cmds = append(cmds, refreshCmd)
		}
		var prCmd tea.Cmd
		m, prCmd = m.prStatusRefreshCmd(time.Now())
		if prCmd != nil {
			cmds = append(cmds, prCmd)
		}
		return m, batchCmds(cmds...)
	case spinner.TickMsg:
		// Keep the tick loop alive even when idle. When there's nothing
		// to spin for we don't advance the spinner frame (skipping the
		// .Update call), but we do schedule the next tick so a new
		// activity arriving mid-session animates immediately. Without
		// this, the loop dies the moment the last activity expires and
		// the next pr-status fetch (or any background work) renders a
		// frozen glyph until a foreground action / jobsListMsg
		// bootstraps a new Tick.
		if !m.busy && len(m.activities) == 0 {
			return m, m.spinner.Tick
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// Refresh the inline loadingItem glyph for any picker that's
		// currently fetching. SetItems is cheap (single placeholder
		// item, no list state reset) and re-reading m.spinner.View()
		// each tick is the simplest way to animate the glyph next to
		// the loading message without writing a custom delegate.
		glyph := m.spinner.View()
		if bp, ok := m.active.(*bookmarkPicker); ok && bp.loading {
			bp.tickLoading(glyph)
		}
		if rp, ok := m.active.(*reviewPicker); ok && rp.loading {
			rp.tickLoading(glyph)
		}
		if op, ok := m.active.(*openPicker); ok && op.loading {
			op.tickLoading(glyph)
		}
		return m, cmd
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
	case devURLTickMsg:
		// Background poll: fire the discoverer (if installed) and
		// reschedule the next tick. The result lands as DevURLsMsg.
		// Unlike refresh, we don't bother gating on overlay state —
		// the discoverer is a silent fan-out that touches no UI.
		if m.devURLDiscoverer == nil {
			return m, nil
		}
		return m, tea.Batch(m.devURLDiscoverer(), scheduleDevURLTick())
	case DevURLsMsg:
		m.devURLs = msg.URLs
		return m, nil
	case StateChangedMsg:
		cmds := []tea.Cmd{}
		if m.stateWatcher != nil {
			cmds = append(cmds, m.stateWatcher())
		}
		if m.canBackgroundRefresh() {
			var refreshCmd tea.Cmd
			m, refreshCmd = m.requestRefresh(false)
			cmds = append(cmds, refreshCmd)
		}
		return m, tea.Batch(cmds...)
	case jobsListMsg:
		prevJobs := m.jobs
		m.jobs = msg.jobs
		m.pruneReviewSetups()
		if jm, ok := m.active.(*jobsModal); ok {
			jm.sync(m.jobs)
			jm.refreshViewport()
		}
		// Retire an optimistic create row whose job has failed — no real
		// row will ever land for it.
		m.pruneOptimisticCreates()
		var expireCmd tea.Cmd
		m, expireCmd = m.syncJobActivities(msg.jobs)
		// A create/review job adds a workspace row and a delete job
		// removes one, but loadDeckItems reads workspace-state.json and
		// the change only shows once the deck refreshes. Waiting for the
		// 5 s poll (or the best-effort fsnotify watcher, which can miss or
		// coalesce the write) makes the row appear/disappear late or not
		// until the next unrelated refresh. So the moment such a job flips
		// to done, kick a row refresh explicitly.
		var createRefreshCmd tea.Cmd
		if workspaceJobJustFinished(prevJobs, msg.jobs) {
			m, createRefreshCmd = m.requestRefresh(true)
		}
		// Bootstrap the spinner whenever activities exist so its glyph
		// in the bottom bar actually animates. The spinner.TickMsg
		// handler self-perpetuates while len(m.activities) > 0; this
		// call is the kickstart when activities first appear from a
		// background refresh that wasn't preceded by a foreground
		// action (which would already batch a Tick).
		if len(m.activities) > 0 {
			return m, tea.Batch(expireCmd, createRefreshCmd, m.spinner.Tick)
		}
		return m, batchCmds(expireCmd, createRefreshCmd)
	case activityExpireMsg:
		m = m.dropActivity(msg.id)
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
			// Spawn failed before a job record exists, so the jobs-list
			// sync will never clear the guard — drop it here so the user
			// can retry the review.
			if msg.spec.Action == "review" {
				delete(m.reviewSetups, reviewSetupKey(msg.spec.RepoRoot, msg.spec.Arg))
			}
			// Retire the optimistic row (create-workspace and review both
			// add one) since no job will land to reconcile it away.
			if isSetupJobAction(msg.spec.Action) {
				m.dropOptimisticCreate(msg.spec.RepoRoot, msg.spec.WorkspaceName)
			}
			m.status = "create: " + msg.err.Error()
			return m, nil
		}
		// Kick off a fresh tray refresh so the user sees the new
		// "running" count immediately rather than waiting up to 2 s
		// for the next tick.
		return m, refreshJobsListCmd(m.jobsListRefresher)
	case NewWorkspaceDoneMsg:
		// Legacy message from the now-removed nested-tea.Program form.
		// Kept for any in-flight callers; the inline form bypasses this
		// path entirely.
		if msg.Cancelled {
			m.status = ""
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
			m.syncProgressViewport()
		case progressEventDone:
			return m.Update(actionResultMsg{action: msg.ev.action, arg: msg.ev.arg, item: msg.ev.item, err: msg.ev.err})
		}
		return m, waitForProgress(m.progressChan)
	case actionResultMsg:
		m.busy = false
		m.progressDone = true
		m.progressDoneAction = msg.action
		var renameExpireCmd tea.Cmd
		if msg.action == ActionRename {
			m, renameExpireCmd = m.finishActivity("workspace:rename:" + msg.item.WorkspaceName)
		}
		var promptExpireCmd tea.Cmd
		if msg.action == ActionSendPrompt {
			m, promptExpireCmd = m.finishActivity("workspace:prompt:" + msg.item.WorkspaceName)
		}
		if msg.err != nil {
			m.progressErr = msg.err
			if n := len(m.progressSteps); n > 0 && m.progressSteps[n-1].State == StepRunning {
				m.progressSteps[n-1].State = StepError
			}
			m.status = "error: " + msg.err.Error()
			return m, batchCmds(renameExpireCmd, promptExpireCmd)
		}
		if n := len(m.progressSteps); n > 0 && m.progressSteps[n-1].State == StepRunning {
			m.progressSteps[n-1].State = StepDone
		}
		m.status = fmt.Sprintf("%s: %s", actionLabel(msg.action, msg.arg), msg.item.WorkspaceName)
		if msg.action == ActionDelete {
			return m, nil
		}
		if msg.action == ActionMergePR {
			// Stay in the progress modal showing the success result; the
			// user dismisses with esc/q/enter. "submitted" rather than
			// "merged" — on a merge-queue repo the PR is added to the
			// queue, not merged on the spot. The log shows which one
			// happened.
			m.status = fmt.Sprintf("PR #%s: merge submitted", msg.arg)
			// Refetch this repo's PR status now so the merged PR drops out
			// of the inbox (the fetch replaces the repo's cache with the
			// open-PR set, sans the just-merged one). A plain refresh would
			// be suppressed by the prStatusMinInterval throttle and keep
			// showing the stale "open" glyph; forcePRStatusRefresh bypasses
			// it.
			var prCmd tea.Cmd
			m, prCmd = m.forcePRStatusRefresh(msg.item.RepoRoot)
			return m, prCmd
		}
		if msg.action == ActionRename {
			m.status = fmt.Sprintf("renamed %s → %s", msg.item.WorkspaceName, msg.arg)
			// Move the cursor to the new name once the refresh lands so
			// the row the user just renamed stays selected.
			m.pendingSelect = Item{ProjectName: msg.item.ProjectName, WorkspaceName: msg.arg}
			var refreshCmd tea.Cmd
			m, refreshCmd = m.requestRefresh(true)
			return m, batchCmds(renameExpireCmd, refreshCmd)
		}
		if msg.action == ActionSendPrompt {
			m.status = fmt.Sprintf("sent prompt → %s/%s", msg.item.ProjectName, msg.item.WorkspaceName)
			return m, promptExpireCmd
		}
		return m, tea.Quit
	case ReviewReloadedMsg:
		m.busy = false
		var expireCmd tea.Cmd
		m, expireCmd = m.finishActivity("workspace:prompt:" + msg.Item.WorkspaceName)
		if msg.Err != nil {
			// Reload (or the subsequent send) failed. Surface it and do not
			// pretend the prompt landed — the reloader sends only after a
			// successful reload, so an error here means nothing was sent.
			m.status = "repair: " + msg.Err.Error()
			return m, expireCmd
		}
		if msg.Reloaded {
			m.status = fmt.Sprintf("repair: reloaded review on %s · sent → %s/%s", msg.ShortHead, msg.Item.ProjectName, msg.Item.WorkspaceName)
		} else {
			m.status = fmt.Sprintf("sent prompt → %s/%s", msg.Item.ProjectName, msg.Item.WorkspaceName)
		}
		return m, expireCmd
	case StateEditDoneMsg:
		if msg.Err != nil {
			m.status = "edit state: " + msg.Err.Error()
		} else {
			m.status = "edit state: done"
		}
		var refreshCmd tea.Cmd
		m, refreshCmd = m.requestRefresh(true)
		return m, refreshCmd
	case HookInstallDoneMsg:
		// Stay quiet when nothing drifted — the common case. Only surface
		// durable feedback when we actually rewrote config, or on error.
		switch {
		case msg.Err != nil:
			m.status = "hooks: " + msg.Err.Error()
		case msg.ClaudeChanged || msg.PiChanged:
			m.status = "hooks: updated agent integrations"
		}
		return m, nil
	case BookmarksDoneMsg:
		m.busy = false
		bp, ok := m.active.(*bookmarkPicker)
		if !ok {
			return m, nil
		}
		if msg.Err != nil {
			m.active = nil
			m.status = "bookmark: " + msg.Err.Error()
			return m, nil
		}
		if len(msg.Bookmarks) == 0 {
			m.active = nil
			m.status = "bookmark: no bookmarks found"
			return m, nil
		}
		bp.setBookmarks(msg.Bookmarks)
		// list.Model renders its own key-help footer (/, enter, esc);
		// duplicating it in the deck's status bar would show the same
		// hints twice.
		m.status = ""
		return m, nil
	case ProjectsDoneMsg:
		m.busy = false
		op, ok := m.active.(*openPicker)
		if !ok {
			return m, nil
		}
		if msg.Err != nil {
			m.active = nil
			m.status = "open: " + msg.Err.Error()
			return m, nil
		}
		if len(msg.Projects) == 0 {
			m.active = nil
			m.status = "open: no projects found (configure deck.project_roots)"
			return m, nil
		}
		op.setProjects(msg.Projects)
		// list.Model renders its own key-help footer; clear the status
		// so the deck's bottom bar doesn't duplicate the picker's hints.
		m.status = ""
		return m, nil
	case PRStatusRepoDoneMsg:
		if m.prStatusByRepo == nil {
			m.prStatusByRepo = make(map[string]map[string]PRStatus)
		}
		if m.prStatusFetchedAt == nil {
			m.prStatusFetchedAt = make(map[string]time.Time)
		}
		if msg.Err == nil && msg.ByHead != nil {
			m.prStatusByRepo[msg.Repo] = msg.ByHead
			m.prStatusFetchedAt[msg.Repo] = time.Now()
		} else if msg.Err != nil {
			m.status = "PR status: " + msg.Err.Error()
		}
		m = m.tickActivity("pr-status", 1)
		return m, nil
	case PRStatusDoneMsg:
		// Per-repo updates have already landed via PRStatusRepoDoneMsg.
		// The closing message just finishes the global activity.
		var expireCmd tea.Cmd
		m, expireCmd = m.finishActivity("pr-status")
		return m, expireCmd
	case PRFetchDoneMsg:
		m.busy = false
		rp, ok := m.active.(*reviewPicker)
		if !ok {
			return m, nil
		}
		if msg.Err != nil {
			m.active = nil
			m.status = "review: " + msg.Err.Error()
			return m, nil
		}
		// setPRs returns the status line (empty unless there were no PRs);
		// on a non-empty list list.Model renders its own key-help footer so
		// the deck's bottom bar stays clear.
		m.status = rp.setPRs(msg.PRs)
		return m, nil
	case refreshDoneMsg:
		m.refreshing = false
		var enrichExpireCmd tea.Cmd
		m, enrichExpireCmd = m.finishActivity("enrich")
		// A change signal arrived while this refresh was in flight, so
		// its snapshot may be stale. Re-run now that the slot is free;
		// this read is guaranteed to start after that signal.
		var rerunCmd tea.Cmd
		if m.refreshPending {
			m.refreshPending = false
			m, rerunCmd = m.requestRefresh(false)
		}
		if msg.err != nil {
			m.status = "refresh: " + msg.err.Error()
			return m, batchCmds(enrichExpireCmd, rerunCmd)
		}
		m.itemsAll = append([]Item(nil), msg.items...)
		// Retire optimistic create rows whose real row has now landed (or
		// whose backing job failed), so the reconciled row replaces the
		// synthetic one.
		m.pruneOptimisticCreates()
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
		// Items just changed — a workspace may have appeared from
		// outside the deck (`awp review`, `jj workspace add`,
		// concurrent deck instance). Re-evaluate the pr-status
		// eligible-repo set; the policy function throttles per repo,
		// so already-fresh repos no-op. Without this trigger, the
		// deck would wait for the next init/link/p-s before noticing
		// a newly-eligible repo.
		var prCmd tea.Cmd
		m, prCmd = m.prStatusRefreshCmd(time.Now())
		cmds := []tea.Cmd{enrichExpireCmd, rerunCmd}
		if prCmd != nil {
			cmds = append(cmds, prCmd)
		}
		return m, batchCmds(cmds...)
	case tea.KeyMsg:
		if m.progressMode {
			// Allow scrolling the log even while the action is still
			// running — useful for long create/delete pipelines where
			// the user wants to read earlier output without waiting
			// for the action to finish.
			switch msg.String() {
			case "pgup", "pgdown", "ctrl+u", "ctrl+d":
				var cmd tea.Cmd
				m.progressViewport, cmd = m.progressViewport.Update(msg)
				return m, cmd
			}
			if !m.progressDone {
				return m, nil
			}
			switch msg.String() {
			case "esc", "q", "enter", "ctrl+c":
				m.progressMode = false
				m.progressSteps = nil
				m.progressLog = nil
				m.progressViewport.SetContent("")
				m.progressErr = nil
				m.progressDone = false
				if m.progressDoneAction == ActionDelete && m.refresher != nil {
					if m.deleteTarget.Current {
						m.pendingSelect = Item{ProjectName: m.deleteTarget.ProjectName, WorkspaceName: "default"}
					}
					var refreshCmd tea.Cmd
					m, refreshCmd = m.requestRefresh(true)
					return m, refreshCmd
				}
				if m.progressDoneAction == ActionMergePR && m.refresher != nil {
					// Reload workspace rows on dismiss. The merged PR's
					// status was already refetched when the merge
					// succeeded (see the ActionMergePR branch in
					// actionResultMsg), so by now the inbox filter has
					// dropped it.
					var refreshCmd tea.Cmd
					m, refreshCmd = m.requestRefresh(true)
					return m, refreshCmd
				}
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
		if m.gotoTopPending {
			m.gotoTopPending = false
			if msg.String() == "g" {
				m.cursor = 0
				return m, nil
			}
			// Any other key cancels the pending `gg` chord.
			m.status = ""
			return m, nil
		}
		if m.pinChordMode {
			m.pinChordMode = false
			switch msg.String() {
			case "esc", "ctrl+c":
				m.status = ""
				return m, nil
			case "m":
				return m.applyPinGroup(pinGroupDefault)
			case "D":
				return m.applyPinGroup("")
			case "R":
				return m.startPinAliasRename()
			}
			// A single lowercase letter targets that register; anything
			// else cancels the chord.
			if r := []rune(msg.String()); len(r) == 1 && r[0] >= 'a' && r[0] <= 'z' {
				return m.applyPinGroup(string(r[0]))
			}
			m.status = ""
			return m, nil
		}
		if m.active != nil {
			cmd := m.active.update(&m, msg)
			return m, cmd
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
				// Inbox-scope find has no project stage to back out
				// to — backspace cancels like esc.
				if m.findStage == findStageWorkspace && m.scope != ScopeInbox {
					m.findStage = findStageProject
					m.findProject = ""
					m.findPinGroup = ""
					m.findRowHints = map[int]string{}
					m.findRowLookup = map[string]int{}
					m.findRowPrefix = map[rune]bool{}
					m.status = "find: project"
					// Re-collapse to the header column and reset scroll.
					m.clampDeckViewport()
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
				m.status = ""
				return m, nil
			}
			if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
				alias := string(msg.Runes[0])
				if ua, ok := m.actionAliasLookup[alias]; ok {
					m.actionMode = false
					// Clear the action-menu listing from the status
					// bar — the triggered action surfaces in the
					// activity bar (and eventually as an action result
					// message), so duplicating it in the grey status
					// segment is noise.
					m.status = ""
					return m.trigger(ActionCustom, ua.Name)
				}
				m.actionMode = false
				m.status = fmt.Sprintf("action: unknown alias %q", alias)
				return m, nil
			}
			return m, nil
		}
		km := m.keymap
		switch {
		case key.Matches(msg, km.Help):
			m.active = newHelpModal()
			// tea.ClearScreen on modal entry: the renderer's
			// previous-frame buffer otherwise leaves stripes of the
			// underlying view visible wherever the popover doesn't
			// write. See doc.go and the matching pattern on `/`
			// (filtering) + the new-workspace form.
			return m, tea.ClearScreen
		case key.Matches(msg, km.Jobs):
			m.active = newJobsModal(m.jobs)
			// tea.ClearScreen on modal entry — same rationale as `?`
			// above. Without this, the deck row list bleeds through
			// the surrounding area of the jobs popover.
			return m, tea.Batch(tea.ClearScreen, refreshJobsListCmd(m.jobsListRefresher))
		case key.Matches(msg, km.Watch):
			item, ok := m.selected()
			if !ok {
				return m, nil
			}
			m.active = newWatchModal(item)
			// tea.ClearScreen on modal entry — same rationale as `J` above.
			return m, tea.Batch(tea.ClearScreen, scheduleWatchTick())
		case key.Matches(msg, km.Quit):
			if m.filter != "" && msg.String() == "esc" {
				m.filter = ""
				m.filterInput.SetValue("")
				m.cursor = 0
				return m, nil
			}
			return m, tea.Quit
		case key.Matches(msg, km.Filter):
			m.filtering = true
			m.filterInput.Focus()
			m.filterInput.SetValue(m.filter)
			// tea.ClearScreen on modal entry; see doc.go and the
			// matching tea.ClearScreen on exit above.
			return m, tea.ClearScreen
		case key.Matches(msg, km.Find):
			if len(m.items()) == 0 {
				return m, nil
			}
			m.startFind()
			return m, nil
		case key.Matches(msg, km.Down):
			if m.cursor < len(m.items())-1 {
				m.cursor++
			}
			return m, nil
		case key.Matches(msg, km.ScopeCycle):
			m.scope = (m.scope + 1) % scopeCount
			m.cursor = 0
			m.status = "scope: " + scopeLabel(m.scope)
			return m, tea.ClearScreen
		case key.Matches(msg, km.Up):
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case key.Matches(msg, km.HalfPageDown):
			m.cursor += m.deckHalfPageStep()
			if m.cursor > len(m.items())-1 {
				m.cursor = len(m.items()) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil
		case key.Matches(msg, km.HalfPageUp):
			m.cursor -= m.deckHalfPageStep()
			if m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil
		case key.Matches(msg, km.Enter):
			return m.trigger(ActionSummon, "")
		case key.Matches(msg, km.AgentWindow):
			return m.trigger(ActionOpenWindow, "agent")
		case key.Matches(msg, km.SendPrompt):
			item, ok := m.selected()
			if !ok {
				m.status = "send prompt: select a workspace row"
				return m, nil
			}
			if item.Virtual {
				m.status = "no workspace yet — press enter to start a review"
				return m, nil
			}
			if strings.TrimSpace(item.WorkspaceName) == "" {
				m.status = "send prompt: select a workspace row"
				return m, nil
			}
			if m2, blocked := m.blockIfSettingUp(item); blocked {
				return m2, nil
			}
			m.promptMode = true
			var initCmd tea.Cmd
			m.promptForm, initCmd = newPromptForm(item, "")
			m.status = "send prompt: type message · enter submit · ctrl+g $EDITOR · esc cancel"
			// tea.ClearScreen on modal entry — same rationale as the
			// other modals (see doc.go).
			return m, batchCmds(initCmd, tea.ClearScreen)
		case key.Matches(msg, km.EditorWindow):
			return m.trigger(ActionOpenWindow, "editor")
		case key.Matches(msg, km.ReviewWindow):
			return m.trigger(ActionOpenWindow, "review")
		case key.Matches(msg, km.ReviewMainWin):
			return m.trigger(ActionOpenWindow, "review:tuicr -r main..@")
		case key.Matches(msg, km.VCSWindow):
			return m.trigger(ActionOpenWindow, "vcs")
		case key.Matches(msg, km.ShellWindow):
			return m.trigger(ActionOpenWindow, "")
		case key.Matches(msg, km.CIWindow):
			return m.trigger(ActionCI, "")
		case key.Matches(msg, km.LastSession):
			if m.handler == nil {
				return m, nil
			}
			return m.startQuickAction(ActionLastSession, Item{}, "")
		case key.Matches(msg, km.Delete):
			item, ok := m.selected()
			if !ok {
				return m, nil
			}
			if item.Virtual {
				m.status = "no workspace yet — press enter to start a review"
				return m, nil
			}
			// Deleting a workspace mid-create races the create subprocess
			// (jj workspace add / bootstrap). Hold off until it finishes.
			if m2, blocked := m.blockIfSettingUp(item); blocked {
				return m2, nil
			}
			m.deleteTarget = item
			var deleteModal *confirmDeleteModal
			var deleteCmd tea.Cmd
			deleteModal, deleteCmd, m.status = newConfirmDelete(item)
			m.active = deleteModal
			return m, deleteCmd
		case key.Matches(msg, km.Rename):
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.WorkspaceName) == "" {
				m.status = "rename: select a workspace row"
				return m, nil
			}
			if item.Virtual {
				m.status = "no workspace yet — press enter to start a review"
				return m, nil
			}
			if strings.TrimSpace(item.WorkspaceName) == "default" {
				m.status = "rename: cannot rename the default workspace"
				return m, nil
			}
			if m2, blocked := m.blockIfSettingUp(item); blocked {
				return m2, nil
			}
			m.renameMode = true
			var initCmd tea.Cmd
			m.renameForm, initCmd = newRenameWorkspaceForm(item)
			m.status = "rename: type new name · enter rename · esc cancel"
			return m, batchCmds(initCmd, tea.ClearScreen)
		case key.Matches(msg, km.LinkBookmark):
			item, ok := m.selected()
			if !ok {
				m.status = "link: select a workspace row"
				return m, nil
			}
			if item.Virtual {
				m.status = "no workspace yet — press enter to start a review"
				return m, nil
			}
			if m2, blocked := m.blockIfSettingUp(item); blocked {
				return m2, nil
			}
			return m.startBookmarkLinker(item)
		case msg.String() == "r":
			// "r" doubles as inline-review from a row. Not exposed on
			// the keymap because the same key behaves differently in
			// the new-menu modal (review picker from new-workspace
			// flow); kept inline here to avoid making the central
			// keymap context-aware.
			if m.prFetcher == nil {
				m.status = "review: not configured"
				return m, nil
			}
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.RepoRoot) == "" {
				m.status = "review: select a row with a known repo"
				return m, nil
			}
			m.active = newReviewPicker(m.spinner.View())
			m.busy = true
			m.status = ""
			return m, tea.Batch(m.spinner.Tick, m.prFetcher(item.RepoRoot))
		case key.Matches(msg, km.NewMenu):
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.RepoRoot) == "" {
				m.status = "new: select a row with a known repo"
				return m, nil
			}
			return m.launchNewForm(NewWorkspaceInitial{}, item.RepoRoot)
		case key.Matches(msg, km.ReviewPick):
			item, ok := m.selected()
			if !ok || strings.TrimSpace(item.RepoRoot) == "" {
				m.status = "review: select a row with a known repo"
				return m, nil
			}
			return m.startReviewPicker(item.RepoRoot)
		case key.Matches(msg, km.Open):
			if m.projectFinder == nil {
				m.status = "open: not configured (set deck.project_roots in config)"
				return m, nil
			}
			m.active = newOpenPicker(m.spinner.View())
			m.busy = true
			m.status = ""
			return m, tea.Batch(m.spinner.Tick, m.projectFinder())
		case key.Matches(msg, km.EditState):
			if m.stateEditor == nil {
				m.status = "edit state: not configured"
				return m, nil
			}
			m.status = "editing workspace-state.json..."
			return m, m.stateEditor()
		case key.Matches(msg, km.UserActions):
			item, ok := m.selected()
			var repoRoot string
			if ok {
				repoRoot = item.RepoRoot
			}
			actions := m.userActionsForRepo(repoRoot)
			if len(actions) == 0 {
				m.status = "no user actions configured"
				return m, nil
			}
			m.actionMenuActions = actions
			m.actionAliasLookup = aliasLookup(actions)
			m.actionMode = true
			m.status = m.actionModeStatus()
			return m, nil
		case key.Matches(msg, km.OpenURL):
			item, ok := m.selected()
			if !ok {
				return m, nil
			}
			url := m.devURLs[item.SessionName]
			if url == "" {
				m.status = "no dev url discovered for this workspace"
				return m, nil
			}
			if err := openBrowser(url); err != nil {
				m.status = "open url: " + err.Error()
			} else {
				m.status = "open: " + url
			}
			return m, nil
		case key.Matches(msg, km.PRMenu):
			if _, ok := m.selected(); !ok {
				return m, nil
			}
			m.active = prMenuModal{}
			m.status = "pr: o open in browser · d description · r repair · s set PR # · esc cancel"
			return m, nil
		case key.Matches(msg, km.PinChord):
			if _, ok := m.selected(); !ok {
				return m, nil
			}
			m.pinChordMode = true
			m.status = "pin: mm default · m<letter> group · mD unpin · mR name · esc cancel"
			return m, nil
		case key.Matches(msg, km.GotoTop):
			m.gotoTopPending = true
			return m, nil
		case key.Matches(msg, km.GotoBottom):
			if n := len(m.items()); n > 0 {
				m.cursor = n - 1
			}
			return m, nil
		}
	}
	// Picker lists drive themselves with async commands — FilterMatchesMsg
	// from the filter input, cursor.BlinkMsg from the filter cursor,
	// statusMessageTimeoutMsg from status timers. The KeyMsg branches
	// above route keys to the active picker; this fallthrough catches
	// everything else so those internal messages reach the list and the
	// filter actually applies as the user types.
	if m.active != nil {
		cmd := m.active.update(&m, msg)
		return m, cmd
	}
	return m, nil
}

// pickerShortHelp builds the short-help binding list rendered in the
// deck footer when a picker (bookmark / review / open) is active. We
// don't use list.Model.ShortHelp() directly because it surfaces "?
// more" (toggles list's own full help — which we hide via
// SetShowHelp(false)) and "q quit" (no-op for our pickers). Instead
// we surface the actually-actionable bindings plus the deck's enter
// pick / esc cancel conventions.
func pickerShortHelp(l list.Model) []key.Binding {
	bindings := []key.Binding{
		l.KeyMap.CursorUp,
		l.KeyMap.CursorDown,
	}
	if l.FilterState() == list.Filtering {
		bindings = append(bindings,
			l.KeyMap.AcceptWhileFiltering,
			l.KeyMap.CancelWhileFiltering,
		)
	} else {
		bindings = append(bindings, l.KeyMap.Filter)
		if l.FilterState() == list.FilterApplied {
			bindings = append(bindings, l.KeyMap.ClearFilter)
		}
		bindings = append(bindings,
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "pick")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		)
	}
	return bindings
}

// launchNewForm enters inline new-workspace-form mode. The form is a
// state of this Model (see doc.go); we do not nest a tea.Program. The
// repo root is stashed so submit can dispatch a create job through the
// existing async-job path.
func (m *Model) launchNewForm(initial NewWorkspaceInitial, repo string) (tea.Model, tea.Cmd) {
	m.active = nil
	if strings.TrimSpace(repo) == "" {
		m.status = "new: select a row with a known repo"
		return *m, nil
	}
	m.newWorkspaceMode = true
	m.newWorkspaceRepo = repo
	m.newWorkspacePR = initial.PRNumber
	var initCmd tea.Cmd
	trunk := ""
	if m.trunkResolver != nil {
		trunk = m.trunkResolver(repo)
	}
	m.newWorkspaceForm, initCmd = newNewWorkspaceForm(initial, m.bookmarkPrefix, trunk)
	m.status = "new workspace..."
	// tea.ClearScreen so the renderer drops its previous-frame buffer
	// and the form's first paint overwrites every cell, including
	// columns the deck row list (or the new-menu) wrote that the form
	// doesn't. See doc.go.
	return *m, tea.Batch(initCmd, tea.ClearScreen)
}

// dispatchRenameForm forwards a message to the rename form and acts on
// its returned action. Submit triggers ActionRename (quick action via
// the handler); cancel closes the form.
func (m Model) dispatchRenameForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd, action := m.renameForm.update(msg)
	m.renameForm = form
	switch action {
	case renameFormActionCancel:
		m.renameMode = false
		m.renameForm = renameWorkspaceForm{}
		m.status = ""
		return m, batchCmds(cmd, tea.ClearScreen)
	case renameFormActionSubmit:
		target := form.target
		newName := form.value()
		m.renameMode = false
		m.renameForm = renameWorkspaceForm{}
		if m.handler == nil {
			m.status = "rename: handler not configured"
			return m, batchCmds(cmd, tea.ClearScreen)
		}
		m.busy = true
		m.status = fmt.Sprintf("renaming %s → %s...", target.WorkspaceName, newName)
		renameID := "workspace:rename:" + target.WorkspaceName
		m = m.startActivity(renameID, renameID, 0)
		handler := m.handler
		dispatch := func() tea.Msg {
			err := handler(ActionRequest{Item: target, Action: ActionRename, Arg: newName, Reporter: noopActionReporter{}})
			return actionResultMsg{action: ActionRename, arg: newName, item: target, err: err}
		}
		return m, batchCmds(cmd, tea.ClearScreen, m.spinner.Tick, dispatch)
	}
	return m, cmd
}

// dispatchPromptForm forwards a message to the prompt form. Submit
// fires ActionSendPrompt through the handler (synchronously, via the
// quick-action path) so the agent receives the typed prompt; cancel
// closes the form. Mirrors dispatchRenameForm.
func (m Model) dispatchPromptForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd, action := m.promptForm.update(msg)
	m.promptForm = form
	switch action {
	case promptFormActionCancel:
		m.promptMode = false
		m.promptForm = promptForm{}
		m.status = ""
		return m, batchCmds(cmd, tea.ClearScreen)
	case promptFormActionSubmit:
		target := form.target
		prompt := form.value()
		repair := form.repair
		prNumber := form.prNumber
		prHeadSHA := form.prHeadSHA
		prURL := form.prURL
		m.promptMode = false
		m.promptForm = promptForm{}
		// Reviewer repair with a reloader wired: reload the (possibly
		// stale) tuicr review window onto the PR's current head, splice the
		// resolved session into the prompt, and send — all inside the
		// reloader. It emits reviewReloadedMsg with the outcome.
		if repair && prNumber > 0 && m.reviewReloader != nil {
			m.busy = true
			m.status = fmt.Sprintf("repair: reloading review window for %s/%s...", target.ProjectName, target.WorkspaceName)
			actID := "workspace:prompt:" + target.WorkspaceName
			m = m.startActivity(actID, actID, 0)
			reload := m.reviewReloader(target, prNumber, prHeadSHA, prURL, prompt)
			return m, batchCmds(cmd, tea.ClearScreen, m.spinner.Tick, reload)
		}
		if m.handler == nil {
			m.status = "send prompt: handler not configured"
			return m, batchCmds(cmd, tea.ClearScreen)
		}
		m.busy = true
		m.status = fmt.Sprintf("sending prompt to %s/%s...", target.ProjectName, target.WorkspaceName)
		actID := "workspace:prompt:" + target.WorkspaceName
		m = m.startActivity(actID, actID, 0)
		handler := m.handler
		dispatch := func() tea.Msg {
			err := handler(ActionRequest{Item: target, Action: ActionSendPrompt, Arg: prompt, Reporter: noopActionReporter{}})
			return actionResultMsg{action: ActionSendPrompt, arg: prompt, item: target, err: err}
		}
		return m, batchCmds(cmd, tea.ClearScreen, m.spinner.Tick, dispatch)
	}
	return m, cmd
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
		m.newWorkspacePR = 0
		m.newWorkspaceForm = newWorkspaceForm{}
		m.status = ""
		// tea.ClearScreen on every modal exit so the row list's first
		// frame after the modal closes overwrites every cell, not just
		// the lines the renderer thinks changed.
		return m, batchCmds(cmd, tea.ClearScreen)
	case newFormActionSubmit:
		req := form.request()
		req.PRNumber = m.newWorkspacePR
		repo := m.newWorkspaceRepo
		m.newWorkspaceMode = false
		m.newWorkspaceRepo = ""
		m.newWorkspacePR = 0
		m.newWorkspaceForm = newWorkspaceForm{}
		updated, dispatchCmd := m.startCreateAction(req, repo)
		return updated, batchCmds(cmd, dispatchCmd, tea.ClearScreen)
	case newFormActionOpenPicker:
		if m.bookmarkFetcher == nil || strings.TrimSpace(m.newWorkspaceRepo) == "" {
			m.newWorkspaceForm.RevertStartFrom()
			m.status = "bookmark picker not configured"
			return m, cmd
		}
		m.newWorkspaceMode = false
		m.active = newBookmarkPicker(m.spinner.View(), bookmarkPurposeNewWorkspaceStartFrom, Item{})
		return m, batchCmds(cmd, m.spinner.Tick, m.bookmarkFetcher(m.newWorkspaceRepo), tea.ClearScreen)
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

// acceptBookmarkSelection branches on bookmarkPurpose to either feed the
// chosen name back to the open new-workspace form or persist it via
// BookmarkLinkHandler. Shared between filter-mode (enter selects
// directly) and nav-mode (enter after committing a filter) so the two
// paths can't diverge.
func (m *Model) acceptBookmarkSelection(name string, purpose bookmarkPurpose, target Item) (tea.Model, tea.Cmd) {
	switch purpose {
	case bookmarkPurposeNewWorkspaceStartFrom:
		m.active = nil
		m.newWorkspaceMode = true
		m.newWorkspaceForm.SetPickedBookmark(name)
		return *m, tea.ClearScreen
	case bookmarkPurposeLinkExisting:
		m.active = nil
		if m.bookmarkLinkHandler == nil {
			m.status = "link: not configured"
			return *m, nil
		}
		if err := m.bookmarkLinkHandler(target, name); err != nil {
			m.status = "link: " + err.Error()
			return *m, nil
		}
		m.status = fmt.Sprintf("linked %s → %s", target.WorkspaceName, name)
		cmds := []tea.Cmd{}
		linkID := "workspace:link:" + target.WorkspaceName
		*m = m.startActivity(linkID, linkID, 0)
		updated, expireCmd := m.finishActivity(linkID)
		*m = updated
		if expireCmd != nil {
			cmds = append(cmds, expireCmd)
		}
		var refreshCmd tea.Cmd
		*m, refreshCmd = m.requestRefresh(true)
		if refreshCmd != nil {
			cmds = append(cmds, refreshCmd)
		}
		// A fresh link is a direct "this workspace is that PR now" signal,
		// so force an immediate PR-status fetch — same as the `p s` PR-number
		// override. forcePRStatusRefresh bypasses both the throttle and the
		// eligibility gate that prStatusRefreshCmd applies, which matters
		// here because the just-linked bookmark isn't in m.itemsAll yet, so
		// the periodic policy wouldn't consider the repo eligible.
		var prCmd tea.Cmd
		*m, prCmd = m.forcePRStatusRefresh(target.RepoRoot)
		if prCmd != nil {
			cmds = append(cmds, prCmd)
		}
		if len(cmds) == 0 {
			return *m, nil
		}
		return *m, tea.Batch(cmds...)
	}
	return *m, nil
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
	m.active = newBookmarkPicker(m.spinner.View(), bookmarkPurposeLinkExisting, target)
	m.busy = true
	m.status = ""
	return *m, tea.Batch(m.spinner.Tick, m.bookmarkFetcher(target.RepoRoot))
}

// startReviewPicker opens the PR picker so the user can pick a PR to
// review. Reached from row-mode `r`; the picker itself is a separate
// modal state, same shape as the bookmark picker.
func (m *Model) startReviewPicker(repo string) (tea.Model, tea.Cmd) {
	if m.prFetcher == nil {
		m.status = "review: not configured"
		return *m, nil
	}
	if strings.TrimSpace(repo) == "" {
		m.status = "review: no repo"
		return *m, nil
	}
	m.active = newReviewPicker(m.spinner.View())
	m.busy = true
	m.status = ""
	return *m, tea.Batch(m.spinner.Tick, m.prFetcher(repo))
}

// workspaceSetupJob returns the in-flight create-workspace or review job
// still preparing this row's workspace, if any. A workspace is registered
// in workspace-state.json (so its row appears in the deck) the moment
// `jj workspace add` runs — well before the bootstrap hooks (`pnpm i`
// and friends, or the review flow's tmux windows) finish and the agent is
// launched. Matching the row to its still-running setup job lets the deck
// badge the row "setting up" and refuse actions that need a ready
// workspace + agent.
//
// Match key: same repo root + same normalized workspace name. Both the
// create spec and the review spec carry the workspace name the flow will
// land under (the review spec's is the predicted pr-<n>-<branch>, see
// startReview); normalizing it the same way the workspace service does
// keeps the match stable against case / separator differences with the
// stored row name.
func (m Model) workspaceSetupJob(item Item) (Job, bool) {
	name := strings.TrimSpace(item.WorkspaceName)
	if name == "" {
		return Job{}, false
	}
	for _, j := range m.jobs {
		if !isSetupJobAction(j.Action) || j.Status.IsTerminal() {
			continue
		}
		if strings.TrimSpace(j.RepoRoot) != strings.TrimSpace(item.RepoRoot) {
			continue
		}
		if normalizeWorkspaceName(j.WorkspaceName) == name {
			return j, true
		}
	}
	return Job{}, false
}

// isSetupJobAction reports whether a job action prepares a workspace whose
// row should read "setting up" while the job runs. Both create-workspace
// and review register a workspace-state row early (at `jj workspace add`)
// and keep working afterward.
func isSetupJobAction(action string) bool {
	return action == "create-workspace" || action == "review"
}

func (m Model) workspaceSettingUp(item Item) bool {
	_, ok := m.workspaceSetupJob(item)
	return ok
}

// workspaceDeleteJob returns the in-flight (non-terminal) delete job
// targeting this row, if any. Mirrors workspaceSetupJob so a row being
// removed gets the same "deleting…" spinner treatment a row being
// created gets for "setting up…".
func (m Model) workspaceDeleteJob(item Item) (Job, bool) {
	name := normalizeWorkspaceName(item.WorkspaceName)
	if name == "" {
		return Job{}, false
	}
	for _, j := range m.jobs {
		if j.Action != "delete" || j.Status.IsTerminal() {
			continue
		}
		if strings.TrimSpace(j.RepoRoot) != strings.TrimSpace(item.RepoRoot) {
			continue
		}
		if normalizeWorkspaceName(j.WorkspaceName) == name {
			return j, true
		}
	}
	return Job{}, false
}

func (m Model) workspaceDeleting(item Item) bool {
	_, ok := m.workspaceDeleteJob(item)
	return ok
}

// workspaceKey is the stable identity for a workspace row: repo root +
// normalized name. Used to reconcile optimistic create rows against the
// rows that land from workspace-state.json.
func workspaceKey(repoRoot, name string) string {
	return strings.TrimSpace(repoRoot) + "\x00" + normalizeWorkspaceName(name)
}

// addOptimisticCreate records a synthetic row so a just-submitted create
// appears in the deck immediately, before the detached subprocess writes
// state. ProjectName is copied from an existing row in the same repo so
// the optimistic row groups under the right project header.
func (m *Model) addOptimisticCreate(item Item) {
	if normalizeWorkspaceName(item.WorkspaceName) == "" {
		return
	}
	item.Optimistic = true
	item.WorkspaceName = normalizeWorkspaceName(item.WorkspaceName)
	if strings.TrimSpace(item.ProjectName) == "" {
		item.ProjectName = m.projectNameForRepo(item.RepoRoot)
	}
	if m.optimisticCreates == nil {
		m.optimisticCreates = map[string]Item{}
	}
	m.optimisticCreates[workspaceKey(item.RepoRoot, item.WorkspaceName)] = item
}

// dropOptimisticCreate removes an optimistic row by (repo, name), e.g.
// when its create spawn fails before any job record exists.
func (m *Model) dropOptimisticCreate(repoRoot, name string) {
	if len(m.optimisticCreates) == 0 {
		return
	}
	delete(m.optimisticCreates, workspaceKey(repoRoot, name))
}

// pruneOptimisticCreates drops synthetic rows once the real row has
// landed in itemsAll, or once the backing create job has failed. A
// successful (done) job is deliberately not a drop trigger — it always
// wrote a real row, so the real-row check reconciles it without a
// disappear/reappear flicker.
func (m *Model) pruneOptimisticCreates() {
	if len(m.optimisticCreates) == 0 {
		return
	}
	real := make(map[string]bool, len(m.itemsAll))
	for _, it := range m.itemsAll {
		real[workspaceKey(it.RepoRoot, it.WorkspaceName)] = true
	}
	for key, opt := range m.optimisticCreates {
		if real[key] {
			delete(m.optimisticCreates, key)
			continue
		}
		if job, ok := m.optimisticCreateJob(opt); ok && job.Status.IsTerminal() && job.Status != JobDone {
			delete(m.optimisticCreates, key)
		}
	}
}

// optimisticCreateJob finds the create-workspace or review job (terminal
// or not) backing an optimistic row. Unlike workspaceSetupJob it also
// matches terminal jobs, so pruneOptimisticCreates can retire a failed
// setup.
func (m Model) optimisticCreateJob(item Item) (Job, bool) {
	name := normalizeWorkspaceName(item.WorkspaceName)
	if name == "" {
		return Job{}, false
	}
	for _, j := range m.jobs {
		if !isSetupJobAction(j.Action) {
			continue
		}
		if strings.TrimSpace(j.RepoRoot) != strings.TrimSpace(item.RepoRoot) {
			continue
		}
		if normalizeWorkspaceName(j.WorkspaceName) == name {
			return j, true
		}
	}
	return Job{}, false
}

// projectNameForRepo returns the project name the deck uses for rows in
// the given repo, taken from an existing row, else the repo's basename.
func (m Model) projectNameForRepo(repoRoot string) string {
	rr := strings.TrimSpace(repoRoot)
	for _, it := range m.itemsAll {
		if strings.TrimSpace(it.RepoRoot) == rr {
			return it.ProjectName
		}
	}
	return filepath.Base(rr)
}

// normalizeWorkspaceName mirrors the name the workspace service records
// for a create request so a job's user-entered name matches the row's
// stored name. An empty-after-normalization name collapses to "".
func normalizeWorkspaceName(s string) string {
	n, err := workspace.NormalizeName(s)
	if err != nil {
		return ""
	}
	return n
}

// workspaceSetupStepLabel returns the label of the create job's current
// (last-running, else last) step for the "setting up · <step>" row hint.
func workspaceSetupStepLabel(j Job) string {
	for i := len(j.Steps) - 1; i >= 0; i-- {
		st := j.Steps[i]
		if !st.Done && !st.Error {
			return strings.TrimSpace(st.Label)
		}
	}
	if n := len(j.Steps); n > 0 {
		return strings.TrimSpace(j.Steps[n-1].Label)
	}
	return ""
}

// blockIfSettingUp bails an action out with a status toast when the
// selected workspace is still being created (tmux session + agent not
// ready). Mirrors the item.Virtual guards on the same action handlers.
func (m Model) blockIfSettingUp(item Item) (Model, bool) {
	if item.Optimistic {
		// Optimistic row: the workspace-state entry (and tmux session,
		// agent, path) don't exist yet, so every lifecycle action is a
		// no-op at best. Toast and swallow.
		m.status = fmt.Sprintf("%s is still being created…", item.WorkspaceName)
		return m, true
	}
	job, ok := m.workspaceSetupJob(item)
	if !ok {
		return m, false
	}
	if lbl := workspaceSetupStepLabel(job); lbl != "" {
		m.status = fmt.Sprintf("%s is still setting up (%s)…", item.WorkspaceName, lbl)
	} else {
		m.status = fmt.Sprintf("%s is still setting up…", item.WorkspaceName)
	}
	return m, true
}

func (m Model) trigger(a Action, arg string) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || m.handler == nil {
		return m, nil
	}
	if item.Virtual {
		// A virtual inbox row has no local workspace to act on. The one
		// gesture that exists is enter (Summon), and what it does depends
		// on whose PR it is:
		//   - your own PR  → open the new-workspace form prefilled with the
		//     PR branch, so you land in a normal working workspace (not the
		//     review flow's tuicr windows).
		//   - awaiting your review → start the review flow, which creates
		//     the workspace and primes the reviewer.
		// The explicit review action (r) always starts a review regardless.
		st, hasSt := m.resolvePRStatus(item)
		reviewReq := hasSt && (st.ReviewRequested || st.ReviewRerequested)
		if a == ActionSummon && !reviewReq {
			// Not awaiting your review → check the branch out via the
			// new-workspace form. Covers both your own PRs and
			// stack-completion links (a stacked PR that's someone else's).
			head, prNum := item.Bookmark, item.PRNumber
			if hasSt {
				head, prNum = st.HeadRefName, st.Number
			}
			name := proposeWorkspaceName(head, m.bookmarkPrefix)
			return m.launchNewForm(NewWorkspaceInitial{Bookmark: head, Name: name, PRNumber: prNum}, item.RepoRoot)
		}
		if a == ActionSummon || a == ActionReview {
			prArg := strconv.Itoa(item.PRNumber)
			if m.reviewSetups[reviewSetupKey(item.RepoRoot, prArg)] {
				m.status = fmt.Sprintf("review for PR %s is already starting…", prArg)
				return m, nil
			}
			// Head ref drives the optimistic row's predicted name. A virtual
			// review row carries it as Bookmark; prefer the live status when
			// present.
			branch := item.Bookmark
			if hasSt && strings.TrimSpace(st.HeadRefName) != "" {
				branch = st.HeadRefName
			}
			return m.startReview(item, item.PRNumber, branch)
		}
		m.status = "no workspace yet — press enter to create it"
		return m, nil
	}
	// The row shows up as soon as the workspace is registered in state,
	// but the tmux session + agent aren't created until the create job's
	// bootstrap hooks finish. Summoning / opening windows before then
	// would attach to a session that doesn't exist yet, so hold the
	// action until setup completes.
	if m2, blocked := m.blockIfSettingUp(item); blocked {
		return m2, nil
	}
	if isProgressAction(a) {
		return m.startAction(a, item, arg)
	}
	return m.startQuickAction(a, item, arg)
}

func isProgressAction(a Action) bool {
	switch a {
	case ActionDelete, ActionDeleteProject, ActionReview, ActionCreateWorkspace, ActionCI, ActionCustom, ActionMergePR:
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
		// ActionReview is dispatched through startReview (which adds the
		// optimistic "setting up" row and predicts the pr-<n>-<branch>
		// workspace name), so it never reaches this async switch — startReview
		// only falls back to startAction on the synchronous path below.
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
// the async launcher. No modal progress mode, no tea.Quit. The
// dispatched job appears in the activity bar via syncJobActivities,
// so we deliberately do not write a "queued · …" status toast — it
// would duplicate the activity entry.
func (m *Model) startAsyncAction(spec AsyncJobSpec) (tea.Model, tea.Cmd) {
	if spec.Action == "review" {
		if m.reviewSetups == nil {
			m.reviewSetups = map[string]bool{}
		}
		m.reviewSetups[reviewSetupKey(spec.RepoRoot, spec.Arg)] = true
	}
	launcher := m.asyncJobLauncher
	dispatch := func() tea.Msg {
		return asyncJobDispatchedMsg{spec: spec, err: launcher(spec)}
	}
	return *m, dispatch
}

// startReview dispatches a PR review and — on the async path — shows the
// PR's workspace immediately as an optimistic "setting up" row, the same
// treatment a freshly-created workspace gets. branch is the PR's head ref,
// used to predict the workspace name the review flow will land under
// (workspace.ReviewWorkspaceName); when it's unknown the optimistic row is
// skipped and the row simply appears after the review job writes state and
// the next refresh surfaces it (the pre-existing behavior).
func (m *Model) startReview(item Item, prNumber int, branch string) (tea.Model, tea.Cmd) {
	arg := strconv.Itoa(prNumber)
	// Synchronous fallback (no async launcher): the deck runs the review in
	// its modal progress view, so there's no row to make optimistic.
	if m.asyncJobLauncher == nil {
		return m.startAction(ActionReview, item, arg)
	}
	// rawName is the workspace the review will land under, predictable only
	// when we know the head ref. Empty branch → skip the optimistic row and
	// leave the spec's WorkspaceName blank (the pre-existing behavior).
	branch = strings.TrimSpace(branch)
	rawName := ""
	if branch != "" {
		rawName = workspace.ReviewWorkspaceName(prNumber, branch)
		// Optimistic row so the review workspace shows up the instant `r` /
		// enter is pressed, badged "setting up" until the review job finishes
		// — mirrors startAsyncCreateAction. Carrying PRNumber lets the inbox
		// virtual-row builder dedup this PR's placeholder row against it, so
		// the two don't render side by side.
		m.addOptimisticCreate(Item{
			WorkspaceName: rawName,
			RepoRoot:      item.RepoRoot,
			Bookmark:      branch,
			PRNumber:      prNumber,
			Status:        "starting",
		})
		// Keep the cursor on the row across the optimistic→real swap, then
		// snap now so the just-added row is selected immediately.
		normalized := normalizeWorkspaceName(rawName)
		m.pendingSelect = Item{WorkspaceName: normalized}
		for i, it := range m.items() {
			if it.WorkspaceName == normalized && strings.TrimSpace(it.RepoRoot) == strings.TrimSpace(item.RepoRoot) {
				m.cursor = i
				break
			}
		}
	}
	// WorkspaceName is the predicted review workspace (pr-<n>-<branch>), not
	// the row the user was on — so workspaceSetupJob / optimisticCreateJob
	// match the review job to the row it actually prepares, and the jobs
	// overlay names the right workspace.
	return m.startAsyncAction(AsyncJobSpec{
		Action:        "review",
		Title:         "review · PR " + arg,
		RepoRoot:      item.RepoRoot,
		WorkspaceName: rawName,
		Arg:           arg,
	})
}

// reviewSetupKey is the stable key for an in-flight virtual-row review
// setup: repo root + PR number (as the string arg passed to the review
// action). Matches whether built from an Item or an async job spec.
func reviewSetupKey(repoRoot, prArg string) string {
	return repoRoot + "#" + prArg
}

// pruneReviewSetups clears guard entries whose backing review job has
// reached a terminal state. It never clears on a missing job: right
// after dispatch the job hasn't appeared in the list yet, and dropping
// the guard then would reopen the double-enter window. On success the
// row also stops being virtual, so a lingering entry is harmless — the
// guard is only consulted on the virtual path.
func (m *Model) pruneReviewSetups() {
	if len(m.reviewSetups) == 0 {
		return
	}
	for _, j := range m.jobs {
		arg, ok := reviewJobArg(j)
		if !ok {
			continue
		}
		switch j.Status {
		case JobDone, JobCancelled, JobError, JobOrphaned:
			delete(m.reviewSetups, reviewSetupKey(j.RepoRoot, arg))
		}
	}
}

// reviewJobArg returns the PR arg a review job was dispatched with,
// recovered from its title ("review · PR <n>"). ok is false for
// non-review jobs or titles that don't match the expected shape.
func reviewJobArg(j Job) (string, bool) {
	if j.Action != "review" {
		return "", false
	}
	const prefix = "review · PR "
	if !strings.HasPrefix(j.Title, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(j.Title, prefix)), true
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
	var prCmd tea.Cmd
	if strings.TrimSpace(req.Bookmark) != "" {
		*m, prCmd = m.forcePRStatusRefresh(repoRoot)
	}
	return *m, batchCmds(m.spinner.Tick, dispatch, waitForProgress(m.progressChan), prCmd)
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
		Action:           "create-workspace",
		RepoRoot:         repoRoot,
		Title:            title,
		Name:             strings.TrimSpace(req.Name),
		Bookmark:         strings.TrimSpace(req.Bookmark),
		BookmarkToCreate: strings.TrimSpace(req.BookmarkToCreate),
		Prompt:           strings.TrimSpace(req.Prompt),
		PRNumber:         req.PRNumber,
		// Carry the requested name so the deck can match this job back to
		// the row that appears (via workspace-state.json) while the job is
		// still bootstrapping — see workspaceSetupJob.
		WorkspaceName: strings.TrimSpace(req.Name),
	}
	// Show the new workspace immediately as an optimistic row, so it
	// appears the instant the form is submitted rather than after the
	// detached subprocess writes workspace-state.json and a refresh
	// surfaces it. refreshDoneMsg reconciles the synthetic row away once
	// the real one lands. When both Name and Bookmark are blank the create
	// flow generates the name, so there's nothing to predict — skip the
	// optimistic row and fall back to the post-refresh behavior.
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Bookmark)
	}
	if name != "" {
		m.addOptimisticCreate(Item{
			WorkspaceName: name,
			RepoRoot:      repoRoot,
			Bookmark:      strings.TrimSpace(req.Bookmark),
			PRNumber:      req.PRNumber,
			PromptPreview: strings.TrimSpace(req.Prompt),
			Status:        "starting",
		})
		// Pre-arm pendingSelect so the cursor stays on the row across the
		// swap from the optimistic row to the real one, then snap now so
		// the just-added row is selected immediately.
		normalized := normalizeWorkspaceName(name)
		m.pendingSelect = Item{WorkspaceName: normalized}
		for i, it := range m.items() {
			if it.WorkspaceName == normalized && strings.TrimSpace(it.RepoRoot) == strings.TrimSpace(repoRoot) {
				m.cursor = i
				break
			}
		}
	}
	launcher := m.asyncJobLauncher
	dispatch := func() tea.Msg {
		err := launcher(spec)
		return asyncJobDispatchedMsg{spec: spec, err: err}
	}
	var prCmd tea.Cmd
	if strings.TrimSpace(req.Bookmark) != "" {
		*m, prCmd = m.forcePRStatusRefresh(repoRoot)
	}
	return *m, batchCmds(dispatch, prCmd)
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
	case ActionRename:
		return "rename"
	case ActionSendPrompt:
		return "send prompt"
	case ActionMergePR:
		if arg != "" {
			return "merge PR #" + arg
		}
		return "merge PR"
	}
	return "action"
}

func (m Model) actionModeStatus() string {
	list := m.actionMenuActions
	if len(list) == 0 {
		list = m.userActions
	}
	parts := make([]string, 0, len(list))
	for _, a := range list {
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
		footer := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(m.progressFooter())
		return lipgloss.JoinVertical(lipgloss.Left, body, footer)
	}
	if m.newWorkspaceMode {
		// Render the inline new-workspace form across the entire
		// viewport. Same pattern as progressMode above; both replace
		// the deck body wholesale when the modal owns the screen.
		return m.newWorkspaceForm.view(m.width, m.height)
	}
	if m.renameMode {
		return m.renameForm.view(m.width, m.height)
	}
	if m.promptMode {
		return m.promptForm.view(m.width, m.height)
	}
	// Popover modals (confirmations, small prompts, help) render as a box
	// over a blank canvas — no body/footer composition. Center small
	// popovers; top-align any that are taller than the viewport (e.g. the
	// help overlay on a short terminal) so their header — which carries the
	// close hint — stays on screen instead of clipping off the top.
	if pm, ok := m.active.(popoverModal); ok {
		content := pm.renderPopover(&m)
		vpos := lipgloss.Center
		if lipgloss.Height(content) > m.height {
			vpos = lipgloss.Top
		}
		return lipgloss.Place(m.width, m.height, lipgloss.Center, vpos, content)
	}

	// The workspace row list runs full-width — per-row metadata lives
	// on the second line of each row (see metaLine). The new-menu and
	// open pickers keep their 70/30 right-pane help block when the
	// terminal is wide enough, falling back to full-width below
	// deckStackThreshold cols. Other pickers (bookmark, review) are
	// always single-column.
	var left, right string
	switch {
	case m.active != nil:
		if bm, ok := m.active.(bodyModal); ok {
			left, right = bm.view(&m)
		}
	default:
		left = m.renderList(m.width)
	}
	var body string
	if right == "" {
		body = left
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, "\n", right)
	}
	// Pad every body row out to m.width so the rightmost columns get
	// overwritten between frames. Without this, when a tall modal (menu,
	// picker, etc.) collapses back to the normal list, the previous
	// frame's right-edge content lingers in those columns. No bg paint
	// — the padding spaces inherit terminal default cell bg, which is
	// what blends with the surrounding tmux pane.
	body = lipgloss.NewStyle().Width(m.width).Render(body)

	statusText := m.status
	if m.busy {
		statusText = m.spinner.View() + " " + m.status
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	var rightSeg string
	switch {
	case m.filtering:
		rightSeg = "/" + m.filterInput.View()
	case m.findMode || m.actionMode:
		rightSeg = dim.Render(m.status + " (esc cancel)")
	// Picker modes hide their own bottom help bar (SetShowHelp(false))
	// and surface the bindings here so they align on the same row as
	// the deck's "? help" hint instead of floating mid-screen above
	// the footer. pickerShortHelp drops the list's "? more" / "q quit"
	// (neither does anything useful in our pickers) and adds the
	// deck's enter pick / esc cancel conventions.
	case m.active != nil:
		// Pickers surface their key hints here; chord modals (pr menu)
		// return "" and fall back to the status line (their menu prompt).
		if fh := m.active.footerHelp(); fh != "" {
			rightSeg = fh
		} else {
			rightSeg = dim.Render(statusText)
		}
	case m.filter != "":
		rightSeg = dim.Render(fmt.Sprintf("filter: %q · %s", m.filter, statusText))
	default:
		rightSeg = dim.Render(statusText)
	}
	// Inset the footer to match the body panels (Padding(1, 1, 1, 1)):
	// 1 col on each side AND 1 row of bottom padding so the status bar
	// has the same breathing room below it as the panels have above
	// their content.
	// Footer inset matches the body panels (Padding(1, 1, 1, 1)):
	// 1 col on each side, 1 row of padding top AND bottom so the status
	// bar has the same breathing room above/below as the panels have
	// around their content.
	// "? help" describes the deck row-mode help overlay. Suppress it on
	// modal screens (pickers, menus, jobs overlay, find/action chords)
	// since the overlay doesn't apply there — those screens surface
	// their own actionable hints in rightSeg.
	hint := "? help"
	if m.active != nil ||
		m.findMode || m.actionMode {
		hint = ""
	}
	footer := composeStatusBar(m.activities, m.spinner.View(), rightSeg, hint, m.width-2)
	footer = lipgloss.NewStyle().Padding(1, 1, 1, 1).Render(footer)
	footerHeight := lipgloss.Height(footer)
	bodyHeight := lipgloss.Height(body)
	pad := m.height - bodyHeight - footerHeight
	if pad < 0 {
		pad = 0
	}
	// Padding between body and footer to pin the footer to the bottom
	// of the alt-screen viewport. Under alt-screen the renderer paints
	// every cell each frame, so we no longer need the space-fill to
	// overwrite leftover cells from a previous tall frame — the bg-
	// blending property of the inline-mode design is also moot.
	// Keeping the rendering as plain spaces (no SGR) so it stays cheap
	// and the alt-screen canvas's default bg shows through unpainted.
	padBlock := ""
	if pad > 0 {
		blanks := make([]string, pad)
		blank := strings.Repeat(" ", m.width)
		for i := range blanks {
			blanks[i] = blank
		}
		padBlock = strings.Join(blanks, "\n")
	}
	return lipgloss.JoinVertical(lipgloss.Left, body, padBlock, footer)
}

// helpBoxDims returns the help popover's outer box width and the inner
// content width, clamped to the viewport. Shared by the help modal (which
// frames a scroll viewport at these dims) and tests.
func helpBoxDims(width int) (boxWidth, innerWidth int) {
	const (
		targetWidth = 110
		boxOverhead = 6 // border (2) + horizontal padding (2*2)
	)
	boxWidth = targetWidth
	if width > 0 && width-8 < boxWidth {
		boxWidth = width - 8
	}
	if boxWidth < 64 {
		boxWidth = 64
	}
	return boxWidth, boxWidth - boxOverhead
}

// helpColumns renders the scrollable help body — the status/PR/activity
// legend (left) and key bindings (right) — clipped to innerWidth. The
// framing box, title, and scroll hint live on the help modal
// (modal_help.go); this is just the content the viewport scrolls.
func helpColumns(innerWidth int) string {
	dot := func(state string, unread bool, label string) string {
		return statusGlyph(state, false, unread) + "  " + label
	}
	statusLines := []string{
		lipgloss.NewStyle().Bold(true).Render("Agent status (left dot)"),
		dot("working", false, "working"),
		dot("waiting", true, "waiting"),
		dot("idle", true, "notified"),
	}

	prDot := func(g, color, label string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(g) + "  " + label
	}
	prLines := []string{
		lipgloss.NewStyle().Bold(true).Render("PR status (right glyph)"),
		prDot(prGlyphDraft, colMuted, "draft"),
		prDot(prGlyphApproved, colSuccess, "approved"),
		prDot(prGlyphInQueue, colSuccess, "in merge queue"),
		prDot(prGlyphCIPend, colWarning, "CI pending"),
		prDot(prGlyphCIFail, colDanger, "CI failing"),
		prDot(prGlyphMerged, colMuted, "merged"),
		prDot(prGlyphClosed, colMuted, "closed"),
		prDot(prGlyphBehind, colWarning, "behind base"),
		prDot(prGlyphDirty, colDanger, "merge conflicts"),
		prDot(prGlyphStale, colWarning, "local copy stale"),
		prDot(prGlyphReviewReq, colInfo, "your review requested"),
		prDot(prGlyphReviewReq, colWarning, "review re-requested"),
		prDot(prGlyphChangesReq, colWarning, "review feedback on your PR"),
		prDot(glyphBlocked, colDanger, "blocked on base (stacked PR)"),
	}
	activityLines := []string{
		lipgloss.NewStyle().Bold(true).Render("Activity bar (bottom)"),
		lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render("⠼ in flight"),
		lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess)).Render("✓") + "  finished",
		lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Render("⚠") + "  failed",
		lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Render("☠") + "  orphaned",
	}
	leftBlock := strings.Join(statusLines, "\n") + "\n\n" +
		strings.Join(prLines, "\n") + "\n\n" +
		strings.Join(activityLines, "\n")

	// Right block: key bindings rendered via bubbles/help. Each group
	// from deckKeyGroups becomes a section with its own title; within
	// the section the bindings flow through help.ShortHelpView so key
	// + description styling stays consistent with every other place
	// that uses charm.NewHelp().
	helpModel := charm.NewHelp()
	helpModel.ShowAll = true
	helpModel.Styles.FullKey = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true)
	helpModel.Styles.FullDesc = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	helpModel.Styles.FullSeparator = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	keyLines := []string{lipgloss.NewStyle().Bold(true).Render("Keys")}
	for i, g := range deckKeyGroups() {
		if i > 0 {
			keyLines = append(keyLines, "")
		}
		keyLines = append(keyLines, headerStyle.Render(g.Title))
		bindings := make([]key.Binding, 0, len(g.Keys))
		for _, kr := range g.Keys {
			bindings = append(bindings, key.NewBinding(
				key.WithKeys(kr[0]),
				key.WithHelp(kr[0], kr[1]),
			))
		}
		// FullHelpView lays each binding on its own line. Passing a
		// single column ([]key.Binding wrapped in [][]) keeps the
		// previous "key   description" stacked layout.
		section := helpModel.FullHelpView([][]key.Binding{bindings})
		keyLines = append(keyLines, lipgloss.NewStyle().Padding(0, 0, 0, 2).Render(section))
	}
	rightBlock := strings.Join(keyLines, "\n")

	// Two-column layout: status legend (with activity-bar legend) on the
	// left, key bindings on the right; falls back to a single stacked block
	// under ~70 cols.
	const gutter = 4

	// Clamp every legend / key line to its column width: truncate with an
	// ellipsis rather than letting lipgloss .Width wrap long lines onto
	// extra rows, which made the overlay much taller than its line count.
	clipBlock := func(block string, w int) string {
		lines := strings.Split(block, "\n")
		for i, ln := range lines {
			lines[i] = ansi.Truncate(ln, w, "…")
		}
		return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
	}

	if innerWidth >= 70 {
		leftWidth := (innerWidth - gutter) * 9 / 20
		rightWidth := innerWidth - gutter - leftWidth
		left := clipBlock(leftBlock, leftWidth)
		right := clipBlock(rightBlock, rightWidth)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gutter), right)
	}
	// Narrow terminal — stack vertically like the old layout.
	return clipBlock(leftBlock, innerWidth) + "\n\n" + clipBlock(rightBlock, innerWidth)
}

func (m Model) renderList(width int) string {
	title := m.styles.Title.Render("awp deck")
	// Scope label is pinned to the top-right corner on the title row
	// (rather than as a subtitle beneath it). Right edge sits at the
	// panel's inner width — width minus the 1-col horizontal padding on
	// each side.
	scope := m.styles.Muted.Render("scope: " + scopeLabel(m.scope))
	gap := (width - 2) - lipgloss.Width(title) - lipgloss.Width(scope)
	if gap < 1 {
		gap = 1
	}
	titleRow := title + strings.Repeat(" ", gap) + scope
	header := []string{titleRow, ""}
	items := m.items()
	if len(items) == 0 {
		header = append(header, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render("No workspaces found."))
		return lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1).Render(strings.Join(header, "\n"))
	}
	projectHints, pinHints, rowHints := m.findHints()
	// Reserve a fixed-width prefix slot at all times so workspace rows
	// and project headers don't shift horizontally between modes (no
	// find / 1-char hint / 2-char hint). 2 cols is exactly a two-char
	// easymotion hint (rendered bare via renderDeckHint — no brackets)
	// or the ┃-plus-space cursor bar, which keeps the whole body
	// hugging the left edge; the status glyph sits one space after the
	// slot, in the same column as the project-header names.
	const prefixWidth = 2
	prefixSlot := lipgloss.NewStyle().Width(prefixWidth)
	// Build the scrollable region (project headers + workspace rows) from
	// the shared structural layout (deckBodyRows) so the renderer and the
	// scroll math (deckBodyCursorRow / deckBodyHeaderRowForCursor) never
	// disagree on row positions — important now that default-only
	// projects collapse to a single row. In parallel we record which body
	// index belongs to each group header, used below to scroll
	// cursor-into-view without stripping the header off a row that needs
	// it for context.
	rows := m.bodyRows(items)
	body := make([]string, 0, len(rows))
	// projectHeaderForRow[i] = body index of the project header governing
	// row i. For a collapsed default-only row it points at the row itself
	// (the row IS its own header), so the sticky-header logic never
	// duplicates it.
	projectHeaderForRow := make([]int, 0, len(rows))
	cursorRow := -1
	s := m.styles
	for _, r := range rows {
		switch r.kind {
		case deckRowSpacer:
			// One slice element = one rendered line; an empty string keeps
			// the row-count-based scroll capacity math exact.
			body = append(body, "")
		case deckRowHeader:
			headerStyle := m.headerStyle(r.project)
			if m.findMode && m.findStage == findStageWorkspace && r.project == m.findProject {
				headerStyle = s.FindHeader
			}
			hintStr := ""
			if hint, ok := projectHints[r.project]; ok {
				hintStr = renderDeckHint(hint)
			}
			headerLine := fmt.Sprintf("%s %s", prefixSlot.Render(hintStr), r.project)
			body = append(body, headerStyle.Render(headerLine))
		case deckRowPinHeader:
			// r.project holds the register key. Render the display label
			// (alias / "pinned" / letter) in the project-header hue. While
			// the g chord is pending, lead with an emphasized [x] chip so
			// the user can see which register each section is — the legend
			// the chord's second keystroke targets.
			headerStyle := s.ProjectHeader
			// In the workspace stage scoped to this register, highlight the
			// section header with the find hue — matches how project
			// headers light up for the selected project.
			if m.findMode && m.findStage == findStageWorkspace && m.findPinGroup == r.project {
				headerStyle = s.FindHeader
			}
			star := headerStyle.Render("★")
			label := headerStyle.Render(m.pinGroupLabel(r.project))
			if m.pinChordMode {
				chip := s.Selected.Render("[" + pinGroupChordLetter(r.project) + "]")
				label = chip + " " + label
			}
			hintStr := ""
			if hint, ok := pinHints[r.project]; ok {
				hintStr = renderDeckHint(hint)
			}
			body = append(body, fmt.Sprintf("%s %s %s", prefixSlot.Render(hintStr), star, label))
		case deckRowSubHeader:
			// Inbox project subheader under a bucket header: muted blue,
			// non-bold, indented one step past the bucket header so the
			// two-level section reads as bucket → project. Blue (not the
			// teal project-header hue) keeps it distinct from the teal
			// "Needs your review" bucket header.
			line := fmt.Sprintf("%s   %s", prefixSlot.Render(""), r.project)
			body = append(body, s.SubHeader.Render(line))
		case deckRowPrimary:
			item := items[r.itemIndex]
			// findProject is "" in the inbox scope's single-stage find
			// (no project stage), so nothing dims there.
			dim := m.findMode && m.findStage == findStageWorkspace &&
				!m.findRowInScope(item)
			prefix := "  "
			if hint, ok := rowHints[r.itemIndex]; ok {
				prefix = renderDeckHint(hint)
			}
			// Style the label segment directly. The dot is rendered with
			// its own ANSI color sequence ending in a reset, which would
			// otherwise truncate any outer Foreground/Bold applied to the
			// whole row — that's why selected rows containing a status dot
			// weren't highlighting past the dot.
			// Labels stay at the terminal default fg — the colored status
			// dot already carries the agent state, and tinting every label
			// flooded the list (especially yellow "waiting" rows, which
			// collided with the yellow selection bar). Selection and
			// find-dim are the only label recolors.
			labelStyle := s.Label
			if r.itemIndex == m.cursor {
				prefix = s.Bar.Render("┃") + " "
				labelStyle = s.Selected
			} else if dim {
				labelStyle = s.Muted
			}
			// Inbox scope sections by bucket, so the project context moves
			// onto the row as a muted chip (mini-deck pattern).
			// A PR stacked on another visible PR nests under it with a tree
			// connector so the dependency reads top-down. The connector is
			// flat — every stacked child (any depth) sits at one indent
			// under its root rather than a per-depth staircase, so deep
			// stacks don't drift right and eat label width. Depth 0 (root /
			// standalone) gets no connector. Teal (Accent) marks it as
			// structure. Applies in every scope that annotates stacks
			// (StackDepth is 0 elsewhere).
			stackPrefix := ""
			if item.StackDepth > 0 {
				stackPrefix = s.Accent.Render("└─ ")
			}
			// Inbox rows carry no [project] chip — the project subheader
			// (inboxBodyRows) provides that context now, so the label starts
			// right after the status glyph (plus any stack connector).
			label := truncate(m.displayLabel(item), max(10, width-19-lipgloss.Width(stackPrefix)))
			// Status is canonical in JSON, so render the stored glyph
			// immediately on the fast first paint. The only tmux-derived
			// override is `working` → `exited` (agent shell death — Claude
			// has no exit hook), which arrives a frame later from the
			// enrichment pass and is rare enough that a brief flash is
			// preferable to a blank glyph slot.
			glyph := statusGlyph(item.Status, dim, item.Unread)
			// A workspace still being created (optimistic row or a live
			// create job) or being deleted shows up in a transient state;
			// badge it with the live spinner so it reads as in-progress
			// rather than an idle/ready row.
			if !dim && (item.Optimistic || m.workspaceSettingUp(item) || m.workspaceDeleting(item)) {
				glyph = m.spinner.View()
			}
			line := fmt.Sprintf("%s %s %s%s", prefixSlot.Render(prefix), glyph, stackPrefix, labelStyle.Render(label))
			body = append(body, fitRow(line, width-2))
			if r.itemIndex == m.cursor {
				cursorRow = len(body) - 1
			}
		case deckRowMeta:
			item := items[r.itemIndex]
			// Secondary line: PR glyphs then muted @author · head · dev.
			// Indented past where the label starts so it visually sits
			// *under* the row rather than next to it — gives the eye a
			// clear "this belongs to the row above" cue without adding
			// row height. The PR glyphs lead this line (instead of
			// trailing the label above) so the primary row stays
			// name-only and the glyph cluster lines up in a column.
			// Truncated to fit; lipgloss.Width pads but doesn't clip.
			const metaIndentW = prefixWidth + 1 + 1 + 1 + 4 // prefix + space + glyph + space + extra subordinate indent
			// Stacked child rows do NOT shift their meta line by the "└─ "
			// connector width: the subtext dedents back to the common meta
			// column so every row's meta lines up, even though the child's
			// label is connector-indented above it.
			metaIndent := strings.Repeat(" ", metaIndentW)
			glyphs := ""
			for _, g := range []string{
				m.prGlyphForItem(item),
				m.prBlockedGlyphForItem(item),
				m.prStaleGlyphForItem(item),
				m.prLocalStaleGlyphForItem(item),
				m.prReviewReqGlyphForItem(item),
			} {
				if g == "" {
					continue
				}
				if glyphs != "" {
					glyphs += " "
				}
				glyphs += g
			}
			metaRoom := max(10, width-2-metaIndentW)
			line := metaIndent
			if glyphs != "" {
				line += glyphs + "  "
				metaRoom = max(10, metaRoom-lipgloss.Width(glyphs)-2)
			}
			// While the workspace is mid-lifecycle, its port/branch meta
			// isn't meaningful yet — surface the transient state instead:
			// "deleting…" while a delete job runs, "setting up · pnpm i"
			// from a live create job's current step, or a plain "creating…"
			// for an optimistic row whose job hasn't registered yet.
			metaSrc := m.metaLine(item)
			setupJob, settingUp := m.workspaceSetupJob(item)
			switch {
			case m.workspaceDeleting(item):
				metaSrc = "deleting…"
			case settingUp:
				if lbl := workspaceSetupStepLabel(setupJob); lbl != "" {
					metaSrc = "setting up · " + lbl
				} else {
					metaSrc = "setting up…"
				}
			case item.Optimistic:
				metaSrc = "creating…"
			}
			metaText := truncate(metaSrc, metaRoom)
			body = append(body, fitRow(line+m.renderMetaText(metaText), width-2))
		case deckRowCollapsed:
			// Quiet default-only project: fold the project header, the
			// lone "default" workspace row, and its meta line into one
			// row. The project name stands in for the workspace label
			// (the "default" name carries no information), with the PR
			// glyphs and the meta text inline after it. Projects whose
			// default workspace has a visible status dot never collapse
			// (see collapsedProjects), so this row carries no dot.
			item := items[r.itemIndex]
			// The project name is colored like a project header so the row
			// reads as project-level rather than a workspace label —
			// otherwise a collapsed row blends into the workspace rows
			// above it. (Collapse only happens in project-grouped scopes,
			// so this is always the project-header treatment.)
			nameStyle := s.ProjectHeader
			// The find hint / cursor bar live in the same fixed prefix
			// slot that project headers use, so the row doesn't shift
			// when find mode toggles a hint in or out. No status glyph:
			// only quiet projects collapse (collapsedProjects gates on
			// statusGlyphVisible), so the dot would always be blank —
			// dropping it lands the project name exactly in the
			// project-header name column.
			prefix := "  "
			hinted := false
			if hint, ok := rowHints[r.itemIndex]; ok {
				prefix, hinted = renderDeckHint(hint), true
			} else if hint, ok := projectHints[item.ProjectName]; ok {
				prefix, hinted = renderDeckHint(hint), true
			}
			// A hint on the row means find mode has it as a live target —
			// one keystroke lands the cursor straight on the workspace
			// (collapsed projects skip the workspace stage), so light the
			// name up to make it pop while find mode is up. Use the same
			// plain default-fg style as a normal/active row label so the
			// highlight matches the other lit rows exactly.
			if hinted {
				nameStyle = s.Label
			}
			if r.itemIndex == m.cursor {
				prefix = s.Bar.Render("┃") + " "
				nameStyle = s.Selected
			}
			name := truncate(item.ProjectName, max(10, width-19))
			line := fmt.Sprintf("%s %s", prefixSlot.Render(prefix), nameStyle.Render(name))
			if prGlyph := m.prGlyphForItem(item); prGlyph != "" {
				line += " " + prGlyph
			}
			if staleGlyph := m.prStaleGlyphForItem(item); staleGlyph != "" {
				line += " " + staleGlyph
			}
			if localStale := m.prLocalStaleGlyphForItem(item); localStale != "" {
				line += " " + localStale
			}
			if reviewReq := m.prReviewReqGlyphForItem(item); reviewReq != "" {
				line += " " + reviewReq
			}
			// Append the meta text inline (semantic tokens colored) when
			// there's room left on the line after the name and glyphs.
			if meta := strings.TrimSpace(m.metaLine(item)); meta != "" {
				metaRoom := width - 2 - lipgloss.Width(line) - 3
				if metaRoom >= 6 {
					line += "   " + m.renderMetaText(truncate(meta, metaRoom))
				}
			}
			body = append(body, fitRow(line, width-2))
			if r.itemIndex == m.cursor {
				cursorRow = len(body) - 1
			}
		}
		projectHeaderForRow = append(projectHeaderForRow, r.headerRow)
	}

	capacity := m.deckBodyCapacity()
	yoff := m.deckYOffset
	// Sticky project header: when the cursor's project header is
	// scrolled above the viewport, pin it as a static line above the
	// viewport's first visible row so the user always knows which
	// project they're navigating in. clampDeckViewport has already
	// adjusted yoff to account for the lost row.
	var stickyHeader string
	cursorHdr := -1
	if cursorRow >= 0 && cursorRow < len(projectHeaderForRow) {
		cursorHdr = projectHeaderForRow[cursorRow]
	}
	if cursorHdr >= 0 && cursorHdr < yoff {
		stickyHeader = body[cursorHdr]
		if capacity > 1 {
			capacity--
		}
	}
	m.deckViewport.Width = width - 2
	m.deckViewport.Height = capacity
	m.deckViewport.SetContent(strings.Join(body, "\n"))
	m.deckViewport.SetYOffset(yoff)

	out := make([]string, 0, len(header)+2)
	out = append(out, header...)
	if stickyHeader != "" {
		out = append(out, stickyHeader)
	}
	out = append(out, m.deckViewport.View())
	return lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1).Render(strings.Join(out, "\n"))
}

// deckStackThreshold is the minimum terminal width (cols) for the
// new-menu / open pickers to render in side-by-side layout with a
// right-pane help block. Below this the picker takes the full width
// (mirroring the bookmark/review pickers).
const deckStackThreshold = 90

// deckStacked reports whether the deck is in the narrow-terminal
// layout where pickers render full-width without a right pane.
func (m Model) deckStacked() bool {
	return m.width > 0 && m.width < deckStackThreshold
}

// pickerSplit returns (leftWidth, rightWidth) for the new-menu / open
// pickers: a 70/30 split with a 3-col gutter when wide enough, or full
// width with a zero right pane in stacked mode.
func pickerSplit(total int, stacked bool) (int, int) {
	if stacked {
		return total, 0
	}
	const gap = 3
	right := max(28, (total-gap)*30/100)
	left := total - right - gap
	if left < 32 {
		left = 32
		right = max(0, total-left-gap)
	}
	return left, right
}

// deckBodyCapacity returns the number of scrollable body rows the left
// column can show given the terminal height. Subtracts the chrome the
// caller renders around the body: the title row + blank header (2
// lines), the panel's own Padding(2, 1, 1, 1) (3 lines = 2 top + 1
// bottom), and the footer row + its Padding (3 lines). Each entry in
// `body` is exactly 1 rendered line, so the math is precise without
// slack. Falls back to a generous capacity when height is unknown so
// tests and initial paints don't accidentally hide rows.
func (m Model) deckBodyCapacity() int {
	if m.height <= 0 {
		return len(m.items()) * 2
	}
	const chrome = 2 + 3 + 3
	rows := m.height - chrome
	if rows < 1 {
		rows = 1
	}
	return rows
}

// deckHalfPageStep is how many workspace rows ctrl+d / ctrl+u move the
// cursor — roughly half a visible screen, mirroring vim's half-page
// scroll. Body capacity is measured in rendered rows and each workspace
// item occupies itemBodyHeight rows, so the visible item count is
// capacity/itemBodyHeight; half of that is the step. clampDeckViewport
// then follows the cursor to bring the new position into view. Always
// at least 1 so the keys never become a no-op on short terminals.
func (m Model) deckHalfPageStep() int {
	visibleItems := m.deckBodyCapacity() / itemBodyHeight
	step := visibleItems / 2
	if step < 1 {
		step = 1
	}
	return step
}

// deckScrollOff is the number of rows kept visible above and below the
// cursor before the viewport starts scrolling — vim's `scrolloff`. The
// effect is a soft "deadzone" near the top and bottom edges: rather
// than scrolling the instant the cursor reaches the edge, the viewport
// follows the cursor as it enters a margin band, so the user always
// has context rows ahead of where they're heading.
const deckScrollOff = 3

// clampDeckViewport keeps the deck row list's cursor in view by
// adjusting deckYOffset. Uses a scrolloff-style margin (deckScrollOff)
// rather than strict edge-triggering: the viewport follows the cursor
// while it's within `deckScrollOff` rows of either edge, so there's
// always a small look-ahead band of context.
//
// The actual viewport.YOffset is synced from deckYOffset in renderList
// after SetContent loads the body so viewport's internal clamp doesn't
// reset us against an empty content buffer. Accounts for the sticky
// project-header line rendered ABOVE the viewport when the cursor's
// project header has scrolled off — that reduces the visible window
// by one row.
func (m *Model) clampDeckViewport() {
	items := m.items()
	if len(items) == 0 || m.height <= 0 {
		m.deckYOffset = 0
		return
	}
	capacity := m.deckBodyCapacity()
	rows := m.bodyRows(items)
	total := len(rows)
	// While find mode is up the layout is collapsed (collapseForFind), and
	// the cursor isn't the thing being navigated — the hint targets are —
	// so scroll to the find focus row rather than the cursor: the top of
	// the header column in the project stage, or the chosen section's
	// header in the workspace stage.
	if m.findMode && m.scope != ScopeInbox {
		m.deckYOffset = findViewportOffset(m.findFocusTopRow(rows), total, capacity)
		return
	}
	if total <= capacity {
		m.deckYOffset = 0
		return
	}
	// Scrolloff can't claim more than half the window — otherwise the
	// top and bottom margins overlap and the cursor's reserved
	// position would lie outside the viewport.
	scrolloff := deckScrollOff
	if max := (capacity - 1) / 2; scrolloff > max {
		scrolloff = max
	}
	if scrolloff < 0 {
		scrolloff = 0
	}
	cursorRow := deckBodyCursorRow(rows, m.cursor)
	cursorHdr := deckBodyHeaderRowForCursor(rows, m.cursor)
	yoff := m.deckYOffset
	// Top margin: scroll up so the cursor sits at least scrolloff rows
	// below the top edge of the viewport.
	if cursorRow-scrolloff < yoff {
		yoff = cursorRow - scrolloff
	}
	// Bottom margin: scroll down so the cursor sits at least
	// scrolloff rows above the bottom edge.
	if cursorRow+scrolloff >= yoff+capacity {
		yoff = cursorRow + scrolloff - capacity + 1
	}
	// Re-check with sticky-aware effective capacity. The sticky header
	// shrinks the visible window by one row, so the bottom margin can
	// push the cursor off; recompute with the shrunken capacity.
	if cursorHdr >= 0 && cursorHdr < yoff {
		effective := capacity - 1
		if effective < 1 {
			effective = 1
		}
		if cursorRow+scrolloff >= yoff+effective {
			yoff = cursorRow + scrolloff - effective + 1
		}
	}
	maxOffset := total - capacity
	if cursorHdr >= 0 && cursorHdr < yoff {
		// Sticky takes a row, so the effective window is one row
		// smaller and yoff can correspondingly be one larger while
		// still keeping the last body row reachable.
		maxOffset = total - capacity + 1
	}
	if yoff > maxOffset {
		yoff = maxOffset
	}
	if yoff < 0 {
		yoff = 0
	}
	m.deckYOffset = yoff
}

// findFocusTopRow returns the body-row index the viewport should bring
// into view while find mode is up. The project stage focuses the top (0)
// so the compacted header column reads from the first section; the
// workspace stage focuses the expanded section's header so the chosen
// project's/register's rows are on screen. rows must be the collapsed
// layout (bodyRows under find mode).
func (m Model) findFocusTopRow(rows []deckBodyRow) int {
	if m.findStage != findStageWorkspace {
		return 0
	}
	for i, r := range rows {
		switch r.kind {
		case deckRowHeader:
			if m.findProject != "" && r.project == m.findProject {
				return i
			}
		case deckRowPinHeader:
			if m.findPinGroup != "" && r.project == m.findPinGroup {
				return i
			}
		}
	}
	return 0
}

// findViewportOffset positions the find focus row near the top of the
// viewport, leaving a deckScrollOff-sized band of context above it, and
// clamps the result to the scrollable range.
func findViewportOffset(focus, total, capacity int) int {
	if total <= capacity {
		return 0
	}
	yoff := focus - deckScrollOff
	if maxOffset := total - capacity; yoff > maxOffset {
		yoff = maxOffset
	}
	if yoff < 0 {
		yoff = 0
	}
	return yoff
}

// itemBodyHeight is the number of body lines a non-collapsed workspace
// item occupies: a primary row (status + label + PR glyph) and a
// secondary muted metadata line (@author · head · dev). It's only used
// as a heuristic for deckHalfPageStep's "visible items" estimate now;
// exact row positions come from deckBodyRows. Default-only projects
// collapse to a single line and so don't follow this height.
const itemBodyHeight = 2

// deckRowKind classifies one rendered line of the deck body. Each kind
// is exactly one terminal row, which keeps the scroll capacity math
// (measured in rows) exact.
type deckRowKind int

const (
	deckRowHeader    deckRowKind = iota // project name on its own line
	deckRowSpacer                       // blank line between projects
	deckRowPrimary                      // item: status-tinted label
	deckRowMeta                         // item: muted @author · branch · …
	deckRowCollapsed                    // default-only project folded into one line
	deckRowPinHeader                    // pinned-register section header (project field holds the register key)
	deckRowSubHeader                    // secondary header (inbox: project name under a bucket header)
)

// deckBodyRow is one structural line of the deck body. It carries enough
// context for both the renderer (renderList) and the scroll math to act
// on it without re-deriving layout — there's a single source of truth
// for where every project header, workspace row, and spacer lands.
type deckBodyRow struct {
	kind      deckRowKind
	itemIndex int    // index into items for primary/meta/collapsed; -1 otherwise
	project   string // project name for header/primary/collapsed; "" for spacer
	headerRow int    // body index of the governing project header (== self for collapsed)
}

// collapsedProjects returns the set of project names that render as a
// single collapsed line: a project whose only workspace is the default
// one. Matches the "default" naming convention the deck already keys off
// for delete/rename. Such a project's header, sole row, and meta line
// carry no information the project name doesn't, so they fold to one row.
func collapsedProjects(items []Item) map[string]bool {
	counts := map[string]int{}
	for _, it := range items {
		counts[it.ProjectName]++
	}
	out := map[string]bool{}
	for _, it := range items {
		if counts[it.ProjectName] != 1 || strings.TrimSpace(it.WorkspaceName) != "default" {
			continue
		}
		// Only quiet rows collapse: a default workspace whose agent has
		// a visible status dot (working, or unread waiting/idle) renders
		// in the full header + workspace + meta layout so the dot sits
		// in its usual column and the row reads like any other active
		// workspace. Collapsed rows therefore never carry a dot.
		if statusGlyphVisible(it.Status, it.Unread) {
			continue
		}
		out[it.ProjectName] = true
	}
	return out
}

// deckBodyRows produces the structural layout of the deck body for the
// given items: one element per rendered line, in render order. Both
// renderList and the scroll math (deckBodyCursorRow /
// deckBodyHeaderRowForCursor) consume this slice so they can never
// disagree about row positions — necessary because collapsed default-only
// projects make per-project height variable. Each group emits a header
// (or a single collapsed row), every group after the first is preceded
// by a blank spacer, and each non-collapsed item contributes a primary +
// meta row.
//
// groups, when non-nil, is a parallel slice giving each item's header
// label — the inbox scope passes bucket labels here so rows section by
// bucket instead of by project. nil falls back to ProjectName grouping.
// Collapse only applies to project grouping (collapsed keys are project
// names), so group-label callers pass collapsed == nil.
func deckBodyRows(items []Item, collapsed map[string]bool, groups []string) []deckBodyRow {
	groupOf := func(i int) string {
		if groups != nil {
			return groups[i]
		}
		return items[i].ProjectName
	}
	rows := make([]deckBodyRow, 0, len(items)*2)
	lastGroup := ""
	headerIdx := -1
	for i, it := range items {
		g := groupOf(i)
		if g != lastGroup {
			if lastGroup != "" {
				rows = append(rows, deckBodyRow{kind: deckRowSpacer, itemIndex: -1, headerRow: headerIdx})
			}
			if groups == nil && collapsed[it.ProjectName] {
				// The collapsed row is its own header — folding the
				// header, row, and meta line into one. headerRow points
				// at itself so the sticky-header logic never re-pins it.
				idx := len(rows)
				rows = append(rows, deckBodyRow{kind: deckRowCollapsed, itemIndex: i, project: it.ProjectName, headerRow: idx})
				headerIdx = idx
				lastGroup = g
				continue
			}
			headerIdx = len(rows)
			rows = append(rows, deckBodyRow{kind: deckRowHeader, itemIndex: -1, project: g, headerRow: headerIdx})
			lastGroup = g
		}
		rows = append(rows, deckBodyRow{kind: deckRowPrimary, itemIndex: i, project: g, headerRow: headerIdx})
		rows = append(rows, deckBodyRow{kind: deckRowMeta, itemIndex: i, project: g, headerRow: headerIdx})
	}
	return rows
}

// bodyRows returns the structural layout for the current scope: project
// grouping (with default-only collapse) everywhere except the inbox
// scope, which sections by bucket header (with counts) and never
// collapses.
func (m Model) bodyRows(items []Item) []deckBodyRow {
	if m.scope == ScopeInbox {
		return m.inboxBodyRows(items)
	}
	pinned := pinnedCount(items)
	var rows []deckBodyRow
	if pinned == 0 {
		rows = deckBodyRows(items, collapsedProjects(items), nil)
	} else {
		rows = m.deckBodyRowsPinned(items, pinned)
	}
	return m.collapseForFind(rows, items)
}

// collapseForFind rewrites the body layout while find mode is up so a
// long list stays scannable. The project stage shows only section
// headers — every workspace row folds away, so the whole project
// skeleton fits on one screen. The workspace stage expands only the
// chosen project / register; the other sections stay as one-line headers
// for context. Returns rows unchanged when find mode is off or in the
// inbox scope (which has no project stage). Header, sub-header, and
// collapsed rows always survive so their body indices stay valid targets
// for headerRow references; only workspace primary/meta rows of
// un-expanded sections are dropped, and headerRow is remapped into the
// compacted slice.
func (m Model) collapseForFind(rows []deckBodyRow, items []Item) []deckBodyRow {
	if !m.findMode || m.scope == ScopeInbox {
		return rows
	}
	// In the project stage the list is a pure header column, so drop the
	// inter-group spacers too — one line per section is the point.
	dropSpacers := m.findStage == findStageProject
	out := make([]deckBodyRow, 0, len(rows))
	oldToNew := make([]int, len(rows))
	for i, r := range rows {
		oldToNew[i] = -1
		switch r.kind {
		case deckRowPrimary, deckRowMeta:
			if r.itemIndex < 0 || r.itemIndex >= len(items) || !m.findRowExpanded(items[r.itemIndex]) {
				continue
			}
		case deckRowSpacer:
			if dropSpacers {
				continue
			}
		}
		oldToNew[i] = len(out)
		out = append(out, r)
	}
	// Remap headerRow into the compacted slice. Every referenced header
	// row is a header/collapsed row, which collapse never drops, so the
	// mapping is always defined.
	for i := range out {
		if h := out[i].headerRow; h >= 0 && h < len(oldToNew) && oldToNew[h] >= 0 {
			out[i].headerRow = oldToNew[h]
		}
	}
	return out
}

// findRowExpanded reports whether a workspace row's section is currently
// expanded under find mode's collapse. The project stage expands nothing
// (only headers show); the workspace stage expands the single scoped
// project or register (findRowInScope). Outside find mode / in the inbox
// scope every row is expanded — collapseForFind gates on those before
// calling, so in practice this only decides workspace-stage scoping.
func (m Model) findRowExpanded(it Item) bool {
	if !m.findMode || m.scope == ScopeInbox {
		return true
	}
	if m.findStage == findStageProject {
		return false
	}
	return m.findRowInScope(it)
}

// inboxBodyRows lays out the inbox scope: a bucket header per section
// (the governing header for sticky-scroll and cursor context), then a
// project subheader whenever the project changes within a bucket, then
// each item's primary + meta rows. items must be pre-ordered by
// (bucket, project, stack) — View.Items guarantees this — so equal
// buckets and projects are already contiguous. The per-row [project]
// chip is dropped in this scope; the subheader carries the project.
func (m Model) inboxBodyRows(items []Item) []deckBodyRow {
	labels := m.inboxGroupLabels(items)
	rows := make([]deckBodyRow, 0, len(items)*2)
	lastBucket, lastProject := "", ""
	bucketHdr := -1
	for i, it := range items {
		if labels[i] != lastBucket {
			if lastBucket != "" {
				rows = append(rows, deckBodyRow{kind: deckRowSpacer, itemIndex: -1, headerRow: bucketHdr})
			}
			bucketHdr = len(rows)
			rows = append(rows, deckBodyRow{kind: deckRowHeader, itemIndex: -1, project: labels[i], headerRow: bucketHdr})
			lastBucket = labels[i]
			lastProject = "" // force a project subheader at the start of every bucket
		}
		if it.ProjectName != lastProject {
			// Blank line between project groups within a bucket (not before
			// the first project — the bucket header already leads that one).
			if lastProject != "" {
				rows = append(rows, deckBodyRow{kind: deckRowSpacer, itemIndex: -1, headerRow: bucketHdr})
			}
			rows = append(rows, deckBodyRow{kind: deckRowSubHeader, itemIndex: -1, project: it.ProjectName, headerRow: bucketHdr})
			lastProject = it.ProjectName
		}
		rows = append(rows, deckBodyRow{kind: deckRowPrimary, itemIndex: i, project: it.ProjectName, headerRow: bucketHdr})
		rows = append(rows, deckBodyRow{kind: deckRowMeta, itemIndex: i, project: it.ProjectName, headerRow: bucketHdr})
	}
	return rows
}

// deckBodyRowsPinned lays out the all / attention body when some rows
// are pinned. items must be ordered pinned-first (items() guarantees
// this): the leading `pinned` items form the pinned region — sectioned
// by register with a deckRowPinHeader per register, never collapsed —
// and the remaining items form the usual project-grouped region (with
// default-only collapse) beneath a blank spacer. Row itemIndex/headerRow
// stay absolute into items so the renderer and scroll math agree.
func (m Model) deckBodyRowsPinned(items []Item, pinned int) []deckBodyRow {
	rows := make([]deckBodyRow, 0, len(items)*2)
	lastKey := ""
	headerIdx := -1
	for i := 0; i < pinned; i++ {
		key := items[i].PinGroup
		if key != lastKey {
			if lastKey != "" {
				rows = append(rows, deckBodyRow{kind: deckRowSpacer, itemIndex: -1, headerRow: headerIdx})
			}
			headerIdx = len(rows)
			rows = append(rows, deckBodyRow{kind: deckRowPinHeader, itemIndex: -1, project: key, headerRow: headerIdx})
			lastKey = key
		}
		rows = append(rows, deckBodyRow{kind: deckRowPrimary, itemIndex: i, project: key, headerRow: headerIdx})
		rows = append(rows, deckBodyRow{kind: deckRowMeta, itemIndex: i, project: key, headerRow: headerIdx})
	}
	// Project-grouped region for the unpinned suffix. Build it against the
	// suffix slice (so collapse detection only considers unpinned rows),
	// then shift each row's itemIndex/headerRow into absolute coordinates.
	suffix := items[pinned:]
	if len(suffix) > 0 {
		rows = append(rows, deckBodyRow{kind: deckRowSpacer, itemIndex: -1, headerRow: headerIdx})
		base := len(rows)
		for _, r := range deckBodyRows(suffix, collapsedProjects(suffix), nil) {
			if r.itemIndex >= 0 {
				r.itemIndex += pinned
			}
			if r.headerRow >= 0 {
				r.headerRow += base
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// inboxGroupLabels returns each item's bucket header label — "Needs your
// review (2)" — parallel to items. items must already be sorted by
// bucket (items() does this in the inbox scope) so equal labels are
// adjacent and deckBodyRows emits one header per bucket.
func (m Model) inboxGroupLabels(items []Item) []string {
	buckets := make([]inboxBucket, len(items))
	counts := make(map[inboxBucket]int, inboxBucketCount)
	for i, it := range items {
		// Section by the stack's bucket (SectionBucket), not the row's
		// own — a stacked PR shares its stack's header so the chain stays
		// contiguous under one section. Items() sets SectionBucket for
		// every inbox row (== own bucket for standalone PRs).
		buckets[i] = it.SectionBucket
		counts[buckets[i]]++
	}
	labels := make([]string, len(items))
	for i, b := range buckets {
		labels[i] = fmt.Sprintf("%s (%d)", inboxBucketLabel(b), counts[b])
	}
	return labels
}

// deckBodyCursorRow returns the body-row index of the PRIMARY (or
// collapsed) line of the workspace row corresponding to items[cursor].
// rows must come from the same bodyRows call the renderer consumes.
func deckBodyCursorRow(rows []deckBodyRow, cursor int) int {
	for idx, r := range rows {
		if r.itemIndex == cursor && (r.kind == deckRowPrimary || r.kind == deckRowCollapsed) {
			return idx
		}
	}
	return 0
}

// deckBodyHeaderRowForCursor returns the body-row index of the group
// header for items[cursor]. Returns -1 when cursor is out of range.
// clampDeckViewport uses this to detect when the cursor's group header
// has scrolled above the viewport so renderList can pin it as a sticky
// line. For a collapsed project the row is its own header, so this
// returns the row's own index and the sticky check never fires for it.
func deckBodyHeaderRowForCursor(rows []deckBodyRow, cursor int) int {
	for _, r := range rows {
		if r.itemIndex == cursor && (r.kind == deckRowPrimary || r.kind == deckRowCollapsed) {
			return r.headerRow
		}
	}
	return -1
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
				{"gg / G", "jump to top / bottom of list"},
				{"ctrl+u / ctrl+d", "jump ½ page up / down"},
				{"/", "filter rows · esc clears"},
				{"f", "find: collapse to sections → expand one → jump"},
				{"P", "cycle scope (all → attention → inbox)"},
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
				{"A", "send a typed prompt to the workspace's agent"},
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
				{"R", "rename workspace"},
				{"D", "delete workspace (or default → delete project)"},
				{"B", "link bookmark to workspace (drives PR glyph)"},
				{"d", "open dev URL in browser (auto-discovered)"},
				{"p o", "open this workspace's PR in browser"},
				{"p m", "merge this workspace's PR (gh pr merge --squash, with confirmation)"},
				{"p d", "open this workspace's PR description in a pr tmux window (gh pr view | less)"},
				{"p r", "repair this workspace's PR (prepopulates a fix prompt)"},
				{"p s", "set PR # override for this workspace (when the bookmark doesn't match the PR head ref)"},
				{",", "edit global state file in $EDITOR"},
			},
		},
		{
			Title: "Pin / group",
			Keys: [][2]string{
				{"m m", "pin selected → default group (again unpins)"},
				{"m a…z", "pin selected → group <letter> (same again unpins, different moves)"},
				{"m D", "unpin selected workspace"},
				{"m R", "name the selected row's group (display alias)"},
			},
		},
		{
			Title: "Async jobs",
			Keys: [][2]string{
				{"J", "jobs overlay (list, cancel, retry, dismiss, open log)"},
				{"w", "watch dev-loop progress for the selected workspace"},
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

func (m *Model) renderProgress(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)).Render(m.progressTitle)
	rows := []string{title, ""}
	if len(m.progressSteps) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render(m.spinner.View()+" starting..."))
	}
	for _, step := range m.progressSteps {
		var glyph, color string
		switch step.State {
		case StepDone:
			glyph, color = "✓", colSuccess
		case StepError:
			glyph, color = "✗", colDanger
		case StepRunning:
			glyph, color = m.spinner.View(), colInfo
		default:
			glyph, color = "○", colMuted
		}
		line := fmt.Sprintf("%s %s", lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph), step.Label)
		rows = append(rows, line)
	}
	if m.progressErr != nil {
		rows = append(rows, "")
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Render("error: "+m.progressErr.Error()))
	}
	if len(m.progressLog) > 0 {
		rows = append(rows, "")
		rows = append(rows, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Bold(true).Render("log"))
		// Reserve rows already accounted for above + 4 for the modal
		// footer / status line. Whatever's left of the height is the
		// log viewport's vertical space; the user pages through with
		// pgup/pgdn/ctrl+u/ctrl+d if the log overflows.
		logHeight := m.height - len(rows) - 4
		if logHeight < 4 {
			logHeight = 4
		}
		m.progressViewport.Width = width - 2
		m.progressViewport.Height = logHeight
		m.progressViewport.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
		m.progressViewport.SetContent(strings.Join(m.progressLog, "\n"))
		rows = append(rows, m.progressViewport.View())
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

func (m Model) selected() (Item, bool) {
	items := m.items()
	if len(items) == 0 || m.cursor < 0 || m.cursor >= len(items) {
		return Item{}, false
	}
	return items[m.cursor], true
}

// applyPinGroup pins, moves, or unpins the selected workspace. target
// "" unpins (gD); otherwise "default" (gg) or a letter register. Aiming
// at the register the row is already in unpins it (toggle); a different
// target moves it. Persists via pinGroupHandler and refreshes so the
// re-sort floats the row into (or out of) the pinned region.
func (m Model) applyPinGroup(target string) (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || item.Virtual || strings.TrimSpace(item.WorkspaceName) == "" {
		m.status = "pin: select a workspace row"
		return m, nil
	}
	current := strings.TrimSpace(item.PinGroup)
	newGroup := target
	switch {
	case target == "": // gD: unpin
		if current == "" {
			m.status = "pin: not pinned"
			return m, nil
		}
		newGroup = ""
	case current == target: // aim at the current register → toggle off
		newGroup = ""
	}
	if m.pinGroupHandler == nil {
		m.status = "pin: handler not configured"
		return m, nil
	}
	if err := m.pinGroupHandler(item, newGroup); err != nil {
		m.status = "pin: " + err.Error()
		return m, nil
	}
	if newGroup == "" {
		m.status = fmt.Sprintf("pin: unpinned %s", item.WorkspaceName)
	} else {
		m.status = fmt.Sprintf("pin: %s → %s", item.WorkspaceName, m.pinGroupLabel(newGroup))
	}
	var refreshCmd tea.Cmd
	m, refreshCmd = m.requestRefresh(false)
	return m, refreshCmd
}

// startPinAliasRename opens the alias-rename text input for the register
// the selected workspace is pinned to. No-op with a hint when the
// selected row isn't pinned.
func (m Model) startPinAliasRename() (tea.Model, tea.Cmd) {
	item, ok := m.selected()
	if !ok || strings.TrimSpace(item.PinGroup) == "" {
		m.status = "pin: select a pinned workspace to name its group"
		return m, nil
	}
	key := strings.TrimSpace(item.PinGroup)
	var aliasModal *pinAliasModal
	var cmd tea.Cmd
	aliasModal, cmd, m.status = newPinAliasModal(&m, key)
	m.active = aliasModal
	return m, cmd
}

func (m *Model) startFind() {
	m.findMode = true
	m.findPendingPrefix = 0
	m.findProject = ""
	m.findPinGroup = ""
	if m.scope == ScopeInbox {
		// The inbox scope has no project headers (rows section by
		// bucket), so find skips the project stage and hints every
		// row directly, like the mini-deck.
		m.findStage = findStageWorkspace
		m.findProjectHints = map[string]string{}
		m.findPinHints = map[string]string{}
		m.findProjectLookup = map[string]findTarget{}
		m.findProjectPrefix = map[rune]bool{}
		m.findRowHints, m.findRowLookup, m.findRowPrefix = m.buildRowHints("")
		m.status = "find: workspace"
		if len(m.findRowLookup) == 0 {
			m.cancelFind("")
		}
		return
	}
	m.findStage = findStageProject
	m.findProjectHints, m.findPinHints, m.findProjectLookup, m.findProjectPrefix = m.buildSectionHints()
	m.findRowHints = map[int]string{}
	m.findRowLookup = map[string]int{}
	m.findRowPrefix = map[rune]bool{}
	m.status = "find: project"
	// The list just collapsed to a header column; reset the viewport to
	// the top so it reads from the first section.
	m.clampDeckViewport()
}

func (m *Model) cancelFind(status string) {
	m.findMode = false
	m.findStage = findStageProject
	m.findProject = ""
	m.findPinGroup = ""
	m.findPendingPrefix = 0
	m.findProjectHints = map[string]string{}
	m.findPinHints = map[string]string{}
	m.findProjectLookup = map[string]findTarget{}
	m.findProjectPrefix = map[rune]bool{}
	m.findRowHints = map[int]string{}
	m.findRowLookup = map[string]int{}
	m.findRowPrefix = map[rune]bool{}
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) handleFindRune(r rune) (tea.Model, tea.Cmd) {
	hadPending := m.findPendingPrefix != 0
	if m.findStage == findStageProject {
		target, ok := findHintStep(r, m.findProjectLookup, m.findProjectPrefix, &m.findPendingPrefix)
		if ok {
			if target.kind == findTargetPin {
				return m.enterPinStage(target.key), nil
			}
			return m.enterWorkspaceStage(target.key), nil
		}
		switch {
		case hadPending:
			m.status = stageStatus(m.findStage)
		case m.findPendingPrefix != 0:
			m.status = fmt.Sprintf("find: project %c…", m.findPendingPrefix)
		}
		return m, nil
	}
	idx, ok := findHintStep(r, m.findRowLookup, m.findRowPrefix, &m.findPendingPrefix)
	if ok {
		m.cursor = idx
		m.cancelFind("")
		if item, ok := m.selected(); ok {
			m.status = "find: " + item.WorkspaceName
		}
		return m, nil
	}
	switch {
	case hadPending:
		m.status = stageStatus(m.findStage)
	case m.findPendingPrefix != 0:
		m.status = fmt.Sprintf("find: workspace %c…", m.findPendingPrefix)
	}
	return m, nil
}

func (m Model) enterWorkspaceStage(project string) Model {
	m.findProject = project
	m.findPinGroup = ""
	m.findStage = findStageWorkspace
	items := m.items()
	matches := []int{}
	for i, item := range items {
		if strings.TrimSpace(item.PinGroup) == "" && item.ProjectName == project {
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
		return m
	}
	// Scroll the now-expanded project into view.
	(&m).clampDeckViewport()
	return m
}

// enterPinStage scopes the workspace stage to a pinned register. Mirrors
// enterWorkspaceStage: auto-selects when the register holds one row,
// otherwise hints the register's rows.
func (m Model) enterPinStage(group string) Model {
	m.findProject = ""
	m.findPinGroup = group
	m.findStage = findStageWorkspace
	items := m.items()
	matches := []int{}
	for i, item := range items {
		if strings.TrimSpace(item.PinGroup) == group {
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
	m.findRowHints, m.findRowLookup, m.findRowPrefix = m.buildPinRowHints(group)
	m.status = "find: workspace"
	if len(m.findRowLookup) == 0 {
		m.cancelFind("find: cancelled")
		return m
	}
	// Scroll the now-expanded register into view.
	(&m).clampDeckViewport()
	return m
}

func stageStatus(stage findStage) string {
	if stage == findStageWorkspace {
		return "find: workspace"
	}
	return "find: project"
}

// buildSectionHints assigns stage-1 find hints across both kinds of
// section: pinned registers (first, in the same default-first/alpha
// order they render) and unpinned projects. Hints are globally unique
// across both kinds so one keystroke lands unambiguously. Returns the
// project-name→hint map (for project headers), the register-key→hint
// map (for pinned section headers), the hint→target lookup, and the
// two-key prefix set.
func (m Model) buildSectionHints() (map[string]string, map[string]string, map[string]findTarget, map[rune]bool) {
	items := m.items()
	targets := []findTarget{}
	labels := []string{}
	seenPin := map[string]struct{}{}
	seenProj := map[string]struct{}{}
	// items() sorts pinned rows first in register order, so encounter
	// order here matches the on-screen section order.
	for _, item := range items {
		if g := strings.TrimSpace(item.PinGroup); g != "" {
			if _, ok := seenPin[g]; ok {
				continue
			}
			seenPin[g] = struct{}{}
			targets = append(targets, findTarget{kind: findTargetPin, key: g})
			labels = append(labels, m.pinGroupLabel(g))
		}
	}
	for _, item := range items {
		if strings.TrimSpace(item.PinGroup) != "" {
			continue
		}
		if _, ok := seenProj[item.ProjectName]; ok {
			continue
		}
		seenProj[item.ProjectName] = struct{}{}
		targets = append(targets, findTarget{kind: findTargetProject, key: item.ProjectName})
		labels = append(labels, item.ProjectName)
	}

	hints := assignSectionHints(labels)
	projectHints := map[string]string{}
	pinHints := map[string]string{}
	lookup := map[string]findTarget{}
	prefix := map[rune]bool{}
	for i, t := range targets {
		hint := hints[i]
		if hint == "" {
			continue
		}
		lookup[hint] = t
		if t.kind == findTargetPin {
			pinHints[t.key] = hint
		} else {
			projectHints[t.key] = hint
		}
		if len([]rune(hint)) == 2 {
			prefix[[]rune(hint)[0]] = true
		}
	}
	return projectHints, pinHints, lookup, prefix
}

// assignSectionHints assigns easymotion hints to a list of section
// labels by index, deriving mnemonics from the labels while tolerating
// duplicate labels (two registers/projects can share a display name).
// It delegates to assignHints over per-index-unique names — the NUL +
// index suffix keeps each name distinct without adding mnemonic letters
// (assignHints only draws a–z from the name) or changing the first
// rune.
func assignSectionHints(labels []string) []string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l + "\x00" + strconv.Itoa(i)
	}
	byName := assignHints(names)
	out := make([]string, len(labels))
	for i := range labels {
		out[i] = byName[names[i]]
	}
	return out
}

// buildRowHints assigns easymotion hints to the unpinned rows of the
// given project. project == "" hints every row (the inbox scope's
// single-stage find). Pinned rows are excluded — they belong to
// register sections and are targeted via buildPinRowHints — so a
// project stage only ever hints the rows shown in that project's
// section.
func (m Model) buildRowHints(project string) (map[int]string, map[string]int, map[rune]bool) {
	if project == "" {
		return m.buildRowHintsScoped(func(Item) bool { return true }, true)
	}
	return m.buildRowHintsScoped(func(it Item) bool {
		return strings.TrimSpace(it.PinGroup) == "" && it.ProjectName == project
	}, false)
}

// buildPinRowHints assigns hints to the rows of a pinned register.
// Names are project-qualified because a register can span projects.
func (m Model) buildPinRowHints(group string) (map[int]string, map[string]int, map[rune]bool) {
	return m.buildRowHintsScoped(func(it Item) bool {
		return strings.TrimSpace(it.PinGroup) == group
	}, true)
}

func (m Model) buildRowHintsScoped(match func(Item) bool, qualify bool) (map[int]string, map[string]int, map[rune]bool) {
	items := m.items()
	rowIdx := []int{}
	names := []string{}
	for i, item := range items {
		if !match(item) {
			continue
		}
		name := item.WorkspaceName
		if qualify {
			name = item.ProjectName + "/" + name
		}
		rowIdx = append(rowIdx, i)
		names = append(names, name)
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

// findHints returns the hints to render this frame: project-header
// hints (keyed by project name), pinned-section-header hints (keyed by
// register key), and row hints (keyed by item index). Section-header
// hints only show in the project stage; row hints only in the workspace
// stage.
func (m Model) findHints() (map[string]string, map[string]string, map[int]string) {
	if !m.findMode {
		return map[string]string{}, map[string]string{}, map[int]string{}
	}
	projectHints := map[string]string{}
	pinHints := map[string]string{}
	if m.findStage == findStageProject {
		for name, hint := range m.findProjectHints {
			projectHints[name] = hint
		}
		for key, hint := range m.findPinHints {
			pinHints[key] = hint
		}
	}
	rowHints := map[int]string{}
	for idx, hint := range m.findRowHints {
		rowHints[idx] = hint
	}
	return projectHints, pinHints, rowHints
}

// findRowInScope reports whether an item belongs to the section the
// workspace stage is scoped to — a pinned register, an unpinned
// project, or (inbox single-stage, neither set) every row. Rows out of
// scope render dimmed while find mode is up.
func (m Model) findRowInScope(it Item) bool {
	switch {
	case m.findPinGroup != "":
		return strings.TrimSpace(it.PinGroup) == m.findPinGroup
	case m.findProject != "":
		return strings.TrimSpace(it.PinGroup) == "" && it.ProjectName == m.findProject
	default:
		return true
	}
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
// target names, preferring the shortest hint for every target: each claims
// a single key when one is free, falling back to a two-key hint only when
// it can't. Targets are processed in list order, so earlier (higher on
// screen) targets win contested keys. A target's candidate keys come from
// its own name — its initial, then the salient letters (word-initials, then
// the rest) — so hints stay mnemonic even when the initial is taken.
//
// The result is prefix-free: a letter used as a single-key hint is never
// also the first key of a two-key hint. The find dispatcher relies on this
// to know whether a keystroke lands immediately (single hint) or opens a
// two-key sequence (prefix). If smart assignment somehow can't cover every
// target, the function falls back to sequential home-row hints.
func assignHints(names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 {
		return out
	}

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
	// singleCandidates lists a name's preferred single keys in priority
	// order: its initial, then its salient letters. mnemonicSecondKeys
	// already excludes the initial, so prepending it can't duplicate.
	singleCandidates := func(name string) []rune {
		first := firstRune(name)
		cands := make([]rune, 0, 8)
		if first != 0 {
			cands = append(cands, first)
		}
		return append(cands, mnemonicSecondKeys(name, first)...)
	}

	alphabet := []rune(findHintAlphabet)
	usedSingle := map[rune]bool{} // letters committed as single-key hints
	usedHint := map[string]bool{} // every assigned hint (single or double)

	// Pass 1: hand each target the highest-priority free single key drawn
	// from its own name. Anything that can't get one spills to pass 2.
	var overflow []string
	for _, name := range names {
		claimed := false
		for _, r := range singleCandidates(name) {
			if usedSingle[r] {
				continue
			}
			usedSingle[r] = true
			usedHint[string(r)] = true
			out[name] = string(r)
			claimed = true
			break
		}
		if !claimed {
			overflow = append(overflow, name)
		}
	}

	// Pass 2: two-key hints for the overflow. The prefix must not be a
	// letter already used as a single hint (keeps the set prefix-free);
	// prefer the name's own letters for both keys, then the alphabet.
	for _, name := range overflow {
		prefixCands := append(singleCandidates(name), alphabet...)
		assigned := false
		for _, p := range prefixCands {
			if p == 0 || usedSingle[p] {
				continue
			}
			secondCands := append(mnemonicSecondKeys(name, p), alphabet...)
			for _, s := range secondCands {
				if s == 0 {
					continue
				}
				hint := string(p) + string(s)
				if usedHint[hint] {
					continue
				}
				usedHint[hint] = true
				out[name] = hint
				assigned = true
				break
			}
			if assigned {
				break
			}
		}
		if !assigned {
			return legacyHints(names)
		}
	}

	return out
}

// legacyHints is the last-resort fallback when smart assignment can't
// cover every target: sequential home-row keys in list order.
func legacyHints(names []string) map[string]string {
	out := map[string]string{}
	for i, n := range names {
		if i >= len(findHintAlphabet) {
			break
		}
		out[n] = string(findHintAlphabet[i])
	}
	return out
}

// Nerd Font codepoints (Octicons + Material Design) used for the per-row PR
// status glyph. The deck assumes a patched font is available. Codepoints live
// in the Private Use Area, so they encode here as \u escapes that the Go
// compiler turns into the same UTF-8 bytes regardless of editor/rendering
// pipeline behavior. Plain "open" has no glyph on purpose — see prGlyphFor.
const (
	prGlyphDraft    = "\U000F1353" // nf-md-pencil_ruler — still being drawn up
	prGlyphClosed   = ""          // nf-oct-git_pull_request_closed
	prGlyphMerged   = ""          // nf-oct-git_merge
	prGlyphApproved = ""          // nf-oct-check
	prGlyphInQueue  = ""          // nf-oct-rocket — PR is in the merge queue
	prGlyphCIFail   = ""          // nf-oct-x
	prGlyphCIPend   = ""          // nf-oct-hourglass
	prGlyphBehind   = ""          // nf-oct-arrow_down — PR is behind the base branch
	prGlyphDirty    = ""          // nf-oct-alert — merge conflicts with the base branch
	prGlyphStale    = ""          // nf-oct-sync — local bookmark tip differs from the PR head (re-review hint)
	// Review-conversation glyphs come from the Material Design set
	// (nf-md-*), not Octicons — chat bubbles read better as "someone
	// wants to hear from you" than any octicon does. Outline = a review
	// is being asked of you; filled = feedback already exists on your
	// PR and is waiting on you.
	prGlyphReviewReq  = "\U000F0EDE" // nf-md-chat_outline — your review is requested on someone else's PR
	prGlyphChangesReq = "\U000F0B79" // nf-md-chat — a reviewer requested changes on YOUR PR
)

// prGlyphFor returns the single glyph for the given PR status per the locked
// priority order: merged → closed → CI failed → CI pending → in merge queue →
// approved → draft. Returns "" when no glyph should render (caller passes a
// zero/empty status when the workspace has no matching PR). Plain "open" is
// deliberately glyph-less: it's the baseline state, so painting it taught the
// eye to skim past the glyph column — only states that deviate from baseline
// earn ink. The details panel still says "open" via prStatusLabel.
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
	if s.IsInMergeQueue && s.State == PRStateOpen {
		return prGlyphInQueue
	}
	if s.ReviewDecision == PRReviewApproved {
		return prGlyphApproved
	}
	if s.IsDraft {
		return prGlyphDraft
	}
	return ""
}

// prGlyphColor picks a foreground color from the palette in palette.go.
// Routes every PR-status branch through a semantic token so the deck
// re-themes when the user's terminal palette changes.
func prGlyphColor(s PRStatus) string {
	if s.State == PRStateMerged {
		return colMuted
	}
	if s.State == PRStateClosed {
		return colMuted
	}
	switch s.CIState {
	case PRCIFailing:
		return colDanger
	case PRCIPending:
		return colWarning
	}
	if s.IsInMergeQueue && s.State == PRStateOpen {
		return colSuccess
	}
	if s.ReviewDecision == PRReviewApproved {
		return colSuccess
	}
	if s.IsDraft {
		return colMuted
	}
	return colAccent
}

// prStatusLabel returns a short human-readable phrase matching the glyph
// priority order. Mirrors prGlyphFor so the words shown in the details panel
// always agree with the glyph drawn in the row.
func prStatusLabel(s PRStatus) string {
	base := prStatusBaseLabel(s)
	suffix := prMergeStateSuffix(s)
	if base == "" {
		return ""
	}
	if suffix == "" {
		return base
	}
	return base + " · " + suffix
}

func prStatusBaseLabel(s PRStatus) string {
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
	if s.IsInMergeQueue && s.State == PRStateOpen {
		return "in merge queue"
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

// prMergeStateSuffix returns a short phrase for the merge-state-status signal
// (behind base or merge conflicts), or "" if the PR is mergeable as-is or in
// a terminal state. Merged/closed PRs never report merge state — there's
// nothing to update.
func prMergeStateSuffix(s PRStatus) string {
	if s.State != PRStateOpen {
		return ""
	}
	switch s.MergeStateStatus {
	case PRMergeStateBehind:
		return "behind base"
	case PRMergeStateDirty:
		return "merge conflicts"
	}
	return ""
}

// prStaleSuffix returns "stale" when the workspace's local commit-id
// differs from the PR's head SHA — the signal that the user's view (or
// their last approval / changes-requested decision) is out of date and a
// fresh re-review pass is warranted. Returns "" when either id is
// unavailable (can't compare) or the PR is no longer open.
func prStaleSuffix(s PRStatus, localCommitID string) string {
	if s.State != PRStateOpen {
		return ""
	}
	if s.HeadRefOid == "" || localCommitID == "" {
		return ""
	}
	if s.HeadRefOid == localCommitID {
		return ""
	}
	return "stale"
}

// prRepairPrompt builds an agent prompt asking the workspace's agent to
// address the PR's actionable problems — merge conflicts, failing CI,
// an out-of-date base branch, a reviewer's changes-requested feedback,
// a pending request for the user's review (someone else's PR), or a
// local copy that's behind origin (stale). Returns "" when the PR is
// healthy or in a terminal (merged/closed) state. When multiple issues
// apply, every one is listed in a single prompt.
//
// `mine` toggles the tone: when true, the prompt asks the agent to
// fix and push (the PR is yours; you intend to ship it). When false,
// the prompt is review-mode — investigate the issue and report back
// in chat, but DO NOT modify files, run jj/git mutations on the
// branch, or push. Reviewing someone else's PR with `p r` shouldn't
// trigger the agent to start rebasing or fixing their CI.
//
// localCommitID is the workspace's local bookmark tip; when non-empty
// and different from s.HeadRefOid (and the PR is open), a "stale" issue
// is added.
func prRepairPrompt(s PRStatus, localCommitID string, mine bool) string {
	if s.State != PRStateOpen {
		return ""
	}
	type issue struct {
		label string
		fix   string // owner action — used when mine == true
		look  string // review action — used when mine == false
	}
	var issues []issue
	if s.MergeStateStatus == PRMergeStateDirty {
		issues = append(issues, issue{
			label: "merge conflicts against its base branch",
			fix:   "resolve the conflicts on this branch (rebase or merge the base in)",
			look:  "identify which files conflict and a one-line summary of why (e.g. both sides changed the same function)",
		})
	}
	if s.CIState == PRCIFailing {
		issues = append(issues, issue{
			label: "failing CI checks",
			fix:   "diagnose the failing checks (e.g. `gh run list`, `gh run view`) and fix the underlying issues",
			look:  "diagnose the failing checks via `gh run list` / `gh run view` and summarize the root cause",
		})
	}
	if s.MergeStateStatus == PRMergeStateBehind {
		issues = append(issues, issue{
			label: "an out-of-date base branch",
			fix:   "update this branch with the latest base",
			look:  "note how far behind the base branch this PR is (e.g. `git log --oneline <base>..@`) so the user can flag it",
		})
	}
	// Same gate as prReviewReqGlyph's owner branch so the chat glyph and
	// the `p r` repair prompt never disagree: formal changes-requested OR
	// plain review comments (which leave reviewDecision at REVIEW_REQUIRED),
	// dropped once the PR is approved. The label adapts because "changes
	// requested" misreads a COMMENTED review; the fix/look text is the
	// same — read the feedback, address it, and (owner tone) re-request
	// review once the comments are addressed.
	if s.ReviewDecision != PRReviewApproved &&
		(s.ReviewDecision == PRReviewChangesRequested || s.HasReviewComments) {
		label := "review comments from a reviewer"
		if s.ReviewDecision == PRReviewChangesRequested {
			label = "changes requested by a reviewer"
		}
		issues = append(issues, issue{
			label: label,
			fix:   "read the review feedback (`gh pr view --comments`; `gh api repos/{owner}/{repo}/pulls/{n}/comments` for inline threads), address each point, push, and re-request review from the reviewer who left it",
			look:  "summarize what the reviewers asked for and which points look unaddressed at the current head",
		})
	}
	if !mine && s.ReviewRequested {
		// The author wants this user's review. Review-tone only: a
		// request for YOUR review can't sit on your own PR, so when the
		// bookmark heuristic says mine the signal is noise. Re-requests
		// (you reviewed, they pushed and asked again) get delta-focused
		// instructions instead of a from-scratch pass.
		// Prefer a local read over `gh pr diff`: `jj git fetch` + parking
		// the working copy on the PR head lets the agent open files at
		// the right revision, chase context, and run tests — a raw patch
		// allows none of that. `gh pr diff` stays as the fallback for
		// heads that aren't fetchable locally (fork PRs).
		localRead := "prefer a local read: `jj git fetch`, then `jj new " + s.HeadRefName + "@origin` to park the working copy on the PR head (this doesn't touch the branch itself); fall back to `gh pr diff` if the head isn't fetchable locally"
		if s.ReviewRerequested {
			issues = append(issues, issue{
				label: "a RE-request for your review — you reviewed before and the author asked again",
				fix:   "re-review the changes since your last pass and report your findings",
				look:  "re-read your earlier feedback (`gh pr view --comments`), check whether each point was addressed at the current head, review what changed since your last pass, and report findings in chat — " + localRead,
			})
		} else {
			issues = append(issues, issue{
				label: "a pending request for your review",
				fix:   "review the changes and report your findings",
				look:  "review the changes and report findings in chat — " + localRead,
			})
		}
	}
	if prStaleSuffix(s, localCommitID) != "" {
		// Stale is a property of the LOCAL working copy, not the PR
		// itself — fetching + re-anchoring is safe regardless of
		// ownership. Kept on both branches.
		issues = append(issues, issue{
			label: "new commits on origin that aren't in your local copy of this branch",
			fix:   "run `jj git fetch` to pick them up, then align this workspace's working copy to the new origin tip (e.g. `jj new " + s.HeadRefName + "@origin`) so subsequent work builds on the latest",
			look:  "run `jj git fetch` to pick them up, then align this workspace's working copy to the new origin tip (e.g. `jj new " + s.HeadRefName + "@origin`) so you're reading the latest version",
		})
	}
	if len(issues) == 0 {
		return ""
	}
	ref := "this PR"
	if s.Number > 0 {
		ref = fmt.Sprintf("PR #%d", s.Number)
	}
	if u := strings.TrimSpace(s.URL); u != "" {
		ref += " (" + u + ")"
	}
	if !mine {
		// Review tone: investigate + summarize, do not mutate.
		if len(issues) == 1 {
			return fmt.Sprintf("%s has %s. You are reviewing this PR — the author is not you. Do NOT modify files, run jj/git mutations on the branch, or push. Please %s, and report back in chat.", ref, issues[0].label, issues[0].look)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s has multiple issues:\n", ref)
		for _, it := range issues {
			fmt.Fprintf(&b, "- %s — please %s.\n", it.label, it.look)
		}
		b.WriteString("You are reviewing this PR — the author is not you. Do NOT modify files, run jj/git mutations on the branch, or push. Report what you find in chat.")
		return b.String()
	}
	// Owner tone: any of these fixes pushes new commits, which under
	// branch protection ("dismiss stale reviews on push") can drop an
	// existing approval. Remind the agent to re-request review once the
	// reviewer's comments are addressed so the PR isn't left silently
	// blocked. Harmless when there was no review to dismiss.
	reReview := " If pushing new commits dismisses an existing review — or the change addresses a reviewer's comments — re-request review from the affected reviewer(s) once their feedback is addressed so the PR isn't left blocked."
	if len(issues) == 1 {
		return fmt.Sprintf("%s has %s. Please %s, then push the fix.%s", ref, issues[0].label, issues[0].fix, reReview)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s has multiple issues to address:\n", ref)
	for _, it := range issues {
		fmt.Fprintf(&b, "- %s — please %s.\n", it.label, it.fix)
	}
	b.WriteString("Push the fixes when done.")
	b.WriteString(reReview)
	return b.String()
}

// itemIsMyPR reports whether the workspace's PR appears to be authored
// by the current user, using the configured bookmark prefix as the
// signal: bookmarks under `<prefix>/...` are the user's by convention,
// bookmarks under any other namespace (e.g. `coworker/foo`,
// `teammate/bar`) are someone else's. Returns true when the prefix is
// unconfigured — preserving the historical "always treat as mine"
// repair behavior for users who haven't opted in.
func itemIsMyPR(item Item, bookmarkPrefix string) bool {
	prefix := strings.TrimSpace(bookmarkPrefix)
	if prefix == "" {
		return true
	}
	bm := strings.TrimSpace(item.Bookmark)
	if bm == "" {
		return true
	}
	return strings.HasPrefix(bm, prefix+"/")
}

// prStaleGlyph returns the glyph to render alongside the primary PR glyph
// when the PR is out of date with its base branch. Empty when up-to-date or
// when the PR isn't open. BEHIND only fires on repos whose branch protection
// requires up-to-date branches; otherwise an out-of-date PR reads as CLEAN.
func prStaleGlyph(s PRStatus) string {
	if s.State != PRStateOpen {
		return ""
	}
	switch s.MergeStateStatus {
	case PRMergeStateBehind:
		return prGlyphBehind
	case PRMergeStateDirty:
		return prGlyphDirty
	}
	return ""
}

// prStaleGlyphColor picks a palette entry matching the stale state. Behind →
// amber (attention but recoverable). Dirty → red (blocked, needs manual fix).
func prStaleGlyphColor(s PRStatus) string {
	if s.MergeStateStatus == PRMergeStateDirty {
		return "203"
	}
	return "214"
}

// prLocalStaleGlyph is the glyph form of prStaleSuffix: the workspace's
// local bookmark tip differs from the PR's head SHA, so the local copy
// (or the user's last review decision) is out of date and a fresh look
// is warranted. Empty when the ids match, either id is unavailable, or
// the PR is no longer open.
func prLocalStaleGlyph(s PRStatus, localCommitID string) string {
	if prStaleSuffix(s, localCommitID) == "" {
		return ""
	}
	return prGlyphStale
}

// prReviewReqGlyph returns the review-conversation glyph, split by
// whose move it is:
//   - YOUR PR + a reviewer left feedback (formal "request changes" OR
//     plain COMMENTED review) → filled chat bubble (feedback exists; the
//     ball is in your court). gh's reviewDecision only catches the
//     formal case, so HasReviewComments covers the comments that leave
//     the verdict at REVIEW_REQUIRED. Suppressed once the PR is APPROVED
//     — feedback on an approved PR is no longer the blocker.
//   - Someone else's PR + your review is requested (or re-requested —
//     GitHub re-adds you to reviewRequests either way) → outline chat
//     bubble (they want your eyes).
//
// Empty when neither applies, the PR is no longer open, or the viewer
// login was unknown at fetch time (both Mine and ReviewRequested stay
// false).
func prReviewReqGlyph(s PRStatus) string {
	if s.State != PRStateOpen {
		return ""
	}
	if s.Mine && s.ReviewDecision != PRReviewApproved &&
		(s.ReviewDecision == PRReviewChangesRequested || s.HasReviewComments) {
		return prGlyphChangesReq
	}
	if !s.Mine && s.ReviewRequested {
		return prGlyphReviewReq
	}
	return ""
}

// prReviewReqGlyphColor: changes requested on your PR → yellow (act);
// review RE-requested on theirs → yellow too (the ball came back to
// you); a first review request → blue (informational).
func prReviewReqGlyphColor(s PRStatus) string {
	if s.Mine || s.ReviewRerequested {
		return colWarning
	}
	return colInfo
}

// resolvePRStatus is the one PR-lookup entry point. See
// deckdata.View.ResolvePRStatus.
func (m Model) resolvePRStatus(item Item) (PRStatus, bool) { return m.rm().ResolvePRStatus(item) }

// prStatusLabelForItem resolves the workspace's PR and returns the
// matched status plus a human-readable label. The label appends a
// "stale" suffix when the workspace's BookmarkCommitID (local bookmark
// tip) differs from the PR head SHA — the re-review hint for the row.
func (m Model) prStatusLabelForItem(item Item) (PRStatus, string, bool) {
	status, ok := m.resolvePRStatus(item)
	if !ok {
		return PRStatus{}, "", false
	}
	label := prStatusLabel(status)
	if label == "" {
		return PRStatus{}, "", false
	}
	if extra := prStaleSuffix(status, item.BookmarkCommitID); extra != "" {
		label += " · " + extra
	}
	return status, label, true
}

// prGlyphForItem returns the rendered PR glyph (with ANSI color) for this
// workspace, or "" when no PR is associated (or no cached status exists).
func (m Model) prGlyphForItem(item Item) string {
	status, ok := m.resolvePRStatus(item)
	if !ok {
		return ""
	}
	g := prGlyphFor(status)
	if g == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(prGlyphColor(status))).Render(g)
}

// prStaleGlyphForItem mirrors prGlyphForItem for the secondary
// "behind base" / "merge conflicts" glyph. Returns "" when the PR is
// up-to-date, no longer open, or has no cached status.
func (m Model) prStaleGlyphForItem(item Item) string {
	status, ok := m.resolvePRStatus(item)
	if !ok {
		return ""
	}
	g := prStaleGlyph(status)
	if g == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(prStaleGlyphColor(status))).Render(g)
}

// prLocalStaleGlyphForItem mirrors prStaleGlyphForItem for the "local
// copy is stale" signal — the row-level glyph twin of the meta line's
// "stale" word, which is easy to miss in the muted text.
func (m Model) prLocalStaleGlyphForItem(item Item) string {
	status, ok := m.resolvePRStatus(item)
	if !ok {
		return ""
	}
	g := prLocalStaleGlyph(status, item.BookmarkCommitID)
	if g == "" {
		return ""
	}
	return m.styles.Warning.Render(g)
}

// prReviewReqGlyphForItem mirrors the other per-item glyph helpers for
// the review-requested chat bubble.
func (m Model) prReviewReqGlyphForItem(item Item) string {
	status, ok := m.resolvePRStatus(item)
	if !ok {
		return ""
	}
	g := prReviewReqGlyph(status)
	if g == "" {
		return ""
	}
	if prReviewReqGlyphColor(status) == colWarning {
		return m.styles.Warning.Render(g)
	}
	return m.styles.Info.Render(g)
}

// prBlockedGlyphForItem badges a stacked PR that can't merge yet because
// an open ancestor in its stack isn't ready — the glyph-column twin of the
// tree connector, so "blocked on base" is visible without reading the
// chain. StackBlocked is set by the stack layout (View.Items).
func (m Model) prBlockedGlyphForItem(item Item) string {
	if !item.StackBlocked {
		return ""
	}
	return m.styles.Danger.Render(glyphBlocked)
}

// statusGlyph renders a colored ● for an agent status. Only "loud" states
// (working/in-progress/running) render a dot unconditionally — every other
// state requires unread=true. This makes the dot strictly an attention
// signal: when the user is viewing the session (or has summoned it since
// the last transition) report-status / the deck refresh clear Unread, and
// the row goes quiet regardless of whether the last hook to write was
// "waiting" or "idle" with stale data. "exited" never renders, even with a
// stale unread flag from an old state file — the agent is gone, so there's
// nothing for the user to act on.
func statusGlyph(status string, dim bool, unread bool) string {
	if !statusGlyphVisible(status, unread) {
		return " "
	}
	color := statusColor(status, dim, unread)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("●")
}

// statusGlyphVisible reports whether statusGlyph renders a dot for this
// status/unread combination. Shared with collapsedProjects so "has a
// dot" and "stays uncollapsed" can never disagree.
func statusGlyphVisible(status string, unread bool) bool {
	if strings.EqualFold(strings.TrimSpace(status), "exited") {
		return false
	}
	return alwaysShownStatus(status) || unread
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
	if dim {
		return colMuted
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return colSuccess
	case "waiting":
		return colWarning
	case "error":
		return colDanger
	case "starting":
		return colAccent
	default: // idle / done / unknown — only rendered when unread (notified)
		return colMuted
	}
}

func renderFindHint(hint string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(colWarning)).
		Render("[" + hint + "]")
}

// renderDeckHint renders an easymotion hint for the deck body's slim
// 2-col prefix slot: the hint characters alone, bold warning, no
// brackets (a two-char hint fills the slot exactly). The mini-deck
// keeps the bracketed renderFindHint form in its wider 4-col slot.
func renderDeckHint(hint string) string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(colWarning)).
		Render(hint)
}

// findHintStep advances one keystroke through an easymotion lookup
// table. It returns (destination, true) when r — or the previously
// pending prefix combined with r — completes a known hint, and
// otherwise sets *pending when r is a registered first-of-two-key
// prefix. Shared between every easymotion surface (mini-deck flat,
// deck's project stage, deck's workspace stage) so the runtime
// behavior can't drift across them.
func findHintStep[T any](r rune, lookup map[string]T, prefix map[rune]bool, pending *rune) (T, bool) {
	var zero T
	if *pending != 0 {
		hint := string(*pending) + string(r)
		*pending = 0
		v, ok := lookup[hint]
		if !ok {
			return zero, false
		}
		return v, true
	}
	if v, ok := lookup[string(r)]; ok {
		return v, true
	}
	if prefix[r] {
		*pending = r
	}
	return zero, false
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

// fitRow clamps a composed deck body line to exactly one terminal row of
// width w. The deck list is a single full-width column with plenty of
// horizontal room, so a long title must truncate, never wrap: lipgloss
// .Width wraps content wider than w onto extra rows (which also breaks
// the deck's one-body-element-per-rendered-row scroll math), so
// .MaxHeight(1) clips any wrap back to a single row. Embedded newlines
// are collapsed to spaces first since they'd force a wrap regardless of
// width. Padding to w is preserved so the viewport repaints the full row.
func fitRow(s string, w int) string {
	if strings.ContainsAny(s, "\r\n") {
		s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	}
	return lipgloss.NewStyle().Width(w).MaxHeight(1).Render(s)
}
