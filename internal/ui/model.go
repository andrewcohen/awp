package ui

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/awplog"
	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

type Focus int

const (
	FocusFiles Focus = iota
	FocusHunks
	FocusFilter
	// FocusComments is the comment index below the file list — a jump index over
	// the change's conversations (see comment_list.go).
	FocusComments
	// FocusSearch is the diff's own search prompt (see search.go). Like
	// FocusFilter it is a mode rather than a pane, so it stays out of the tab
	// rotation.
	FocusSearch
)

// DefaultRefreshInterval is how often the viewer re-reads the diff. Live
// refresh is only safe now that a reload preserves the reading position (see
// anchor.go); it was disabled in April 2026 precisely because it did not.
//
// A poll rather than a filesystem watch, deliberately: `jj diff` snapshots the
// working copy, so this already costs a subprocess, and an unchanged diff is
// dropped by fingerprint before it touches any view state. Watching the tree
// with fsnotify means per-directory watches on macOS (kqueue, no recursive
// watch) — worth doing only if this interval proves too costly in practice.
const DefaultRefreshInterval = 2 * time.Second

// minBodyHeight is the smallest two-pane body we will render into. Below
// this the panes have no room for content and the borders collide.
const minBodyHeight = 6

// chromeHeight is the rows this model's own header + footer occupy when it
// runs standalone (`awp diff`). Embedded, the host owns the chrome and
// calls SetSize with the body budget directly.
const chromeHeight = 4

// hScrollStep is how many columns h/l pan the hunk pane. Single columns make
// reading the tail of a long line tedious; a tab-ish step gets there in a
// few presses while still landing predictably.
const hScrollStep = 8

// minVisibleColumns is how much of the longest line must stay on screen, so
// panning can't leave the pane blank.
const minVisibleColumns = 16

// The app-wide selection marker: a left bar on the selected row, and an
// equally wide blank on every other row so labels line up.
const (
	selectionPrefixBar   = "┃ "
	selectionPrefixBlank = "  "
)

// cursorScrollMargin keeps this many rows visible beyond the cursor when the
// viewport follows it, so you can see what is coming rather than reading from
// the very edge of the pane. Collapses near the stream's ends.
const cursorScrollMargin = 2

// OpenFunc hands a file to $EDITOR: which directory to run in, which file, which
// line.
//
// dir comes from the viewer rather than from the host, because it is the same fact
// as the path's root — resolveFilePath joins RepoRoot with the diff's relative
// path, and the editor has to start in the working copy that path was resolved
// against. Two hosts deriving it separately is how they would come to disagree,
// and the wrong answer is invisible: the file still opens, and only everything the
// editor infers from cwd is about another project.
type OpenFunc func(dir, filePath string, line int) tea.Cmd

type Model struct {
	RepoRoot        string
	RefreshInterval time.Duration
	LoadDiff        func() (string, error)
	OpenFile        OpenFunc
	// ResolveBase names what the diff is against — "main", "andrew/parent" —
	// for a host that wants to say so in its chrome. Optional.
	//
	// Separate from LoadDiff, and resolved on its own cadence, because it is a
	// different question: LoadDiff answers "what changed" every refresh tick,
	// while the base changes only when you rebase. Resolving it per tick would
	// spend a subprocess every couple of seconds to re-learn the same answer, so
	// it runs once on open and again on an explicit refresh. It runs as its own
	// command rather than on the open path so a slow resolve cannot delay the
	// first frame.
	ResolveBase func() string
	// baseLabel is the last answer ResolveBase gave, empty until it lands.
	baseLabel string
	// HostKeys are bindings the surrounding program owns, listed after the view's
	// own in the `?` reference. A host intercepts its keys before the view sees
	// them, so this is the only way they can be documented where the reader looks
	// for them — and leaving it empty is how standalone `awp diff` avoids
	// advertising keys only the deck has.
	HostKeys []charm.KeyGroup
	// Subject is what this is a review of, for the standalone header. A host with
	// its own chrome (the deck) renders Body and answers this in its footer
	// instead, so it leaves this empty.
	Subject Subject
	// The `-` chord (see scope.go): scopes are the ranges a host offers, scopeIndex
	// the one being read, scopePick the chord waiting for the key that picks.
	scopes     []ScopeOption
	scopeIndex int
	scopePick  bool
	// pendingZ is the `z` chord waiting for its second key — `zz` centres the diff
	// on the cursor, the way it does in vim.
	pendingZ bool

	files       []diff.FileDiff
	filtered    []diff.FileDiff
	filesCursor int
	// stream is the row geometry of the whole diff (see stream.go). Rebuilt
	// only when the file set, width or wrap changes.
	stream streamIndex
	// streamScroll is the index of the top visible stream row.
	streamScroll int
	// cursorRow is the stream row the cursor is on. The viewport follows it;
	// it is what "the line you are on" means for opening an editor, and
	// later for anchoring a comment.
	cursorRow int
	// visualAnchor is the fixed end of a visual range, visualNone when no range is
	// being selected; the moving end is the cursor (see visual.go).
	visualAnchor int
	// marks says which rows carry a ranged comment's left bar, and in whose kind's
	// colour (see range_marks.go). Derived from the placed rows on every rebuild.
	marks rangeMarks
	// The publish prompt (see publish.go): publishing is the prompt being up,
	// publishCursor which verdict is selected, publishBusy a submission in flight —
	// which blocks a second one, since a comment is only marked published once
	// GitHub has answered for it.
	publishing    bool
	publishCursor int
	publishBusy   bool
	// publishStage is choose → preview → report; publishReport holds the plan or
	// the outcome, and publishScroll reads a long one.
	publishStage  publishStage
	publishReport []string
	publishScroll int
	// The merge prompt (see merge.go), the same shape one screen shorter: merging
	// is the prompt being up, mergeBusy a merge in flight — which blocks a second
	// one, since gh has already been asked.
	merging     bool
	mergeBusy   bool
	mergeStage  mergeStage
	mergeReport []string
	mergeScroll int
	// summaryEditor is the review-body box inside that flow — the same compose box
	// the stream uses, so a summary is written with the keys already learnt here.
	summaryEditor commentEditor
	// summarySources is the review's own summary remarks that the box was prefilled
	// from. Held so the publish path can reconcile them against whatever was sent
	// rather than leaving stale copies beside it.
	summarySources []review.Comment
	focus          Focus
	filterInput    textinput.Model
	// searchInput and searchQuery are the diff's content search (see search.go).
	// The query outlives the prompt so n/N keep working after enter; searchOrigin
	// is where the cursor was when the prompt opened, so esc can put it back.
	searchInput  textinput.Model
	searchQuery  string
	searchOrigin int
	width        int
	// bodyHeight is the height of the two-pane body (file list + hunks),
	// excluding this model's own header/footer. Standalone it is derived
	// from the terminal height; embedded (the deck's `c` modal) the host
	// sets it directly via SetSize, since the host owns the chrome.
	bodyHeight int
	// hunkWidth is the hunk pane's content width, cached so the update path
	// (scroll clamping, cursor sync) can lay out rows at the same width the
	// renderer will use. Wrapped lines occupy more than one row, so that
	// geometry is width-dependent.
	hunkWidth int
	wrap      bool
	// sideBySide draws a change as old-beside-new rather than old-above-new (`|`).
	// A reading preference for the change in front of you, like the scope menu, so
	// it is not persisted.
	sideBySide bool
	// hunkHScroll is how many columns the hunk pane's line content is panned
	// left. Only meaningful when wrap is off — wrapped lines have no
	// horizontal overflow.
	hunkHScroll int
	// fingerprint is the identity of the currently loaded diff text; a reload
	// matching it is a no-op.
	fingerprint uint64
	loaded      bool
	// comments are the findings anchored into this diff, placed during the
	// geometry pass (see comments.go).
	comments []review.Comment
	// commentIndex is the left column's jump index over those conversations,
	// derived from the placed stream. Cached rather than recomputed per frame:
	// building it walks every row, which is exactly the per-frame cost the
	// geometry/render split exists to avoid.
	commentIndex   []commentEntry
	commentsCursor int
	// threads are the PR's existing conversation, mirrored from GitHub and
	// rendered alongside local comments.
	threads          []review.Thread
	threadVisibility ThreadVisibility
	// threadFold overrides a mirrored thread's default fold state, by thread id.
	// True means expanded. Absent means the default — resolved threads fold, open
	// ones do not (see threadFolded). Not persisted: how you left a fold is a
	// reading position, not a property of the review.
	threadFold map[string]bool
	// ResolveThread toggles a GitHub thread's resolved state.
	ResolveThread ThreadResolver
	// ReplyToThread posts a reply into a GitHub thread, returning the id of the
	// comment it created. Nil leaves replying unavailable, which the viewer says
	// rather than opening a box whose contents would have nowhere to go.
	ReplyToThread ThreadReplier
	// SaveComment persists a comment the user wrote. Nil disables commenting, so
	// the standalone viewer works with no store configured.
	SaveComment CommentSink
	// SendComment additionally hands a comment to the workspace's agent. Nil
	// leaves the send exit unavailable.
	SendComment CommentSink
	// SendUnsent hands the agent every remark written and not sent yet, and returns
	// how many went. Nil leaves ctrl+s outside the compose box unavailable.
	//
	// It takes no list and returns a count: which records qualify is the store's
	// answer, read off disk, not this model's — the comments here are a frame behind
	// by construction, since a finding filed while you read arrives on a tick.
	SendUnsent func() (int, error)
	// ApproveProposal says yes to an agent's proposal, returning the record as
	// written. Nil leaves `A` unavailable, which the viewer reports rather than
	// appearing to approve something and recording nothing.
	//
	// It returns the comment because approving is only half of the gesture: the
	// other half hands that record to SendComment, and the agent's prompt is
	// rendered from what the store actually wrote.
	ApproveProposal func(id string) (review.Comment, error)
	// UpdateComment revises an existing comment; DeleteComment removes one.
	//
	// UpdateComment carries a revision — a new body — and nothing else. The store
	// keeps the record's state, timestamps and publish record, because a compose box
	// does not own them.
	UpdateComment CommentSink
	DeleteComment CommentDeleter
	// RecordPublished notes that a comment reached GitHub, against the id GitHub gave
	// it. Separate from UpdateComment, which drops exactly that (see above): a reply
	// posted through it went out and still read as unsent.
	RecordPublished func(id, remoteID string) error
	// LastSavedComment returns the record the store just wrote, including the id
	// it assigned. The id is what lets the agent reply on the thread.
	LastSavedComment func() (review.Comment, bool)
	// ReplyComment files a reply against a parent comment, which also reopens
	// that parent — an answered remark needs the reviewer again.
	ReplyComment func(parentID string, c review.Comment) error
	// PublishReview sends the review to its PR, with a verdict — "approve",
	// "request-changes", "comment", or empty to post the comments without
	// submitting a review. It returns what happened, in the words the CLI's own
	// publish prints. Nil leaves `P` unavailable, which it says rather than
	// silently doing nothing.
	PublishReview func(verdict, summary string, dryRun bool) (string, error)
	// MergePR merges the PR this review is on, returning what gh reported —
	// which is the whole outcome, since a squash that falls back to the merge
	// queue says so there and nowhere else. Nil leaves `M` unavailable, which
	// covers both reasons for it: no PR, or a host that offers no merging.
	//
	// dryRun asks for the call it would make without making it, the same
	// contract PublishReview has. That is what the confirm screen shows, and it
	// is also where a refusal that can be known in advance belongs — the
	// reviewer finds out before the box appears rather than after confirming.
	MergePR func(dryRun bool) (string, error)
	// LoadComments re-reads the review's comments, so findings filed while the
	// view is open appear without reopening it.
	LoadComments func() ([]review.Comment, error)
	// LoadThreads re-reads the mirrored remote threads, for the same reason. It
	// is a local store read, not a fetch: something else — the pr-status job —
	// owns refreshing the mirror from GitHub, so a review tick never waits on
	// the network.
	LoadThreads func() ([]review.Thread, error)
	editing     bool
	editor      commentEditor
	// showHelp is the `?` key reference, drawn in place of the panes. helpVP
	// scrolls it — there are more bindings than fit a short terminal.
	showHelp bool
	helpVP   viewport.Model
	// cache holds rendered fragments between frames (see stream_render.go). A
	// pointer because the render path takes Model by value; it is scratch space
	// shared by those copies, not state.
	cache *renderCache
	// hl is the diff body's syntax colour, nil when AWP_DIFF_SYNTAX is unset. A
	// pointer for the same reason cache is: it holds the lexers and the lexed lines,
	// which are scratch shared by the copies of Model the render path makes.
	hl *highlighter
	// hideLeft drops the left column (`\`), giving the stream the full width.
	// The file and comment cursors keep their state while it is hidden, so
	// unhiding returns you to where you were rather than to the top.
	hideLeft bool
	// ReviewedFiles maps a path to the content hash it had when marked
	// reviewed, and MarkReviewed persists a change to that. Hash rather than a
	// bare flag so a later edit resurfaces the file: marking something reviewed
	// and then having the agent silently change it is the worst failure a review
	// tool has, and awp's agent edits while you review.
	ReviewedFiles map[string]string
	MarkReviewed  func(path, hash string) error
	// status is the footer's one line, and statusErr colours it as a failure. Set a
	// failure through fail() rather than writing both: that is what puts the reason
	// somewhere it can still be read afterwards.
	status     string
	statusErr  bool
	refreshing bool
}

// fail reports a failure twice: to the status line, for whoever is at the keyboard
// now, and to the log, for working out afterwards what actually happened.
//
// One method rather than the two assignments it replaces, because the log is only
// as complete as the sites that remember to write to it — and the site that forgets
// is discovered exactly when someone needs it. A status line cannot be copied,
// cannot be scrolled back to, and is gone on the next keystroke; when the reason a
// publish was refused is GitHub's own sentence, that sentence has to survive the
// keystroke.
//
// Everything the reviewer is told is what goes in the log, verbatim. Somewhere
// short enough for a footer and complete enough for a bug report is the same string
// here, and if that ever stops being true the log is the one that should get the
// longer version.
// The two assignments below are the only ones in the package: everywhere else
// reports a failure by calling this. See failure_log_test.go, which enforces it.
func (m *Model) fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	m.status = msg
	m.statusErr = true
	awplog.Errorf("diff: %s", msg)
}

// SetSize sizes the viewer for a host that owns its own chrome: width is
// the full width available to the body, bodyHeight the number of rows the
// two panes may occupy. Standalone use goes through tea.WindowSizeMsg
// instead.
func (m *Model) SetSize(width, bodyHeight int) {
	height := max(minBodyHeight, bodyHeight)
	_, right := m.paneWidthsFor(width)
	// Every stream row reserves the selection-prefix columns, so the width
	// available to content — and therefore the wrap geometry — is narrower
	// than the pane. Getting this wrong makes wrapped row counts disagree
	// with what is rendered.
	hunkWidth := right - 4 - lipgloss.Width(selectionPrefixBlank)
	// Nothing about the layout moved, so there is nothing to re-lay.
	//
	// This early return is the difference between a frame costing a millisecond
	// and costing twenty. The deck calls SetSize once per frame — it has no way to
	// know whether the terminal changed — and the rebuild at the bottom of this
	// function is a full geometry and placement pass over every row of the diff.
	// Paying that per frame is invisible on a change with no comments (placement
	// has nothing to do) and crippling on one with many, because placement is
	// O(comments × rows).
	//
	// Compared on the *derived* geometry rather than on the arguments, because
	// `\` (hide the left column) changes the pane split without changing the
	// terminal size and does need the rebuild.
	if m.width == width && m.bodyHeight == height && m.hunkWidth == hunkWidth {
		return
	}
	m.width = width
	m.bodyHeight = height
	m.hunkWidth = hunkWidth
	if m.editing {
		// The box lives in the stream, so a resize has to re-lay its text area
		// too — an area left at the old width wraps inside the new box and makes
		// it taller than the geometry reserved.
		m.editor.setWidth(m.hunkWidth)
	}
	if m.showHelp {
		// Re-lay the reference for the new size. Its content is truncated to width,
		// so a stale layout would either overflow the panel or leave it half empty.
		m.resizeHelp()
	}
	if m.publishing {
		// The summary box fills whatever the chrome leaves in both directions, so a
		// resize moves it.
		m.resizeSummaryBox()
	}
	m.rebuildStream()
}

// resizeHelp re-lays the `?` overlay for the current body size, keeping the
// scroll position where the reader left it.
func (m *Model) resizeHelp() {
	at := m.helpVP.YOffset()
	m.helpVP = newHelpViewport(m.width, m.bodyHeight, m.scopeHelpRow(), m.HostKeys)
	m.helpVP.SetYOffset(at)
}

// rebuildStream re-indexes the diff for the current width and wrap setting,
// then re-clamps the scroll against the new geometry. Every mutation of the
// file set, width or wrap must go through here — it is the only place the
// index is built, so it cannot silently go stale.
func (m *Model) rebuildStream() {
	// Every cached fragment was rendered from the stream this is about to replace,
	// so they all die with it. This is the only place that happens, which is what
	// lets the cache outlive a frame at all — see renderCache in stream_render.go.
	m.cache.drop()
	idx := withComments(
		buildStreamLayout(m.filtered, m.hunkWidth, m.wrap, m.sideBySide, m.isCollapsed),
		m.placeComments, m.threadCollapsed,
	)
	// The index is built before the compose box is spliced in, so a half-written
	// comment never shows up as a listed conversation.
	m.stream = idx
	m.commentIndex = m.commentEntries(idx)
	if m.editing {
		// editing names the comment the box replaces; empty for a new comment or a
		// reply, which append instead.
		m.stream = withEditor(idx, m.editorAnchorRow(idx), commentEditorRows, m.editor.editing)
	}
	// After the splice: the box's rows shift every index after it, and a mark is a
	// row index.
	m.marks = m.buildRangeMarks(m.stream.rows)
	m.clampCommentsCursor()
	m.clampCursor()
	// A visual range is an index into the rows this just replaced (see the note at
	// the top of visual.go), so it does not survive them changing. Abandoned rather
	// than clamped: the anchor row still existing does not mean it still shows the
	// line it was put on, and losing a two-keystroke gesture is a far smaller
	// surprise than commenting on lines you did not select. `c` consumes the range
	// before it gets here.
	m.clearVisual()
	m.followCursor()
	m.followEditor()
	m.syncFileCursorToCursor()
}

// followEditor scrolls the compose box into view. Called after any rebuild while
// editing, because the box's rows move whenever the diff or the comment set does.
//
// It aims for the row *above* the box — the line being commented on — so the code
// under discussion stays visible. When the box is too tall for the pane the top
// wins, since that is where the text is being typed.
func (m *Model) followEditor() {
	if !m.editing {
		return
	}
	first, last := -1, -1
	for i, r := range m.stream.rows {
		if r.kind != rowEditor {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return
	}
	height := m.streamContentHeight()
	if last > m.streamScroll+height-1 {
		m.streamScroll = last - height + 1
	}
	if top := max(0, first-1); top < m.streamScroll {
		m.streamScroll = top
	}
	m.clampStreamScroll()
}

// paneWidths splits the body between the file list and the hunk pane. Both
// View and SetSize go through this so the cached hunkWidth matches what the
// renderer actually uses.
func paneWidths(width int) (left, right int) {
	left = max(24, width/3)
	return left, max(30, width-left)
}

// paneWidthsFor is paneWidths honouring the left column's visibility: hidden, the
// stream gets everything. Not folded into paneWidths because the geometry pass
// and the render pass both have to agree with it, and a caller that forgot the
// flag would compute wrap widths for a pane that is not the size it thinks.
func (m Model) paneWidthsFor(width int) (left, right int) {
	if m.hideLeft {
		return 0, width
	}
	return paneWidths(width)
}

// Status returns the viewer's status text and whether it is an error, so a
// host can surface it in its own footer.
func (m Model) Status() (string, bool) { return m.status, m.statusErr }

// Filtering reports whether a text input owns the keyboard — the file filter or
// the comment compose box. A host must not treat keys as its own bindings while
// this is true; `q` and `esc` in particular belong to the input.
func (m Model) Filtering() bool {
	// FocusSearch counts: the diff's search prompt takes the keyboard the same way
	// the filter does, and a host that grabbed `q` or `esc` first would close the
	// view out from under someone typing a query. So does the publish prompt, where
	// `esc` and `q` mean "don't publish" and a host that took them would close the
	// whole view on someone who was declining to.
	// The merge prompt is the same bargain with a worse failure: `q` there means
	// "don't merge", and a host that closed the view instead would leave the
	// reviewer unsure which of the two had happened.
	return m.focus == FocusFilter || m.focus == FocusSearch || m.editing ||
		m.publishing || m.merging
}

// HelpVisible reports whether the `?` overlay is up. Like Filtering, it tells a
// host to keep its hands off the keyboard: `esc` and `q` close the overlay, and a
// host that grabbed them first would close the whole view instead — leaving no
// way out of the help but out of the diff.
func (m Model) HelpVisible() bool { return m.showHelp }

type diffLoadedMsg struct {
	files []diff.FileDiff
	// fingerprint identifies the raw diff text this message was built from, so
	// a reload that changed nothing can be dropped before it touches any view
	// state. Live refresh polls far more often than the diff actually changes.
	fingerprint uint64
	err         error
}

type autoRefreshTickMsg struct{}

func New(repoRoot string, loadFn func() (string, error), openFn OpenFunc) Model {
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.CharLimit = 128
	si := textinput.New()
	si.Placeholder = "search..."
	si.CharLimit = 128
	return Model{
		RepoRoot:        repoRoot,
		RefreshInterval: DefaultRefreshInterval,
		LoadDiff:        loadFn,
		OpenFile:        openFn,
		filterInput:     ti,
		searchInput:     si,
		visualAnchor:    visualNone,
		cache:           newRenderCache(),
		hl:              newHighlighter(),
		status:          "loading...",
		// Open on the diff itself. Reading the change is what you came for;
		// the file list is a jump index you reach for second.
		focus: FocusHunks,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadDiffCmd(m.LoadDiff), resolveBaseCmd(m.ResolveBase), scheduleRefresh(m.RefreshInterval))
}

// Base names what the diff is against, empty until the host's resolver answers
// (or always, when there is no resolver). A host's chrome should fall back to its
// own wording rather than showing a blank.
func (m Model) Base() string { return m.baseLabel }

// baseResolvedMsg carries the answer back from ResolveBase.
type baseResolvedMsg struct{ label string }

func resolveBaseCmd(fn func() string) tea.Cmd {
	if fn == nil {
		return nil
	}
	return func() tea.Msg { return baseResolvedMsg{label: fn()} }
}

func scheduleRefresh(d time.Duration) tea.Cmd {
	if d <= 0 {
		return nil
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return autoRefreshTickMsg{} })
}

func loadDiffCmd(fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		raw, err := fn()
		if err != nil {
			return diffLoadedMsg{err: err}
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(raw))
		return diffLoadedMsg{files: diff.ParseGitDiff(raw), fingerprint: h.Sum64()}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height-chromeHeight)
		return m, nil
	case diffLoadedMsg:
		m.refreshing = false
		if msg.err != nil {
			m.fail("error: %v", msg.err)
			return m, scheduleRefresh(m.RefreshInterval)
		}
		// Unchanged diff: leave every bit of view state alone. Live refresh
		// polls on a timer, so most reloads land here, and doing nothing is
		// what makes the polling invisible.
		if m.loaded && msg.fingerprint == m.fingerprint {
			m.statusErr = false
			return m, scheduleRefresh(m.RefreshInterval)
		}
		anchor, hadAnchor := m.captureAnchor()
		screenOffset := m.cursorRow - m.streamScroll

		first := !m.loaded
		m.loaded = true
		m.fingerprint = msg.fingerprint
		m.files = msg.files
		m.applyFilter()
		if first || !hadAnchor {
			m.resetStreamView()
			m.rebuildStream()
			m.cursorToFirstLine()
		} else {
			m.restoreAnchor(anchor, screenOffset)
		}
		// No file count: the file list is headed "Files (n)", so spending footer
		// width to say it again buys nothing. "no changes" stays, because an empty
		// view otherwise looks like a failure to load.
		m.status = ""
		if len(m.filtered) == 0 {
			m.status = "no changes"
		}
		m.statusErr = false
		return m, scheduleRefresh(m.RefreshInterval)
	case baseResolvedMsg:
		// Only overwrite a known label with another known one: a resolve that comes
		// back empty (jj erroring in a workspace that was fine a moment ago) should
		// leave the chrome saying what it said, not blank it.
		if strings.TrimSpace(msg.label) != "" {
			m.baseLabel = msg.label
		}
		return m, nil
	case autoRefreshTickMsg:
		// Comments are re-read on the tick, not gated on the diff changing: an
		// agent replying edits no files, so a fingerprint-gated reload would
		// never fire and a reply would sit invisible until the view was reopened.
		// The mirrored PR threads come along for the ride — a reviewer's comment
		// changes no files either.
		m.reloadComments()
		m.reloadThreads()
		if !m.refreshing {
			m.refreshing = true
			return m, loadDiffCmd(m.LoadDiff)
		}
		return m, scheduleRefresh(m.RefreshInterval)
	case composeEditedMsg:
		// Back from $EDITOR. Ignored if no box is open — the editor was suspending
		// the program, so nothing could have closed one, but a stale message must not
		// resurrect it.
		//
		// Two boxes can want this: the stream's comment box and the publish flow's
		// review-summary box. They are never open at once — with the stream box up
		// every key belongs to it, so `P` types a P — so which one is on screen is
		// enough to say where the body belongs.
		summary := m.publishing && m.publishStage == publishComposing
		if !m.editing && !summary {
			return m, nil
		}
		if msg.err != nil {
			m.fail("editor: %v", msg.err)
			return m, nil
		}
		if summary {
			m.summaryEditor.setBody(msg.body)
			return m, nil
		}
		m.editor.setBody(msg.body)
		return m, nil
	case publishDoneMsg:
		return m.applyPublishDone(msg)
	case mergeDoneMsg:
		return m.applyMergeDone(msg)
	case threadReplyDoneMsg:
		return m.applyThreadReplyDone(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	// Non-key messages the compose box needs — the cursor blink, chiefly. Without
	// this the box renders a static cursor, since nothing else routes them. The
	// review-summary box is a compose box too, and it is the one on screen when the
	// publish flow is at that stage.
	if m.publishing && m.publishStage == publishComposing {
		editor, cmd, _ := m.summaryEditor.update(msg)
		m.summaryEditor = editor
		return m, cmd
	}
	if m.editing {
		editor, cmd, _ := m.editor.update(msg)
		m.editor = editor
		return m, cmd
	}
	if m.focus == FocusFilter {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.applyFilter()
		return m, cmd
	}
	if m.focus == FocusSearch {
		// The blink, chiefly — but not applySearchInput: a non-key message cannot
		// have changed the query, and re-seeking on every blink would fight the
		// cursor.
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The compose box owns every key while it is up, including the ones that
	// would otherwise navigate or close.
	if m.editing {
		return m.handleEditorKey(msg)
	}
	key := msg.String()
	// The `-` chord swallows exactly the next key (see scope.go). Checked before
	// the modes below because it is one keypress deep and nothing else can be up
	// while it is.
	if m.scopePick {
		return m.handleScopeKey(key)
	}
	// So does `z`, and for the same reason — one keypress deep, nothing else up.
	if m.pendingZ {
		return m.handleZKey(key)
	}
	// The publish prompt owns the keyboard while it is up, for the same reason the
	// help overlay does: nothing behind it is navigable, and its `esc` means
	// "don't publish".
	if m.publishing {
		return m.handlePublishKey(msg)
	}
	// And so does the merge prompt, for the same reason: its `esc` means "don't
	// merge", and nothing behind it is navigable.
	if m.merging {
		return m.handleMergeKey(msg)
	}
	// The overlay owns the keyboard while it is up: nothing behind it is
	// navigable, so forwarding keys would move a cursor nobody can see. Scroll
	// keys go to its viewport instead.
	if m.showHelp {
		switch key {
		case "?", "esc", "q", "enter":
			m.showHelp = false
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.helpVP, cmd = m.helpVP.Update(msg)
		return m, cmd
	}
	if m.focus == FocusFilter {
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		case "esc", "enter":
			m.focus = FocusFiles
			m.filterInput.Blur()
			if key == "esc" {
				m.filterInput.SetValue("")
				m.applyFilter()
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			m.applyFilter()
			return m, cmd
		}
	}
	if m.focus == FocusSearch {
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			// Keep the query: n/N after confirming is the point of having one.
			m.endSearch(true)
			return m, nil
		case "esc":
			m.endSearch(false)
			return m, nil
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			// Previewed as you type, from where the search started — so the cursor
			// lands on the first match of what you have typed so far rather than
			// walking further down the file with every keystroke.
			m.applySearchInput()
			return m, cmd
		}
	}

	switch key {
	case "q", "ctrl+c", charm.PaneLeaveKey:
		// ctrl+\ because this view can be the program inside a pane — `awp diff`
		// in one, or the review window a workspace opens — and there the key that
		// gives the terminal back is the pane's, not q's. Under a handed-over pane
		// the deck is suspended and reading nothing, so if this switch does not
		// answer for it, nothing does.
		return m, tea.Quit
	case `\`:
		m.hideLeft = !m.hideLeft
		if m.hideLeft && m.focus != FocusHunks {
			// Focus cannot stay on a pane that is no longer drawn: the keys would
			// move a selection nobody can see.
			m.focus = FocusHunks
		}
		// The stream's width changed, so its wrap geometry has to be rebuilt —
		// SetSize is the only thing that knows how to derive hunkWidth.
		m.SetSize(m.width, m.bodyHeight)
		m.followCursor()
		return m, nil
	case "?":
		m.showHelp = true
		// Built on open rather than kept in sync: it is cheap, and it means the
		// reference is always laid out for the size the terminal is now, and always
		// opens at the top.
		m.helpVP = newHelpViewport(m.width, m.bodyHeight, m.scopeHelpRow(), m.HostKeys)
		return m, nil
	case "r":
		// `r` marked a manual refresh until live refresh made that redundant
		// (phase 2). It now marks the file at the cursor reviewed, which is the
		// gesture used most while working through a change.
		return m.toggleReviewed()
	case "-":
		// Only when a host installed more than one range to offer; otherwise the key
		// does nothing rather than opening a menu with one answer in it.
		if len(m.scopes) > 1 {
			m.scopePick = true
		}
		return m, nil
	case "ctrl+r":
		m.refreshing = true
		m.status = "refreshing…"
		// An explicit refresh re-resolves the base too: a rebase is exactly the
		// kind of thing you press this after, and it is the only thing that moves
		// the base.
		return m, tea.Batch(loadDiffCmd(m.LoadDiff), resolveBaseCmd(m.ResolveBase))
	case "/":
		// `/` means search to anyone who has used vim or less, and from the diff —
		// where nearly all the time goes — it used to mean "filter the file list".
		// It now does the thing the pane you are in makes it mean: search the
		// content here, filter the list from the lists.
		if m.focus == FocusHunks {
			m.beginSearch()
			return m, nil
		}
		m.focus = FocusFilter
		m.filterInput.Focus()
		return m, nil
	case "w":
		if m.sideBySide {
			// Refused out loud. One line-pair is one row is what the split layout is
			// built on, and a key that quietly did nothing would read as broken.
			m.status = "wrap is off in side-by-side — h/l pans"
			return m, nil
		}
		// Wrap changes how many rows each line occupies, so the geometry has
		// to be rebuilt and the scroll re-clamped against it. Wrapped lines
		// have no horizontal overflow left to pan over, so the column offset
		// is dropped rather than kept hidden.
		m.wrap = !m.wrap
		if m.wrap {
			m.hunkHScroll = 0
		}
		m.rebuildStream()
		return m, nil
	case "|":
		return m.toggleSideBySide()
	case "T":
		m.cycleThreadVisibility()
		return m, nil
	// Publishing is about the review as a whole, so it works from any pane — the
	// same reasoning as `T`. Capital, because it leaves the machine.
	case "P":
		m.beginPublish()
		return m, nil
	// Merging is about the PR as a whole, so it works from any pane too. Capital
	// for the same reason `P` is: it leaves the machine, and it is the one key here
	// that ends the change rather than describing it.
	case "M":
		return m, m.beginMerge()
	// Sending is about the review as a whole too, so it works from any pane — same
	// reasoning as T, P and M. Not capital, and it is the same key the compose box
	// uses for the one remark in it: inside the box ctrl+s means "save and send
	// this", out here it means "send what is waiting". One verb, one key, and the
	// two cannot collide because the box has the keyboard when it is open.
	case "ctrl+s":
		m.sendUnsent()
		return m, nil
	case "tab", "shift+tab":
		m.cycleFocus(key == "tab")
		return m, nil
	case "ctrl+d":
		m.pageDown()
		return m, nil
	case "ctrl+u":
		m.pageUp()
		return m, nil
	}

	if m.focus == FocusFiles {
		switch key {
		// The file list is an index into the stream: moving it seeks.
		case "j", "down":
			m.seekToFile(m.filesCursor + 1)
		case "k", "up":
			m.seekToFile(m.filesCursor - 1)
		// Drill into the selected file. tab does the same thing, but enter is
		// what a two-pane layout invites you to press on a list row.
		case "enter":
			m.focus = FocusHunks
		case "e":
			return m, m.openCurrentFile()
		}
	}

	if m.focus == FocusComments {
		switch key {
		// Same as the file list: moving the selection seeks the diff, so the
		// conversation is already on screen by the time you get there.
		case "j", "down":
			m.seekToComment(m.commentsCursor + 1)
		case "k", "up":
			m.seekToComment(m.commentsCursor - 1)
		// The cursor is already on the comment; enter just hands the keyboard to
		// the diff so replying and resolving work.
		case "enter":
			m.focus = FocusHunks
		// Delete acts through the cursor, which the index keeps parked on the
		// selected conversation — so this is the same gesture as `D` in the diff,
		// reachable from the list you are already scanning.
		case "D":
			return m.deleteFromIndex()
		// Resolving is the same gesture as deleting, for the same reason: the list
		// you scan conversations in is the list you settle them from.
		case "R":
			return m.resolveFromIndex()
		// And so is approving — the index is where a pending proposal announces
		// itself, so it has to be answerable without seeking into the diff.
		case "A":
			m.seekToComment(m.commentsCursor)
			return m.approveAtCursor()
		}
	}

	if m.focus == FocusHunks {
		if len(m.filtered) == 0 {
			return m, nil
		}
		switch key {
		// One continuous scroll over the whole diff — there is no file
		// boundary to stop at.
		case "j", "down":
			m.moveCursor(1)
		case "k", "up":
			m.moveCursor(-1)
		case "l", "right":
			m.scrollHunksHorizontally(hScrollStep)
		case "h", "left":
			m.scrollHunksHorizontally(-hScrollStep)
		case "0", "home":
			m.panToLineStart()
		case "$", "end":
			m.panToLineEnd()
		case "}", "]":
			m.jumpHunk(1)
		case "{", "[":
			m.jumpHunk(-1)
		// `z` opens the chord `zz` closes. Only in the diff: it is about where this
		// pane is scrolled, and the two lists scroll to follow their own selection.
		case "z":
			m.pendingZ = true
			return m, nil
		case "g":
			m.cursorRow = 0
			m.followCursor()
			m.syncFileCursorToCursor()
		case "G":
			m.cursorRow = max(0, len(m.stream.rows)-1)
			m.followCursor()
			m.syncFileCursorToCursor()
		// n/N step the search, wrapping at the ends. No-ops with no query rather
		// than reporting an error — pressing them idly is not a mistake worth a
		// message.
		case "n":
			if strings.TrimSpace(m.searchQuery) != "" {
				m.seekMatch(true, false)
			}
		case "N":
			if strings.TrimSpace(m.searchQuery) != "" {
				m.seekMatch(false, false)
			}
		// enter opens and closes the GitHub thread under the cursor. Free to bind
		// here: in the two lists enter hands the keyboard to the diff, and in the
		// diff it had no meaning at all.
		case "enter":
			return m.toggleThreadFold()
		// `v` starts a range at the cursor and the movement keys extend it — the
		// vim gesture, so there is one letter to learn and nothing else.
		case "v":
			m.toggleVisual()
			return m, nil
		case "esc":
			// Only meaningful with a range up. Pressing it idly in the diff is not a
			// mistake worth a message.
			if m.visualActive() {
				m.cancelVisual()
			}
			return m, nil
		case "c":
			return m.startComment()
		case "C":
			return m.startFileComment()
		case "i":
			return m.startEdit()
		case "D":
			return m.deleteCommentAtCursor()
		case "R":
			return m.toggleResolved()
		case "A":
			return m.approveAtCursor()
		case "e":
			return m, m.openAtCursor()
		}
	}
	return m, nil
}

func (m *Model) applyFilter() {
	needle := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	m.filtered = m.filtered[:0]
	if needle == "" {
		m.filtered = append(m.filtered, m.files...)
	} else {
		for _, f := range m.files {
			if strings.Contains(strings.ToLower(diff.DisplayPath(f)), needle) {
				m.filtered = append(m.filtered, f)
			}
		}
	}
	if m.filesCursor >= len(m.filtered) {
		m.filesCursor = max(0, len(m.filtered)-1)
		m.resetStreamView()
	}
	// The file set just changed, so the geometry has to be rebuilt — and this
	// must happen on every path out of applyFilter, including the unfiltered
	// one.
	m.rebuildStream()
}

func (m *Model) pageDown() {
	step := m.pageStep()
	switch m.focus {
	case FocusHunks:
		m.moveCursor(step)
	case FocusComments:
		m.seekToComment(min(len(m.commentIndex)-1, m.commentsCursor+step))
	default:
		if len(m.filtered) > 0 {
			m.seekToFile(min(len(m.filtered)-1, m.filesCursor+step))
		}
	}
}

func (m *Model) pageUp() {
	step := m.pageStep()
	switch m.focus {
	case FocusHunks:
		m.moveCursor(-step)
	case FocusComments:
		m.seekToComment(max(0, m.commentsCursor-step))
	default:
		m.seekToFile(max(0, m.filesCursor-step))
	}
}

// resetStreamView returns the stream to its start. Used when the diff
// reloads or the filter changes the file set out from under the scroll.
func (m *Model) resetStreamView() {
	m.streamScroll = 0
	m.cursorRow = 0
	m.hunkHScroll = 0
}

// cursorToFirstLine puts the cursor on the first row of actual diff content,
// skipping the leading file divider and hunk header. You open a diff to read
// code, and every gesture that acts on the cursor — commenting especially —
// needs a line rather than a header.
func (m *Model) cursorToFirstLine() {
	for i, r := range m.stream.rows {
		if r.kind == rowLine {
			m.cursorRow = i
			m.followCursor()
			m.syncFileCursorToCursor()
			return
		}
	}
}

// cursorToFileFirstLine puts the cursor on the first diff line at or after
// file i's divider, which is where a reader wants to be after the row count
// changed under them.
//
// Searching forward from the divider rather than within the file is what makes
// one rule cover both directions of `r`: a file just collapsed has nothing but a
// divider, so the first line found belongs to the next file; a file just expanded
// has its own lines, so the first line found is its own. Falls back to the
// nearest line above when there is nothing after — collapsing the last file — and
// leaves the cursor on the divider when the whole change has no lines left to
// land on.
func (m *Model) cursorToFileFirstLine(i int) {
	if i < 0 || i >= len(m.stream.fileStart) {
		return
	}
	from := m.stream.fileStart[i]
	m.cursorRow = from
	for r := from; r < len(m.stream.rows); r++ {
		if m.stream.rows[r].kind == rowLine {
			m.cursorRow = r
			break
		}
	}
	if m.stream.rows[m.cursorRow].kind != rowLine {
		for r := from - 1; r >= 0; r-- {
			if m.stream.rows[r].kind == rowLine {
				m.cursorRow = r
				break
			}
		}
	}
	m.hunkHScroll = 0
	m.clampCursor()
	m.followCursor()
	m.syncFileCursorToCursor()
}

// scrollHunksHorizontally pans the stream's line content by delta columns.
// No-op under wrap, where nothing overflows the pane.
func (m *Model) scrollHunksHorizontally(delta int) {
	if m.wrap {
		return
	}
	m.hunkHScroll = max(0, m.hunkHScroll+delta)
	m.clampHunkHScroll()
}

// panToLineStart returns the pane to the left edge (vim's `0`).
func (m *Model) panToLineStart() {
	if m.wrap {
		return
	}
	m.hunkHScroll = 0
}

// panToLineEnd pans as far right as the clamp allows (vim's `$`).
func (m *Model) panToLineEnd() {
	if m.wrap {
		return
	}
	m.hunkHScroll = m.maxHunkHScroll()
}

// clampHunkHScroll stops the pan before the longest line has scrolled
// entirely out of view, so the pane can't be panned into empty space.
func (m *Model) clampHunkHScroll() {
	m.hunkHScroll = min(m.hunkHScroll, m.maxHunkHScroll())
}

// maxHunkHScroll is the furthest the pane may pan: enough to reach the end of
// the longest line in the current file while keeping minVisibleColumns of it
// on screen.
func (m Model) maxHunkHScroll() int {
	f, ok := m.currentFile()
	if !ok {
		return 0
	}
	return max(0, maxContentWidth(f)-minVisibleColumns)
}

// maxContentWidth is the display width of the longest line in a file's
// hunks. Measured on the raw content (no styling applied yet), so it needs
// no ANSI handling.
func maxContentWidth(f diff.FileDiff) int {
	widest := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			widest = max(widest, lipgloss.Width(l.Content))
		}
	}
	return widest
}

// moveCursor moves the line cursor by delta rows and lets the viewport follow.
func (m *Model) moveCursor(delta int) {
	m.cursorRow += delta
	m.clampCursor()
	m.followCursor()
	m.syncFileCursorToCursor()
}

func (m *Model) clampCursor() {
	m.cursorRow = min(max(m.cursorRow, 0), max(0, len(m.stream.rows)-1))
}

// followCursor scrolls the viewport the minimum needed to keep the cursor
// visible, holding cursorScrollMargin rows of lookahead where the stream is
// long enough to afford it.
func (m *Model) followCursor() {
	height := m.streamContentHeight()
	margin := cursorScrollMargin
	// A margin only makes sense if it fits: on a short pane, insisting on
	// lookahead would fight the clamp forever.
	if height <= 2*margin+1 {
		margin = 0
	}
	if top := m.cursorRow - margin; top < m.streamScroll {
		m.streamScroll = top
	}
	if bottom := m.cursorRow + margin; bottom > m.streamScroll+height-1 {
		m.streamScroll = bottom - height + 1
	}
	m.clampStreamScroll()
}

// centerCursor scrolls so the cursor's row sits in the middle of the pane,
// rather than the minimum distance followCursor would move.
//
// Used when the cursor *arrives* somewhere it was sent — selecting a
// conversation from the index — because a minimal scroll puts the thing you
// asked for on the last row of the pane, showing its first line and nothing
// else. Centering gives it the bottom half of the pane to open into and keeps
// the code it is about in view above it.
//
// Not used when the cursor *moves* under j/k: re-centering on every row would
// scroll the text under a stationary cursor, which reads as the page sliding
// rather than the cursor walking. The clamp handles both ends — near the top
// there is nothing to scroll away, so the row simply sits where it falls.
func (m *Model) centerCursor() {
	m.streamScroll = m.cursorRow - m.streamContentHeight()/2
	m.clampStreamScroll()
}

// handleZKey answers the key that follows `z`.
//
// Only `zz` means anything; everything else cancels, esc included, the same
// bargain handleScopeKey makes. A mistyped second key must not fall through and
// do whatever that letter means on its own — `zc` silently opening the compose
// box is worse than `zc` doing nothing.
//
// Centring is what the comment index already does when it seeks (see
// centerCursor), so this is the same gesture reached deliberately rather than as
// a side effect of jumping somewhere. The cursor does not move: `zz` is about
// where the pane is scrolled, not where you are.
func (m Model) handleZKey(key string) (tea.Model, tea.Cmd) {
	m.pendingZ = false
	if key != "z" {
		return m, nil
	}
	m.centerCursor()
	return m, nil
}

func (m *Model) clampStreamScroll() {
	m.streamScroll = min(m.maxStreamScroll(), max(0, m.streamScroll))
}

// maxStreamScroll is the furthest the stream can scroll: far enough to bring
// any row — including the last — to the top.
//
// Stopping earlier (when the final row reaches the *bottom*) would avoid the
// blank space below the end, but it also makes a late file's header
// unreachable: seeking to the last file would clamp short of it, leaving the
// file list pointing at one file while the cursor sits in another. Vim scrolls
// this way for the same reason.
func (m Model) maxStreamScroll() int {
	return max(0, len(m.stream.rows)-1)
}

func (m Model) streamContentHeight() int {
	return max(1, m.bodyHeight)
}

// syncFileCursorToCursor points the file list at whichever file the cursor is
// in. The two directions are deliberately one-way each — seeking moves the
// cursor, moving the cursor updates the file list — so they cannot feed back
// into each other.
func (m *Model) syncFileCursorToCursor() {
	if len(m.stream.rows) == 0 {
		m.filesCursor = 0
		return
	}
	// Not every row belongs to a file. The review-summary section at the top of the
	// stream and the detached section at the foot are both remarks about no
	// particular file, and their rows say so with file -1. Copying that into the file
	// cursor made it an index into m.filtered that indexes nothing: `ctrl+d` to the
	// bottom of a change with a detached comment, then `h`, and the pan's clamp asked
	// for the current file and took the whole deck down with an index out of range.
	//
	// So a row with no file leaves the file cursor alone. Where you were is the only
	// answer that is true — you scrolled past the end of the last file, you did not
	// move to another one — and it keeps the file list's selection where the reader
	// left it instead of snapping to the top of the change.
	if file := m.stream.fileAt(m.cursorRow); file >= 0 {
		m.filesCursor = file
	}
}

// seekToFile puts the cursor on a file's divider row.
func (m *Model) seekToFile(i int) {
	if i < 0 || i >= len(m.stream.fileStart) {
		return
	}
	m.filesCursor = i
	m.cursorRow = m.stream.fileStart[i]
	m.hunkHScroll = 0
	m.clampCursor()
	m.followCursor()
}

// jumpHunk moves the cursor to the next or previous hunk header anywhere in the
// diff — with one continuous stream, hunk hops are not confined to a file.
func (m *Model) jumpHunk(delta int) {
	var target int
	if delta > 0 {
		target = m.stream.nextHunkStart(m.cursorRow)
	} else {
		target = m.stream.prevHunkStart(m.cursorRow)
	}
	if target < 0 {
		return
	}
	m.cursorRow = target
	m.clampCursor()
	m.followCursor()
	m.syncFileCursorToCursor()
}

// currentFile is the file the cursor is on, if any.
func (m Model) currentFile() (diff.FileDiff, bool) {
	// Both ends. The upper bound was always checked; the lower one was not, and a
	// file cursor of -1 is expressible — the stream's fileless rows carry it (see
	// syncFileCursorToCursor). One panic in a Bubble Tea program takes the whole deck
	// with it, so the guard belongs at the read as well as at the write.
	if len(m.filtered) == 0 || m.filesCursor < 0 || m.filesCursor >= len(m.filtered) {
		return diff.FileDiff{}, false
	}
	return m.filtered[m.filesCursor], true
}

func (m Model) pageStep() int {
	return max(1, m.bodyHeight/2)
}

func (m Model) openCurrentFile() tea.Cmd {
	// Through currentFile rather than indexing m.filtered directly, so the bounds are
	// checked in one place: `e` on a row belonging to no file used to panic here for
	// exactly the same reason the pan did.
	f, ok := m.currentFile()
	if !ok || m.OpenFile == nil {
		return nil
	}
	return m.OpenFile(m.RepoRoot, m.resolveFilePath(f), diff.FirstChangedLine(f))
}

// openAtCursor opens the file for the row the cursor is on, at the line that
// row shows.
func (m Model) openAtCursor() tea.Cmd {
	if len(m.stream.rows) == 0 || m.OpenFile == nil {
		return nil
	}
	r := m.stream.rows[min(max(m.cursorRow, 0), len(m.stream.rows)-1)]
	if r.file < 0 || r.file >= len(m.filtered) {
		return nil
	}
	f := m.filtered[r.file]
	line := diff.FirstChangedLine(f)
	switch r.kind {
	case rowLine:
		// A removed line has no new-side number; fall back to the old one so
		// the editor still lands in the right neighbourhood.
		if r.newNo > 0 {
			line = r.newNo
		} else if r.oldNo > 0 {
			line = r.oldNo
		}
	case rowHunkHeader:
		if r.hunk >= 0 && r.hunk < len(f.Hunks) {
			line = diff.HunkChangedLine(f.Hunks[r.hunk])
		}
	}
	return m.OpenFile(m.RepoRoot, m.resolveFilePath(f), line)
}

func (m Model) resolveFilePath(f diff.FileDiff) string {
	p := f.NewPath
	if p == "" {
		p = f.OldPath
	}
	if m.RepoRoot == "" || p == "" {
		return p
	}
	return filepath.Join(m.RepoRoot, filepath.FromSlash(p))
}

// View satisfies tea.Model. Bubble Tea v2 asks the view to declare the
// terminal features it wants, so alt-screen is stated here rather than
// as a tea.NewProgram option. The content itself comes from render, which
// stays a plain string so tests and the panel helpers can call it.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	if m.width == 0 {
		return "loading..."
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		m.Body(m.width, m.bodyHeight),
		m.renderFooter(),
	)
}

// Body renders just the two panes — file list and hunks — at the given
// size, without this model's header or footer. A host that owns its own
// chrome (the deck's `c` modal) renders this instead of View.
func (m Model) Body(width, height int) string {
	if width <= 0 {
		return ""
	}
	height = max(minBodyHeight, height)
	if m.showHelp {
		// In place of the panes rather than over them, so the body keeps the exact
		// height the host budgeted and the footer does not move.
		return renderHelpOverlay(m.helpVP, width, height)
	}
	if m.publishing {
		return m.renderPublishOverlay(width, height)
	}
	if m.merging {
		return m.renderMergeOverlay(width, height)
	}
	leftWidth, rightWidth := m.paneWidthsFor(width)
	if m.hideLeft {
		// No JoinHorizontal with an empty left block: lipgloss would still
		// contribute its zero-width column, and the stream is already the full
		// width, so there is nothing to join.
		return m.renderStreamPanel(rightWidth, height)
	}
	// The compose box is a run of rows inside the stream (see withEditor), not a
	// panel docked below it, so the body's size never changes while writing a
	// comment and the box sits against the code it is about.
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.renderLeftColumn(leftWidth, height),
		m.renderStreamPanel(rightWidth, height),
	)
}

var (
	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Accent)).Padding(0, 1)
	styleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	// styleSelectedIdle is the same `┃` marker in a pane the keyboard has left.
	//
	// The bar still has to be there — it is where you come back to — but in the
	// selection hue it was the brightest thing in a pane whose keys are dead, and it
	// slides down the diff as the file list or comment index is driven. Muted, it
	// reads as a bookmark instead of as the thing you are moving.
	styleSelectedIdle = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleDim          = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleMuted        = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	stylePathDir      = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	// The stream's file divider is a structural header, so it carries the
	// accent hue (see the design system in CLAUDE.md) — or the selection hue
	// when it is the file the cursor is in.
	styleFileRule        = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent))
	styleFileRuleBase    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true)
	styleFileRuleCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning))
	styleFileRuleCurBase = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	stylePathBase        = lipgloss.NewStyle().Bold(true)
	styleAdded           = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success))
	styleDeleted         = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger))
	styleContext         = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	// styleCode is a syntax-painted line's base — the terminal's own foreground.
	//
	// Deliberately uncoloured. In that treatment the change type is not on the body
	// at all; the gutter glyph and the line numbers carry it, and anything the lexer
	// had no opinion about stays the colour ordinary text is. Tinting the base by
	// change type as well is what the flag's other setting does, and it fights every
	// hue the lexer puts on top of it.
	styleCode   = lipgloss.NewStyle()
	styleLineNo = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	// Cursorline: the row the cursor is on takes a subtle background across the
	// full pane width, vim-style. Every style used on that row has to carry the
	// background itself — an enclosing style can't provide it, because the
	// inner styles each end with a reset that would clear it mid-row.
	//
	// charm.Cursorline is an adaptive colour a hair off the terminal
	// background. BgPanel (ANSI 0) was tried first and reads far too strong:
	// it is sized for chip fills, where contrast is the point.
	cursorlineBg      = charm.Cursorline
	styleCursorLineNo = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Background(cursorlineBg)
	styleCursorFill   = lipgloss.NewStyle().Background(cursorlineBg)
	// A comment's hue says what kind of remark it is — what the reader is expected
	// to do about it. It lands on the left bar and the header only. Authorship is
	// carried by the 🤖 marker on the body instead, which frees the colour for the
	// thing a label cannot convey at a glance.
	//
	// A plain comment is Info-hued so it reads as annotation rather than as diff
	// content — nothing in a diff line is ever blue. A suggestion proposes a
	// change, so it takes Danger, the hue the app already uses for "this needs
	// doing". A question is waiting on an answer, which is Warning's role. Praise
	// asks for nothing, so it takes Success — the one hue in the palette that does
	// not mean "deal with me", which is the whole distinction the kind exists to
	// draw.
	styleCommentHead    = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Info)).Bold(true)
	styleSuggestionHead = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Bold(true)
	styleQuestionHead   = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	stylePraiseHead     = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success)).Bold(true)
	// The prose itself is not tinted. A whole paragraph in the kind's hue was
	// harder to read against the block's fill and no more informative than a
	// coloured edge, so the signal lives on the bar and the label while the body
	// takes the palette's emphasized-text token — which needs to be explicit
	// rather than terminal-default, since these cells carry a background.
	styleCommentText = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Strong))
	// Comments are painted across the full width so they read as blocks set into
	// the diff rather than as loose text between code lines. BgPanel is the
	// palette's chip background — a comment box is exactly that — which keeps
	// charm.Cursorline the only non-ANSI-16 value in the palette.
	commentBg         = lipgloss.Color(charm.BgPanel)
	styleCommentFill  = lipgloss.NewStyle().Background(commentBg)
	styleOrphanHeader = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	styleReviewHeader = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true)
	// Muted, not the header's accent: the stand-in is a hint about a section with
	// nothing in it, and it sits at the top of every diff. Anything brighter would
	// be the first thing the eye lands on, every time, saying nothing happened.
	styleReviewEmpty   = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted))
	styleAddedCursor   = styleAdded.Background(cursorlineBg)
	styleDeletedCursor = styleDeleted.Background(cursorlineBg)
	styleContextCursor = styleContext.Background(cursorlineBg)
	styleCodeCursor    = styleCode.Background(cursorlineBg)
	// The backwash. A painted line's change type lives in its background, because
	// the lexer has taken the foreground — see charm.AddedBg. Four, because the
	// cursor's row is a step brighter than the row beneath it whatever kind of line
	// it is.
	styleCodeAdded         = styleCode.Background(charm.AddedBg)
	styleCodeRemoved       = styleCode.Background(charm.RemovedBg)
	styleCodeAddedCursor   = styleCode.Background(charm.AddedBgCursor)
	styleCodeRemovedCursor = styleCode.Background(charm.RemovedBgCursor)
	styleHunkHeaderCursor  = styleHunkHeader.Background(cursorlineBg)
	styleSelectedCursor    = styleSelected.Background(cursorlineBg)
	styleStatus            = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Muted)).Padding(0, 1)
	styleStatusErr         = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Padding(0, 1)
	styleHunkHeader        = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true)
	styleFocusBorder       = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(charm.Accent))
	styleNormalBorder      = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(charm.Muted))
	styleAddedBadge        = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Success)).Bold(true).Padding(0, 1)
	styleDeletedBadge      = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Danger)).Bold(true).Padding(0, 1)
	styleModifiedBadge     = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true).Padding(0, 1)
	styleRenameBadge       = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Accent)).Bold(true).Padding(0, 1)
	styleSelectedBadge     = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true).Padding(0, 1)
	styleSelectedPathDir   = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	styleSelectedPathBase  = lipgloss.NewStyle().Foreground(lipgloss.Color(charm.Warning)).Bold(true)
	// The left column's rows carry the same cursorline band the diff's do, so
	// every style that can land on a selected row needs a variant holding the
	// background — including the muted ones, since a rename arrow or an index
	// row's summary sits mid-row and an unpainted cell there is a hole in the
	// band.
	styleSelectedPathDirCursor  = styleSelectedPathDir.Background(cursorlineBg)
	styleSelectedPathBaseCursor = styleSelectedPathBase.Background(cursorlineBg)
	styleSelectedBadgeCursor    = styleSelectedBadge.Background(cursorlineBg)
	styleMutedCursor            = styleMuted.Background(cursorlineBg)
)

// Subject is what a standalone view is a review of, so its header can answer the
// same questions the deck's footer does. Empty fields are omitted rather than
// shown blank.
type Subject struct {
	// Workspace is the workspace the working directory sits in, empty in a plain
	// repo — which is a legitimate place to read a diff, so it is not an error.
	Workspace string
	// PR is the pinned pull request as `repo#number`, empty when there is none.
	PR string
}

func (m Model) renderHeader() string {
	name := filepath.Base(m.RepoRoot)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = m.RepoRoot
	}
	if name == "" {
		name = "current repo"
	}
	// "review", not "diff": this is the same surface the deck's `c` opens, and
	// calling it a diff undersold what the keys here do.
	//
	// The segments answer, in order, the questions a reviewer has on arriving:
	// what am I looking at, whose change, which PR, and against what. The deck
	// says the same things in its footer; here there is no deck footer to say
	// them, and the header is the only chrome above the panes.
	segs := []string{"awp review", name}
	if m.Subject.Workspace != "" {
		segs = append(segs, m.Subject.Workspace)
	}
	if m.Subject.PR != "" {
		segs = append(segs, m.Subject.PR)
	}
	switch {
	case m.baseLabel != "":
		// The base it resolved — "vs main" — says what you are reading against, where
		// the scope's own name only says how it was picked.
		segs = append(segs, "vs "+m.baseLabel)
	case m.ScopeLabel() != "":
		// Until the resolve lands, and permanently for a scope with no base to name.
		segs = append(segs, m.ScopeLabel())
	}
	return styleHeader.Render(" " + strings.Join(segs, " · ") + " ")
}

func (m Model) renderFooter() string {
	// One affordance, not a legend. The full keymap lives behind `?` — see
	// help.go for why it stopped being spelled out on every frame.
	hint := "? help"
	filterLine := strings.Repeat(" ", max(1, m.width))
	if m.scopePick {
		// The chord owns the whole footer for the one keypress it lives: the
		// alternatives are the only thing worth saying, and nothing else is readable
		// in that moment anyway.
		return lipgloss.JoinVertical(lipgloss.Left, filterLine, styleDim.Render(m.scopeMenuHint()))
	}
	switch m.focus {
	case FocusFilter:
		// The filter is one of the two modes worth spelling out: it is modal, and
		// the keys that leave it are not the ones that work anywhere else.
		hint = "enter:confirm  esc:clear"
		filterLine = "  Filter files: " + m.filterInput.View()
	case FocusSearch:
		hint = "enter:keep  esc:cancel  n/N:next/prev"
		filterLine = "  Search diff: " + m.searchInput.View()
	}
	statusStyle := styleStatus
	if m.statusErr {
		statusStyle = styleStatusErr
	}
	st := statusStyle.Render(m.status)
	footerLine := lipgloss.JoinHorizontal(lipgloss.Left, st, "  ", styleDim.Render(hint))
	return lipgloss.JoinVertical(lipgloss.Left, filterLine, footerLine)
}

func (m Model) renderFileList(width, height int) string {
	border := styleNormalBorder
	if m.focus == FocusFiles {
		border = styleFocusBorder
	}
	rows := []string{styleDim.Render(fmt.Sprintf(" Files (%d)", len(m.filtered)))}
	// The window is over tree rows, not files: directory headings take rows of
	// their own, so counting in files would scroll by the wrong amount and could
	// leave the cursor's row off screen.
	tree := fileTreeRows(m.filtered)
	start, end := visibleRange(treeRowOf(tree, m.filesCursor), max(1, height-2), len(tree))
	contentWidth := width - 4
	for i := start; i < end; i++ {
		r := tree[i]
		if r.isDir() {
			rows = append(rows, renderTreeDir(r, contentWidth))
			continue
		}
		selected := r.file == m.filesCursor
		// The band is painted only while this pane holds the keyboard — the same
		// rule the diff pane follows, so there is never more than one band on
		// screen to mistake for the active selection.
		band := selected && m.focus == FocusFiles
		// No outer selected-row wrap: renderFileRow already emits per-segment
		// ANSI attributes, so an enclosing style can't add bold to text that
		// has already declared its own.
		row := m.renderFileRow(m.filtered[r.file], r, contentWidth, selected, band)
		if band {
			row = bandRow(row, width-2)
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return panelBox(rows, width, height, border)
}

// renderTreeDir is a directory heading: structure rather than a destination, so
// it is muted and reserves the selection columns without ever filling them.
func renderTreeDir(r fileTreeRow, width int) string {
	indent := treeIndent(r.depth, width)
	avail := max(1, width-lipgloss.Width(selectionPrefixBlank)-len(indent))
	return selectionPrefixBlank + indent + styleMuted.Render(truncate(r.label, avail))
}

// renderFileRow draws one file. The tree row supplies its indent and the name to
// show — the basename, since the heading above it already said the rest.
func (m Model) renderFileRow(f diff.FileDiff, r fileTreeRow, width int, selected, band bool) string {
	// The `┃ ` bar is the app-wide selection marker (see the design system in
	// CLAUDE.md). Unselected rows reserve the same two columns so labels stay
	// aligned down the list.
	prefix := selectionPrefixBlank
	switch {
	case band:
		prefix = styleSelectedCursor.Render(selectionPrefixBar)
	case selected:
		prefix = styleSelected.Render(selectionPrefixBar)
	}
	indent := treeIndent(r.depth, width)
	width -= lipgloss.Width(indent)
	if band {
		// The indent is part of the row, so it has to carry the band like
		// everything else on it.
		indent = styleCursorFill.Render(indent)
	}
	badge := statusBadge(f.Status, selected, band)
	avail := width - lipgloss.Width(selectionPrefixBlank) - lipgloss.Width(badge) - 1
	name := renderPath(r.label, avail, selected, band)
	return prefix + indent + badge + gap(band) + name
}

// gap is the single space between a row's segments, painted when the row carries
// the band so it does not read as a notch cut out of it.
func gap(band bool) string {
	if band {
		return styleCursorFill.Render(" ")
	}
	return " "
}

// bandRow pads a banded row out to the pane's full inner width, so the
// cursorline reaches the border on both sides rather than stopping wherever the
// text happened to end.
//
// Padded here rather than left to the bordered block's own Width: lipgloss pads
// with unstyled spaces, which would leave the right end of the band unpainted.
func bandRow(row string, innerWidth int) string {
	if pad := innerWidth - lipgloss.Width(row); pad > 0 {
		row += styleCursorFill.Render(strings.Repeat(" ", pad))
	}
	return row
}

func statusBadge(status string, selected, band bool) string {
	var style lipgloss.Style
	switch status {
	case "A":
		style = styleAddedBadge
	case "D":
		style = styleDeletedBadge
	case "R":
		style = styleRenameBadge
	default:
		style = styleModifiedBadge
	}
	switch {
	case band:
		style = styleSelectedBadgeCursor
	case selected:
		style = styleSelectedBadge
	}
	return style.Render(status)
}

func renderPath(path string, width int, selected, band bool) string {
	dirStyle, baseStyle, sepStyle := stylePathDir, stylePathBase, styleMuted
	switch {
	case band:
		dirStyle, baseStyle, sepStyle = styleSelectedPathDirCursor, styleSelectedPathBaseCursor, styleMutedCursor
	case selected:
		dirStyle, baseStyle = styleSelectedPathDir, styleSelectedPathBase
	}
	return renderPathWith(path, width, dirStyle, baseStyle, sepStyle)
}

// renderPathWith renders a path with caller-chosen styles for its directory
// and basename, so surfaces needing a different hue (the stream's file
// divider) don't re-implement rename-arrow splitting and truncation.
//
// sepStyle is the rename arrow's, which the caller has to choose too: it sits
// between the two halves of the row, so a banded row needs it carrying the
// background like everything else on that line.
func renderPathWith(path string, width int, dirStyle, baseStyle, sepStyle lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if strings.Contains(path, " → ") {
		parts := strings.SplitN(path, " → ", 2)
		left := renderSinglePathWith(parts[0], max(1, (width-3)/2), dirStyle, baseStyle)
		right := renderSinglePathWith(parts[1], max(1, width-lipgloss.Width(left)-3), dirStyle, baseStyle)
		return truncateStyled(left+sepStyle.Render(" → ")+right, width)
	}
	return renderSinglePathWith(path, width, dirStyle, baseStyle)
}

func renderSinglePathWith(path string, width int, dirStyle, baseStyle lipgloss.Style) string {
	path = truncate(path, width)
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if dir == "." || dir == string(filepath.Separator) {
		dir = ""
	}
	if dir == "" {
		return baseStyle.Render(base)
	}
	return dirStyle.Render(dir+"/") + baseStyle.Render(base)
}

func hunkLineNumberWidths(h diff.Hunk) (int, int) {
	oldWidth, newWidth := 1, 1
	oldLine, newLine := h.OldStart, h.NewStart
	for _, l := range h.Lines {
		switch l.Type {
		case '+':
			newWidth = max(newWidth, len(strconv.Itoa(newLine)))
			newLine++
		case '-':
			oldWidth = max(oldWidth, len(strconv.Itoa(oldLine)))
			oldLine++
		default:
			oldWidth = max(oldWidth, len(strconv.Itoa(oldLine)))
			newWidth = max(newWidth, len(strconv.Itoa(newLine)))
			oldLine++
			newLine++
		}
	}
	return oldWidth, newWidth
}

func lineNoText(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func truncateStyled(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func visibleRange(cursor, height, total int) (int, int) {
	if total == 0 {
		return 0, 0
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > total {
		end = total
		start = max(0, end-height)
	}
	return start, end
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
