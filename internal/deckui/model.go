package deckui

import (
	"fmt"
	"io"
	"net/url"
	"sort"
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

type Item struct {
	ProjectName      string
	WorkspaceName    string
	Path             string
	RepoRoot         string
	Bookmark         string // jj bookmark associated with this workspace (still used for the new-workspace form, not for PR lookup)
	PRNumber         int    // PR this workspace is associated with; when > 0, used to resolve PR status
	Status           string
	Unread           bool
	PromptPreview    string
	HeadDesc         string
	HeadChangeID     string // jj short change-id of the working-copy commit
	BookmarkCommitID string // full hex commit-id of the workspace's local bookmark; compared to PR head SHA on GitHub to detect "behind remote" / re-review
	TmuxWindow       string
	SessionName      string
	Active           bool
	Current          bool
	// Virtual marks a synthetic inbox row that has no local workspace —
	// an open PR you haven't pulled down yet, either awaiting your review
	// (inboxVirtualReviewItems) or your own (inboxVirtualMineItems). It
	// resolves PR status via PRNumber and renders read-only: enter starts
	// the review flow for a review-requested PR, or opens the prefilled
	// new-workspace form for your own; other workspace actions are no-ops.
	Virtual bool
	// PinGroup is the register this workspace is pinned to (from
	// Entry.PinGroup): "" unpinned, "default" the gg register, or a
	// single lowercase letter a–z. Pinned rows float to a section at the
	// top of the deck in the All/Attention scopes.
	PinGroup string
}

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

// resetBookmarkList clears items, cursor, and filter on the picker's
// list.Model. Called from every close/cancel path so the picker reopens
// in a clean state.
func resetBookmarkList(l *list.Model) {
	l.SetItems(nil)
	l.ResetFilter()
	l.ResetSelected()
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

func resetReviewList(l *list.Model) {
	l.SetItems(nil)
	l.ResetFilter()
	l.ResetSelected()
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

func resetOpenList(l *list.Model) {
	l.SetItems(nil)
	l.ResetFilter()
	l.ResetSelected()
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
// workspace via the `g` chord. group == "" unpins; "default" is the
// gg register; otherwise a single lowercase letter a–z. The handler
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

// Scope controls which items are shown in the deck list. Cycled with `P`;
// not persisted unless an initial scope is supplied via `awp deck --scope`.
// Declaration order is the cycle order (all → attention → inbox → all).
type Scope int

const (
	ScopeAll       Scope = iota // every known workspace across all projects
	ScopeAttention              // matches the mini-deck filter: active agent or unread notification
	ScopeInbox                  // open-PR workspaces sectioned by next-move bucket (GitHub-inbox style)
)

const scopeCount = 3

// ParseScope maps the user-facing names accepted by `awp deck --scope`
// onto Scope values. Names are matched case-insensitively; hyphens and
// spaces are interchangeable. `pr` and the legacy `open-pr` are accepted
// as aliases for `inbox`.
func ParseScope(s string) (Scope, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, " ", "-"))) {
	case "all":
		return ScopeAll, true
	case "attention":
		return ScopeAttention, true
	case "inbox", "pr", "open-pr":
		return ScopeInbox, true
	}
	return ScopeAll, false
}

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

// PRMergeStateStatus mirrors gh's mergeStateStatus. BEHIND only surfaces when
// the repo's branch protection requires up-to-date branches; otherwise an
// out-of-date PR is reported as CLEAN.
type PRMergeStateStatus string

const (
	PRMergeStateBehind   PRMergeStateStatus = "BEHIND"
	PRMergeStateBlocked  PRMergeStateStatus = "BLOCKED"
	PRMergeStateClean    PRMergeStateStatus = "CLEAN"
	PRMergeStateDirty    PRMergeStateStatus = "DIRTY"
	PRMergeStateDraft    PRMergeStateStatus = "DRAFT"
	PRMergeStateHasHooks PRMergeStateStatus = "HAS_HOOKS"
	PRMergeStateUnknown  PRMergeStateStatus = "UNKNOWN"
	PRMergeStateUnstable PRMergeStateStatus = "UNSTABLE"
)

// PRStatus is the per-PR projection consumed by the row glyph.
// HeadRefOid is the head commit SHA at the time the PR sync ran; callers
// compare it against a local commit-id to detect "PR has moved since I
// last looked," which feeds the re-review signal.
type PRStatus struct {
	Number           int
	HeadRefName      string
	HeadRefOid       string
	Title            string
	Author           string
	URL              string
	State            PRState
	IsDraft          bool
	IsInMergeQueue   bool
	ReviewDecision   PRReviewDecision
	CIState          PRCIState
	MergeStateStatus PRMergeStateStatus
	// ReviewRequested: the deck owner's review is currently requested on
	// this PR. ReviewRerequested narrows it: they already reviewed and
	// the author asked again. Mine: the deck owner authored this PR.
	// All three are computed against the gh viewer login at fetch time
	// (cli/pr_status_projection.go), so the cache stays viewer-agnostic
	// at render time.
	ReviewRequested   bool
	ReviewRerequested bool
	Mine              bool
	// HasReviewComments: a reviewer left COMMENTED or CHANGES_REQUESTED
	// feedback on this PR. Distinct from ReviewDecision — plain review
	// comments never flip GitHub's branch-protection verdict off
	// REVIEW_REQUIRED, so this is the only signal that catches "someone
	// gave you feedback to look at" on your own PR.
	HasReviewComments bool
}

// inboxBucket sections the inbox scope the way GitHub's pull-request
// inbox does: by what the deck owner's next move is. Declaration order
// is render order — most urgent next-move first.
type inboxBucket int

const (
	inboxNeedsYourReview inboxBucket = iota // someone else's PR, your review is the blocker
	inboxNeedsAction                        // your PR, something to fix (feedback, CI, conflicts)
	inboxReadyToMerge                       // your PR, approved + green — go press the button
	inboxOtherOpen                          // open PR that's neither yours nor awaiting you
	inboxMine                               // your PR, ball in someone else's court — waiting for review or still a draft
	inboxBucketCount
)

func inboxBucketLabel(b inboxBucket) string {
	switch b {
	case inboxNeedsYourReview:
		return "Needs your review"
	case inboxNeedsAction:
		return "Needs action"
	case inboxReadyToMerge:
		return "Ready to merge"
	case inboxOtherOpen:
		return "Other open PRs"
	default:
		return "Mine"
	}
}

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

// bucketFromHeaderLabel recovers the bucket from a header label like
// "Needs action (2)" — the count suffix is stripped and the base
// matched against inboxBucketLabel. Both sides go through the same
// label function, so the round-trip is exact.
func bucketFromHeaderLabel(label string) (inboxBucket, bool) {
	base := label
	if i := strings.LastIndex(label, " ("); i >= 0 {
		base = label[:i]
	}
	for b := inboxBucket(0); b < inboxBucketCount; b++ {
		if inboxBucketLabel(b) == base {
			return b, true
		}
	}
	return 0, false
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
const pinGroupDefault = "default"

// pinGroupLabel is the display label for a register: its alias when one
// is set, otherwise "pinned" for the default register or the bare
// letter for a lettered register.
func (m Model) pinGroupLabel(key string) string {
	if alias := strings.TrimSpace(m.pinGroupAliases[key]); alias != "" {
		return alias
	}
	if key == pinGroupDefault {
		return "pinned"
	}
	return key
}

// pinGroupChordLetter is the keystroke that targets a register in the
// `g` chord — "g" for the default register (gg), the letter otherwise.
// Shown as an emphasized [x] chip in the section header while the chord
// is pending.
func pinGroupChordLetter(key string) string {
	if key == pinGroupDefault {
		return "g"
	}
	return key
}

// pinGroupSortKey orders registers: the default register first, then
// the rest case-insensitively by display label (alias or letter).
func (m Model) pinGroupSortKey(key string) string {
	if key == pinGroupDefault {
		return "\x00"
	}
	return "\x01" + strings.ToLower(m.pinGroupLabel(key))
}

// pinnedCount returns how many leading items in a pinned-first ordering
// carry a register. items() sorts pinned rows ahead of unpinned ones in
// the all / attention scopes, so this is the length of that prefix.
func pinnedCount(items []Item) int {
	n := 0
	for _, it := range items {
		if strings.TrimSpace(it.PinGroup) != "" {
			n++
		}
	}
	return n
}

// prInboxBucket classifies an OPEN PR into its inbox section. Callers
// filter merged/closed PRs out of the inbox scope before classifying.
//
// Precedence, locked by tests: a review request always wins (it names
// you regardless of the PR's own state); within your own PRs the draft
// check precedes CI/decision checks — a draft isn't submitted for
// review yet, so its CI state is informational, not actionable, and it
// belongs in the bottom "Mine" pile rather than "Needs action".
// Anything of yours that isn't broken, ready, or a draft (i.e. waiting
// on reviewers) also lands in "Mine".
func prInboxBucket(s PRStatus) inboxBucket {
	if s.ReviewRequested || s.ReviewRerequested {
		return inboxNeedsYourReview
	}
	if !s.Mine {
		return inboxOtherOpen
	}
	if s.IsDraft {
		return inboxMine
	}
	if s.ReviewDecision == PRReviewChangesRequested ||
		s.CIState == PRCIFailing ||
		s.MergeStateStatus == PRMergeStateDirty ||
		s.MergeStateStatus == PRMergeStateBehind {
		return inboxNeedsAction
	}
	if s.IsInMergeQueue ||
		(s.ReviewDecision == PRReviewApproved &&
			(s.CIState == PRCIPassing || s.CIState == PRCINone) &&
			s.MergeStateStatus == PRMergeStateClean) {
		return inboxReadyToMerge
	}
	return inboxMine
}

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
	deckViewport      viewport.Model
	deckYOffset       int
	width             int
	height            int
	status            string
	handler           Handler
	filterInput       textinput.Model
	filtering         bool
	filter            string
	confirmDelete     bool
	deleteIsProject   bool // confirmDelete branch: project-level delete (typed confirmation)
	deleteInput       textinput.Model
	deleteErr         string
	helpMode          bool
	deleteTarget      Item
	confirmMergePR    bool     // merge-PR confirmation modal active
	mergeTarget       Item     // workspace whose PR the modal will merge
	mergeStatus       PRStatus // cached PR status shown in the modal (number/title)
	pendingSelect     Item     // after next refresh, cursor jumps to this (project, workspace) if present
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
	refreshing        bool // true while a m.refresher() command is in flight
	refreshPending    bool // a change signal arrived mid-refresh; re-run on completion
	hookInstaller     HookInstaller
	stateWatcher      StateChangeWatcher
	prFetcher         PRFetcher
	prStatusFetcher   PRStatusFetcher
	prStatusByRepo    map[string]map[string]PRStatus // repoRoot → headRefName → status
	prStatusFetchedAt map[string]time.Time           // repoRoot → wall clock of last successful fetch
	bookmarkFetcher   BookmarkFetcher
	trunkResolver     TrunkResolver
	stateEditor       StateEditorLauncher
	reviewMode        bool
	reviewLoading     bool
	reviewList        list.Model
	reviewDelegate    *reviewItemDelegate
	prMenuMode        bool
	// prNumberSetMode is true while the `p s` chord's numeric input
	// modal is open. prNumberInput / prNumberTarget back the modal.
	prNumberSetMode     bool
	prNumberInput       textinput.Model
	prNumberTarget      Item
	prNumberErr         string
	prNumberLinkHandler PRNumberLinkHandler
	// gChordMode is true after the user presses `g` and before the
	// second key of the pin chord (gg / g<letter> / gD / gR) arrives.
	// While pending, renderList highlights the register letter in each
	// pinned section header so the user can see which registers are in
	// use before choosing one.
	gChordMode bool
	// pinAliasMode is true while the `gR` alias-rename text input is
	// open. pinAliasInput / pinAliasTarget back the modal; pinAliasErr
	// surfaces validation.
	pinAliasMode         bool
	pinAliasInput        textinput.Model
	pinAliasTarget       string // register key being renamed
	pinAliasErr          string
	pinGroupHandler      PinGroupHandler
	pinGroupAliasHandler PinGroupAliasHandler
	pinGroupAliases      map[string]string // register key → display alias
	bookmarkMode         bool
	bookmarkLoading     bool
	bookmarkList        list.Model
	bookmarkPurpose     bookmarkPurpose
	bookmarkLinkTarget  Item
	bookmarkLinkHandler BookmarkLinkHandler
	// bookmarkPrefix mirrors config.Deck.BookmarkPrefix. When non-empty
	// and a bookmark picked for the new-workspace flow begins with
	// "<prefix>/", the form's workspace-name field is pre-filled with the
	// stripped tail so the user gets a clean default ("andrew/foo" → "foo").
	bookmarkPrefix          string
	userActions             []UserAction
	userActionsResolver     UserActionsResolver
	actionMode              bool
	actionMenuActions       []UserAction
	actionAliasLookup       map[string]UserAction
	spinner                 spinner.Model
	busy                    bool
	progressViewport        viewport.Model
	progressMode            bool
	progressTitle           string
	progressSteps           []ProgressStep
	progressLog             []string
	progressErr             error
	progressDone            bool
	progressDoneAction      Action
	progressChan            chan progressEvent
	openMode                bool
	openLoading             bool
	openList                list.Model
	projectFinder           ProjectFinder
	projectOpener           ProjectOpener
	asyncJobLauncher        AsyncJobLauncher
	jobsListRefresher       JobsListRefresher
	jobCancelHandler        JobCancelHandler
	jobDismissHandler       JobDismissHandler
	jobLogOpener            JobLogOpener
	jobRetryHandler         JobRetryHandler
	jobDeleteWorkspaceRetry JobDeleteWorkspaceRetryHandler
	jobs                    []Job
	jobsOverlay             bool
	jobsList                list.Model
	jobsViewport            viewport.Model

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
	devURLs          map[string]string
	devURLDiscoverer DevURLDiscoverer
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
	reviewList, reviewDelegate := newReviewList()
	m := Model{
		itemsAll:          append([]Item(nil), items...),
		scope:             ScopeAll,
		findProjectHints:  map[string]string{},
		findProjectLookup: map[string]string{},
		findProjectPrefix: map[rune]bool{},
		findRowHints:      map[int]string{},
		findRowLookup:     map[string]int{},
		findRowPrefix:     map[rune]bool{},
		handler:           handler,
		filterInput:       fi,
		bookmarkList:      newBookmarkList(),
		reviewList:        reviewList,
		reviewDelegate:    reviewDelegate,
		openList:          newOpenList(),
		jobsList:          newJobsList(),
		jobsViewport:      newJobsViewport(),
		deckViewport:      newDeckViewport(),
		progressViewport:  newProgressViewport(),
		keymap:            newDeckKeyMap(),
		styles:            newDeckStyles(),
		spinner:           sp,
	}
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

func (m Model) items() []Item {
	src := m.itemsAll
	switch m.scope {
	case ScopeInbox:
		filtered := make([]Item, 0, len(src))
		for _, it := range src {
			if _, ok := m.itemOpenPRStatus(it); ok {
				filtered = append(filtered, it)
			}
		}
		// Surface review-requested PRs you haven't checked out yet as
		// synthetic read-only rows, so "Needs your review" isn't limited
		// to PRs that already have a local workspace.
		filtered = append(filtered, m.inboxVirtualReviewItems(filtered)...)
		// Likewise surface your own open PRs that have no local workspace
		// so the Mine / Needs action / Ready to merge buckets aren't
		// limited to PRs you happen to have checked out. Passing the
		// review virtuals in too dedups against them.
		filtered = append(filtered, m.inboxVirtualMineItems(filtered)...)
		src = filtered
	case ScopeAttention:
		filtered := make([]Item, 0, len(src))
		for _, it := range src {
			if AttentionIncluded(it.Status, it.Unread, it.Active) {
				filtered = append(filtered, it)
			}
		}
		src = filtered
	}
	f := strings.ToLower(strings.TrimSpace(m.filter))
	if f != "" {
		out := make([]Item, 0, len(src))
		for _, it := range src {
			if strings.Contains(strings.ToLower(it.WorkspaceName), f) ||
				strings.Contains(strings.ToLower(it.ProjectName), f) ||
				strings.Contains(strings.ToLower(m.displayLabel(it)), f) {
				out = append(out, it)
			}
		}
		src = out
	}
	// Sort by (project, displayed label) so rows alphabetize by what the
	// user actually sees — PR title when one is resolved from the cache,
	// workspace name otherwise. Stable sort preserves the upstream
	// ordering for ties. The inbox scope sorts by bucket first so rows
	// section under the bucket headers in next-move order.
	sorted := append([]Item(nil), src...)
	byProjectLabel := func(i, j int) bool {
		if sorted[i].ProjectName != sorted[j].ProjectName {
			return sorted[i].ProjectName < sorted[j].ProjectName
		}
		return strings.ToLower(m.displayLabel(sorted[i])) < strings.ToLower(m.displayLabel(sorted[j]))
	}
	if m.scope == ScopeInbox {
		sort.SliceStable(sorted, func(i, j int) bool {
			bi, bj := m.itemInboxBucket(sorted[i]), m.itemInboxBucket(sorted[j])
			if bi != bj {
				return bi < bj
			}
			// Within "Needs your review", surface re-reviews first — PRs
			// you already reviewed that the author pushed to and
			// re-requested. They're cheaper to act on (you only need to
			// look at what changed) and easy to lose track of.
			if bi == inboxNeedsYourReview {
				ri, rj := m.itemNeedsReReview(sorted[i]), m.itemNeedsReReview(sorted[j])
				if ri != rj {
					return ri
				}
			}
			return byProjectLabel(i, j)
		})
	} else {
		// All / attention scopes float pinned rows to the top, ordered by
		// register (default first, then alphabetical by alias-or-letter),
		// then by label within a register. Unpinned rows keep the
		// (project, label) ordering. bodyRows relies on this pinned-first
		// prefix to section the pinned region.
		sort.SliceStable(sorted, func(i, j int) bool {
			pi := strings.TrimSpace(sorted[i].PinGroup) != ""
			pj := strings.TrimSpace(sorted[j].PinGroup) != ""
			if pi != pj {
				return pi
			}
			if pi {
				ki, kj := m.pinGroupSortKey(sorted[i].PinGroup), m.pinGroupSortKey(sorted[j].PinGroup)
				if ki != kj {
					return ki < kj
				}
				return strings.ToLower(m.displayLabel(sorted[i])) < strings.ToLower(m.displayLabel(sorted[j]))
			}
			return byProjectLabel(i, j)
		})
	}
	return sorted
}

// itemInboxBucket classifies a workspace for the inbox scope. Items the
// scope filter would exclude (no open PR resolvable) land in the
// catch-all bucket; the filter runs first, so that's defensive only.
func (m Model) itemInboxBucket(it Item) inboxBucket {
	st, ok := m.itemOpenPRStatus(it)
	if !ok {
		return inboxOtherOpen
	}
	return prInboxBucket(st)
}

// itemNeedsReReview reports whether the row is a re-request: you
// reviewed the PR before and the author pushed and asked again. Used to
// sort these to the top of the "Needs your review" bucket.
func (m Model) itemNeedsReReview(it Item) bool {
	st, ok := m.itemOpenPRStatus(it)
	return ok && st.ReviewRerequested
}

// inboxVirtualReviewItems synthesizes read-only inbox rows for
// review-requested PRs that have no local workspace, so "Needs your
// review" surfaces PRs you haven't pulled down yet. The PR status cache
// only holds repos where you already have at least one workspace, so a
// virtual row is always a not-yet-checked-out PR in a repo you work in;
// its project name is borrowed from a sibling workspace in that repo.
//
// real is the inbox scope's already-filtered workspace rows; PRs they
// resolve to are skipped so a checked-out PR never doubles up. Each
// virtual Item resolves its status via PRNumber (no bookmark on file)
// and carries the PR head ref so the meta line can show the branch.
func (m Model) inboxVirtualReviewItems(real []Item) []Item {
	// PRs already represented by a real workspace row, by repo → PR#.
	seen := map[string]map[int]bool{}
	for _, it := range real {
		if st, ok := m.resolvePRStatus(it); ok {
			if seen[it.RepoRoot] == nil {
				seen[it.RepoRoot] = map[int]bool{}
			}
			seen[it.RepoRoot][st.Number] = true
		}
	}
	projectByRepo := map[string]string{}
	for _, it := range m.itemsAll {
		if it.RepoRoot != "" && projectByRepo[it.RepoRoot] == "" {
			projectByRepo[it.RepoRoot] = it.ProjectName
		}
	}
	var out []Item
	for repo, byHead := range m.prStatusByRepo {
		for _, st := range byHead {
			if st.State != PRStateOpen {
				continue
			}
			if !st.ReviewRequested && !st.ReviewRerequested {
				continue
			}
			if seen[repo][st.Number] {
				continue
			}
			project := projectByRepo[repo]
			if project == "" {
				project = repoBaseName(repo)
			}
			out = append(out, Item{
				ProjectName:   project,
				WorkspaceName: fmt.Sprintf("#%d", st.Number),
				RepoRoot:      repo,
				PRNumber:      st.Number,
				Bookmark:      st.HeadRefName, // drives the branch token on the meta line
				Virtual:       true,
			})
		}
	}
	return out
}

// inboxVirtualMineItems synthesizes read-only inbox rows for your own
// open PRs that have no local workspace yet — the authored-by-you
// counterpart to inboxVirtualReviewItems. Without it, the Mine / Needs
// action / Ready to merge buckets only show PRs you happen to have
// checked out; a PR you opened from another machine (or whose workspace
// you deleted) would silently vanish from your inbox.
//
// Review-requested PRs are intentionally skipped here — inboxVirtualReviewItems
// already covers them (you can't request review from yourself, so this is
// belt-and-suspenders). prInboxBucket later sorts each row into its
// section by PR state. existing should be the real workspace rows plus
// the review virtuals so we dedup against both, by repo → PR#.
func (m Model) inboxVirtualMineItems(existing []Item) []Item {
	seen := map[string]map[int]bool{}
	for _, it := range existing {
		if st, ok := m.resolvePRStatus(it); ok {
			if seen[it.RepoRoot] == nil {
				seen[it.RepoRoot] = map[int]bool{}
			}
			seen[it.RepoRoot][st.Number] = true
		}
	}
	projectByRepo := map[string]string{}
	for _, it := range m.itemsAll {
		if it.RepoRoot != "" && projectByRepo[it.RepoRoot] == "" {
			projectByRepo[it.RepoRoot] = it.ProjectName
		}
	}
	var out []Item
	for repo, byHead := range m.prStatusByRepo {
		for _, st := range byHead {
			if st.State != PRStateOpen {
				continue
			}
			if !st.Mine {
				continue
			}
			if st.ReviewRequested || st.ReviewRerequested {
				continue // covered by inboxVirtualReviewItems
			}
			if seen[repo][st.Number] {
				continue
			}
			project := projectByRepo[repo]
			if project == "" {
				project = repoBaseName(repo)
			}
			out = append(out, Item{
				ProjectName:   project,
				WorkspaceName: fmt.Sprintf("#%d", st.Number),
				RepoRoot:      repo,
				PRNumber:      st.Number,
				Bookmark:      st.HeadRefName, // drives the branch token on the meta line
				Virtual:       true,
			})
		}
	}
	return out
}

// repoBaseName returns the last path segment of a repo root, used as a
// fallback project label for a virtual row when no sibling workspace
// supplies one.
func repoBaseName(repo string) string {
	repo = strings.TrimRight(repo, "/")
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// displayLabel returns the text that renders on a row: "#N title" when a
// PR is resolvable from the cache, falling back to the workspace name.
func (m Model) displayLabel(it Item) string {
	if pr, ok := m.resolvePRStatus(it); ok {
		if t := strings.TrimSpace(pr.Title); t != "" {
			return fmt.Sprintf("#%d %s", pr.Number, t)
		}
	}
	return it.WorkspaceName
}

// Nerd Font glyphs used on the meta line. Both require a Nerd Font
// to render \u2014 they fall back to missing-glyph boxes otherwise.
const (
	glyphBranch   = "\uf418"     // nf-oct-git_branch
	glyphKeyboard = "\U000F030C" // nf-md-keyboard
	glyphReturn   = "\U000F0311" // nf-md-keyboard_return \u2014 leads the "to review" hint on virtual rows
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
	var parts []string
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
		// No local workspace — call out the one action that exists. For a
		// PR awaiting your review that's "to review"; for your own PR with
		// no workspace it's "to check out" (enter creates the workspace
		// either way — same flow, honest verb).
		hint := "to review"
		if hasPR && pr.Mine && !pr.ReviewRequested && !pr.ReviewRerequested {
			hint = "to check out"
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

// itemOpenPRStatus returns the cached status of the workspace's OPEN PR.
// Drafts count — the inbox scope sections them into "Your drafts" rather
// than hiding them. Resolution goes through resolvePRStatus, so a pinned
// PRNumber works even when no bookmark is on file. Used by ScopeInbox.
func (m Model) itemOpenPRStatus(it Item) (PRStatus, bool) {
	st, ok := m.resolvePRStatus(it)
	if !ok || st.State != PRStateOpen {
		return PRStatus{}, false
	}
	return st, true
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
		!m.confirmDelete && !m.filtering &&
		!m.findMode && !m.actionMode &&
		!m.bookmarkMode && !m.reviewMode &&
		!m.openMode && !m.helpMode && !m.newWorkspaceMode &&
		!m.prMenuMode && !m.prNumberSetMode && !m.pinAliasMode
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
	if m.newWorkspaceMode {
		return m.dispatchNewWorkspaceForm(msg)
	}
	if m.renameMode {
		return m.dispatchRenameForm(msg)
	}
	if m.promptMode {
		return m.dispatchPromptForm(msg)
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
		if m.bookmarkMode && m.bookmarkLoading {
			m.bookmarkList.SetItems([]list.Item{loadingItem{label: glyph + " loading bookmarks..."}})
		}
		if m.reviewMode && m.reviewLoading {
			m.reviewList.SetItems([]list.Item{loadingItem{label: glyph + " loading PRs..."}})
		}
		if m.openMode && m.openLoading {
			m.openList.SetItems([]list.Item{loadingItem{label: glyph + " scanning project roots..."}})
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
		m.jobs = msg.jobs
		m.syncJobsListItems()
		m.refreshJobsViewport()
		var expireCmd tea.Cmd
		m, expireCmd = m.syncJobActivities(msg.jobs)
		// Bootstrap the spinner whenever activities exist so its glyph
		// in the bottom bar actually animates. The spinner.TickMsg
		// handler self-perpetuates while len(m.activities) > 0; this
		// call is the kickstart when activities first appear from a
		// background refresh that wasn't preceded by a foreground
		// action (which would already batch a Tick).
		if len(m.activities) > 0 {
			return m, tea.Batch(expireCmd, m.spinner.Tick)
		}
		return m, expireCmd
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
		items := make([]list.Item, 0, len(msg.Bookmarks))
		for _, b := range msg.Bookmarks {
			items = append(items, bookmarkItem{name: b})
		}
		m.bookmarkList.SetShowStatusBar(true)
		m.bookmarkList.SetItems(items)
		m.bookmarkList.ResetSelected()
		m.bookmarkList.Title = bookmarkPickerTitle(m.bookmarkPurpose, m.bookmarkLinkTarget)
		// list.Model renders its own key-help footer (/, enter, esc);
		// duplicating it in the deck's status bar would show the same
		// hints twice.
		m.status = ""
		return m, nil
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
		items := make([]list.Item, 0, len(msg.Projects))
		for _, p := range msg.Projects {
			items = append(items, projectItem{project: p})
		}
		m.openList.SetShowStatusBar(true)
		m.openList.SetItems(items)
		m.openList.ResetSelected()
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
		if !m.reviewMode {
			return m, nil
		}
		m.reviewLoading = false
		if msg.Err != nil {
			m.reviewMode = false
			m.status = "review: " + msg.Err.Error()
			return m, nil
		}
		items := make([]list.Item, 0, len(msg.PRs))
		for _, pr := range msg.PRs {
			items = append(items, reviewItem{pr: pr})
		}
		m.reviewList.SetShowStatusBar(true)
		m.reviewList.SetItems(items)
		m.reviewList.ResetSelected()
		m.reviewDelegate.recompute(items)
		if len(msg.PRs) == 0 {
			m.status = "review: no open PRs (esc to cancel)"
		} else {
			// list.Model renders its own key-help footer; clear the
			// status so the picker's chrome isn't duplicated in the
			// deck's bottom bar.
			m.status = ""
		}
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
		if m.confirmDelete {
			if m.deleteIsProject {
				switch msg.String() {
				case "esc", "ctrl+c":
					m.confirmDelete = false
					m.deleteIsProject = false
					m.deleteInput.Blur()
					m.deleteInput.SetValue("")
					m.deleteErr = ""
					m.status = ""
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
				m.status = ""
				return m, nil
			}
			return m, nil
		}
		if m.confirmMergePR {
			switch strings.ToLower(msg.String()) {
			case "y", "enter":
				m.confirmMergePR = false
				if m.handler == nil {
					m.status = "merge: handler not configured"
					return m, nil
				}
				return m.startAction(ActionMergePR, m.mergeTarget, strconv.Itoa(m.mergeStatus.Number))
			case "n", "esc", "q":
				m.confirmMergePR = false
				m.status = ""
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
		if m.prNumberSetMode {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.prNumberSetMode = false
				m.prNumberInput.Blur()
				m.prNumberInput.SetValue("")
				m.prNumberErr = ""
				m.status = ""
				return m, tea.ClearScreen
			case "enter":
				typed := strings.TrimSpace(m.prNumberInput.Value())
				prNumber := 0
				if typed != "" {
					n, err := strconv.Atoi(typed)
					if err != nil || n < 0 {
						m.prNumberErr = "enter a non-negative integer (or blank to clear)"
						return m, nil
					}
					prNumber = n
				}
				if m.prNumberLinkHandler == nil {
					m.prNumberSetMode = false
					m.prNumberInput.Blur()
					m.prNumberInput.SetValue("")
					m.status = "pr: set PR # handler not configured"
					return m, tea.ClearScreen
				}
				target := m.prNumberTarget
				if err := m.prNumberLinkHandler(target, prNumber); err != nil {
					m.prNumberErr = err.Error()
					return m, nil
				}
				m.prNumberSetMode = false
				m.prNumberInput.Blur()
				m.prNumberInput.SetValue("")
				m.prNumberErr = ""
				if prNumber == 0 {
					m.status = fmt.Sprintf("pr: cleared PR # override on %s/%s", target.ProjectName, target.WorkspaceName)
				} else {
					m.status = fmt.Sprintf("pr: pinned %s/%s → PR #%d", target.ProjectName, target.WorkspaceName, prNumber)
				}
				// Force a PR-status refetch alongside the row refresh:
				// the override may point at a PR not in the cache yet
				// (cold start, stale cache, or the PR appeared after the
				// last gh poll). Bypassing the 60s throttle ensures the
				// pinned PR's status shows up on the next paint instead
				// of waiting up to a minute.
				var prCmd tea.Cmd
				m, prCmd = m.forcePRStatusRefresh(target.RepoRoot)
				var refreshCmd tea.Cmd
				m, refreshCmd = m.requestRefresh(true)
				return m, batchCmds(tea.ClearScreen, refreshCmd, prCmd)
			}
			var cmd tea.Cmd
			m.prNumberInput, cmd = m.prNumberInput.Update(msg)
			m.prNumberErr = ""
			return m, cmd
		}
		if m.pinAliasMode {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.pinAliasMode = false
				m.pinAliasInput.Blur()
				m.pinAliasInput.SetValue("")
				m.pinAliasErr = ""
				m.status = ""
				return m, tea.ClearScreen
			case "enter":
				alias := strings.TrimSpace(m.pinAliasInput.Value())
				key := m.pinAliasTarget
				if m.pinGroupAliasHandler != nil {
					if err := m.pinGroupAliasHandler(key, alias); err != nil {
						m.pinAliasErr = err.Error()
						return m, nil
					}
				}
				// Update the in-memory map so the section header re-renders
				// with the new label on the next paint without a reload.
				if m.pinGroupAliases == nil {
					m.pinGroupAliases = map[string]string{}
				}
				if alias == "" {
					delete(m.pinGroupAliases, key)
				} else {
					m.pinGroupAliases[key] = alias
				}
				m.pinAliasMode = false
				m.pinAliasInput.Blur()
				m.pinAliasInput.SetValue("")
				m.pinAliasErr = ""
				if alias == "" {
					m.status = fmt.Sprintf("pin: cleared name for group %s", pinGroupChordLetter(key))
				} else {
					m.status = fmt.Sprintf("pin: group %s → %s", pinGroupChordLetter(key), alias)
				}
				return m, tea.ClearScreen
			}
			var cmd tea.Cmd
			m.pinAliasInput, cmd = m.pinAliasInput.Update(msg)
			m.pinAliasErr = ""
			return m, cmd
		}
		if m.gChordMode {
			m.gChordMode = false
			switch msg.String() {
			case "esc", "ctrl+c":
				m.status = ""
				return m, nil
			case "g":
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
		if m.prMenuMode {
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				m.prMenuMode = false
				m.status = "pr: cancelled"
				return m, nil
			case "o":
				m.prMenuMode = false
				item, ok := m.selected()
				if !ok {
					return m, nil
				}
				status, _, ok := m.prStatusLabelForItem(item)
				if !ok {
					m.status = "pr: no PR for this workspace"
					return m, nil
				}
				url := strings.TrimSpace(status.URL)
				if url == "" {
					m.status = "pr: no URL on cached PR (re-open the deck to refresh)"
					return m, nil
				}
				if err := openBrowser(url); err != nil {
					m.status = "pr: " + err.Error()
				} else {
					m.status = "pr: opened " + url
				}
				return m, nil
			case "d":
				m.prMenuMode = false
				item, ok := m.selected()
				if !ok {
					return m, nil
				}
				status, _, ok := m.prStatusLabelForItem(item)
				if !ok {
					m.status = "pr: no PR for this workspace"
					return m, nil
				}
				if status.Number <= 0 {
					m.status = "pr: description unavailable (no PR number cached — try p s)"
					return m, nil
				}
				// Open the description the way a review opens: a dedicated
				// tmux window in the workspace's session. gh renders the
				// body with TTY formatting; less keeps it scrollable and
				// searchable, and q drops back to a shell in the window.
				winCmd := fmt.Sprintf("env GH_FORCE_TTY=100%% gh pr view %d | less -R", status.Number)
				return m.trigger(ActionOpenWindow, "pr:"+winCmd)
			case "r":
				m.prMenuMode = false
				item, ok := m.selected()
				if !ok {
					return m, nil
				}
				status, label, ok := m.prStatusLabelForItem(item)
				if !ok {
					m.status = "pr: no PR for this workspace"
					return m, nil
				}
				prompt := prRepairPrompt(status, item.BookmarkCommitID, itemIsMyPR(item, m.bookmarkPrefix))
				if prompt == "" {
					m.status = "pr: nothing to repair (" + label + ")"
					return m, nil
				}
				// Don't dispatch the repair prompt straight to the agent.
				// Hand it to the send-prompt form prepopulated, so the user
				// can review and edit it before sending. Same form/flow as
				// the `A` "send a typed prompt" dialog.
				m.promptMode = true
				var initCmd tea.Cmd
				m.promptForm, initCmd = newPromptForm(item, prompt)
				m.status = "repair: review prompt · enter send · ctrl+g $EDITOR · esc cancel"
				return m, batchCmds(initCmd, tea.ClearScreen)
			case "m":
				m.prMenuMode = false
				item, ok := m.selected()
				if !ok {
					return m, nil
				}
				status, _, ok := m.prStatusLabelForItem(item)
				if !ok {
					m.status = "pr: no PR for this workspace"
					return m, nil
				}
				if status.Number <= 0 {
					m.status = "pr: merge unavailable (no PR number cached — try p s)"
					return m, nil
				}
				if status.State != PRStateOpen {
					m.status = fmt.Sprintf("pr: #%d is %s — nothing to merge", status.Number, strings.ToLower(string(status.State)))
					return m, nil
				}
				m.confirmMergePR = true
				m.mergeTarget = item
				m.mergeStatus = status
				m.status = fmt.Sprintf("merge PR #%d? [y/N]", status.Number)
				return m, tea.ClearScreen
			case "s":
				m.prMenuMode = false
				item, ok := m.selected()
				if !ok || strings.TrimSpace(item.WorkspaceName) == "" {
					m.status = "pr: select a workspace row"
					return m, nil
				}
				ti := textinput.New()
				ti.Placeholder = "PR # (blank or 0 to clear)"
				ti.CharLimit = 12
				if item.PRNumber > 0 {
					ti.SetValue(strconv.Itoa(item.PRNumber))
				}
				ti.Focus()
				m.prNumberInput = ti
				m.prNumberTarget = item
				m.prNumberErr = ""
				m.prNumberSetMode = true
				m.status = fmt.Sprintf("set PR # for %s/%s — enter saves · esc cancels", item.ProjectName, item.WorkspaceName)
				return m, batchCmds(tea.ClearScreen, textinput.Blink)
			}
			return m, nil
		}
		if m.openMode {
			if m.openLoading {
				switch msg.String() {
				case "esc", "ctrl+c":
					m.openMode = false
					m.openLoading = false
					m.status = ""
				}
				return m, nil
			}
			filtering := m.openList.FilterState() == list.Filtering
			switch msg.String() {
			case "enter":
				// See bookmark mode for rationale: enter during filter
				// commits the filter; second enter actually picks.
				if filtering {
					break
				}
				if m.projectOpener == nil {
					return m, nil
				}
				it, ok := m.openList.SelectedItem().(projectItem)
				if !ok {
					return m, nil
				}
				if err := m.projectOpener(it.project); err != nil {
					m.status = "open: " + err.Error()
					return m, nil
				}
				return m, tea.Quit
			case "esc", "ctrl+c":
				if !filtering && m.openList.FilterState() != list.FilterApplied {
					m.openMode = false
					resetOpenList(&m.openList)
					m.status = ""
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.openList, cmd = m.openList.Update(msg)
			return m, cmd
		}
		if m.bookmarkMode {
			if m.bookmarkLoading {
				switch msg.String() {
				case "esc", "ctrl+c":
					m.bookmarkMode = false
					m.bookmarkLoading = false
					m.status = ""
					if m.bookmarkPurpose == bookmarkPurposeNewWorkspaceStartFrom {
						m.bookmarkPurpose = bookmarkPurposeNewWorkspace
						m.newWorkspaceMode = true
						m.newWorkspaceForm.RevertStartFrom()
						return m, tea.ClearScreen
					}
				}
				return m, nil
			}
			// list.Model owns cursor + filter + paginator state. We only
			// intercept the keys whose default semantics don't match the
			// existing picker UX:
			//   - enter: select the highlighted row directly, even while
			//     filtering (list's default would just commit the filter,
			//     forcing a double-enter to pick).
			//   - esc / ctrl+c while not filtering: close the picker
			//     (list's default is no-op at that point; first esc still
			//     clears an active filter via list's own handling).
			filtering := m.bookmarkList.FilterState() == list.Filtering
			switch msg.String() {
			case "enter":
				// During filter typing the DefaultDelegate hides the
				// selection indicator (intentional: list disables
				// CursorUp/Down while filtering). Defer enter to the
				// list so AcceptWhileFiltering commits the filter —
				// the selector becomes visible, j/k navigates, and a
				// second enter actually picks.
				if filtering {
					break
				}
				if it, ok := m.bookmarkList.SelectedItem().(bookmarkItem); ok && strings.TrimSpace(it.name) != "" {
					return m.acceptBookmarkSelection(it.name)
				}
				return m, nil
			case "esc", "ctrl+c":
				if !filtering && m.bookmarkList.FilterState() != list.FilterApplied {
					fromForm := m.bookmarkPurpose == bookmarkPurposeNewWorkspaceStartFrom
					m.bookmarkMode = false
					resetBookmarkList(&m.bookmarkList)
					m.bookmarkPurpose = bookmarkPurposeNewWorkspace
					m.bookmarkLinkTarget = Item{}
					m.status = ""
					if fromForm {
						m.newWorkspaceMode = true
						m.newWorkspaceForm.RevertStartFrom()
						return m, tea.ClearScreen
					}
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.bookmarkList, cmd = m.bookmarkList.Update(msg)
			return m, cmd
		}
		if m.reviewMode {
			if m.reviewLoading {
				switch msg.String() {
				case "esc", "q", "ctrl+c":
					m.reviewMode = false
					m.reviewLoading = false
					m.status = ""
				}
				return m, nil
			}
			filtering := m.reviewList.FilterState() == list.Filtering
			switch msg.String() {
			case "enter":
				// See bookmark mode for rationale: enter during filter
				// commits the filter; second enter actually picks.
				if filtering {
					break
				}
				if m.handler == nil {
					return m, nil
				}
				it, ok := m.reviewList.SelectedItem().(reviewItem)
				if !ok {
					return m, nil
				}
				pr := it.pr
				item, _ := m.selected()
				m.reviewMode = false
				resetReviewList(&m.reviewList)
				var prCmd tea.Cmd
				m, prCmd = m.forcePRStatusRefresh(item.RepoRoot)
				updated, dispatchCmd := m.startAction(ActionReview, item, strconv.Itoa(pr.Number))
				return updated, batchCmds(prCmd, dispatchCmd)
			case "esc", "ctrl+c":
				if !filtering && m.reviewList.FilterState() != list.FilterApplied {
					m.reviewMode = false
					resetReviewList(&m.reviewList)
					m.status = ""
					return m, nil
				}
			case "q":
				if !filtering {
					m.reviewMode = false
					resetReviewList(&m.reviewList)
					m.status = ""
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.reviewList, cmd = m.reviewList.Update(msg)
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
		km := m.keymap
		switch {
		case key.Matches(msg, km.Help):
			m.helpMode = true
			// tea.ClearScreen on modal entry: the renderer's
			// previous-frame buffer otherwise leaves stripes of the
			// underlying view visible wherever the popover doesn't
			// write. See doc.go and the matching pattern on `/`
			// (filtering) + the new-workspace form.
			return m, tea.ClearScreen
		case key.Matches(msg, km.Jobs):
			m.jobsOverlay = true
			m.jobsList.ResetSelected()
			m.syncJobsListItems()
			m.refreshJobsViewport()
			// tea.ClearScreen on modal entry — same rationale as `?`
			// above. Without this, the deck row list bleeds through
			// the surrounding area of the jobs popover.
			return m, tea.Batch(tea.ClearScreen, refreshJobsListCmd(m.jobsListRefresher))
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
			m.reviewMode = true
			m.reviewLoading = true
			resetReviewList(&m.reviewList)
			m.reviewList.SetShowStatusBar(false)
			m.reviewList.SetItems([]list.Item{loadingItem{label: m.spinner.View() + " loading PRs..."}})
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
			m.openMode = true
			m.openLoading = true
			resetOpenList(&m.openList)
			m.openList.SetShowStatusBar(false)
			m.openList.SetItems([]list.Item{loadingItem{label: m.spinner.View() + " scanning project roots..."}})
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
			m.prMenuMode = true
			m.status = "pr: o open in browser · d description · r repair · s set PR # · esc cancel"
			return m, nil
		case key.Matches(msg, km.PinChord):
			if _, ok := m.selected(); !ok {
				return m, nil
			}
			m.gChordMode = true
			m.status = "pin: gg default · g<letter> group · gD unpin · gR name · esc cancel"
			return m, nil
		}
	}
	// Picker lists drive themselves with async commands — FilterMatchesMsg
	// from the filter input, cursor.BlinkMsg from the filter cursor,
	// statusMessageTimeoutMsg from status timers. The KeyMsg branches
	// above route keys to the active picker; this fallthrough catches
	// everything else so those internal messages reach the list and the
	// filter actually applies as the user types.
	if m.bookmarkMode {
		var cmd tea.Cmd
		m.bookmarkList, cmd = m.bookmarkList.Update(msg)
		return m, cmd
	}
	if m.reviewMode {
		var cmd tea.Cmd
		m.reviewList, cmd = m.reviewList.Update(msg)
		return m, cmd
	}
	if m.openMode {
		var cmd tea.Cmd
		m.openList, cmd = m.openList.Update(msg)
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
	m.bookmarkMode = false
	resetBookmarkList(&m.bookmarkList)
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
		m.promptMode = false
		m.promptForm = promptForm{}
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
		m.bookmarkMode = true
		m.bookmarkPurpose = bookmarkPurposeNewWorkspaceStartFrom
		m.bookmarkLoading = true
		resetBookmarkList(&m.bookmarkList)
		m.bookmarkList.Title = bookmarkPickerTitle(m.bookmarkPurpose, Item{})
		m.bookmarkList.SetShowStatusBar(false)
		m.bookmarkList.SetItems([]list.Item{loadingItem{label: m.spinner.View() + " loading bookmarks..."}})
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
func (m *Model) acceptBookmarkSelection(name string) (tea.Model, tea.Cmd) {
	switch m.bookmarkPurpose {
	case bookmarkPurposeNewWorkspaceStartFrom:
		m.bookmarkMode = false
		resetBookmarkList(&m.bookmarkList)
		m.bookmarkPurpose = bookmarkPurposeNewWorkspace
		m.newWorkspaceMode = true
		m.newWorkspaceForm.SetPickedBookmark(name)
		return *m, tea.ClearScreen
	case bookmarkPurposeLinkExisting:
		target := m.bookmarkLinkTarget
		m.bookmarkMode = false
		resetBookmarkList(&m.bookmarkList)
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
		var prCmd tea.Cmd
		*m, prCmd = m.prStatusRefreshCmd(time.Now())
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
	m.bookmarkMode = true
	m.bookmarkPurpose = bookmarkPurposeLinkExisting
	m.bookmarkLinkTarget = target
	m.bookmarkLoading = true
	resetBookmarkList(&m.bookmarkList)
	m.bookmarkList.Title = bookmarkPickerTitle(m.bookmarkPurpose, m.bookmarkLinkTarget)
	m.bookmarkList.SetShowStatusBar(false)
	m.bookmarkList.SetItems([]list.Item{loadingItem{label: m.spinner.View() + " loading bookmarks..."}})
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
	m.reviewMode = true
	m.reviewLoading = true
	resetReviewList(&m.reviewList)
	m.reviewList.SetShowStatusBar(false)
	m.reviewList.SetItems([]list.Item{loadingItem{label: m.spinner.View() + " loading PRs..."}})
	m.busy = true
	m.status = ""
	return *m, tea.Batch(m.spinner.Tick, m.prFetcher(repo))
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
		if a == ActionSummon {
			if st, ok := m.resolvePRStatus(item); ok && st.Mine &&
				!st.ReviewRequested && !st.ReviewRerequested {
				name := proposeWorkspaceName(st.HeadRefName, m.bookmarkPrefix)
				return m.launchNewForm(NewWorkspaceInitial{Bookmark: st.HeadRefName, Name: name, PRNumber: st.Number}, item.RepoRoot)
			}
		}
		if a == ActionSummon || a == ActionReview {
			return m.startAction(ActionReview, item, strconv.Itoa(item.PRNumber))
		}
		m.status = "no workspace yet — press enter to create it"
		return m, nil
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
// the async launcher. No modal progress mode, no tea.Quit. The
// dispatched job appears in the activity bar via syncJobActivities,
// so we deliberately do not write a "queued · …" status toast — it
// would duplicate the activity entry.
func (m *Model) startAsyncAction(spec AsyncJobSpec) (tea.Model, tea.Cmd) {
	launcher := m.asyncJobLauncher
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
	}
	// Pre-arm pendingSelect with the new workspace's likely name so the
	// cursor snaps to it as soon as the refresh after the subprocess
	// write lands. If the eventual name differs (e.g. the create flow
	// generated a name when both Name and Bookmark were blank) the
	// snap will be a no-op and the cursor stays put.
	if n := strings.TrimSpace(req.Name); n != "" {
		m.pendingSelect = Item{WorkspaceName: n}
	} else if b := strings.TrimSpace(req.Bookmark); b != "" {
		m.pendingSelect = Item{WorkspaceName: b}
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

	// The workspace row list runs full-width — per-row metadata lives
	// on the second line of each row (see metaLine). The new-menu and
	// open pickers keep their 70/30 right-pane help block when the
	// terminal is wide enough, falling back to full-width below
	// deckStackThreshold cols. Other pickers (bookmark, review) are
	// always single-column.
	var left, right string
	switch {
	case m.openMode:
		leftW, rightW := pickerSplit(m.width, m.deckStacked())
		left = m.renderOpenList(leftW)
		if rightW > 0 {
			right = m.renderOpenDetails(rightW)
		}
	case m.bookmarkMode:
		// JoinHorizontal between a short loading-state left pane and a
		// tall static right pane caused painting bleed during load
		// (lipgloss pads with empty rows, not space-filled rows, and
		// JoinVertical's pad newlines don't clear residue).
		// Single-column avoids the issue and gives the list more room.
		left = m.renderBookmarkList(m.width)
	case m.reviewMode:
		left = m.renderReviewList(m.width)
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
	case m.reviewMode && !m.reviewLoading:
		rightSeg = m.reviewList.Help.ShortHelpView(pickerShortHelp(m.reviewList))
	case m.bookmarkMode && !m.bookmarkLoading:
		rightSeg = m.bookmarkList.Help.ShortHelpView(pickerShortHelp(m.bookmarkList))
	case m.openMode && !m.openLoading:
		rightSeg = m.openList.Help.ShortHelpView(pickerShortHelp(m.openList))
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
	if m.bookmarkMode || m.reviewMode || m.openMode ||
		m.jobsOverlay || m.prMenuMode || m.findMode || m.actionMode {
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
	view := lipgloss.JoinVertical(lipgloss.Left, body, padBlock, footer)
	if m.confirmDelete {
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderDeleteConfirm())
	}
	if m.confirmMergePR {
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderMergePRConfirm())
	}
	if m.prNumberSetMode {
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderPRNumberSet())
	}
	if m.pinAliasMode {
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderPinAliasSet())
	}
	if m.helpMode {
		// Center the help box over the existing view as a popover.
		view = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.renderHelp(m.width))
	}
	if m.jobsOverlay {
		view = renderJobsOverlay(m.width, m.height, &m.jobsList, &m.jobsViewport, len(m.jobs) == 0)
	}
	return view
}

// updateJobsOverlay handles keypresses while the J overlay is active.
// Selection + scroll are owned by bubbles (jobsList and jobsViewport);
// this function intercepts the overlay's close keys and job-action
// shortcuts (c/x/r/o/y), then delegates the rest to the appropriate
// bubble. Actions only fire when the list isn't actively filtering so
// the letter keys can be typed into the filter input.
func (m Model) updateJobsOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtering := m.jobsList.FilterState() == list.Filtering
	if !filtering {
		switch msg.String() {
		case "esc", "q", "J":
			if m.jobsList.FilterState() == list.FilterApplied {
				m.jobsList.ResetFilter()
				return m, nil
			}
			m.jobsOverlay = false
			return m, nil
		case "g":
			m.jobsList.Select(0)
			m.refreshJobsViewport()
			return m, nil
		case "G":
			if n := len(m.jobsList.Items()); n > 0 {
				m.jobsList.Select(n - 1)
				m.refreshJobsViewport()
			}
			return m, nil
		case "c":
			j, ok := m.selectedJob()
			if !ok || m.jobCancelHandler == nil {
				return m, nil
			}
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
			j, ok := m.selectedJob()
			if !ok || m.jobDismissHandler == nil {
				return m, nil
			}
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
			j, ok := m.selectedJob()
			if !ok || m.jobRetryHandler == nil {
				return m, nil
			}
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
		case "D":
			// Delete-workspace-and-retry. Only meaningful for jobs whose
			// ErrorKind tags them as recoverable via this affordance.
			j, ok := m.selectedJob()
			if !ok || m.jobDeleteWorkspaceRetry == nil {
				return m, nil
			}
			if j.ErrorKind != "stale_workspace" {
				m.status = "D: only applies to jobs failed with a stale workspace"
				return m, nil
			}
			if strings.TrimSpace(j.ErrorWorkspace) == "" {
				m.status = "D: job has no error workspace recorded"
				return m, nil
			}
			handler := m.jobDeleteWorkspaceRetry
			id := j.ID
			return m, func() tea.Msg {
				return JobActionDoneMsg{JobID: id, Kind: "delete-and-retry", Err: handler(id)}
			}
		case "o":
			j, ok := m.selectedJob()
			if !ok || m.jobLogOpener == nil {
				return m, nil
			}
			return m, m.jobLogOpener(j.ID)
		case "y":
			j, ok := m.selectedJob()
			if !ok {
				return m, nil
			}
			text := jobDetailsForCopy(j)
			if err := writeSystemClipboard(text); err != nil {
				m.status = "copy: " + err.Error()
			} else {
				m.status = fmt.Sprintf("copied %d bytes to clipboard", len(text))
			}
			return m, nil
		}
	} else if msg.String() == "esc" {
		// While actively filtering, esc cancels the filter (list owns
		// that) — never closes the overlay.
		var cmd tea.Cmd
		m.jobsList, cmd = m.jobsList.Update(msg)
		m.refreshJobsViewport()
		return m, cmd
	}
	// Route pgup/pgdn/ctrl+u/ctrl+d to the details viewport so the user
	// can scroll the log without moving the list selection.
	switch msg.String() {
	case "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.jobsViewport, cmd = m.jobsViewport.Update(msg)
		return m, cmd
	}
	priorIdx := m.jobsList.Index()
	var cmd tea.Cmd
	m.jobsList, cmd = m.jobsList.Update(msg)
	if m.jobsList.Index() != priorIdx {
		m.refreshJobsViewport()
	}
	return m, cmd
}

// selectedJob returns the currently highlighted Job and ok=true, or
// ok=false when there are no items or the cast fails.
func (m Model) selectedJob() (Job, bool) {
	it, ok := m.jobsList.SelectedItem().(jobItem)
	if !ok {
		return Job{}, false
	}
	return it.job, true
}

// syncJobsListItems projects m.jobs into the bubbles/list items slice
// while preserving the cursor on the same Job ID when possible. Called
// from jobsListMsg (live refresh) and from the J open path.
func (m *Model) syncJobsListItems() {
	var selectedID string
	if it, ok := m.jobsList.SelectedItem().(jobItem); ok {
		selectedID = it.job.ID
	}
	items := make([]list.Item, 0, len(m.jobs))
	keepIdx := -1
	for i, j := range m.jobs {
		items = append(items, jobItem{job: j})
		if j.ID == selectedID {
			keepIdx = i
		}
	}
	m.jobsList.SetItems(items)
	if keepIdx >= 0 {
		m.jobsList.Select(keepIdx)
	}
}

// refreshJobsViewport re-renders the details pane content from the
// currently selected job. viewport.SetContent preserves YOffset when
// the existing scroll position is still valid, so this is safe to call
// on every Update tick.
func (m *Model) refreshJobsViewport() {
	width := m.jobsViewport.Width
	if width <= 0 {
		width = 40
	}
	if j, ok := m.selectedJob(); ok {
		m.jobsViewport.SetContent(renderJobDetails(j, width))
	} else {
		m.jobsViewport.SetContent("")
	}
}

func (m Model) renderHelp(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent)).Render("awp deck — help (?, esc, or enter to close)")

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
	// left, key bindings on the right. Box widens to fit both — clamps to
	// the available viewport width and falls back to a single tall block
	// under ~70 cols.
	const (
		targetWidth = 110
		gutter      = 4
		boxOverhead = 6 // border (2) + horizontal padding (2*2)
	)
	boxWidth := targetWidth
	if width > 0 && width-8 < boxWidth {
		boxWidth = width - 8
	}
	if boxWidth < 64 {
		boxWidth = 64
	}
	innerWidth := boxWidth - boxOverhead

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

	var cols string
	if innerWidth >= 70 {
		leftWidth := (innerWidth - gutter) * 9 / 20
		rightWidth := innerWidth - gutter - leftWidth
		left := clipBlock(leftBlock, leftWidth)
		right := clipBlock(rightBlock, rightWidth)
		cols = lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gutter), right)
	} else {
		// Narrow terminal — stack vertically like the old layout.
		cols = clipBlock(leftBlock, innerWidth) + "\n\n" + clipBlock(rightBlock, innerWidth)
	}

	body := lipgloss.JoinVertical(lipgloss.Left, title, "", cols)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(boxWidth).
		Render(body)
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
		rows := append(header, lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)).Render("No workspaces found."))
		return lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1).Render(strings.Join(rows, "\n"))
	}
	projectHints, rowHints := m.findHints()
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
			star := s.ProjectHeader.Render("★")
			label := s.ProjectHeader.Render(m.pinGroupLabel(r.project))
			if m.gChordMode {
				chip := s.Selected.Render("[" + pinGroupChordLetter(r.project) + "]")
				label = chip + " " + label
			}
			body = append(body, fmt.Sprintf("%s %s %s", prefixSlot.Render(""), star, label))
		case deckRowPrimary:
			item := items[r.itemIndex]
			// findProject is "" in the inbox scope's single-stage find
			// (no project stage), so nothing dims there.
			dim := m.findMode && m.findStage == findStageWorkspace &&
				m.findProject != "" && item.ProjectName != m.findProject
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
			chip := ""
			if m.scope == ScopeInbox {
				chip = s.Muted.Render("["+item.ProjectName+"]") + " "
			}
			label := truncate(m.displayLabel(item), max(10, width-19-lipgloss.Width(chip)))
			// Status is canonical in JSON, so render the stored glyph
			// immediately on the fast first paint. The only tmux-derived
			// override is `working` → `exited` (agent shell death — Claude
			// has no exit hook), which arrives a frame later from the
			// enrichment pass and is rare enough that a brief flash is
			// preferable to a blank glyph slot.
			glyph := statusGlyph(item.Status, dim, item.Unread)
			line := fmt.Sprintf("%s %s %s%s", prefixSlot.Render(prefix), glyph, chip, labelStyle.Render(label))
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
			metaIndent := strings.Repeat(" ", metaIndentW)
			glyphs := ""
			for _, g := range []string{
				m.prGlyphForItem(item),
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
			metaText := truncate(m.metaLine(item), metaRoom)
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
		return deckBodyRows(items, nil, m.inboxGroupLabels(items))
	}
	pinned := pinnedCount(items)
	if pinned == 0 {
		return deckBodyRows(items, collapsedProjects(items), nil)
	}
	return m.deckBodyRowsPinned(items, pinned)
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
		buckets[i] = m.itemInboxBucket(it)
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
				{"ctrl+u / ctrl+d", "jump ½ page up / down"},
				{"/", "filter rows · esc clears"},
				{"f", "find: project → workspace easymotion jump"},
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
				{"g g", "pin selected → default group (again unpins)"},
				{"g a…z", "pin selected → group <letter> (same again unpins, different moves)"},
				{"g D", "unpin selected workspace"},
				{"g R", "name the selected row's group (display alias)"},
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

func (m *Model) renderOpenList(width int) string {
	containerStyle := lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1)
	listWidth := width - 2
	if listWidth < 8 {
		listWidth = 8
	}
	listHeight := m.height - 5
	if listHeight < 3 {
		listHeight = 3
	}
	m.openList.SetSize(listWidth, listHeight)
	return containerStyle.Render(m.openList.View())
}

func (m Model) renderOpenDetails(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("open")
	lines := []string{title, ""}
	if it, ok := m.openList.SelectedItem().(projectItem); ok {
		lines = append(lines,
			"Selection: "+it.project.Name,
			"Path:      "+it.project.Path,
		)
	} else {
		lines = append(lines, "Pick a project to summon (or create) its default workspace.")
	}
	lines = append(lines, "",
		"Keys:",
		"/        fuzzy filter",
		"↑/↓      navigate",
		"enter    open",
		"esc      cancel",
	)
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderBookmarkList(width int) string {
	containerStyle := lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1)
	// Reserve 2 rows for container padding plus 3 for the deck's bottom
	// footer (status line + 1 row top/bottom padding). list.Model handles
	// its own title, status bar, paginator, and help footer inside the
	// remaining space — and a single loadingItem during the fetch so
	// the chrome's shape stays constant.
	listWidth := width - 2
	if listWidth < 8 {
		listWidth = 8
	}
	listHeight := m.height - 5
	if listHeight < 3 {
		listHeight = 3
	}
	m.bookmarkList.SetSize(listWidth, listHeight)
	return containerStyle.Render(m.bookmarkList.View())
}

func (m *Model) renderReviewList(width int) string {
	containerStyle := lipgloss.NewStyle().Width(width).Padding(2, 1, 1, 1)
	listWidth := width - 2
	if listWidth < 8 {
		listWidth = 8
	}
	listHeight := m.height - 5
	if listHeight < 3 {
		listHeight = 3
	}
	m.reviewList.SetSize(listWidth, listHeight)
	return containerStyle.Render(m.reviewList.View())
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

func (m Model) renderDeleteConfirm() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colDanger)).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colDanger))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)

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

func (m Model) renderMergePRConfirm() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(64)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	prStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo)).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colStrong))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))

	s := m.mergeStatus
	cmd := fmt.Sprintf("gh pr merge %d --squash", s.Number)

	lines := []string{
		titleStyle.Render(fmt.Sprintf("Merge PR %s?", prStyle.Render("#"+strconv.Itoa(s.Number)))),
		"",
	}
	if title := strings.TrimSpace(s.Title); title != "" {
		lines = append(lines, truncate(title, 58))
	}
	lines = append(lines,
		"",
		labelStyle.Render("Runs:"),
		cmdStyle.Render("  "+cmd),
	)
	if s.IsInMergeQueue {
		// The rocket is showing for this row — the PR is already in the
		// repo's merge queue.
		lines = append(lines, labelStyle.Render(fmt.Sprintf("  %s already in the merge queue — re-adds it to the queue", prGlyphInQueue)))
	} else {
		lines = append(lines, labelStyle.Render("  squash by default; falls back to the merge queue if required"))
	}
	lines = append(lines,
		"",
		hintStyle.Render("y confirm · n / esc cancel"),
	)
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m Model) selected() (Item, bool) {
	items := m.items()
	if len(items) == 0 || m.cursor < 0 || m.cursor >= len(items) {
		return Item{}, false
	}
	return items[m.cursor], true
}

func (m Model) renderPRNumberSet() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)

	target := strings.TrimSpace(m.prNumberTarget.WorkspaceName)
	if target == "" {
		target = "this workspace"
	}
	current := "none"
	if m.prNumberTarget.PRNumber > 0 {
		current = fmt.Sprintf("#%d", m.prNumberTarget.PRNumber)
	}
	lines := []string{
		titleStyle.Render("Pin PR # for " + target),
		"",
		mutedStyle.Render("Pins this workspace to a specific PR so the deck"),
		mutedStyle.Render("resolves status directly by number."),
		"",
		mutedStyle.Render("Current PR: " + current),
		"",
		mutedStyle.Render("PR number (blank or 0 clears):"),
		m.prNumberInput.View(),
	}
	if m.prNumberErr != "" {
		lines = append(lines, "", errStyle.Render(m.prNumberErr))
	}
	lines = append(lines, "", hintStyle.Render("enter save · esc cancel"))
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
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
	ti := textinput.New()
	ti.Placeholder = "group name (blank clears)"
	ti.CharLimit = 40
	ti.SetValue(strings.TrimSpace(m.pinGroupAliases[key]))
	ti.Focus()
	m.pinAliasInput = ti
	m.pinAliasTarget = key
	m.pinAliasErr = ""
	m.pinAliasMode = true
	m.status = fmt.Sprintf("name group %s — enter saves · esc cancels", pinGroupChordLetter(key))
	return m, batchCmds(tea.ClearScreen, textinput.Blink)
}

func (m Model) renderPinAliasSet() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2).
		Width(60)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)).Bold(true)

	current := "none"
	if a := strings.TrimSpace(m.pinGroupAliases[m.pinAliasTarget]); a != "" {
		current = a
	}
	lines := []string{
		titleStyle.Render("Name pin group " + pinGroupChordLetter(m.pinAliasTarget)),
		"",
		mutedStyle.Render("Sets the display label for this register in the"),
		mutedStyle.Render("pinned section headers. Cosmetic — the register"),
		mutedStyle.Render("key stays the letter you pin with."),
		"",
		mutedStyle.Render("Current name: " + current),
		"",
		mutedStyle.Render("Name (blank clears):"),
		m.pinAliasInput.View(),
	}
	if m.pinAliasErr != "" {
		lines = append(lines, "", errStyle.Render(m.pinAliasErr))
	}
	lines = append(lines, "", mutedStyle.Render("enter save · esc cancel"))
	return box.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m *Model) startFind() {
	m.findMode = true
	m.findPendingPrefix = 0
	m.findProject = ""
	if m.scope == ScopeInbox {
		// The inbox scope has no project headers (rows section by
		// bucket), so find skips the project stage and hints every
		// row directly, like the mini-deck.
		m.findStage = findStageWorkspace
		m.findProjectHints = map[string]string{}
		m.findProjectLookup = map[string]string{}
		m.findProjectPrefix = map[rune]bool{}
		m.findRowHints, m.findRowLookup, m.findRowPrefix = m.buildRowHints("")
		m.status = "find: workspace"
		if len(m.findRowLookup) == 0 {
			m.cancelFind("")
		}
		return
	}
	m.findStage = findStageProject
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
	hadPending := m.findPendingPrefix != 0
	if m.findStage == findStageProject {
		project, ok := findHintStep(r, m.findProjectLookup, m.findProjectPrefix, &m.findPendingPrefix)
		if ok {
			return m.enterWorkspaceStage(project), nil
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

// buildRowHints assigns easymotion hints to the rows of the given
// project. project == "" hints every row (the inbox scope's
// single-stage find); names are project-qualified there so duplicate
// workspace names across projects can't collide on one hint.
func (m Model) buildRowHints(project string) (map[int]string, map[string]int, map[rune]bool) {
	items := m.items()
	rowIdx := []int{}
	names := []string{}
	for i, item := range items {
		if project != "" && item.ProjectName != project {
			continue
		}
		name := item.WorkspaceName
		if project == "" {
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
	if len(issues) == 1 {
		return fmt.Sprintf("%s has %s. Please %s, then push the fix.", ref, issues[0].label, issues[0].fix)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s has multiple issues to address:\n", ref)
	for _, it := range issues {
		fmt.Fprintf(&b, "- %s — please %s.\n", it.label, it.fix)
	}
	b.WriteString("Push the fixes when done.")
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

// resolvePRStatus is the one PR-lookup entry point. Returns the cached
// PR for this workspace via item.PRNumber. Falls back to a bookmark →
// headRefName lookup ONLY when PRNumber is unset and a bookmark is
// present — kept as a compat path so workspaces created before the
// rename (and not yet migrated by the deck's state load) still resolve.
// Once the migration runs against every stale entry, the bookmark
// branch is dead code; we'll delete it in a follow-up after the next
// deck release.
func (m Model) resolvePRStatus(item Item) (PRStatus, bool) {
	repo := strings.TrimSpace(item.RepoRoot)
	if repo == "" {
		return PRStatus{}, false
	}
	byHead, ok := m.prStatusByRepo[repo]
	if !ok {
		return PRStatus{}, false
	}
	if item.PRNumber > 0 {
		for _, s := range byHead {
			if s.Number == item.PRNumber {
				return s, true
			}
		}
		return PRStatus{}, false
	}
	if bm := strings.TrimSpace(item.Bookmark); bm != "" {
		if s, ok := byHead[bm]; ok {
			return s, true
		}
	}
	return PRStatus{}, false
}

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
