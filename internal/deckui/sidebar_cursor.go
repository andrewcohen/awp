package deckui

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// The strip's cursor: what makes it somewhere you can go rather than only something
// you can see (#350).
//
// The strip was built to read and do nothing, and the argument for that was about
// focus: a keyboard in here would have to say what focus means with a pane, a split
// half and a strip on screen at once. What answers it is making the door a cycle
// rather than a mode. ctrl+\ already means "somewhere else", and it now means it
// three times — pane → sidebar → deck → pane — so there is no second binding to
// learn and no state to be in that a single key does not leave.
//
// The cursor itself is a row, not an index. The strip renders the all scope,
// unfiltered, partitioned into bands; the row list renders whatever scope and filter
// it is set to. An index means a different workspace on each, and it means a
// different one again after the poll that re-bands a row. So the two surfaces share
// the row and each resolves its own position — see focusRow, which is the same trade
// in the other direction, and keepCursorOn, which is the row list making it against
// its own list.

// sidebarRowsInOrder is every row the strip lists, top to bottom.
//
// The same call sidebarLines makes, so the order the cursor walks and the order on
// screen cannot disagree. It takes the groups apart again because a cursor does not
// care which band a row is in — j from the last waiting row goes to the first error
// row, the way a cursor crossing a project header does on the row list.
func (m Model) sidebarRowsInOrder() []Item {
	v := m.sidebarView()
	groups := sidebarSections(v.Items(), m.pinGroupAliases, time.Now())
	rows := make([]Item, 0, len(v.Items()))
	for _, g := range groups {
		rows = append(rows, g.items...)
	}
	return rows
}

// sidebarCursorIndex is where the cursor sits in that order, or -1 when the row it
// names is not on the strip at all.
//
// Resolved every time rather than stored, which is the point of holding the cursor
// as a row: a workspace that was deleted, or that a refresh moved from `working` to
// `waiting`, does not leave an index pointing at whatever slid into its place.
func (m Model) sidebarCursorIndex() int {
	if !m.sidebarHasCursor() {
		return -1
	}
	for i, it := range m.sidebarRowsInOrder() {
		if sameRow(m.sidebarCursor, it) {
			return i
		}
	}
	return -1
}

// enterSidebar gives the strip the keyboard, seeding its cursor from the row list's.
//
// Seeded rather than remembered, so arriving at the strip lands on the row the deck
// was already pointing at — which, since the pane keys move the row list's cursor to
// whatever they opened, is normally the workspace you are in. A cursor that
// remembered where it was last would be pointing at whatever you were reading before
// the pane you are now leaving.
//
// A row the strip does not list falls back to its first row, and an empty strip
// takes no cursor at all rather than an invented one.
func (m *Model) enterSidebar() {
	m.sidebarFocus = true
	if sel, ok := m.selected(); ok {
		m.sidebarCursor = sel
		if m.sidebarCursorIndex() >= 0 {
			return
		}
	}
	rows := m.sidebarRowsInOrder()
	if len(rows) == 0 {
		m.sidebarCursor = Item{}
		return
	}
	m.sidebarCursor = rows[0]
}

// leaveSidebar takes the keyboard back off the strip, leaving the row list's cursor
// on the row the strip's was on.
//
// The write-back is what makes it one cursor rather than two: walking the strip and
// then going on to the deck has to arrive at the row you walked to, or the cycle's
// second step would silently undo its first. focusRow declines a row the row list
// does not have — filtered out, or in another scope — which leaves the row list's
// cursor where it was rather than moving it somewhere arbitrary.
func (m *Model) leaveSidebar() {
	if m.sidebarHasCursor() {
		m.takeRowToTheDeck(m.sidebarCursor)
	}
	m.sidebarFocus = false
}

// takeRowToTheDeck puts the row list's cursor on a row the strip was pointing at,
// showing the row if the list was not showing it.
//
// focusRow alone declines a row the list does not hold — filtered out, or in another
// scope — and leaving the cursor put is the right answer for a *click*, which is a
// gesture made while reading. It is the wrong answer here: the next key is aimed at
// this row, and a cursor that quietly stayed where it was would aim it at a different
// workspace. The strip is the all scope, unfiltered, so that is the view the row is
// known to exist in, and the deck goes there rather than losing the row.
//
// Only when it has to. A row already on screen changes nothing, which is nearly
// always — both surfaces start in the all scope with no filter, and they diverge only
// when a filter was typed or the scope was cycled.
func (m *Model) takeRowToTheDeck(row Item) {
	m.focusRow(row)
	if sel, ok := m.selected(); ok && sameRow(sel, row) {
		return
	}
	if m.filter != "" {
		m.filter = ""
		m.filterInput.SetValue("")
	}
	m.scope = ScopeAll
	m.focusRow(row)
	m.status = sidebarLabel(row) + ": shown in the all scope, which is what the sidebar lists"
}

// enterSidebarFromPane is the cycle's first step, and reports whether it happened.
//
// It answers false when the strip is not on screen, and then ctrl+\ means what it
// always meant — a pane on a terminal too narrow for a strip, or with the strip
// turned off, still leaves straight to the deck on one press. So the key gains a
// stop rather than changing what it does, and the version of awp you already know
// how to use is the one where you never turned the sidebar on.
func (m *Model) enterSidebarFromPane() bool {
	if !m.showsSidebar() {
		return false
	}
	m.enterSidebar()
	m.status = ""
	return true
}

// sidebarKey handles the keys the strip owns while it holds the keyboard, and
// reports whether it took the press. Everything it declines falls through to the
// deck's own row-mode dispatch — the strip has no key list of its own, which is the
// whole point of #350.
//
// Three it takes:
//
// ctrl+\ goes on to the deck. The cycle's second step, and it closes the pane on the
// way, because the deck is a screen rather than an overlay — pane → sidebar → deck →
// pane, where the last leg is km.ResumePane, which the row list already had.
//
// esc gives the keyboard back to the pane. It has to be taken here because the
// row-mode Quit binding reads esc as "quit the deck", which from a strip in front of
// a live pane is the most expensive reading of the cheapest key. `q` keeps quitting:
// it means the same thing on every deck surface and this is one.
//
// ctrl+b is the arrangement's, not the strip's, and the arrangement is still on
// screen. Forwarded rather than swallowed so ctrl+b S can still hide the strip you
// are standing on — see the guard in Update, which hands the keyboard back when it
// goes.
func (m *Model) sidebarKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.showsSidebar() {
		// The strip went away underneath the keyboard: the terminal was resized under
		// the minimum, or ctrl+b S hid it. The keys belong to whatever is still there.
		m.sidebarFocus = false
		return nil, false
	}
	if paneMenuPressed(m, msg) && m.active != nil {
		return m.active.update(m, msg), true
	}
	switch msg.String() {
	case PaneLeaveKey:
		if msg.IsRepeat {
			// Held, not pressed again — the same guard the pane and the split have,
			// and for the same reason: this key is a cycle, so a repeat that gets
			// through walks the whole cycle for as long as the key is down (#307).
			return nil, true
		}
		return m.sidebarToTheDeck(), true
	case "esc":
		m.sidebarFocus = false
		m.status = ""
		return nil, true
	}

	// Movement is the strip's own, because the strip is what is on screen to move
	// through. The bindings are the row list's — one keymap, so j is j on both.
	km := m.keymap
	switch {
	case key.Matches(msg, km.Down):
		m.moveSidebarCursor(1)
		return nil, true
	case key.Matches(msg, km.Up):
		m.moveSidebarCursor(-1)
		return nil, true
	case key.Matches(msg, km.HalfPageDown):
		m.moveSidebarCursor(sidebarHalfPage)
		return nil, true
	case key.Matches(msg, km.HalfPageUp):
		m.moveSidebarCursor(-sidebarHalfPage)
		return nil, true
	case key.Matches(msg, km.GotoBottom):
		m.moveSidebarCursor(len(m.sidebarRowsInOrder()))
		return nil, true
	case key.Matches(msg, km.GotoTop):
		// Arms the chord; the second `g` lands on the strip — see the gotoTopPending
		// branch in Update, which is the deck's and is checked before this is.
		m.gotoTopPending = true
		m.status = "g again for the top"
		return nil, true
	}

	// enter is going *to* a workspace, not putting one beside what you are in. It is
	// the same act a click on the row is, so it is the same function — see
	// goToSidebarRow, whose comment makes the argument: two ways into a workspace that
	// opened different things would be the two disagreeing about what entering one is.
	//
	// That is also why it is not on the list below. A window key says "show me this
	// thing about that row" and the answer belongs beside what you are already in; a
	// bare enter says "take me there", and taking you there is what it has always
	// meant on the row list. Resuming that workspace's own arrangement is part of it:
	// a workspace last left as a split comes back as that split, which it cannot do as
	// half of somebody else's.
	if key.Matches(msg, km.Enter) {
		m.sidebarFocus = false
		return m.goToSidebarRow(m.sidebarCursor), true
	}

	// A key that opens a program keeps the pane you were in and puts the program
	// beside it, which is what `|` means everywhere else. The strip is a thing you
	// glance at *while working in something*, so a row you pick off it is nearly
	// always the second thing you want on screen rather than a replacement for the
	// first — and the pane is the expensive one to lose, since re-opening it repaints
	// a program you were reading mid-thought.
	//
	// The same keys, the same vocabulary: `c` diff, `v` vcs, `e` editor, `s` shell,
	// `i` ci, `W` watch. From a split it replaces the focused half, exactly as ctrl+b
	// and a window key do in there.
	if kind, ok := sidebarProgramKind(msg); ok {
		return m.openBesideFromSidebar(kind), true
	}

	// Everything else opens one of the deck's own screens — a confirm, a form, a
	// picker, a menu — about the row under the cursor. Those float *here*, over the
	// arrangement, rather than sending you back to the row list to answer them.
	//
	// The pane is what you are working in and the question is about a different
	// workspace. Answering a yes/no about `flaky-login-test` should not cost the
	// program you were reading, and the strip's whole argument is that you should not
	// have to leave what you are in to deal with what is waiting — a verb that threw
	// the pane away would be re-introducing the trip the strip exists to avoid.
	//
	// So the arrangement steps aside rather than closing: it moves to m.overlayHost,
	// which keeps it alive and off m.active while the modal has the screen, and
	// restoreOverlayHost puts it back the moment nothing is floating over it. The row
	// list's cursor still moves to the strip's row, because that is what the verb
	// about to run reads.
	m.suspendForOverlay()
	return nil, false
}

// suspendForOverlay steps the arrangement aside so a modal can float over it.
//
// The keyboard leaves the strip because the modal is what has it now — but
// sidebarFocus is the flag the restore reads to know where to put the keys back, so
// it is remembered rather than dropped. Coming out of a confirm lands you on the
// strip you pressed the key from, on the same row.
func (m *Model) suspendForOverlay() {
	if m.sidebarHasCursor() {
		m.takeRowToTheDeck(m.sidebarCursor)
	}
	m.sidebarFocus = false
	if h, ok := m.active.(hostedModal); ok {
		m.overlayHost = h
		m.overlayReturns = true
		m.active = nil
	}
}

// restoreOverlayHost puts the arrangement back on screen once nothing is floating
// over it, and the keyboard back on the strip.
//
// The condition is "nothing else owns the screen" rather than "the modal that opened
// closed", because what a verb opens is not always what it ends in: `D` opens a
// confirm, the confirm starts a delete, and the delete's progress log owns the screen
// after the confirm is gone. Asking about the screen rather than about a particular
// modal covers all of it, and is one check rather than one per verb.
func (m *Model) restoreOverlayHost() {
	if m.overlayHost == nil || !m.overlayIdle() {
		return
	}
	m.active = m.overlayHost
	m.overlayHost = nil
	if m.overlayReturns && m.showsSidebar() {
		m.sidebarFocus = true
	}
	m.overlayReturns = false
}

// hostFrame is the arrangement drawn as if nothing were over it: the pane or split,
// the strip beside it, the deck's row above.
//
// Rendered by putting the arrangement back on a copy of the model rather than by a
// second renderer — this is the frame view() already knows how to draw, and a
// separate path for "the same screen, but underneath something" would be the one
// that drifts. The copy is why m is a value receiver everywhere in the render path.
func (m Model) hostFrame() string {
	m.active = m.overlayHost
	m.overlayHost = nil
	// The strip is drawn as the keyboard left it: the cursor's bar goes muted, which
	// is exactly right — the keys are in the modal on top, and the bar is where they
	// come back to.
	m.sidebarFocus = false
	return m.view()
}

// overlayBox is what is floating: the box itself, not a screen with the box on it.
//
// The distinction is the whole thing. view() hands a popover back already placed on
// a full-size blank canvas, and compositing *that* over the arrangement paints the
// blank canvas over it too — the frame is still there and every cell of it has been
// covered by a space. So a popover is asked for its own box and nothing else, and
// the compositor is what places it.
//
// A modal that is genuinely a screen — a form, a picker, the progress log — has no
// box to ask for and returns the screen. It covers the frame because that is what it
// is: a picker is not a question about what is behind it. The arrangement is still
// held, so it comes back when the screen goes.
func (m Model) overlayBox() string {
	m.overlayHost = nil
	if pm, ok := m.active.(popoverModal); ok {
		return pm.renderPopover(&m, m.childBox())
	}
	return m.view()
}

// overlayIdle reports whether the screen is free — nothing modal on it, and no mode
// that renders in place of the deck's body.
//
// The list is the branches of view() that come before the arrangement's own, which
// is the definition of "something else is on screen" this has to agree with. A mode
// missing from here reads as the pane coming back underneath a form still being
// typed into.
func (m *Model) overlayIdle() bool {
	return m.active == nil &&
		!m.progressMode &&
		!m.newWorkspaceMode &&
		!m.renameMode &&
		!m.promptMode &&
		!m.filtering &&
		!m.findMode &&
		!m.actionMode
}

// sidebarProgramKind is the window kind a key names, for the keys that open one
// beside what is already on screen.
//
// splitKindFor and nothing added to it: the same table the `|` chord and both ctrl+b
// menus read, so the strip cannot drift into a fourth spelling of the window keys.
// The chord has no key for the agent — `|a` would be the agent beside itself — and
// the strip does not add one, because the way to a row's agent from here is enter,
// which goes to that workspace rather than borrowing half of this one's screen.
func sidebarProgramKind(msg tea.KeyPressMsg) (string, bool) {
	return splitKindFor(msg.String())
}

// openBesideFromSidebar puts the strip's row on screen beside what is already there.
func (m *Model) openBesideFromSidebar(kind string) tea.Cmd {
	row, ok := m.sidebarProgramRow()
	if !ok {
		return nil
	}
	switch active := m.active.(type) {
	case *panePopover:
		return active.splitWith(m, row, kind)
	case *splitModal:
		return active.replaceRight(m, row, kind)
	}
	// No arrangement to respect. Nothing reaches here today — the strip is only ever
	// on screen over one — but the fallback is the plain thing rather than nothing.
	m.leaveSidebar()
	cmd, _ := m.openPaneOrArrangement(row, kind)
	return cmd
}

// sidebarProgramRow is the strip's row, checked for the things that make opening a
// program from it meaningless.
//
// The same two refusals a click gets (see goToSidebarRow), and worth repeating
// rather than sharing: a virtual inbox row has no workspace to open, and a workspace
// still being set up has no directory to open one in yet.
func (m *Model) sidebarProgramRow() (Item, bool) {
	row := m.sidebarCursor
	if !m.sidebarHasCursor() {
		return Item{}, false
	}
	if row.Virtual {
		m.status = "no workspace yet — " + PaneLeaveKey + " to the deck, then enter"
		return Item{}, false
	}
	if _, blocked := m.blockIfSettingUp(row); blocked {
		return Item{}, false
	}
	return row, true
}

// sidebarHalfPage is what ctrl+d / ctrl+u move by on the strip.
//
// A count of rows rather than a fraction of the strip's height, because a row on the
// strip is two lines plus the blank between them and the bands put headers among
// them — so "half a screen" in lines is not a number of rows anyone can predict.
// Eight is about half a tall strip's rows, and it is the same jump on any terminal.
const sidebarHalfPage = 8

// sidebarToTheDeck is the cycle's second step: off the strip, out of the
// arrangement, onto the row list standing on the row the strip was on.
func (m *Model) sidebarToTheDeck() tea.Cmd {
	m.leaveSidebar()
	if h, ok := m.active.(hostedModal); ok {
		return h.close(m)
	}
	return nil
}

// moveSidebarCursor walks the cursor by delta rows, clamped at both ends.
//
// Clamped rather than wrapped, which is what the row list does (see the Down and Up
// cases in Update) — and the strip is a list of the same rows, so it stops where
// that stops.
func (m *Model) moveSidebarCursor(delta int) {
	rows := m.sidebarRowsInOrder()
	if len(rows) == 0 {
		m.sidebarCursor = Item{}
		return
	}
	i := m.sidebarCursorIndex()
	if i < 0 {
		// The cursor's row has gone. Land on the end it was heading for rather than
		// on where it used to be, which is not a place any more.
		if delta < 0 {
			i = len(rows) - 1
		} else {
			i = 0
		}
		m.sidebarCursor = rows[i]
		return
	}
	m.sidebarCursor = rows[min(max(i+delta, 0), len(rows)-1)]
}
