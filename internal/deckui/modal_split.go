package deckui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// splitModal is two children side by side: the workspace's agent on the left,
// and one other thing you wanted to look at while it works on the right.
//
// It exists because the two halves of the job are not the same thing. Watching
// an agent and reading what it wrote are different activities, and doing them
// one at a time through a pane you enter and leave means the answer to "is this
// diff what it said it did" is a keystroke and a repaint away from the sentence
// that claimed it. The deck already had every piece: a child is a plain struct
// with a render method, and since 257a it is told the region it renders into
// rather than assuming it owns the screen. A split is what having that seam is
// for.
//
// Both halves are ordinary modals, so `|c` (a pty beside awp's own diff viewer)
// and `|v` (two ptys) are one feature rather than two. What differs between them
// is entirely inside the child.
type splitModal struct {
	// left is the agent, always. It is the reason the split exists — the other
	// half is what you wanted beside it.
	left modal
	// right is whatever the chord's second key named.
	right modal
	// rightFocused is where the keys go. The split opens with the right half
	// focused: you pressed `|c` because you want to read the diff, and the agent
	// is the reference beside it.
	rightFocused bool
	// zoomed gives the focused half the whole box. For the one case a split is
	// wrong — a wide diff, a moment of typing at the agent — without giving up
	// the other half and having to re-open it.
	zoomed bool
	// label is what the right half is, for the chrome. The left half is the
	// agent by construction and names itself.
	label string
	// leftFrac is how much of the width the left half gets, as a fraction rather
	// than a column count so a terminal resize keeps the divider where you put it
	// instead of leaving it wherever it happened to be in the old width.
	//
	// The zero value is not 0.5 — a split built without an opinion should be even,
	// and a struct literal that forgets the field would otherwise open with the
	// left half at zero columns. splitLeftFrac reads it, and treats zero as even.
	leftFrac float64
	// dragging is whether the pointer has the divider. Set by a press on it and
	// cleared by the release, so the motion in between belongs to the divider
	// rather than to whichever program the pointer has run over.
	dragging bool
	// prefixArmed is whether the last key was the reserved one, so the next key
	// is read as a verb rather than typed at whichever program has focus.
	//
	// A state resolved by the next key rather than by a clock: with no timeout
	// there is nothing to tune and nothing that behaves differently on a slow
	// day. It is also why a held key cannot thrash — re-arming an armed prefix
	// is idempotent, where the single-pane ctrl+\ that flips straight to the deck
	// repeats as fast as the terminal sends it.
	prefixArmed bool
}

// splitPrefixMenu is the verb menu for a split.
//
// The window keys are on it too, and mean here what they mean in a single pane's
// menu — that kind, on screen — except that with two halves already up there is
// nowhere to put a third, so they replace the focused one. Same key, same
// vocabulary, one arrangement's worth of difference in what it does.
//
// The arrangement verbs come first: they are what the menu is armed for while a
// split is already up, and the window keys under them are the longer list.
func splitPrefixMenu(m *Model) deckMenu {
	verbs := [][2]string{
		{"h/l/tab", "move the keyboard to the other half"},
		{"< >", "move the divider · = re-centres it"},
		{"o", "zoom the focused half, and again to go back"},
		{"x", "close the focused half"},
	}
	verbs = append(verbs, splitKindVerbs(func(label string) string {
		return "put " + label + " in this half"
	})...)
	verbs = append(verbs,
		[2]string{alternateKey, "go to the arrangement before this one"},
		captainVerb(),
		sidebarVerb(m.sidebar),
		menuCancelVerb,
	)
	return menu(verbs...)
}

// prefixKey reads one key while the menu is armed. It returns the command to run;
// the menu is always disarmed by it, since every key either is a verb or cancels.
func (s *splitModal) prefixKey(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	if paneMenuPressed(m, msg) {
		// The menu key again re-arms rather than resolving, so holding it cannot do
		// anything. Nothing to redraw: the menu is rendered from prefixArmed on every
		// frame rather than written anywhere when it opens.
		return nil
	}
	pressed := msg.String()
	s.prefixArmed = false
	if kind, ok := splitKindFor(pressed); ok {
		m.status = ""
		return s.replaceHalf(m, kind)
	}
	switch pressed {
	case "l", "right":
		s.rightFocused = true
	case "h", "left":
		s.rightFocused = false
	case "tab":
		s.rightFocused = !s.rightFocused
	case "<":
		s.resize(m, -splitResizeStep)
	case ">":
		s.resize(m, splitResizeStep)
	case "=":
		s.setFrac(m, splitEvenFrac)
	case "o":
		s.zoomed = !s.zoomed
	case "x":
		return s.closeHalf(m)
	case sidebarKey:
		m.toggleSidebar()
		return nil
	case alternateKey:
		return m.alternateFrom(func() tea.Cmd { return s.close(m) })
	case captainKey:
		// Takes the whole split down, both halves. The captain is a place you go
		// rather than a half you add: it is about no workspace, so it has no
		// business sitting beside one workspace's agent.
		return m.captainFrom(func() tea.Cmd { return s.close(m) })
	}
	// Anything else — esc included — cancels, having consumed the key. It does
	// not fall through to the focused program: a mistyped verb typing itself at
	// an agent is how a prefix becomes something you stop trusting.
	m.status = ""
	return nil
}

// hostedPane is the pane whose program the terminal's mouse and cursor belong
// to, when one is on screen at all.
//
// The pane itself when it fills the deck; the focused half when a split is up,
// and only if that half is a pane — a cursor drawn for the diff viewer's half
// while the keys are in the agent's would be a blinking block in the wrong
// program. The two callers are the mouse mode and the cursor position, both of
// which have exactly one answer per frame.
func (m Model) hostedPane() (*panePopover, bool) {
	switch c := m.active.(type) {
	case *panePopover:
		return c, true
	case *splitModal:
		p, ok := c.focused().(*panePopover)
		return p, ok
	}
	return nil, false
}

// splitFits reports whether a box can carry a split at all: two halves that are
// each a pane, and nothing more.
//
// There used to be a second floor — 120 columns, on the argument that a 50-column
// diff is a column of line numbers and the left third of the code. True, and not
// the deck's call to make: the key was pressed, a narrow split is legible enough to
// be worth having on the terminal you are actually using, and `ctrl+b o` zooms a
// half to the whole screen for the moment it is not. A refusal that names a width
// you cannot change is a key that does nothing.
//
// paneFits is the floor that stays, because below it there is no pane — the pty
// would be started at a size no program lays out at.
func splitFits(b box) bool { return paneFits(b.w/2, b.h) }

// splitEvenFrac is the divider's resting place, and what `=` restores.
const splitEvenFrac = 0.5

// splitResizeStep is how far one `<` or `>` moves the divider, as a fraction of
// the width.
//
// A fraction rather than a column count so the same keypress feels the same on
// a 120-column terminal and a 400-column one. At 5% a wide terminal still moves
// by several columns a tap, which is what a resize key is for; a column at a
// time would be a key you hold, and holding a key behind a prefix does nothing
// (see prefixKey).
const splitResizeStep = 0.05

// splitHalfMinW is the narrowest a half may be squeezed to.
//
// The pane minimum plus its own chrome: below this a half is a border around
// nothing, and the pty behind it would be resized to a width its program cannot
// lay out. The clamp is why `<` held at the wall does nothing rather than
// collapsing a half you would then have to re-open.
const splitHalfMinW = paneMinW + paneChromeW

// resize moves the divider by frac of the width, clamped.
//
// It says nothing about where the divider ended up. It used to print "split: 84
// / 42 columns", which is a sentence restating a thing already on screen: you
// pressed the key and the divider moved. The bar above reports state in glyphs
// and numbers precisely so it can be glanced at, and prose about a resize is
// what that rule exists to keep off it.
func (s *splitModal) resize(m *Model, frac float64) {
	before := s.splitCol(m.childBox())
	s.leftFrac = splitLeftFrac(s.leftFrac) + frac
	after := s.splitCol(m.childBox())
	if after == before {
		// Clamped at a wall. Put the fraction back where the wall is, so repeated
		// taps do not accumulate a fraction the clamp is hiding and then have to be
		// undone one by one on the way back.
		s.leftFrac = float64(after) / float64(max(1, m.width))
	}
	s.setFrac(m, s.leftFrac)
}

// setFrac puts the divider at frac and remembers it in both places it is
// remembered: this arrangement, so leaving the split and coming back re-opens at
// this width, and the deck's preferences, so the *next* split — and the next deck —
// starts here rather than back at even.
//
// One function, because those are two different memories with two different scopes
// and a divider move has to reach both. It used to reach only the first, and `=` did
// not even reach that: it assigned leftFrac and returned, so re-centring a split was
// forgotten the moment you left it.
//
// Global rather than per workspace for the reason the sidebar is (#348): how wide you
// like the agent beside a diff is a fact about how you work, and a per-workspace width
// would resize the panes under you as you moved between rows.
func (s *splitModal) setFrac(m *Model, frac float64) {
	s.leftFrac = frac
	m.recordArrangement(s)
	m.rememberSplitFrac(frac)
}

// splitLeftFrac is the left half's share, reading the zero value as even.
func splitLeftFrac(frac float64) float64 {
	if frac <= 0 {
		return splitEvenFrac
	}
	return frac
}

// splitCol is the column the divider sits in for a given box: the left half's
// width, clamped so neither half falls under splitHalfMinW.
//
// One function so the renderer, the halves' boxes and the resize keys cannot
// come to disagree about where the divider is. In a box too narrow for two
// minimum halves it returns the middle and lets splitFits refuse the split —
// clamping against itself here would put the divider outside the box.
func (s *splitModal) splitCol(b box) int {
	col := int(splitLeftFrac(s.leftFrac) * float64(b.w))
	if b.w < 2*splitHalfMinW {
		return b.w / 2
	}
	return min(max(col, splitHalfMinW), b.w-splitHalfMinW)
}

// boxes divides the box the split was given between its two halves.
//
// The divider is at splitCol, which is even until you move it with `<` / `>`.
// An odd column goes to the right half. Whatever is over there was opened to be
// read — a diff, a log, a description — and the left is an agent whose output
// wraps to whatever it is given.
func (s *splitModal) boxes(b box) (left, right box) {
	if s.zoomed {
		// The unfocused half gets nothing, which is a box no renderer is asked
		// for: render skips it entirely rather than handing out a zero-width
		// region and trusting fifteen renderers to check.
		if s.rightFocused {
			return box{}, b.focus(true)
		}
		return b.focus(true), box{}
	}
	left, right = b.splitAt(s.splitCol(b))
	return left.focus(!s.rightFocused), right.focus(s.rightFocused)
}

// focused is the half the keys go to.
func (s *splitModal) focused() modal {
	if s.rightFocused {
		return s.right
	}
	return s.left
}

// boxOf answers where one of the halves lives inside the split's own box, for
// the mouse and the cursor. A child that is not one of the halves — or is the
// zoomed-away one — gets the empty box, which both callers read as "nowhere".
func (s *splitModal) boxOf(child modal, full box) box {
	left, right := s.boxes(full)
	switch child {
	case s.left:
		return left
	case s.right:
		return right
	}
	return box{}
}

// update routes the message to the focused half.
//
// Non-key messages go to both, because the half without the keyboard is still
// running: a pty's output, its exit, a refresh landing behind the diff. Routing
// those by focus is how the unfocused half would silently stop painting.
//
// A half that closes itself — a pty whose process exited, a viewer that was
// quit — collapses the split onto the other one rather than taking it down.
// Children close by setting m.active to nil, which they do without knowing they
// are in a split, so that is what this watches for.
func (s *splitModal) update(m *Model, msg tea.Msg) tea.Cmd {
	if key, isKey := msg.(tea.KeyPressMsg); isKey {
		pressed := key.String()
		if s.prefixArmed {
			return s.prefixKey(m, key)
		}
		if paneMenuPressed(m, key) {
			s.prefixArmed = true
			return nil
		}
		if pressed == PaneLeaveKey {
			if key.IsRepeat {
				// Held, not pressed again — the same guard a single pane has, and for
				// the same reason: the deck's own ctrl+\ comes back in, so a repeat
				// that gets through flaps (#307).
				return nil
			}
			return s.close(m)
		}
		return s.deliver(m, s.focused(), msg)
	}
	if _, isPaste := msg.(tea.PasteMsg); isPaste {
		return s.deliver(m, s.focused(), msg)
	}
	if mouse, isMouse := msg.(tea.MouseMsg); isMouse {
		if s.dragDivider(m, mouse) {
			return nil
		}
		// Where the event goes and what holds the keyboard are two questions. An
		// event goes to the half under the pointer either way; only a click moves
		// the keyboard there.
		half := s.halfUnder(m.childBox(), mouse)
		if mouseTakesFocus(mouse) {
			s.focusHalf(half)
		}
		return s.deliver(m, half, msg)
	}
	left := s.deliver(m, s.left, msg)
	if m.active != s {
		// The left half collapsed the split while handling that message; the
		// right half is now the deck's child and has already been handed the
		// message it needs by the collapse.
		return left
	}
	return tea.Batch(left, s.deliver(m, s.right, msg))
}

// deliver hands one message to one half, and collapses the split if that half
// closed itself in the process.
func (s *splitModal) deliver(m *Model, child modal, msg tea.Msg) tea.Cmd {
	if child == nil {
		return nil
	}
	// The child sees itself as the deck's child while it runs, so a close that
	// sets m.active = nil is legible to us afterwards and its own view of the
	// world is not a special case.
	m.active = child
	cmd := child.update(m, msg)
	if m.active == child {
		m.active = s
		return cmd
	}
	// It closed itself (or replaced itself with something else — a form, a
	// confirm). Either way this half is done.
	return tea.Batch(cmd, s.collapse(m, child))
}

// collapse ends the split, leaving whichever half is still alive as the deck's
// child. Closing the last one leaves the row list.
func (s *splitModal) collapse(m *Model, gone modal) tea.Cmd {
	survivor := s.left
	if gone == s.left {
		survivor = s.right
	}
	// Whatever the departing half left in m.active is the deck's own business
	// again — a form it opened, or nil.
	if replacement := m.active; replacement != nil && replacement != gone {
		return nil
	}
	m.active = survivor
	if survivor == nil {
		return nil
	}
	if p, ok := survivor.(*panePopover); ok {
		m.status = p.label
		// What is on screen is one pane now, so that is what coming back should
		// find. Recorded here rather than only where a key takes a half off,
		// because this is the one place every collapse goes through: the diff
		// viewer quitting with `q`, a pane's program exiting, a half replaced by a
		// form. Only ctrl+b x used to record, so a split taken apart any other way
		// came back rebuilt.
		m.recordArrangementValue(paneArrangement{left: paneRef{
			project:   p.project,
			workspace: p.workspace,
			kind:      p.kind,
		}})
	}
	return nil
}

// splitGrabCols is how far either side of the divider counts as grabbing it.
//
// The two halves' borders butt, so the divider is two columns wide with nothing
// between them, and a target two columns wide is one you miss. One column of
// slack on each side costs nothing: the cells it takes belong to the borders,
// which is the one place in a half that is not a cell of a program.
const splitGrabCols = 1

// dragDivider handles a mouse event that belongs to the divider rather than to
// either half, reporting whether it consumed it.
//
// A press on the divider starts a drag, motion moves it, and anything else ends
// one. It is checked before the halves get a look because a press on the border
// is not a cell either program has — paneMouse already refuses it — so this is
// spending an event that was going nowhere.
//
// While dragging, every motion event is consumed regardless of where the pointer
// is: a hand that runs ahead of the divider (which is clamped, and stops) must
// not start typing at whichever program is under the pointer.
func (s *splitModal) dragDivider(m *Model, msg tea.MouseMsg) bool {
	if s.zoomed {
		// One half, no divider.
		return false
	}
	// A mouse event carries a screen column; the divider is a column of the box.
	// Converted once, here, because both branches below need it in the box's terms
	// and each having its own idea of the origin is what went wrong: the click test
	// compared a screen column against a box column, and the motion measured the
	// fraction against the whole terminal. Both were right while childBox started
	// at zero, and the sidebar (#333) is what stopped it.
	b := m.childBox()
	x := msg.Mouse().X - b.x
	switch msg.(type) {
	case tea.MouseMotionMsg:
		if !s.dragging {
			return false
		}
		// Where you dragged it to is where it should come back — the same reason the
		// keyboard resize records, and through the same function.
		s.setFrac(m, float64(x)/float64(max(1, b.w)))
		return true
	case tea.MouseClickMsg:
		col := s.splitCol(b)
		if x < col-1-splitGrabCols || x > col+splitGrabCols {
			return false
		}
		s.dragging = true
		return true
	default:
		// A release, a wheel, anything else: the drag is over. Consumed only if
		// there was one, so an ordinary click in a half still reaches it.
		if s.dragging {
			s.dragging = false
			return true
		}
		return false
	}
}

// halfUnder is which half a mouse event landed in. It moves nothing — see
// mouseTakesFocus for the other half of the question.
func (s *splitModal) halfUnder(b box, msg tea.MouseMsg) modal {
	left, right := s.boxes(b)
	x := msg.Mouse().X
	if right.w > 0 && x >= right.x {
		return s.right
	}
	if left.w > 0 {
		return s.left
	}
	return s.focused()
}

// focusHalf puts the keyboard in the named half.
func (s *splitModal) focusHalf(half modal) {
	if half == s.right {
		s.rightFocused = true
		return
	}
	if half == s.left {
		s.rightFocused = false
	}
	// Anything else is the zoomed-away half or nothing at all, and neither is a
	// place the keyboard can go.
}

// mouseTakesFocus reports whether a mouse event means "put the keyboard here".
//
// A click does: pointing at a half and pressing is how you say which one you mean,
// and that is what a mouse is for. Nothing else does.
//
// The wheel is the case this exists for (#340). Scrolling is reading — you turn the
// wheel over what you want to look at, not what you want to type into — so a wheel
// that moved the keyboard meant a glance at the diff sent your next keystroke into
// it instead of the agent, with nothing on screen having said so. Motion and release
// are excluded for a plainer reason: they belong to whatever gesture is already
// under way, and a pointer crossing the divider on its way somewhere is not a
// decision about anything.
func mouseTakesFocus(msg tea.MouseMsg) bool {
	_, click := msg.(tea.MouseClickMsg)
	return click
}

// closeHalf drops the focused half and leaves the other one as a whole pane.
func (s *splitModal) closeHalf(m *Model) tea.Cmd {
	going := s.focused()
	m.active = going
	var cmd tea.Cmd
	switch c := going.(type) {
	case *panePopover:
		cmd = c.close(m)
	case *diffModal:
		m.active = nil
	}
	// collapse records the survivor as the arrangement — see there; it is the same
	// answer whether a key took this half off or the half closed itself.
	return tea.Batch(cmd, s.collapse(m, going))
}

// replaceHalf swaps the focused half for a fresh child of the named kind.
//
// What a window key means with two halves already up: the same "put that on
// screen" as in a single pane's menu, with the only place to put it being the half
// you are looking at. The other half is untouched — it is the one you are keeping.
//
// The new child is built before the old one is closed, so a kind this deck cannot
// open leaves the split exactly as it was rather than half-torn-down.
func (s *splitModal) replaceHalf(m *Model, kind string) tea.Cmd {
	item, ok := m.topRowRow()
	if !ok {
		m.status = "split: this pane's workspace is not on the deck any more"
		return nil
	}
	left, right := s.boxes(m.childBox())
	b := left
	if s.rightFocused {
		b = right
	}
	child, cmd, ok := m.openChild(item, kind, b)
	// openChild installs what it built, because the paths it calls are the same ones
	// that open a whole-screen pane. The split is still what is on screen.
	m.active = s
	if !ok {
		return nil
	}
	var closed tea.Cmd
	if p, isPane := s.focused().(*panePopover); isPane {
		closed = p.close(m)
	}
	if s.rightFocused {
		s.right = child
	} else {
		s.left = child
	}
	s.label = PaneLabel(kind)
	m.recordArrangement(s)
	m.status = ""
	return tea.Batch(closed, cmd)
}

// close tears down both halves, which is what leaving the split means for the
// processes in it: nothing is left running behind the deck that the deck cannot
// see. The zmx sessions the panes were attached to keep running — closing a pane
// has never meant killing what it was showing.
func (s *splitModal) close(m *Model) tea.Cmd {
	var cmds []tea.Cmd
	for _, child := range []modal{s.left, s.right} {
		if p, ok := child.(*panePopover); ok {
			m.active = p
			cmds = append(cmds, p.close(m))
		}
	}
	m.active = nil
	return tea.Batch(cmds...)
}

func (s *splitModal) footerHelp() string { return s.focused().footerHelp() }

// renderPopover draws both halves, side by side, filling the box.
//
// The row above them is the deck's, not the split's — see host_bar.go, and
// childBox, which is what has already taken its row out of this box.
//
// A popover rather than a body modal because the halves are someone else's
// full-screen programs and the deck's footer beneath them would be a second
// status line on a screen whose first one is at the top.
func (s *splitModal) renderPopover(m *Model, b box) string {
	if s.zoomed {
		return renderChild(m, s.focused(), b.focus(true))
	}
	left, right := s.boxes(b)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		renderChild(m, s.left, left),
		renderChild(m, s.right, right))
}

// renderChild renders one modal into one box, whichever kind of modal it is.
//
// The two interfaces differ in what the deck does around them — a body modal
// gets the deck's footer, a popover is placed on a blank canvas — and inside a
// split neither of those applies: the half is the whole of what that region
// shows. So this is the one place that difference is flattened, rather than the
// split holding two typed fields and every operation being written twice.
func renderChild(m *Model, child modal, b box) string {
	if b.w <= 0 || b.h <= 0 {
		return ""
	}
	switch c := child.(type) {
	case popoverModal:
		return c.renderPopover(m, b)
	case bodyModal:
		// A body modal normally renders above the deck's footer and sizes itself
		// to leave room for it. A split has no deck footer — each half carries
		// its own chrome — so those rows are the child's after all, and without
		// this the half comes up short by exactly the footer and the frame ends
		// in a band of dead rows.
		b.h += footerRows
		left, right := c.view(m, b)
		if right == "" {
			return left
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return ""
}

// openSplit puts the row's agent on the left and the named kind on the right.
//
// Both halves are built through the paths that already build them, so a pane in
// a split is opened, sized, recorded and torn down exactly the way a whole-screen
// one is — the only difference being the box it is handed. That is the whole
// reason 257a came first.
func (m *Model) openSplit(item Item, kind string) (tea.Cmd, bool) {
	// The divider where you last left it, not the middle: how wide you like the agent
	// beside a diff is a preference, and a fresh split that always opened even meant
	// re-dragging it every time.
	if cmd, ok := m.openSplitKinds(item, PaneKindAgent, kind, splitLeftFrac(m.splitFrac)); ok {
		return cmd, true
	}
	return nil, true
}

// openSplitKinds is openSplit with both halves named and the divider placed, which
// is what re-opening a remembered split needs: the left half is whatever you were
// in when you split it, not necessarily the agent.
//
// Reports false when the split could not be built — a terminal too narrow, a kind
// this deck cannot open — having said why. The caller decides what to do instead;
// re-opening a remembered arrangement falls back to the left half alone, while the
// `|` chord has nothing to fall back to.
func (m *Model) openSplitKinds(item Item, leftKind, rightKind string, frac float64) (tea.Cmd, bool) {
	full := m.childBox()
	if !splitFits(full) {
		// The floor is a pane, so the number that matters is the pane's minimum, and it
		// is per half rather than for the terminal.
		m.status = fmt.Sprintf("split: this terminal is %d columns, %d needed for two panes", full.w, 2*(paneMinW+paneChromeW))
		return nil, false
	}
	// The right half's box is what the kind is opened into. Built from a
	// throwaway split so the arithmetic is the same one the renderer will do,
	// rather than a second copy of it that agrees today.
	probe := &splitModal{leftFrac: frac}
	leftBox, rightBox := probe.boxes(full)

	left, cmdLeft, ok := m.openChild(item, leftKind, leftBox)
	if !ok {
		return nil, false
	}
	right, cmdRight, ok := m.openChild(item, rightKind, rightBox)
	if !ok {
		// The left half opened and its neighbour did not, so there is nothing
		// to put beside it. Left as a whole pane rather than torn down: it is
		// what `a` would have given you, and the status line says why the rest
		// did not happen. Reported as a success for that reason — something is on
		// screen, and it is not the caller's fallback that put it there.
		m.active = left
		return cmdLeft, true
	}
	s := &splitModal{left: left, right: right, rightFocused: true, label: PaneLabel(rightKind), leftFrac: frac}
	m.active = s
	m.recordArrangement(s)
	m.status = ""
	return tea.Batch(cmdLeft, cmdRight), true
}

// openChild builds one half: a pane for a hosted kind, or the deck's own diff
// viewer for the review kind.
//
// It returns the child rather than installing it, which is the one thing the
// existing open paths do not do — they set m.active, because until now a child
// was the only child. So this calls them and takes what they installed, leaving
// the construction itself in exactly one place per kind.
func (m *Model) openChild(item Item, kind string, b box) (modal, tea.Cmd, bool) {
	if kind == SplitKindDiff {
		if m.diffLoad == nil {
			m.status = "split: the diff viewer is not wired up here"
			return nil, nil, false
		}
		dm, loadCmd := newDiffModal(item, ScopeStackBase, m.diffLoad, m.diffOpen, m.diffBase, m.diffScopes, m.diffComments)
		// Half a terminal, and the left column would spend a third of that on a
		// file tree and a comment index — where the diff is the thing you opened
		// the half to read. `\` brings them back for the file you need to jump
		// around in, which is the minority of the time in a split.
		dm.inner.HideLeftColumn(true)
		// And every file folded. Half a terminal is where you open a diff to answer
		// "what did it touch" before "what did it write", and an expanded first file
		// means scrolling past it to find out there are eight. `enter` opens one, and
		// having opened it you have said so, so it stays open.
		dm.inner.FoldFiles(true)
		return dm, loadCmd, true
	}
	p, cmd, handled := m.newPane(item, kind, b, false)
	if !handled {
		m.status = "split: nothing here hosts a " + PaneLabel(kind) + " pane"
		return nil, nil, false
	}
	if p == nil {
		// newPane already said why in m.status — or it handed the terminal over,
		// which is a whole-screen arrangement and not a half of anything.
		return nil, nil, false
	}
	return p, cmd, true
}

// SplitKindDiff is the right-half kind that is awp's own diff viewer rather than
// a hosted process.
//
// It is spelled as a kind so the chord's table has one column of them, but no
// backend describes it: a review is the one action the deck already renders
// itself, and running it as a pty would mean a second awp inside the first.
const SplitKindDiff = "diff"

// SplitFracSaver records where the split's divider should sit next time, as the
// left half's share of the width.
//
// A hook for the reason ScopeSaver and SidebarSaver are: deckui is the UI and has
// no business knowing where ~/.awp is.
//
// A fraction rather than a column count, because the same preference has to mean
// the same layout on a terminal of another width — and because splitCol already
// clamps it, a fraction chosen on a wide screen degrades to "as far over as this
// terminal allows" on a narrow one instead of needing to be validated on load.
type SplitFracSaver func(float64) error

// WithSplitFrac opens new splits at the divider position last left.
func (m Model) WithSplitFrac(frac float64) Model {
	m.splitFrac = frac
	return m
}

// WithSplitFracSaver sets the hook called when the divider moves.
func (m Model) WithSplitFracSaver(save SplitFracSaver) Model {
	m.saveSplitFrac = save
	return m
}

// rememberSplitFrac records the divider's new home for the next split and the next
// deck.
//
// A failed save is said, not refused: the divider is already where you put it, which
// is what you asked for, and all that is lost is that it will not be there next time.
// Same treatment the scope's and the sidebar's savers get.
func (m *Model) rememberSplitFrac(frac float64) {
	m.splitFrac = frac
	if m.saveSplitFrac == nil {
		return
	}
	if err := m.saveSplitFrac(frac); err != nil {
		m.status = fmt.Sprintf("split: %v", err)
	}
}
