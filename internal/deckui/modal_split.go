package deckui

import (
	"fmt"
	"strings"

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

// splitPrefixHint is the verb menu, shown in the status bar while the prefix is
// up.
const splitPrefixHint = PaneLeaveKey + ": h/l/tab focus · < > = size · o zoom · x close this half · q leave · esc cancel"

// prefixKey reads one key while the prefix is armed. It returns the command to
// run; the prefix is always disarmed by it, since every key either is a verb or
// cancels.
func (s *splitModal) prefixKey(m *Model, pressed string) tea.Cmd {
	if pressed == PaneLeaveKey {
		// The reserved key again re-arms rather than resolving, which is the whole
		// reason a held key cannot do anything here. It was going to be the verb
		// for "leave" — two taps, the way tmux spells its own prefix twice — until
		// the test for a key repeat pointed out that a repeat and a deliberate
		// double tap are the same bytes. Without the Kitty keyboard protocol
		// nothing can tell them apart, so leaving got a letter instead.
		m.status = splitPrefixHint
		return nil
	}
	s.prefixArmed = false
	switch pressed {
	case "q":
		// The deck's own key for leaving a thing, behind the prefix so it does not
		// have to be taken from either half — `q` in a shell is a command and in
		// the diff viewer is already bound.
		m.status = ""
		return s.close(m)
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
		s.leftFrac = splitEvenFrac
	case "o":
		s.zoomed = !s.zoomed
	case "x":
		return s.closeHalf(m)
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

// splitMinW is the narrowest terminal a split is worth opening in.
//
// Two halves of a 100-column terminal are two 50-column panes, and a 50-column
// diff is not a diff — it is a column of line numbers and the left third of the
// code. Below this the split refuses and says the width it wants, rather than
// opening something technically correct and useless. paneMinW is the floor for a
// pane to exist at all, which is a different and much lower question.
const splitMinW = 120

// splitFits reports whether a box can carry a split at all.
func splitFits(b box) bool { return b.w >= splitMinW && paneFits(b.w/2, b.h) }

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

// resize moves the divider by frac of the width, clamped, and says where it
// ended up. It reports the new share rather than staying silent because the
// divider has no gutter to grab and no number on it — the status line is where
// you see that the key did anything at all.
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
	m.status = fmt.Sprintf("split: %d / %d columns", after, max(0, m.width-after))
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
	left, right := s.boxes(s.bodyBox(full))
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
			return s.prefixKey(m, pressed)
		}
		if pressed == PaneLeaveKey {
			s.prefixArmed = true
			m.status = splitPrefixHint
			return nil
		}
		return s.deliver(m, s.focused(), msg)
	}
	if _, isPaste := msg.(tea.PasteMsg); isPaste {
		return s.deliver(m, s.focused(), msg)
	}
	if _, isMouse := msg.(tea.MouseMsg); isMouse {
		// A click is where it landed, not where the keyboard was. It also moves
		// the keyboard there, which is what a mouse is for.
		return s.deliver(m, s.halfAt(m, msg.(tea.MouseMsg)), msg)
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
	}
	return nil
}

// halfAt is which half a mouse event landed in.
func (s *splitModal) halfAt(m *Model, msg tea.MouseMsg) modal {
	left, right := s.boxes(m.childBox())
	x := msg.Mouse().X
	if right.w > 0 && x >= right.x {
		s.rightFocused = true
		return s.right
	}
	if left.w > 0 {
		s.rightFocused = false
		return s.left
	}
	return s.focused()
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
	return tea.Batch(cmd, s.collapse(m, going))
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

// splitBarRows is the one row of chrome the split keeps for itself, above both
// halves and spanning the terminal.
const splitBarRows = 1

// bodyBox is what is left for the halves once the status row has taken its own.
//
// Both the renderer and boxOf go through this. They used to each do the
// arithmetic, and only the renderer remembered the status row — so a hosted
// program painted one row lower than the deck thought its terminal started,
// which is where its cursor was drawn and where a click was translated from.
func (s *splitModal) bodyBox(b box) box {
	b.y += splitBarRows
	b.h -= splitBarRows
	return b
}

// renderPopover draws the status row, then both halves beneath it.
//
// A popover rather than a body modal because the halves carry their own inner
// chrome and the deck's footer beneath them would be a third status line on a
// screen that already has two.
func (s *splitModal) renderPopover(m *Model, b box) string {
	bar := s.statusBar(m, b)
	body := s.bodyBox(b)
	if s.zoomed {
		return bar + "\n" + renderChild(m, s.focused(), body.focus(true))
	}
	left, right := s.boxes(body)
	return bar + "\n" + lipgloss.JoinHorizontal(lipgloss.Top,
		renderChild(m, s.left, left),
		renderChild(m, s.right, right))
}

// statusBar is the split's one row: what needs you, what the two halves are and
// which of them has the keys, and how to leave.
//
// It is above the halves rather than inside them because it answers for the
// screen rather than for either half — the attention badge is the deck's, and the
// leave key leaves the whole arrangement. A copy in each half's own header said
// the same numbers twice and the same key twice, and neither copy could mark
// which half was which.
//
// It is also where the armed prefix's menu goes. That went over the bottom
// border in the first cut, for want of a row the split owned; this is that row,
// so nothing is painted over a border any more.
func (s *splitModal) statusBar(m *Model, b box) string {
	if s.prefixArmed {
		return padBar(m.styles.FindHeader.Render(truncate(splitPrefixHint, max(1, b.w))), b.w)
	}
	segs := make([]string, 0, 3)
	if badge := m.renderAttentionSummary(countAttention(m.mergedItemsAll())); badge != "" {
		segs = append(segs, badge)
	}
	segs = append(segs, s.halfNames(m))
	left := strings.Join(segs, m.styles.PaneHint.Render(" · "))

	hint := m.styles.PaneHint.Render(PaneLeaveKey + " keys · " + PaneLeaveKey + " q deck")
	gap := b.w - lipgloss.Width(left) - lipgloss.Width(hint)
	if gap < 1 {
		// Too narrow for both. The hint is how you get out of an arrangement whose
		// keys are not the ones any half would suggest; the labels are readable off
		// the halves themselves.
		return padBar(hint, b.w)
	}
	return padBar(left+strings.Repeat(" ", gap)+hint, b.w)
}

// halfNames is the two halves, with the focused one marked by the selection
// treatment the rest of the deck uses for "this is the active row".
func (s *splitModal) halfNames(m *Model) string {
	name := func(child modal, fallback string, focused bool) string {
		label := fallback
		if p, ok := child.(*panePopover); ok {
			// A pane's own label is "<kind> · <project>/<workspace>". Only the kind
			// belongs here: both halves are the same workspace, so naming it twice
			// spends the row on the one thing that cannot distinguish them.
			if kind, _, found := strings.Cut(p.label, " · "); found {
				label = kind
			} else {
				label = p.label
			}
		}
		if focused {
			return m.styles.Bar.Render("┃") + " " + m.styles.Selected.Render(label)
		}
		return m.styles.Muted.Render("  " + label)
	}
	return name(s.left, PaneKindAgent, !s.rightFocused) +
		name(s.right, s.label, s.rightFocused)
}

// padBar fills a bar row out to the width, so the row is opaque rather than
// letting the frame beneath show through the cells it did not write.
func padBar(bar string, w int) string {
	if pad := w - lipgloss.Width(bar); pad > 0 {
		return bar + strings.Repeat(" ", pad)
	}
	return bar
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
	full := m.childBox()
	if !splitFits(full) {
		m.status = fmt.Sprintf("split: this terminal is %d columns, %d needed for two panes", full.w, splitMinW)
		return nil, true
	}
	// The right half's box is what the kind is opened into. Built from a
	// throwaway split so the arithmetic is the same one the renderer will do,
	// rather than a second copy of it that agrees today.
	probe := &splitModal{}
	leftBox, rightBox := probe.boxes(probe.bodyBox(full))

	left, cmdLeft, ok := m.openChild(item, PaneKindAgent, leftBox)
	if !ok {
		return nil, true
	}
	right, cmdRight, ok := m.openChild(item, kind, rightBox)
	if !ok {
		// The agent half opened and its neighbour did not, so there is nothing
		// to put beside it. Left as a whole pane rather than torn down: it is
		// what `a` would have given you, and the status line says why the rest
		// did not happen.
		m.active = left
		return cmdLeft, true
	}
	s := &splitModal{left: left, right: right, rightFocused: true, label: PaneLabel(kind)}
	m.active = s
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
		return dm, loadCmd, true
	}
	p, cmd, handled := m.newPane(item, kind, b)
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
