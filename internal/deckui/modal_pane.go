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
	term    vterm.Hosted
	label   string
	restore func()
	setW    int
	setH    int
	// opened is when the process started, which is how the exit is judged. An
	// exit is only worth reporting on its own if it happened before you could
	// have read anything — see paneQuickExit.
	opened time.Time
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

// matches is the row-identity comparison, in one place because the deck asks it
// of both the scoped list and the unscoped one.
func (r paneRef) matches(it Item) bool {
	return it.ProjectName == r.project && it.WorkspaceName == r.workspace
}

// label is the pane in the words a status line uses: the program, then the row.
func (r paneRef) label() string {
	return PaneLabel(r.kind) + " · " + r.project + "/" + r.workspace
}

// openPane hosts the given window kind for the selected row, filling the deck.
// It reports false when there is no backend for it, so the caller can fall back
// to tmux.
func (m *Model) openPane(item Item, kind string) (tea.Cmd, bool) {
	p, cmd, handled := m.newPane(item, kind, m.childBox())
	if handled && p != nil {
		m.active = p
	}
	return cmd, handled
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
func (m *Model) newPane(item Item, kind string, b box) (*panePopover, tea.Cmd, bool) {
	if m.panes == nil || !m.panes.Describes(kind) {
		return nil, nil, false
	}
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
		m.recordPane(ref)
		return nil, m.handOverTerminal(cmd, restore, PaneLabel(kind)), true
	}
	m.paneGen++
	term, err := vterm.Open(m.paneGen, w, h, cmd, m.hostColors)
	if err != nil {
		if restore != nil {
			restore()
		}
		m.status = PaneLabel(kind) + ": " + err.Error()
		return nil, nil, true
	}

	p := &panePopover{
		term:    term,
		label:   PaneLabel(kind) + " · " + item.ProjectName + "/" + item.WorkspaceName,
		restore: restore,
		setW:    w,
		setH:    h,
		opened:  time.Now(),
	}
	m.recordPane(ref)
	m.status = ""
	return p, tea.Batch(term.AwaitOutput(), term.AwaitExit()), true
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
	if ref == m.lastPane {
		return
	}
	m.prevPane = m.lastPane
	m.lastPane = ref
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
		m.status = "no pane to go back to yet — open one first (a agent, e editor, v vcs, s shell)"
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
			m.status = "no pane to switch to yet — open one first (a agent, e editor, v vcs, s shell)"
		}
		return nil, true
	}
	return m.reopenPane(m.prevPane)
}

// reopenPane resolves a remembered pane against the rows as they are now and
// opens it, which is the half resumePane and alternatePane share.
func (m *Model) reopenPane(ref paneRef) (tea.Cmd, bool) {
	// The scoped list first, so the cursor can land on the row: leaving the pane
	// again has to put you on the row the pane was, or the keys and ctrl+\
	// disagree about where you are.
	for i, it := range m.items() {
		if ref.matches(it) {
			m.cursor = i
			return m.openRememberedPane(it, ref)
		}
	}
	// Not in the current scope — an agent that exited drops out of attention —
	// but the pane is still one you were in, and refusing to go back to it because
	// of a filter would make the key depend on which list you are looking at. No
	// cursor move: there is no row here to move it to.
	for _, it := range m.itemsAll {
		if ref.matches(it) {
			return m.openRememberedPane(it, ref)
		}
	}
	m.status = ref.label() + ": that workspace is not on the deck any more"
	return nil, true
}

// openRememberedPane is the tail of reopenPane, shared by the two lookups.
func (m *Model) openRememberedPane(it Item, ref paneRef) (tea.Cmd, bool) {
	if m2, blocked := m.blockIfSettingUp(it); blocked {
		*m = m2
		return nil, true
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
	_ = p.term.Close()
	if p.restore != nil {
		p.restore()
		p.restore = nil
	}
	if m.active == p {
		m.active = nil
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
		if msg.String() == PaneLeaveKey {
			if msg.IsRepeat {
				// Held, not pressed again. Swallowed rather than closed on or
				// forwarded: the deck's own ctrl+\ goes back into the pane this
				// would close, so a repeat that gets through flaps between the two
				// (#307), and passing it to the program means holding the key
				// sprays it at whatever is running.
				return nil
			}
			return p.close(m)
		}
		p.term.SendKey(msg)
		return nil

	case tea.PasteMsg:
		p.term.SendText(msg.Content)
		return nil

	case tea.MouseMsg:
		// The deck asks for mouse events only while a pane is up (see View),
		// so anything arriving here belongs to the hosted program — but in the
		// deck's coordinates, not its own.
		inner, ok := paneMouse(msg, m.boxOf(p))
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
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(border)).
		Width(boxW).
		Render(p.term.View())
}
