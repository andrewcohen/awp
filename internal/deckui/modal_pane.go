package deckui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// paneLeaveKey gives the keyboard back to the deck.
//
// It has to be a key nothing inside the pane wants, because everything else
// belongs to the program: esc, q and ctrl+c all mean something to an agent.
// ctrl+\ is normally SIGQUIT, which is exactly why nothing interactive binds
// it, and the deck reads it as a key because its own terminal is in raw mode.
const paneLeaveKey = "ctrl+\\"

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
	term    *vterm.Term
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

// openPane hosts the given window kind for the selected row. It reports false
// when there is no backend for it, so the caller can fall back to tmux.
func (m *Model) openPane(item Item, kind string) (tea.Cmd, bool) {
	if m.panes == nil || !m.panes.Describes(kind) {
		return nil, false
	}
	handover := os.Getenv(PaneExecEnv) != ""
	// A handed-over pane is the whole terminal, so there is no size it does not
	// fit. The emulated one has to leave room for its own chrome.
	if !handover && !paneFits(m.width, m.height) {
		m.status = fmt.Sprintf("this terminal is %dx%d, too small for a pane", m.width, m.height)
		return nil, true
	}

	w, h := paneDims(m.width, m.height)
	cmd, restore, err := m.panes.Open(item, kind, w, h)
	if err != nil {
		m.status = PaneLabel(kind) + ": " + err.Error()
		return nil, true
	}
	if handover {
		return m.handOverTerminal(cmd, restore, PaneLabel(kind)), true
	}
	m.paneGen++
	term, err := vterm.Start(m.paneGen, w, h, cmd, m.hostColors)
	if err != nil {
		if restore != nil {
			restore()
		}
		m.status = PaneLabel(kind) + ": " + err.Error()
		return nil, true
	}

	p := &panePopover{
		term:    term,
		label:   PaneLabel(kind) + " · " + item.ProjectName + "/" + item.WorkspaceName,
		restore: restore,
		setW:    w,
		setH:    h,
		opened:  time.Now(),
	}
	m.active = p
	m.status = ""
	return tea.Batch(term.AwaitOutput(), term.AwaitExit()), true
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
		return paneExecDoneMsg{label: label, err: err}
	})
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
		if msg.String() == paneLeaveKey {
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
		inner, ok := paneMouse(msg, m.width, m.height)
		if !ok {
			return nil
		}
		p.term.SendMouse(inner)
		return nil
	}
	return nil
}

// The popover's chrome is one row and the border, and no more. Every cell it
// takes is one the hosted program does not get, and unlike the deck's other
// overlays — which frame a fixed amount of awp's own text — a pane is showing
// someone else's full-screen program.
//
// So there is no padding, and the leave hint shares the header row with the
// label instead of costing two more rows of its own. The border stays: it is
// what says where the pane ends when its program does not fill it.
const (
	paneHeaderRows = 1
	paneChromeW    = borderCells
	paneChromeH    = borderCells + paneHeaderRows
	paneMinW       = 20
	paneMinH       = 5
)

// paneInsetX / paneInsetY are where the terminal starts inside the popover:
// past the left border, and past the top border and the header row.
const (
	paneInsetX = 1
	paneInsetY = 1 + paneHeaderRows
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
func (p *panePopover) screenCursor(deckW, deckH int) (x, y int, ok bool) {
	if !paneFits(deckW, deckH) {
		return 0, 0, false
	}
	w, h := paneDims(deckW, deckH)
	boxW, boxH := paneBox(w, h)
	originX, originY := (deckW-boxW)/2, (deckH-boxH)/2
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
func paneMouse(msg tea.MouseMsg, deckW, deckH int) (tea.MouseMsg, bool) {
	if !paneFits(deckW, deckH) {
		return nil, false
	}
	w, h := paneDims(deckW, deckH)
	boxW, boxH := paneBox(w, h)
	originX, originY := (deckW-boxW)/2, (deckH-boxH)/2
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

func (p *panePopover) renderPopover(m *Model) string {
	w, h := paneDims(m.width, m.height)
	if w != p.setW || h != p.setH {
		// The deck was resized, so the pty and the emulator have to follow
		// together or the process lays out for one width while we render at
		// another.
		if err := p.term.Resize(w, h); err == nil {
			p.setW, p.setH = w, h
		}
	}

	boxW, _ := paneBox(w, h)
	body := lipgloss.JoinVertical(lipgloss.Left, p.header(m, w), p.term.View())
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Width(boxW).
		Render(body)
}

// header is the pane's one row of chrome: what you are looking at on the left,
// how to leave on the right. It doubles as the status line the hint used to
// have a row of its own for.
func (p *panePopover) header(m *Model, w int) string {
	hint := m.styles.PaneHint.Render(paneLeaveKey + " deck")
	label := m.styles.PaneTitle.Render(truncate(p.label, w-lipgloss.Width(hint)-1))
	gap := w - lipgloss.Width(label) - lipgloss.Width(hint)
	if gap < 1 {
		// Too narrow for both; the label is the one you can infer without.
		return hint
	}
	return label + strings.Repeat(" ", gap) + hint
}
