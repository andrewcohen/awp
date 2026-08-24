package deckui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/vterm"
)

// PaneLeaveKey gives the keyboard back to the deck. See charm.PaneLeaveKey for
// why it is that key and why it is declared a package down.
//
// Kept here as well because this is where the deck's readers look, and because
// every reference in this package predates the move. Re-exported rather than
// re-spelled: the pane chrome's hint and the key awp's own hosted programs bind
// have to be one string or the hint is a lie.
const PaneLeaveKey = charm.PaneLeaveKey

// PaneMenuKey opens the menu of things you can do to what is on screen. See
// charm.PaneMenuKey for why it is the shifted leave key, and paneMenuPressed for
// why pressing it is not the same question as naming it.
const PaneMenuKey = charm.PaneMenuKey

// alternateKey goes to the arrangement before this one, in both ctrl+b menus.
//
// The same letter the deck's row list binds for the same act, because it is the
// same act — the pane you were in two arrangements ago — and a verb that needed a
// different key depending on whether you were looking at the deck or at a program
// would be two things to learn for one idea. TestTheMenusAlternateKeyIsTheDecksL
// is what keeps the two spellings one.
const alternateKey = "L"

// paneMenuPressed reports whether this key is the menu key.
//
// A predicate rather than a string comparison against msg.String(), because that is
// where the terminals' disagreements were resolved when the menu was ctrl+| — read
// as `|` with ctrl by a terminal that resolves the shift, as `\` with ctrl and shift
// by one that does not, and as the leave key by one that reports neither. ctrl+b has
// none of that: 0x02 is 0x02 everywhere. Kept as a function anyway, since it is the
// one place the question is asked and the next key that wants a condition on it will
// want it here.
func paneMenuPressed(_ *Model, msg tea.KeyPressMsg) bool {
	return msg.Mod&tea.ModCtrl != 0 && msg.Code == 'b'
}

// PaneBackend turns a workspace and a window kind into a process the deck can
// host on a pty it owns, instead of handing off to a tmux window.
//
// The deck's UI does not change when one is present — the same keys do the
// same conceptual thing. Only where the process lives changes, which is why
// this is an interface and not a fork of the deck.
type PaneBackend interface {
	// Open returns the command for the item's pane of this kind, sized w×h,
	// plus a func that undoes anything Open had to set up. kind is the same
	// string ActionOpenWindow uses: "agent", "editor", "vcs", or "" for a
	// shell.
	Open(item Item, kind string, w, h int) (cmd *exec.Cmd, restore func(), err error)
	// Describes reports whether this backend handles the kind. Anything it
	// declines falls through to the ordinary tmux-window path, so review
	// windows and the PR-description window keep working unchanged.
	Describes(kind string) bool
}

// PaneSession is one long-lived process a backend is holding, described in the
// deck's own terms rather than the backend's.
type PaneSession struct {
	// Item is the deck row this session belongs to, already resolved by the
	// backend. Zero when no row matches — a session can outlive the workspace
	// it was started for.
	//
	// Resolved by the backend rather than matched here on purpose: session
	// names are sanitized (a workspace with a dot in it is not spelled the
	// same in its session name), so matching means knowing the naming scheme.
	// Doing it in the deck would be a second copy of that scheme, and the one
	// that drifts. HasItem says whether it found one.
	Item    Item
	HasItem bool
	// Name is the backend's own identifier for this session, opaque to the deck
	// and only ever handed straight back to the backend — EndSession takes it.
	//
	// Separate from Label because they answer different questions: Label is what
	// a human reads on a row, Name is what the substrate will accept. Deriving
	// one from the other would put the naming scheme in a second place, which is
	// what resolving Item in the backend already exists to avoid.
	Name string
	// Label is what to call this session on screen, from the session's own
	// name — so a session with no surviving row is still nameable.
	Label string
	Kind  string
	// Live is false for a session whose command has exited. zmx keeps such a
	// session listed so its output can still be read, so "listed" and
	// "running" are genuinely different questions.
	Live bool
	// Attached is true while some client has it open — including this deck.
	Attached bool
	PID      int
	Started  time.Time
	// Cmd is what the session is running, for a display that wants to say so.
	Cmd string
}

// PaneSessioner is a PaneBackend that can say which sessions it is holding.
//
// Separate from PaneBackend because the tmux deck has no answer: it hosts no
// sessions of its own, and the keys that depend on this (z, and eventually the
// row model's live-session marks) are only bound when a backend implements it.
// That is also what keeps the deck from growing a second path to zmx — the
// backend it already holds is the one place this is asked.
type PaneSessioner interface {
	PaneBackend
	// Sessions lists what is live now, resolving each against the deck's rows.
	//
	// Called on demand rather than cached: a session can appear or die without
	// the deck doing anything. items is passed in so the backend can tie each
	// session to a row using its own naming scheme, which is the only place
	// that scheme is written down.
	Sessions(items []Item) ([]PaneSession, error)
	// EndSession stops the session with this PaneSession.Name, killing whatever
	// is running in it.
	//
	// The deck needs this because a session outlives every deck that opened it:
	// nothing else removes one, so without it the only way to stop an agent is to
	// leave awp and run the multiplexer's own command. Deleting a workspace reaps
	// its sessions as part of the delete, so this is for the ones no delete will
	// ever cover — a session whose workspace is already gone, or an agent you
	// just want stopped.
	EndSession(name string) error
}

// panePopover is a hosted process shown in place of the deck body.
type panePopover struct {
	// term is the interface rather than *vterm.Term so which emulator interprets
	// the pane is vterm.Open's decision, not this struct's. See vterm.Hosted.
	term  vterm.Hosted
	label string
	// kind is the window kind this pane is hosting — the same string the backend
	// was asked for. The label is that kind rendered for a human beside the row's
	// name; keeping the kind itself is what lets a live pane be described back as a
	// paneRef, which is how an arrangement is remembered (see recordArrangement).
	kind string
	// project / workspace are which row this pane is of, so the host bar can
	// look that row up and report its PR, CI and dev-loop state while you are
	// inside the pane. The label is a rendered string and cannot be matched
	// against anything; parsing it back apart would be one more way to say a
	// thing that is already known at the point the pane is built.
	project   string
	workspace string
	restore   func()
	setW      int
	setH      int
	// sel is the text dragged over with the mouse, for a pane whose program does
	// not want the mouse itself — see pane_selection.go.
	sel paneSelection
	// lastBox is the region this pane was last drawn in, and what a mouse event is
	// translated against.
	//
	// Recorded rather than asked for, because asking got the wrong answer inside a
	// split. m.boxOf(child) returns the whole childBox when m.active is the child,
	// and splitModal.deliver sets m.active to the half before calling its update —
	// so a mouse event arriving at a half was measured from the left edge of the
	// screen instead of from the half's own origin. A drag in the right half
	// selected nothing at all, and every click forwarded to a program in a right
	// half had been landing on the wrong cell since panes learned about the mouse.
	//
	// The drawn box is also the more honest source: it is where the cells the
	// pointer is over actually are, rather than where a second derivation says they
	// should be. Zero until the first render, which is the one frame where there is
	// nothing on screen to point at.
	lastBox box
	// opened is when the process started, which is how the exit is judged. An
	// exit is only worth reporting on its own if it happened before you could
	// have read anything — see paneQuickExit.
	opened time.Time
	// returnTo is what goes back in this pane's place when its program ends,
	// rather than the place itself going away.
	//
	// A pane is normally the destination: you open an agent, you leave it, the
	// arrangement it was in ends. A pane opened *from* something else is a
	// detour — `e` in the diff viewer opens $EDITOR on the file under the cursor
	// — and a detour ends by coming back. Without this the half collapsed when
	// the editor quit, taking the diff and everything you had scrolled to with
	// it, and the way back was to reopen the review and find your place again.
	//
	// Nil for every pane you opened on purpose, which is nearly all of them.
	returnTo modal
	// prefixArmed is whether the last key was the reserved one, so the next key
	// is read as a verb rather than typed at the hosted program. The split's own
	// field of the same name carries the argument for why this is a state resolved
	// by the next key and not by a clock.
	prefixArmed bool
	// actions is the user actions submenu, armed by `x` at the ctrl+b menu and
	// resolved by the alias pressed next. Non-empty is what "armed" means, so
	// there is no second flag that could disagree with it — and it holds the list
	// rather than re-reading the config on the second key, so the action you press
	// is the one you were shown.
	actions []UserAction
}

// PaneKindAgent is the window kind whose process is the workspace's agent.
//
// Named because three places have to mean the same one: the backend that maps
// kinds to commands, the deck asking whether it starts agents at all, and the
// backend deciding where a workspace's parked prompt goes.
const PaneKindAgent = "agent"

// PaneKindCI and PaneKindWatch are the two window kinds not spelled by an
// ActionOpenWindow arg on its own: `i` is its own action, and `W`'s arg carries
// the tmux command after the name. Named here so the deck and the backend agree
// on the string without either writing it out a second time.
const (
	PaneKindCI    = "ci"
	PaneKindWatch = "watch"
)

// PaneKindEditor is the workspace's $EDITOR. Named for the reason PaneKindAgent
// is: the split chord, the deck's `e`, and the diff viewer's own `e` all have to
// mean the same kind, and the third of those was written after the string had
// already been spelled out twice.
const PaneKindEditor = "editor"

// PaneKindShell is the shell's kind, which is the empty string: a shell is what a
// window key with no kind after it opens, and the backend reads the absence as
// "the user's shell" rather than looking a name up.
//
// Named so a call site asking for one can say which of the two things an empty
// kind is — the shell, or a caller that forgot to pass anything.
const PaneKindShell = ""

// PaneKindActionPrefix namespaces the kind of a user action's pane.
//
// A user action is named by whoever wrote the config, so without a namespace an
// action called "agent" would address the workspace's agent session — the
// prefix is what makes the two unspellable as one.
//
// An underscore rather than a colon because a kind has to survive being written
// into a session name and read back out of one: the substrate sanitizes a name
// to a conservative set, so a separator outside it would come back as something
// else and the round trip would not find the action again.
const PaneKindActionPrefix = "action_"

// PaneKindForAction is the pane kind that runs the named user action.
func PaneKindForAction(name string) string { return PaneKindActionPrefix + name }

// ActionFromPaneKind is the inverse, and reports whether the kind was a user
// action's at all.
//
// The name it returns is whatever the kind carried, which for a kind read back
// out of a session name is the sanitized spelling rather than the one in the
// config. Matching it against the configured actions is the resolver's job, and
// it is the one place that knows the sanitizing rule.
func ActionFromPaneKind(kind string) (string, bool) {
	name, ok := strings.CutPrefix(kind, PaneKindActionPrefix)
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// hostsAgents says the workspace's agent runs on a pty this deck owns.
//
// It is the one question the create flow has to get right: a deck that hosts
// agents must not let anything else start one, or the workspace ends up with
// two — the visible one and the one holding the prompt.
func (m Model) hostsAgents() bool {
	return m.panes != nil && m.panes.Describes(PaneKindAgent)
}

// PaneExecEnv switches a pane from being emulated to being handed the terminal.
//
// It exists to be measured against the emulator, because the two differ in
// exactly the hops every open pane defect lives in. `zmx attach` run from a plain
// shell is correct — dim text, cursor shapes, shift+enter, latency — and a pane
// showing the same session is not; the paths are identical up to the attach
// client and diverge only at our pty, x/vt, and the recompose into the deck's
// frame. Handing the child the real terminal deletes those three rather than
// re-implementing what they dropped.
//
// The cost is the deck's chrome: no border, no label row, no status bar, and no
// background refresh, because the deck is suspended rather than drawing. Whether
// that is a fair trade is the thing this flag is for.
const PaneExecEnv = "AWP_PANE_EXEC"

// paneExecDoneMsg reports that a handed-over pane has given the terminal back.
type paneExecDoneMsg struct {
	label string
	err   error
}

// paneRef names a pane by the row it belongs to and the kind of program it
// runs, which is everything openPane needs to open it again.
//
// The row's identity rather than the Item itself, because the Item captured at
// open time is a snapshot: a rename changes the workspace's path and its
// session name, and a delete leaves nothing to open at all. Resolving the row
// again when the key is pressed means a workspace that has moved is followed
// and one that is gone is a refusal — where a stored Item would open a pane on
// a directory that is no longer there.
type paneRef struct {
	project   string
	workspace string
	kind      string
}

// set reports whether a pane has been opened yet, so L can say so rather than
// opening the zero row's shell.
func (r paneRef) set() bool { return strings.TrimSpace(r.workspace) != "" }

// paneArrangement is what was on screen, which is not always one pane: a split of
// two is a thing you set up and expect to find again, so what the deck remembers
// has to be the arrangement rather than a single program.
//
// The row is the left half's, because both halves are of the same workspace — a
// split is two views of one thing, which is why it exists.
type paneArrangement struct {
	// left is the whole answer for a single pane, and the left half of a split.
	left paneRef
	// rightKind is the kind beside it. A kind rather than a paneRef for the same
	// reason the row lives on left: a second row would be a second workspace, which
	// a split has never been.
	//
	// hasRight is what says there was a second half at all, because an empty kind is
	// a real one — it is the shell, which the window keys spell as `s` and the
	// backend as "". Inferring "no split" from an empty string would make `|s` the
	// one split the deck could not remember.
	rightKind string
	hasRight  bool
	// leftFrac is where the divider was, so a split you widened comes back the
	// width you made it rather than snapping back to even. Zero means even — see
	// splitLeftFrac, which is where that is read.
	leftFrac float64
}

func (a paneArrangement) set() bool   { return a.left.set() }
func (a paneArrangement) split() bool { return a.hasRight }

// label is the arrangement in the words a status line uses.
func (a paneArrangement) label() string {
	if !a.split() {
		return a.left.label()
	}
	// The pair, in the order they are on screen. `|` because that is the key that
	// opens one.
	return PaneLabel(a.left.kind) + " | " + PaneLabel(a.rightKind) +
		" · " + a.left.project + "/" + a.left.workspace
}

// childKind describes a live half in the terms a kind is spelled in, so an
// arrangement can be recorded from what is on screen rather than from whatever the
// call that built it happened to still have in scope.
func childKind(c modal) (string, bool) {
	switch t := c.(type) {
	case *panePopover:
		return t.kind, true
	case *diffModal:
		return SplitKindDiff, true
	}
	return "", false
}

// matches is the row-identity comparison, in one place because the deck asks it
// of both the scoped list and the unscoped one.
func (r paneRef) matches(it Item) bool {
	return it.ProjectName == r.project && it.WorkspaceName == r.workspace
}

// label is the pane in the words a status line uses: the program, then the row.
func (r paneRef) label() string {
	return PaneLabel(r.kind) + " · " + r.project + "/" + r.workspace
}

// paneWorkspaceLabel is the row the pane is of, as the top row names it:
// "<project> · <label>" when the workspace has a display label, otherwise
// "<project>/<workspace>".
//
// The separator changes with the thing after it, because the two are not the same
// kind of name. A workspace name is a path component and reads as one; a label is
// a sentence you wrote, and `proj/the widget rewrite` claims a directory that does
// not exist. The `·` is the same separator the row already puts after the kind.
//
// Presentation only: nothing resolves a pane from this string (see
// panePopover.project / .workspace, and workspace/display_name_test.go).
func paneWorkspaceLabel(it Item) string {
	if label := strings.TrimSpace(it.DisplayName); label != "" {
		return it.ProjectName + " · " + label
	}
	return it.ProjectName + "/" + it.WorkspaceName
}

// openPane hosts the given window kind for the selected row, filling the deck.
// It reports false when there is no backend for it, so the caller can fall back
// to tmux.
//
// Whatever arrangement was up goes first — see closeArrangement for why it has to
// be first and not last.
func (m *Model) openPane(item Item, kind string) (tea.Cmd, bool) {
	closed := m.closeArrangement()
	p, cmd, handled := m.newPane(item, kind, m.childBox(), true)
	if handled && p != nil {
		m.active = p
	}
	return tea.Batch(closed, cmd), handled
}

// closeArrangement tears down every pane the deck is holding — the arrangement on
// screen, and the one parked behind a modal — and is what opening another one
// calls first.
//
// First, rather than after the new pane is up, and that ordering is the whole
// point. A pane is a `zmx attach` client, and a session takes its size from, and
// accepts input from, exactly one client: the leader. The daemon hands leadership
// to a client at attach only when there is no leader (`handleInit`), and otherwise
// moves it on the first *keyboard* input from another client — mouse reports are
// explicitly excluded, being terminal chatter rather than someone typing.
//
// So a pane left running behind the one you just opened keeps the session it was
// attached to, and the new client is a spectator: zmx drops its mouse events on the
// floor, and ignores the size it reports, so a split's half never reflows. Pressing
// any key flips leadership and both start working, which is exactly what this looked
// like from the outside — the mouse and the reflow "needing a keystroke first".
//
// A pane opened over another used to leak the old client outright — one live `zmx
// attach` per pane per deck run, each of them still leading a session the deck would
// open again later. Closing the outgoing arrangement before the new client attaches
// leaves the session with no leader for the new one to claim.
//
// The cost is that an open that then fails leaves the deck on its row list rather
// than back in the pane you were in. That is the honest order: the pane you were in
// is the one holding the session hostage.
func (m *Model) closeArrangement() tea.Cmd {
	var cmds []tea.Cmd
	// The parked one as well as the visible one: a verb pressed on the strip steps
	// the arrangement aside into overlayHost (see suspendForOverlay), and a pane
	// opened from the modal that floated over it would otherwise orphan a client
	// nothing on the deck can reach any more.
	for _, held := range []modal{m.active, m.overlayHost} {
		switch c := held.(type) {
		case *panePopover:
			cmds = append(cmds, c.close(m))
		case *splitModal:
			cmds = append(cmds, c.close(m))
		}
	}
	m.overlayReturns = false
	return tea.Batch(cmds...)
}

// newPane builds a pane for the kind, sized and started for the box it will be
// rendered into, and returns it without installing it anywhere.
//
// Separate from openPane because a split needs two of these and neither of them
// is "the deck's child" — the split is. It returns a nil pane with handled=true
// for the two cases that are neither a refusal nor a pane: a handed-over
// terminal, which has no popover because the deck is suspended behind it, and a
// failure, which has already said so in m.status.
//
// The box is an argument rather than read from the Model because it is the whole
// reason this can be called twice: a pty started for half the screen has to be
// started at half the width, or the program lays itself out for a width it will
// never be drawn at.
// remember is whether opening this pane is the arrangement to come back to. False
// for a half of a split, which records itself once as a pair rather than letting
// each half register as the last thing you were in — see recordArrangement.
func (m *Model) newPane(item Item, kind string, b box, remember bool) (*panePopover, tea.Cmd, bool) {
	if m.panes == nil || !m.panes.Describes(kind) {
		return nil, nil, false
	}
	// Opening a program is going into it, so the keyboard leaves the strip. Every
	// pane the deck opens is built here, including from a key pressed on the strip
	// itself — which is the whole point of #350, and would otherwise start a pane
	// whose keys were still being read by the sidebar in front of it.
	m.sidebarFocus = false
	handover := os.Getenv(PaneExecEnv) != ""
	// A handed-over pane is the whole terminal, so there is no size it does not
	// fit. The emulated one has to leave room for its own chrome.
	if !handover && !paneFits(b.w, b.h) {
		m.status = fmt.Sprintf("this pane gets %dx%d, too small to host one", b.w, b.h)
		return nil, nil, true
	}

	w, h := paneDims(b.w, b.h)
	cmd, restore, err := m.panes.Open(item, kind, w, h)
	if err != nil {
		m.status = PaneLabel(kind) + ": " + err.Error()
		return nil, nil, true
	}
	// Recorded here rather than at the top, so a kind the backend refused or an
	// open that failed is not somewhere the deck claims you can go back to.
	ref := paneRef{project: item.ProjectName, workspace: item.WorkspaceName, kind: kind}
	if handover {
		if remember {
			m.recordPane(ref)
		}
		return nil, m.handOverTerminal(cmd, restore, PaneLabel(kind)), true
	}
	p, startCmd, err := m.paneRunning(cmd, b, panePopover{
		label:     PaneLabel(kind) + " · " + paneWorkspaceLabel(item),
		kind:      kind,
		project:   item.ProjectName,
		workspace: item.WorkspaceName,
		restore:   restore,
	})
	if err != nil {
		if restore != nil {
			restore()
		}
		m.status = PaneLabel(kind) + ": " + err.Error()
		return nil, nil, true
	}
	if remember {
		m.recordPane(ref)
	}
	m.status = ""
	return p, startCmd, true
}

// paneRunning starts a command on a terminal sized for the box and returns the
// pane showing it, with the command that pumps its frames.
//
// The one place a pane's terminal is opened. seed carries the fields that differ
// per caller — the label, which row it is of, what to undo when it closes — and
// this fills in the ones that come from starting it. A second spelling of "open
// a terminal and wrap it in a popover" is how a pane ends up outside
// vterm.CloseAll's registry, or drawn at a size its pty never heard about.
func (m *Model) paneRunning(cmd *exec.Cmd, b box, seed panePopover) (*panePopover, tea.Cmd, error) {
	// A pane's program talks to the pty and to nothing else. creack/pty wires the
	// pty into the three descriptors it finds empty and leaves any the caller
	// filled in — so a command carrying the deck's own os.Stdout draws over the
	// deck's screen while the pane it is supposedly in stays blank, which reads as
	// the program never starting. Cleared here rather than trusted of every
	// builder: a command is built to be run, and where it runs is this function's
	// answer.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	w, h := paneDims(b.w, b.h)
	m.paneGen++
	term, err := m.terminalOpener()(m.paneGen, w, h, cmd, m.hostColors)
	if err != nil {
		return nil, nil, err
	}
	p := seed
	p.term = term
	p.setW, p.setH = w, h
	p.opened = time.Now()
	return &p, tea.Batch(term.AwaitOutput(), term.AwaitExit()), nil
}

// termOpener is what starts the terminal a pane's process runs in.
type termOpener func(gen, w, h int, c *exec.Cmd, host vterm.HostColors) (vterm.Hosted, error)

// defaultTermOpener is what a deck that has not been told otherwise uses.
//
// A package variable, which is the one piece of global state in here, and it buys
// something specific: the test binary points it at a fake in an init, so every
// deck a test constructs — and they are constructed at twenty-odd sites, by
// calling New directly rather than through any shared helper — gets a terminal
// without each of those sites saying so. Production never assigns it; the deck
// reads it once per pane.
//
// The alternative was every test that opens a pane needing the real emulator,
// which is cgo against an archive built by Zig: a plain checkout could then run
// none of the tests about what the deck does with a pane, which is most of what
// this package is.
var defaultTermOpener termOpener = vterm.Open

// terminalOpener is how this deck starts a pane's terminal: its own opener if it
// was given one, else the package default. See Model.openTerm.
func (m *Model) terminalOpener() termOpener {
	if m.openTerm != nil {
		return m.openTerm
	}
	return defaultTermOpener
}

// recordPane remembers a pane the deck has just opened, keeping the two the
// keyboard can reach: the one you are in, and the one before it.
//
// Recorded on the way in rather than on the way out because the two are the same
// answer — the pane you are in is the pane you will have last left — and leaving
// happens down several paths (ctrl+\, the program exiting, a handover returning)
// that would each have to remember.
//
// Re-entering the same pane does not push, which is the whole reason the check is
// here: ctrl+\ resuming the pane you were just in must not overwrite the
// alternate, or the second key would have nothing left to reach and holding one
// pane open would erase the memory of every other.
func (m *Model) recordPane(ref paneRef) {
	m.recordArrangementValue(paneArrangement{left: ref})
}

// recordArrangementValue is recordPane for a whole arrangement.
//
// A split replaces the memory of its own left half rather than pushing it aside:
// splitting the pane you are in is one continuous act, and pushing would spend the
// alternate slot on the pane you can already see half of.
func (m *Model) recordArrangementValue(arr paneArrangement) {
	if arr == m.lastPane {
		return
	}
	if arr.split() && arr.left == m.lastPane.left && !m.lastPane.split() {
		m.lastPane = arr
		m.persistArrangement()
		return
	}
	m.prevPane = m.lastPane
	m.lastPane = arr
	m.persistArrangement()
}

// persistArrangement writes what is on screen down, so the next deck opens into it.
//
// Here rather than at exit, and here rather than at each of the places that build a
// pane: this is the one funnel every change to "what was on screen" already goes
// through, which is the same reason recordArrangementValue exists at all. A deck is
// killed, or its terminal closes, or the machine restarts — a save at exit is a save
// that does not happen on the occasions you most wanted it.
//
// A failure is dropped rather than shown. Everything else about the pane worked; what
// was lost is only that the next launch will not start here, and a status line
// complaining about a preferences file over the pane you just opened is noise about
// something you did not ask for. It is the one saver whose write you did not trigger
// deliberately, which is why this one is silent where the others say so.
func (m *Model) persistArrangement() {
	if m.saveArrangement == nil {
		return
	}
	_ = m.saveArrangement(m.lastPane.exported())
}

// recordArrangement remembers the split as it now stands — both kinds and the
// divider — so leaving it and coming back finds it rather than one pane.
//
// Called from every place a split changes shape rather than only where one is
// built, because "what was on screen" is the question being answered: a half
// replaced, a half closed or a divider moved are all changes to the thing you
// expect to come back to.
//
// A split whose left half is not a pane is not recorded. Both halves being views of
// one workspace is what makes a single row enough to describe the pair, and the
// left half is where that row is read from.
func (m *Model) recordArrangement(s *splitModal) {
	left, isPane := s.left.(*panePopover)
	if !isPane {
		return
	}
	rightKind, ok := childKind(s.right)
	if !ok {
		return
	}
	m.recordArrangementValue(paneArrangement{
		left:      paneRef{project: left.project, workspace: left.workspace, kind: left.kind},
		rightKind: rightKind,
		hasRight:  true,
		leftFrac:  s.leftFrac,
	})
}

// openPaneOrArrangement opens a kind on a row, or — when that row and that kind
// are the arrangement you last left — the whole arrangement it was part of.
//
// This is what every key that enters a workspace goes through (enter, a, e, v, s,
// i), and it is the difference between remembering a split and remembering it only
// for one key. ctrl+\ resumed the arrangement from the start, because resuming is
// all it does; enter opened a bare agent pane, which is not what the workspace
// looked like when you left it — and worse, opening it recorded itself, so the
// split you had set up was forgotten by the act of going back to look at it.
//
// Gated on the kind as well as the row: a split's left half is the agent, so it is
// the agent key that brings the pair back and `e` on the same workspace means the
// editor alone. Press the key the left half was and you get the pair.
func (m *Model) openPaneOrArrangement(item Item, kind string) (tea.Cmd, bool) {
	arr := m.lastPane
	if arr.split() && arr.left.matches(item) && arr.left.kind == kind {
		// Ahead of the open rather than inside openSplitKinds, which has two callers
		// that mean to keep what is on screen — splitWith reuses the pane it is
		// splitting as the left half, and goToRowArrangement holds the old
		// arrangement until the new one is built. See closeArrangement.
		closed := m.closeArrangement()
		if cmd, ok := m.openSplitKinds(item, arr.rightKind, arr.leftFrac); ok {
			return tea.Batch(closed, cmd), true
		}
		// A right half this deck cannot build any more (the diff viewer unwired, a
		// kind dropped from the config) is not a reason to refuse the key. Fall
		// through to the single pane, which is what the row asked for — carrying the
		// close, which has already happened.
		cmd, handled := m.openPane(item, kind)
		return tea.Batch(closed, cmd), handled
	}
	return m.openPane(item, kind)
}

// resumePane goes back into the pane you just left, which is what ctrl+\ means
// from the row list.
//
// The same key leaves a pane, so the pair is one gesture: out to check something,
// back to carry on. That is the common half of what L used to do on its own, and
// giving it to the key that already means "hop between the pane and the deck"
// frees L to be the alternate — see alternatePane.
//
// Reports false only when there is no pane backend at all. Every other outcome is
// handled here, because a key that silently does nothing reads as broken.
func (m *Model) resumePane() (tea.Cmd, bool) {
	if m.panes == nil {
		return nil, false
	}
	if !m.lastPane.set() {
		m.status = "no pane to go back to yet — open one first (enter agent, e editor, v vcs, s shell)"
		return nil, true
	}
	return m.reopenPane(m.lastPane)
}

// alternatePane switches to the previous pane — the one before the pane you were
// last in — which is what L means on a deck that hosts its own panes.
//
// `tmux switch-client -l` is the same gesture one substrate over, and the point
// of it is that the two most recent things you were in are one keypress apart:
// press it twice and you are back. That only works if the key reaches the *other*
// pane, which is why resuming (ctrl+\) and alternating (L) are two keys rather
// than one — a single slot can only ever offer you the thing you just had.
func (m *Model) alternatePane() (tea.Cmd, bool) {
	if m.panes == nil {
		return nil, false
	}
	if !m.prevPane.set() {
		if m.lastPane.set() {
			// One pane deep: there is nothing to alternate with, and the useful
			// thing to say is which key does have somewhere to go.
			m.status = "only one pane so far — " + PaneLeaveKey + " goes back to " + m.lastPane.label()
		} else {
			m.status = "no pane to switch to yet — open one first (enter agent, e editor, v vcs, s shell)"
		}
		return nil, true
	}
	return m.reopenPane(m.prevPane)
}

// alternateFrom is alternatePane pressed from inside the thing being left, which
// is what the pane menu's L does.
//
// The order is the whole content of it: the current arrangement has to be torn
// down before the previous one opens, or two panes are installed and the first
// one's terminal is never closed. Checked before anything is closed rather than
// after, because alternatePane's refusals are messages — "only one pane so far" —
// and a key that says that having already dropped the pane you were reading would
// be the worst of both answers.
//
// close is the teardown, passed in rather than switched on here: a pane closes
// itself, a split closes both halves, and each already knows how.
func (m *Model) alternateFrom(close func() tea.Cmd) tea.Cmd {
	if m.panes == nil || !m.prevPane.set() {
		// Nowhere to go. alternatePane is still the one that says so, so the wording
		// is the same as it is from the row list.
		cmd, _ := m.alternatePane()
		return cmd
	}
	closed := close()
	opened, _ := m.alternatePane()
	return tea.Batch(closed, opened)
}

// fullscreenShellKey is the ctrl+b verb for a shell on the whole screen.
//
// The same letter as the window key that splits to one, in the other case: `s`
// puts a shell beside the agent, `S` gives it the terminal. A shell is the kind
// most often wanted at full width — a build's output, a command whose lines are
// longer than half a screen — and until this key the only way to one was out to
// the deck and back in, which takes the arrangement down anyway and costs two
// screens on the way.
const fullscreenShellKey = "S"

// fullscreenShellVerb is how the ctrl+b menus name the key. It says "instead of
// this" because the neighbouring window keys all say "beside", and the difference
// between `s` and `S` is exactly that.
func fullscreenShellVerb() [2]string {
	return [2]string{fullscreenShellKey, "a shell on the whole screen, instead of this"}
}

// fullscreenShell takes the arrangement down and opens a shell filling the deck.
//
// Same order and same reason as alternateFrom: the row is resolved before
// anything closes, so a pane whose workspace has gone keeps its screen and gets a
// message rather than being dropped for a shell that cannot be opened. close is
// passed in for the same reason too — a pane closes itself, a split closes both
// halves.
func (m *Model) fullscreenShell(close func() tea.Cmd) tea.Cmd {
	item, ok := m.topRowRow()
	if !ok {
		m.status = "shell: this pane's workspace is not on the deck any more"
		return nil
	}
	closed := close()
	opened, handled := m.openPane(item, PaneKindShell)
	if !handled {
		// No backend for a shell, so the deck is now on its row list with nothing
		// said. Only reachable from a host that describes some kinds and not this
		// one; openPane says so itself when the open fails.
		m.status = "shell: this deck cannot host one"
		return closed
	}
	return tea.Batch(closed, opened)
}

// reopenPane resolves a remembered pane against the rows as they are now and
// opens it, which is the half resumePane and alternatePane share.
func (m *Model) reopenPane(arr paneArrangement) (tea.Cmd, bool) {
	ref := arr.left
	// The scoped list first, so the cursor can land on the row: leaving the pane
	// again has to put you on the row the pane was, or the keys and ctrl+\
	// disagree about where you are.
	for i, it := range m.items() {
		if ref.matches(it) {
			m.cursor = i
			return m.openRememberedPane(it, arr)
		}
	}
	// Not in the current scope — an agent that exited drops out of attention —
	// but the pane is still one you were in, and refusing to go back to it because
	// of a filter would make the key depend on which list you are looking at. No
	// cursor move: there is no row here to move it to.
	for _, it := range m.itemsAll {
		if ref.matches(it) {
			return m.openRememberedPane(it, arr)
		}
	}
	m.status = ref.label() + ": that workspace is not on the deck any more"
	return nil, true
}

// openRememberedPane is the tail of reopenPane, shared by the two lookups.
func (m *Model) openRememberedPane(it Item, arr paneArrangement) (tea.Cmd, bool) {
	ref := arr.left
	if m2, blocked := m.blockIfSettingUp(it); blocked {
		*m = m2
		return nil, true
	}
	if arr.split() {
		// The arrangement was two things side by side, so that is what comes back.
		// A terminal too narrow for a split now falls through to the left half
		// alone rather than refusing: the pane is what you were working in, and the
		// second half is the part that does not fit.
		if cmd, ok := m.openSplitKinds(it, arr.rightKind, arr.leftFrac); ok {
			return cmd, true
		}
	}
	cmd, handled := m.openPane(it, ref.kind)
	if !handled {
		// The backend has stopped describing the kind since — a user action
		// deleted from the config is the way this happens.
		m.status = ref.label() + ": this deck has no pane for that any more"
	}
	return cmd, true
}

// handOverTerminal suspends the deck and gives the child the real tty.
//
// Nothing goes on m.active: there is no modal, because the deck is not drawing.
// Bubble Tea stops its renderer for the duration and repaints the whole frame
// when the child exits, which is also when the deck needs to catch up — an agent
// you were just working in has been reporting status the whole time.
//
// The child's env has to be corrected on the way past. The backend built it for
// an emulated pane, so it states TERM=xterm-256color; a child on the real
// terminal should carry the real TERM. See vterm.HostTerm, which restores that
// one variable and leaves the multiplexer markers dropped.
func (m *Model) handOverTerminal(cmd *exec.Cmd, restore func(), label string) tea.Cmd {
	cmd.Env = vterm.HostTerm(cmd.Env)
	m.status = ""
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if restore != nil {
			restore()
		}
		if leftByLeaveKey(err) {
			err = nil
		}
		return paneExecDoneMsg{label: label, err: err}
	})
}

// leftByLeaveKey reports whether a handed-over child died of the leave key.
//
// ctrl+\ is SIGQUIT, which is exactly why it was chosen — nothing interactive
// binds it. But a handed-over pane has no deck reading keys in front of the
// child, so the key reaches the terminal, and a child that left it in cooked
// mode gets the line discipline's interpretation rather than the byte: `i`'s CI
// watch is a `bash -c`, so ctrl+\ ends it. That is the leave key working, and
// reporting it as `ci: exited: signal: quit` describes the way out of a pane as
// a crash.
//
// Only for the handed-over case. In an emulated pane the deck takes ctrl+\
// before anything is forwarded, so a SIGQUIT reaching that child came from
// somewhere else entirely and is worth saying out loud.
func leftByLeaveKey(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGQUIT
}

// paneLabel is what the pane's chrome and its errors call it.
//
// A user action drops its namespace here: the prefix exists so two kinds cannot
// collide, and the user typed the name without it.
func PaneLabel(kind string) string {
	if kind == "" {
		return "shell"
	}
	if name, ok := ActionFromPaneKind(kind); ok {
		return name
	}
	return kind
}

// close tears the pane down and returns the command that catches the deck up.
//
// A pane is open for as long as you are working in it, and the agent inside is
// reporting status the whole time. Without the refresh the row list you land
// back on is whatever it was when you opened the pane, until the next poll —
// so leaving a pane looked like status had stopped updating.
func (p *panePopover) close(m *Model) tea.Cmd {
	// The strip renders only over a hosted program, so a pane going away takes the
	// surface the keyboard was on with it — whether it went because ctrl+\ walked
	// the cycle or because the program inside it exited on its own.
	m.sidebarFocus = false
	if m.overlayHost == p {
		// It closed while something was floating over it — the program inside exited
		// under a confirm. Forgetting it here is what stops restoreOverlayHost from
		// putting a dead pane back on screen when the confirm goes.
		m.overlayHost = nil
		m.overlayReturns = false
	}
	_ = p.term.Close()
	if p.restore != nil {
		p.restore()
		p.restore = nil
	}
	if m.active == p {
		m.active = nil
	}
	// The captain is held beside active rather than in it, so it has its own slot
	// to give back — and giving it back is what puts the keys into whatever the
	// captain was floating over. Asked unconditionally: a pane is in exactly one of
	// the two places, and the one it is not in does not match.
	if m.captain == p {
		m.captain = nil
	}
	var cmd tea.Cmd
	*m, cmd = m.requestRefresh(false)
	return cmd
}

// paneQuickExit is how soon a pane's process dying is surprising in itself.
//
// A pane you worked in and then left by typing `exit` is not news. One that was
// gone before you could read it is, even with a clean exit status — that is what
// a program refusing to start looks like from here.
const paneQuickExit = 2 * time.Second

// paneExitReasonMax bounds the pane's last line, which is a whole terminal row
// wide and would otherwise push everything else out of the status bar.
const paneExitReasonMax = 80

// paneExitStatus says why a pane closed, or "" when it closing was unremarkable.
//
// The reason is the pane's own last line. Nothing above this knows what a `zmx
// attach` that will not attach prints, and it should not have to: whatever the
// program said on its way out is better than any sentence written here about the
// class of thing that might have happened.
func paneExitStatus(label string, err error, lived time.Duration, reason string) string {
	if err == nil && lived >= paneQuickExit {
		return ""
	}
	status := label + ": "
	if err != nil {
		status += "exited: " + err.Error()
	} else {
		status += "exited immediately"
	}
	if reason != "" {
		status += " — " + truncate(reason, paneExitReasonMax)
	}
	return status
}

func (p *panePopover) footerHelp() string { return "" }

// panePrefixMenu is the menu for a single pane: the window keys, each of which
// puts that kind beside the pane you are in. The arrangement verbs a split's menu
// also carries — focus, size, zoom, close a half — have nothing to act on until
// there are two halves, so they are absent rather than listed and inert.
//
// No verb for leaving. ctrl+\ is a door on every surface and needs no menu in
// front of it.
func panePrefixMenu(m *Model) deckMenu {
	verbs := splitKindVerbs(func(label string) string { return "split, with " + label + " beside this" })
	verbs = append(verbs,
		fullscreenShellVerb(),
		userActionsVerb(m.userActionsFor()),
		[2]string{alternateKey, "go to the arrangement before this one"},
		captainVerb(),
		sidebarVerb(m.sidebar),
		menuCancelVerb,
	)
	return menu(verbs...)
}

// prefixKey reads one key while a single pane's menu is armed. Like the split's,
// it disarms on anything that is not the menu key, and swallows what it read
// rather than letting a mistyped verb type itself at the program.
func (p *panePopover) prefixKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	if paneMenuPressed(m, msg) {
		// The menu key again re-arms rather than resolving, so holding it cannot do
		// anything. Nothing to redraw: the menu is rendered from prefixArmed on every
		// frame rather than written anywhere when it opens.
		return nil
	}
	p.prefixArmed = false
	m.status = ""
	if kind, ok := splitKindFor(msg.String()); ok {
		item, ok := m.topRowRow()
		if !ok {
			// The pane outlived its row — a workspace deleted while you were inside
			// it. The pane is still usable; there is just nothing to resolve a second
			// program against.
			m.status = "split: this pane's workspace is not on the deck any more"
			return nil
		}
		return p.splitWith(m, item, kind)
	}
	if msg.String() == userActionsMenuKey {
		if actions := m.userActionsFor(); len(aliasLookup(actions)) > 0 {
			p.actions = actions
			return nil
		}
		// The verb was not on the menu, so nothing is owed but the disarm that
		// already happened. Silence rather than "no user actions configured": the
		// key was never offered, and a message about a key you were not shown
		// reads as something having gone wrong.
		return nil
	}
	switch msg.String() {
	case sidebarKey:
		m.toggleSidebar()
	case fullscreenShellKey:
		return m.fullscreenShell(func() tea.Cmd { return p.close(m) })
	case alternateKey:
		return m.alternateFrom(func() tea.Cmd { return p.close(m) })
	case captainKey:
		return m.captainOverPane()
	}
	return nil
}

// actionKey reads the alias at the user actions submenu. A foreground action lands
// beside this pane the way every other window key from this menu does — you asked for
// it from inside something you were watching, so the thing you were watching stays.
// Anything unrecognised closes the submenu, esc included: a mistyped alias must not
// fall through and type itself at the program.
func (p *panePopover) actionKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	actions := p.actions
	p.actions = nil
	ua, ok := resolveActionKey(actions, msg.String())
	if !ok {
		m.status = ""
		return nil
	}
	if ua.Background {
		return startBackgroundAction(m, ua.Name)
	}
	item, ok := m.topRowRow()
	if !ok {
		m.status = "split: this pane's workspace is not on the deck any more"
		return nil
	}
	return p.splitWith(m, item, PaneKindForAction(ua.Name))
}

// splitWith puts kind beside this pane, in the right half.
//
// The left half is the agent, which is the deck's layout invariant and not
// something a split made from inside a pane gets to opt out of. When this pane is
// the agent it is reused as the left half, and reusing it is the point: the agent
// you are watching is the reason you wanted something beside it, and re-opening it
// as a fresh left half would resize and repaint the program you were reading
// mid-thought.
//
// When it is not — `ctrl+b c` from inside an editor — the agent is opened as the
// left half and this pane is closed, rather than becoming a left half that is not
// the agent. The kind you asked for is still what lands on the right: it is what
// the key said, and the half it goes in is the half that changes.
//
// The reused pane needs no resize here. renderPopover asks its terminal for the box
// it is handed, so the next frame is what moves the pty to half the width.
//
// item is which row the split is of, and it is an argument because there are two
// answers: the pane's own row for a window key pressed inside it, and the row under
// the sidebar's cursor for one pressed on the strip. It used to be read from
// m.topRowRow() in here, which is exactly the shape of ambient state this codebase
// keeps turning into a required argument — the strip would have been a second caller
// with no way to say what it meant.
func (p *panePopover) splitWith(m *Model, item Item, kind string) tea.Cmd {
	full := m.childBox()
	if !splitFits(full) {
		// The floor is a pane, so the number that matters is the pane's minimum, and it
		// is per half rather than for the terminal.
		m.status = fmt.Sprintf("split: this terminal is %d columns, %d needed for two panes", full.w, 2*(paneMinW+paneChromeW))
		return nil
	}
	if p.kind != PaneKindAgent {
		// Built through the ordinary path so the agent half is opened, sized and
		// recorded exactly as every other split's left half is. It installs the split
		// itself; this pane is closed only once that has succeeded, so a refusal
		// leaves what you were in on screen.
		cmd, ok := m.openSplitKinds(item, kind, splitLeftFrac(m.splitFrac))
		if !ok {
			m.active = p
			return nil
		}
		return tea.Batch(p.close(m), cmd)
	}
	probe := &splitModal{}
	_, rightBox := probe.boxes(full)
	right, cmd, ok := m.openChild(item, kind, rightBox)
	if !ok {
		// openChild said why, and installed nothing. Back to the pane as it was.
		m.active = p
		return nil
	}
	s := &splitModal{left: p, right: right, rightFocused: true, label: PaneLabel(kind)}
	m.active = s
	m.recordArrangement(s)
	m.status = ""
	return cmd
}

func (p *panePopover) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case vterm.OutputMsg:
		if msg.Gen != p.term.Gen() {
			// A frame from a pane that has already closed. Painting it would
			// put the previous process's screen inside this one.
			return nil
		}
		return p.term.AwaitOutput()

	case vterm.ExitMsg:
		if msg.Gen != p.term.Gen() {
			return nil
		}
		// The screen has to be read before the close, which throws it away, and
		// the status set after it, because close is what puts the deck back —
		// there is nowhere to show a message until it has.
		reason := p.term.LastLine()
		lived := time.Since(p.opened)
		cmd := p.close(m)
		if s := paneExitStatus(p.label, msg.Err, lived, reason); s != "" {
			m.status = s
		}
		return cmd

	case tea.KeyPressMsg:
		if len(p.actions) > 0 {
			return p.actionKey(m, msg)
		}
		if p.prefixArmed {
			return p.prefixKey(m, msg)
		}
		if paneMenuPressed(m, msg) {
			p.prefixArmed = true
			return nil
		}
		if msg.String() == PaneLeaveKey {
			if msg.IsRepeat {
				// Held, not pressed again. Swallowed rather than closed on or
				// forwarded: the deck's own ctrl+\ goes back into the pane this would
				// close, so a repeat that gets through flaps between the two (#307),
				// and passing it to the program means holding the key sprays it at
				// whatever is running.
				return nil
			}
			if m.enterSidebarFromPane() {
				return nil
			}
			return p.close(m)
		}
		// Typing means you are done with whatever you had picked out, and a
		// highlight left over a screen the program is about to redraw marks whatever
		// text moves under it. It also means you are done reading history: the
		// program will answer on the bottom row, which is not where the view is.
		p.clearSelection()
		p.snapToTail()
		p.term.SendKey(msg)
		return nil

	case tea.PasteMsg:
		p.term.SendText(msg.Content)
		return nil

	case tea.MouseMsg:
		// A program that did not ask for the mouse does not get it: the drag is a
		// selection and the wheel is the scrollback, which are the only ways a pane
		// can have either — see pane_selection.go for why the host terminal cannot
		// do it, and pane_scroll.go for what nothing else was answering.
		if p.paneSelects() {
			if p.scrollMouse(msg) {
				return nil
			}
			if cmd, consumed := p.selectMouse(m, msg); consumed {
				return cmd
			}
			return nil
		}
		// The deck asks for mouse events only while a pane is up (see View),
		// so anything arriving here belongs to the hosted program — but in the
		// deck's coordinates, not its own.
		inner, ok := paneMouse(msg, p.lastBox)
		if !ok {
			return nil
		}
		p.term.SendMouse(inner)
		return nil
	}
	return nil
}

// The popover's chrome is the border, and no more. Every cell it takes is one
// the hosted program does not get, and unlike the deck's other overlays — which
// frame a fixed amount of awp's own text — a pane is showing someone else's
// full-screen program.
//
// So there is no padding and no header. The pane used to carry its own row for
// the label, the attention badge and the leave hint; all three are the deck's
// questions rather than the pane's, and they now sit on the deck's bar above it
// — in the same cells whether one pane is up or a split of two (see
// host_bar.go). The border stays: it is what says where the pane ends when its
// program does not fill it.
const (
	paneChromeW = borderCells
	paneChromeH = borderCells
	paneMinW    = 20
	paneMinH    = 5
)

// paneInsetX / paneInsetY are where the terminal starts inside the popover: past
// the border, on both axes.
const (
	paneInsetX = 1
	paneInsetY = 1
)

func paneDims(deckW, deckH int) (w, h int) { return deckW - paneChromeW, deckH - paneChromeH }

// paneBox is the popover's outer size for a terminal of w×h. renderPopover and
// screenCursor both derive from it rather than each doing the arithmetic, so
// the cursor cannot land somewhere the box isn't.
func paneBox(w, h int) (boxW, boxH int) {
	return w + paneChromeW, h + paneChromeH
}

// screenCursor is where the hosted program's cursor lands on the deck's own
// screen: the centred popover's origin, plus the chrome around the terminal,
// plus wherever the program put it.
//
// ok is false when there should be no cursor at all — the pane does not fit,
// the program has hidden its cursor, or it sits outside the terminal. A
// full-screen program like jjui hides the cursor and then leaves it wherever
// was convenient, so honouring that is what stops a blinking block appearing
// at an arbitrary spot on its screen.
//
// The box size is computed rather than measured so this does not have to
// render the popover a second time.
func (p *panePopover) screenCursor(b box) (x, y int, ok bool) {
	if !paneFits(b.w, b.h) {
		return 0, 0, false
	}
	w, h := paneDims(b.w, b.h)
	boxW, boxH := paneBox(w, h)
	originX, originY := b.x+(b.w-boxW)/2, b.y+(b.h-boxH)/2
	cx, cy, visible := p.term.Cursor()
	if !visible || cx < 0 || cy < 0 || cx >= w || cy >= h {
		return 0, 0, false
	}
	return originX + paneInsetX + cx, originY + paneInsetY + cy, true
}

// paneMouse translates a mouse event from the deck's screen into the hosted
// terminal's own grid, reporting false for one that lands outside it.
//
// The program is told where the pointer is and draws its own selection there,
// so an untranslated event does not look like a mouse bug — it looks like the
// highlight appearing paneInsetY rows below the pointer, because that is
// exactly where the program was told to put it.
//
// Derived from the same paneDims / paneBox / paneInset as screenCursor, in the
// opposite direction, so the two cannot come to disagree about where the
// terminal starts. The bounds check mirrors it too: a click on the border or
// the header row is not a cell the program has.
func paneMouse(msg tea.MouseMsg, b box) (tea.MouseMsg, bool) {
	if !paneFits(b.w, b.h) {
		return nil, false
	}
	w, h := paneDims(b.w, b.h)
	boxW, boxH := paneBox(w, h)
	originX, originY := b.x+(b.w-boxW)/2, b.y+(b.h-boxH)/2
	mouse := msg.Mouse()
	x, y := mouse.X-originX-paneInsetX, mouse.Y-originY-paneInsetY
	if x < 0 || y < 0 || x >= w || y >= h {
		return nil, false
	}
	mouse.X, mouse.Y = x, y
	// Each concrete message is a defined type over Mouse, so the kind has to be
	// carried across by hand — the program distinguishes a click from motion.
	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mouse), true
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mouse), true
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(mouse), true
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(mouse), true
	}
	return nil, false
}

func paneFits(deckW, deckH int) bool {
	w, h := paneDims(deckW, deckH)
	return w >= paneMinW && h >= paneMinH
}

func (p *panePopover) renderPopover(m *Model, b box) string {
	// Where the pointer will find these cells, for the mouse to translate against.
	p.lastBox = b
	w, h := paneDims(b.w, b.h)
	if w != p.setW || h != p.setH {
		// The deck was resized, so the pty and the emulator have to follow
		// together or the process lays out for one width while we render at
		// another.
		if err := p.term.Resize(w, h); err == nil {
			p.setW, p.setH = w, h
		}
	}

	boxW, _ := paneBox(w, h)
	// A pane the keyboard has left drops its border a tier, per the design
	// system: in a split exactly one half may look like the active one, and the
	// border is the only chrome a pane has to say it with. The program inside is
	// untouched — it goes on painting whatever it paints.
	border := colAccent
	if b.blurred {
		border = colMuted
	}
	screen := p.term.View()
	// The selection is painted over the program's own screen rather than being
	// something the program knows about, because it is not the program's: awp made
	// it, out of cells the program has already drawn.
	if p.paneSelects() {
		screen = tintSelection(screen, p.selectionRows(w), w)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Width(boxW).
		Render(screen)
}
