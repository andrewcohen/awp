package deckui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/ui"
	"github.com/andrewcohen/awp/internal/vterm"
)

// The strip's cursor (#350).
//
// What these pin is that the cursor is a *row* and not a position. Everything that
// went wrong in the surfaces this replaces went wrong the same way: an index kept
// across a re-sort names a different workspace than the one the user was looking at,
// and the next key they press is aimed at whatever slid into place.

// TestTheStripsCursorMarksOnlyItsOwnRow. The mark is the name's own hue and weight,
// so what is asserted is that exactly one row renders differently from how it renders
// unmarked — the hue itself cannot be read back, since lipgloss strips colour with no
// TTY.
func TestTheStripsCursorMarksOnlyItsOwnRow(t *testing.T) {
	m, _ := sidebarPane(t)
	rows := m.sidebarRowsInOrder()
	if len(rows) < 2 {
		t.Fatalf("need two rows to tell a marked one from an unmarked one, got %d", len(rows))
	}
	v := m.sidebarView()
	bare := make([]string, len(rows))
	for i, it := range rows {
		bare[i] = strings.Join(m.sidebarRow(v, it, sidebarDefaultWidth), "\n")
	}

	m.sidebarFocus = true
	m.sidebarCursor = rows[0]

	for i, it := range rows {
		got := strings.Join(m.sidebarRow(v, it, sidebarDefaultWidth), "\n")
		marked := got != bare[i]
		if want := i == 0; marked != want {
			t.Errorf("%s: marked %v, want %v", it.WorkspaceName, marked, want)
		}
	}
}

// TestTheStripsCursorWearsNoBar. The one place the strip departs from the design
// system's selection treatment, and deliberately: the `┃` needs a column ahead of the
// status dot on *every* line — a header that skipped it would sit two columns left of
// the names under it — and at 36 columns those two come off names that are already
// truncating.
func TestTheStripsCursorWearsNoBar(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarFocus = true
	m.sidebarCursor = m.sidebarRowsInOrder()[0]

	for _, l := range m.sidebarLines(box{w: sidebarDefaultWidth, h: 40}) {
		if plain := ansi.Strip(l.text); strings.Contains(plain, "┃") {
			t.Errorf("a bar came back: %q", plain)
		}
	}
}

// TestEveryStripLineSharesOneLeftEdge. Nothing on a row is indented from its section
// header: the dot sits in the header's own first column, so the headers' letters and
// the rows' names read as one edge instead of two.
//
// This is what the cursor's bar would have cost, and the test that would have caught
// it costing only some lines.
func TestEveryStripLineSharesOneLeftEdge(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarFocus = true
	m.sidebarCursor = m.sidebarRowsInOrder()[0]

	for _, l := range m.sidebarLines(box{w: sidebarDefaultWidth, h: 40}) {
		plain := ansi.Strip(l.text)
		if strings.TrimSpace(plain) == "" {
			continue
		}
		got := len(plain) - len(strings.TrimLeft(plain, " "))
		// A row's second line is inset under its name by the status dot's columns —
		// see sidebarIndent. Everything else shares the one edge.
		if got != sidebarPadX && got != sidebarPadX+len(sidebarIndent) {
			t.Errorf("line %q starts at column %d, want %d (or %d for a meta line)",
				plain, got, sidebarPadX, sidebarPadX+len(sidebarIndent))
		}
	}
}

// TestTheStripsCursorGoesQuietWhenTheKeyboardIsElsewhere. The tier the design system
// gives a pane the keyboard has left — spent here on the mark itself rather than on a
// dimmer version of it, since the row the keys come back to is the row you are in,
// which the band already says.
func TestTheStripsCursorGoesQuietWhenTheKeyboardIsElsewhere(t *testing.T) {
	m, _ := sidebarPane(t)
	it := m.sidebarRowsInOrder()[0]
	m.sidebarCursor = it
	v := m.sidebarView()

	m.sidebarFocus = true
	focused := m.sidebarRow(v, it, sidebarDefaultWidth)[0]
	m.sidebarFocus = false
	idle := m.sidebarRow(v, it, sidebarDefaultWidth)[0]

	if focused == idle {
		t.Error("the cursor's row renders the same whether or not the strip has the keyboard")
	}
	if lipgloss.Width(focused) != lipgloss.Width(idle) {
		t.Errorf("the two tiers are %d and %d columns — a hue must not move anything",
			lipgloss.Width(focused), lipgloss.Width(idle))
	}
	if ansi.Strip(focused) != ansi.Strip(idle) {
		t.Errorf("the text differs, not just its style:\n%q\n%q",
			ansi.Strip(focused), ansi.Strip(idle))
	}
}

// TestTheStripsCursorIsARowNotAnIndex. A refresh that re-orders the strip leaves the
// cursor on the workspace it was on, not on the position it was at.
func TestTheStripsCursorIsARowNotAnIndex(t *testing.T) {
	m, _ := sidebarPane(t)
	rows := m.sidebarRowsInOrder()
	if len(rows) < 2 {
		t.Fatalf("need two rows, got %d", len(rows))
	}
	target := rows[len(rows)-1]
	m.sidebarCursor = target

	// Everything the strip sorts on, changed under it: the first row starts working,
	// which moves it to another band.
	swapped := make([]Item, len(m.itemsAll))
	copy(swapped, m.itemsAll)
	for i := range swapped {
		if sameRow(swapped[i], rows[0]) {
			swapped[i].Status = "working"
			swapped[i].Unread = false
		}
	}
	m.itemsAll = swapped

	if !m.sidebarOnCursor(target) {
		t.Errorf("the cursor left %s when the strip re-ordered", target.WorkspaceName)
	}
}

// TestArrivingAtTheStripLandsOnTheDecksRow. Entering the strip seeds its cursor from
// the row list's, so the surface you arrive on is pointing at the row you left.
func TestArrivingAtTheStripLandsOnTheDecksRow(t *testing.T) {
	m, _ := sidebarPane(t)
	items := m.items()
	if len(items) < 2 {
		t.Fatalf("need two rows, got %d", len(items))
	}
	m.cursor = 1
	want := items[1]

	(&m).enterSidebar()

	if !m.sidebarOnCursor(want) {
		t.Errorf("arrived on %+v, want the row list's %s", m.sidebarCursor, want.WorkspaceName)
	}
}

// TestLeavingTheStripTakesTheRowWithIt. The write-back that makes it one cursor: walk
// the strip, go on to the deck, and the deck is on the row you walked to.
func TestLeavingTheStripTakesTheRowWithIt(t *testing.T) {
	m, _ := sidebarPane(t)
	(&m).enterSidebar()
	(&m).moveSidebarCursor(1)
	walked := m.sidebarCursor

	(&m).leaveSidebar()

	if m.sidebarFocus {
		t.Error("the strip still holds the keyboard")
	}
	sel, ok := m.selected()
	if !ok {
		t.Fatal("the row list has no selection")
	}
	if !sameRow(sel, walked) {
		t.Errorf("the row list is on %s, want the %s the strip walked to",
			sel.WorkspaceName, walked.WorkspaceName)
	}
}

// TestTheLeaveKeyCyclesPaneSidebarDeck. The door is a cycle rather than a mode,
// which is what dissolves the focus question the strip was built around: there is no
// state a single key does not leave, and it is the key that already means "somewhere
// else".
func TestTheLeaveKeyCyclesPaneSidebarDeck(t *testing.T) {
	m, _ := sidebarPane(t)
	if m.sidebarFocus {
		t.Fatal("the strip had the keyboard before anything was pressed")
	}

	m = pressDeck(t, m, leaveKey())
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("the first press took the pane down (active is %T), want it left standing", m.active)
	}
	if !m.sidebarFocus {
		t.Fatal("the first press did not put the keyboard on the strip")
	}

	m = pressDeck(t, m, leaveKey())
	if m.active != nil {
		t.Fatalf("the second press left %T open, want the row list", m.active)
	}
	if m.sidebarFocus {
		t.Error("the deck arrived with the keyboard still on a strip that is not up")
	}

	m = pressDeck(t, m, leaveKey())
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("the third press gave %T, want the pane back — the cycle has to close", m.active)
	}
	if m.sidebarFocus {
		t.Error("coming back into the pane left the keyboard on the strip")
	}
}

// TestTheLeaveKeyStillLeavesWhenTheStripIsDown. The key gains a stop rather than
// changing what it does: with no strip on screen one press is still the deck.
func TestTheLeaveKeyStillLeavesWhenTheStripIsDown(t *testing.T) {
	m := sidebarDeck(t)
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("no pane opened: %T", m.active)
	}
	if m.showsSidebar() {
		t.Fatal("the strip is up, but this test is about it being down")
	}

	m = pressDeck(t, m, leaveKey())

	if m.active != nil {
		t.Errorf("one press left %T open, want the row list", m.active)
	}
}

// TestEscFromTheStripGoesBackIntoThePane. The row-mode Quit binding reads esc as
// "quit the deck", which from a strip standing in front of a live pane is the most
// expensive reading of the cheapest key.
func TestEscFromTheStripGoesBackIntoThePane(t *testing.T) {
	m, _ := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	if !m.sidebarFocus {
		t.Fatal("the strip does not have the keyboard")
	}

	m = pressDeck(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.sidebarFocus {
		t.Error("esc left the keyboard on the strip")
	}
	if _, ok := m.active.(*panePopover); !ok {
		t.Errorf("esc gave %T, want the pane it came from", m.active)
	}
}

// TestAPaneExitingTakesTheKeyboardOffTheStrip. The strip renders only over a hosted
// program, so it cannot hold the keys once there is nothing under it — however the
// pane went.
func TestAPaneExitingTakesTheKeyboardOffTheStrip(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	if !m.sidebarFocus {
		t.Fatal("the strip does not have the keyboard")
	}

	p.close(&m)

	if m.sidebarFocus {
		t.Error("the keyboard is on a strip with nothing under it")
	}
}

// TestAVerbOnTheStripActsOnTheRowUnderItsCursor. The point of #350: the strip stops
// being a second, weaker list with a subset of verbs. `D` on a sidebar row is the
// same `D` the row list has, aimed at the row the strip is pointing at.
//
// Delete is the one to test with because it names its target back: the confirm says
// which workspace, so a key that acted on the row list's cursor instead of the
// strip's would be visible rather than merely wrong.
func TestAVerbOnTheStripActsOnTheRowUnderItsCursor(t *testing.T) {
	m, _ := sidebarPane(t)
	m.cursor = 0
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("j"))
	target := m.sidebarCursor
	if sameRow(target, m.items()[0]) {
		t.Fatal("the strip's cursor did not move off the row list's, so this proves nothing")
	}

	m = pressDeck(t, m, runeKey("D"))

	sel, ok := m.selected()
	if !ok {
		t.Fatal("the row list has no selection after the verb")
	}
	if !sameRow(sel, target) {
		t.Errorf("D was aimed at %s, want the strip's row %s", sel.WorkspaceName, target.WorkspaceName)
	}
	if m.active == nil {
		t.Error("D opened nothing — want the delete confirm")
	}
}

// TestAVerbOnTheStripFloatsOverTheArrangement. The pane is what you are working in
// and the question is about a different workspace, so answering it must not cost the
// program you were reading — the strip exists to save exactly that trip.
//
// The arrangement steps aside rather than closing: off m.active, so the modal owns
// the screen, but alive on m.overlayHost.
func TestAVerbOnTheStripFloatsOverTheArrangement(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())

	m = pressDeck(t, m, runeKey("D"))

	if m.active == nil {
		t.Fatal("D opened nothing — want the delete confirm")
	}
	if m.active == p {
		t.Fatal("the pane is still the deck's child, with a modal opened over it")
	}
	if m.overlayHost != p {
		t.Errorf("the pane is %v, want it kept as the overlay's host", m.overlayHost)
	}
	if m.sidebarFocus {
		t.Error("the keyboard is still on the strip — the modal has it")
	}
}

// TestClosingAnOverlayPutsYouBackOnTheStrip. The other half of floating: answering
// the question returns you to where you asked it, on the row you asked about.
func TestClosingAnOverlayPutsYouBackOnTheStrip(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("j"))
	asked := m.sidebarCursor
	m = pressDeck(t, m, runeKey("D"))

	m = pressDeck(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.active != p {
		t.Fatalf("the pane did not come back: active is %T", m.active)
	}
	if m.overlayHost != nil {
		t.Error("the arrangement is on screen and still held as an overlay host")
	}
	if !m.sidebarFocus {
		t.Error("the keyboard did not go back to the strip it was pressed from")
	}
	if !m.sidebarOnCursor(asked) {
		t.Errorf("the strip's cursor is on %+v, want the %s it was on",
			m.sidebarCursor, asked.WorkspaceName)
	}
}

// TestAnOverlayIsDrawnOverTheArrangement. Composited rather than replacing the
// screen — the same treatment the ctrl+b menu gets, and for the same reason: what
// the question is about is beside it, and what you were working in is behind it.
func TestAnOverlayIsDrawnOverTheArrangement(t *testing.T) {
	m, _ := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("D"))

	frame := ansi.Strip(m.render())

	if !strings.Contains(frame, sidebarSectionLabel(sectionWaiting)) {
		t.Errorf("the strip is not on screen behind the overlay:\n%s", frame)
	}
	if got := lipgloss.Height(frame); got != m.height {
		t.Errorf("the frame is %d rows, want the terminal's %d", got, m.height)
	}
}

// TestMovementStaysOnTheStrip. The other half of the same rule: a key that only
// moves a cursor has nothing to act on, so it does not leave.
func TestMovementStaysOnTheStrip(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	first := m.sidebarCursor

	for _, k := range []string{"j", "k", "G"} {
		m = pressDeck(t, m, runeKey(k))
		if !m.sidebarFocus {
			t.Fatalf("%q took the keyboard off the strip", k)
		}
		if m.active != p {
			t.Fatalf("%q closed the pane (active is %T)", k, m.active)
		}
	}
	if sameRow(m.sidebarCursor, first) && len(m.sidebarRowsInOrder()) > 1 {
		t.Error("nothing moved the cursor")
	}
}

// TestTheStripTakesARowTheRowListWasNotShowing. The strip lists the all scope,
// unfiltered, so it can point at a row the deck is currently filtering out — and the
// next key is aimed at that row, so the deck has to go where it can be seen.
func TestTheStripTakesARowTheRowListWasNotShowing(t *testing.T) {
	m, _ := sidebarPane(t)
	rows := m.sidebarRowsInOrder()
	target := rows[len(rows)-1]
	m.filter = "no-such-workspace"
	if len(m.items()) != 0 {
		t.Fatalf("the filter left %d rows, wanted none", len(m.items()))
	}

	(&m).enterSidebar()
	m.sidebarCursor = target
	(&m).leaveSidebar()

	sel, ok := m.selected()
	if !ok {
		t.Fatal("the row list still has no selection")
	}
	if !sameRow(sel, target) {
		t.Errorf("the deck landed on %s, want %s", sel.WorkspaceName, target.WorkspaceName)
	}
	if m.filter != "" {
		t.Errorf("the filter %q survived, so the row it hides is still hidden", m.filter)
	}
}

// TestTheStripScrollsToKeepItsCursor. The "+N more" count was justified by nothing
// being able to move a cursor in here. Now something can, and a cursor that walks
// off the bottom of a strip that will not follow it is a cursor you cannot see.
func TestTheStripScrollsToKeepItsCursor(t *testing.T) {
	items := make([]Item, 0, 20)
	for i := range 20 {
		items = append(items, Item{
			ProjectName: "proj", WorkspaceName: "ws" + string(rune('a'+i)),
			Path: "/tmp", RepoRoot: "/tmp", Status: "idle",
		})
	}
	m := stripDeck(items)
	m.sidebar = true

	// A strip too short for the rows in it, so there is something to scroll.
	b := box{w: sidebarDefaultWidth, h: 12}
	rows := m.sidebarRowsInOrder()
	if len(m.sidebarLines(b)) >= len(rows)*2 {
		t.Fatalf("the strip is tall enough to hold everything, so this proves nothing")
	}

	last := rows[len(rows)-1]
	m.sidebarCursor = last
	m.sidebarFocus = true

	onScreen := false
	for _, l := range m.sidebarLines(b) {
		if l.item != nil && sameRow(*l.item, last) {
			onScreen = true
		}
	}
	if !onScreen {
		t.Errorf("the strip did not scroll to %s — the cursor is off screen",
			last.WorkspaceName)
	}
	if got := len(m.sidebarLines(b)); got > b.h {
		t.Errorf("the strip drew %d lines into %d rows", got, b.h)
	}
}

// TestTheStripSitsAtTheTopWithNoCursor. The window is derived from the cursor, so
// with no cursor it is the strip as it always was: a glance at what is waiting,
// sorted so that what wants you is what is on screen.
func TestTheStripSitsAtTheTopWithNoCursor(t *testing.T) {
	items := make([]Item, 0, 20)
	for i := range 20 {
		items = append(items, Item{
			ProjectName: "proj", WorkspaceName: "ws" + string(rune('a'+i)),
			Path: "/tmp", RepoRoot: "/tmp", Status: "idle",
		})
	}
	m := stripDeck(items)
	m.sidebar = true
	b := box{w: sidebarDefaultWidth, h: 12}

	lines := m.sidebarLines(b)
	first := m.sidebarRowsInOrder()[0]
	if lines[1].item == nil || !sameRow(*lines[1].item, first) {
		t.Errorf("the strip does not start at its first row")
	}
	if !strings.Contains(ansi.Strip(lines[len(lines)-1].text), "more") {
		t.Errorf("nothing said the list stops early: %q", ansi.Strip(lines[len(lines)-1].text))
	}
}

// TestOpeningAProgramFromTheStripKeepsThePane. A window key on the strip means what
// `|` means: the pane you were in stays, and the row you picked opens beside it.
//
// The strip is glanced at *while working in something*, so a row picked off it is
// nearly always the second thing you want on screen rather than a replacement for
// the first.
func TestOpeningAProgramFromTheStripGoesToItsRow(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("j"))
	target := m.sidebarCursor
	if target.WorkspaceName == p.workspace {
		t.Fatal("j did not move to another row, so this says nothing about crossing workspaces")
	}

	m = pressDeck(t, m, runeKey("e"))

	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("e gave %T, want a split on the strip's row", m.active)
	}
	// The left half is that row's agent, not the one that was up. This used to
	// assert the opposite — the pane you were in was kept and the other row's
	// program went beside it — on the argument that the strip is glanced at while
	// working and the pane is the expensive thing to lose. What that produced was
	// one workspace's agent beside another's editor: two halves about different
	// work, which the labels say only if you read them. The rows on the strip are
	// the ones you are not in, so picking one means going there; adding a program
	// beside what you are in is ctrl+b, which is still exactly that.
	left, ok := s.left.(*panePopover)
	if !ok {
		t.Fatalf("the left half is %T, want the agent pane", s.left)
	}
	if left.workspace != target.WorkspaceName {
		t.Errorf("the left half is the agent of %s, want the strip's row %s", left.workspace, target.WorkspaceName)
	}
	right, ok := s.right.(*panePopover)
	if !ok {
		t.Fatalf("the right half is %T, want a pane", s.right)
	}
	if right.workspace != target.WorkspaceName {
		t.Errorf("the right half is of %s, want the strip's row %s",
			right.workspace, target.WorkspaceName)
	}
	if !s.rightFocused {
		t.Error("the keys did not go to the half that was just opened")
	}
	if m.sidebarFocus {
		t.Error("the keyboard is still on the strip")
	}
}

// TestEnterFromTheStripGoesToThatWorkspace. enter is not a window key: it takes you
// *to* the row rather than putting it beside what you are in, which is what it has
// always meant on the row list and what a click on the strip already did.
//
// The split it must not make is the tell — half of another workspace's screen is
// where you are working, not where you went.
func TestEnterFromTheStripGoesToThatWorkspace(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("j"))
	target := m.sidebarCursor

	m = pressDeck(t, m, agentKey())

	if s, split := m.active.(*splitModal); split {
		t.Fatalf("enter made a split (%s beside %s) — it is not a window key",
			s.label, target.WorkspaceName)
	}
	got, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("enter gave %T, want the row's pane", m.active)
	}
	if got == p {
		t.Fatal("enter left you in the pane you were already in")
	}
	if got.workspace != target.WorkspaceName {
		t.Errorf("enter opened %s, want the strip's row %s", got.workspace, target.WorkspaceName)
	}
	if m.sidebarFocus {
		t.Error("the keyboard is still on the strip — enter goes into the workspace")
	}
}

// TestEnterOnTheStripIsTheSameDoorAsAClick. Both resolve to goToSidebarRow, so a
// workspace last left as a split comes back as that split either way. Two ways in
// that opened different things would be two answers to what entering a workspace is.
func TestEnterOnTheStripIsTheSameDoorAsAClick(t *testing.T) {
	byKey, _ := sidebarPane(t)
	byKey = pressDeck(t, byKey, leaveKey())
	byKey = pressDeck(t, byKey, runeKey("j"))
	target := byKey.sidebarCursor
	byKey = pressDeck(t, byKey, agentKey())

	byClick, _ := sidebarPane(t)
	(&byClick).goToSidebarRow(target)

	keyed, okKey := byKey.active.(*panePopover)
	clicked, okClick := byClick.active.(*panePopover)
	if !okKey || !okClick {
		t.Fatalf("enter gave %T, the click gave %T", byKey.active, byClick.active)
	}
	if keyed.workspace != clicked.workspace || keyed.kind != clicked.kind {
		t.Errorf("enter opened %s of %s, the click opened %s of %s",
			keyed.kind, keyed.workspace, clicked.kind, clicked.workspace)
	}
}

// TestAProgramFromTheStripReplacesTheRightHalf. With two halves already up there is
// one place to put a third thing, and it is the right half — always, whichever half
// the keys are in, because the left half is the agent and stays the agent. The same
// answer ctrl+b and a window key give from inside a split.
func TestAProgramFromTheStripReplacesTheRightHalf(t *testing.T) {
	m, _ := sidebarPane(t)
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("s"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("no split opened: %T", m.active)
	}
	left := s.left

	m = pressDeck(t, m, leaveKey())
	if !m.sidebarFocus {
		t.Fatal("the leave key did not put the keyboard on the strip")
	}
	// The row the split is already about: replacing the right half is what happens
	// within a row. Another row is a move instead — see
	// TestOpeningAProgramFromTheStripGoesToItsRow.
	for _, r := range m.sidebarRowsInOrder() {
		if lp, ok := left.(*panePopover); ok && r.WorkspaceName == lp.workspace {
			m.sidebarCursor = r
			break
		}
	}
	target := m.sidebarCursor
	m = pressDeck(t, m, runeKey("e"))

	s, ok = m.active.(*splitModal)
	if !ok {
		t.Fatalf("e gave %T, want the split back", m.active)
	}
	if s.left != left {
		t.Error("the left half was replaced — it is the agent, and it stays the agent")
	}
	right, ok := s.right.(*panePopover)
	if !ok {
		t.Fatalf("the right half is %T, want a pane", s.right)
	}
	if right.workspace != target.WorkspaceName || right.kind != "editor" {
		t.Errorf("the right half is %s of %s, want the editor of %s",
			right.kind, right.workspace, target.WorkspaceName)
	}
}

// TestASuspendedPaneKeepsReadingItsProgram. A pane re-arms its own read on every
// frame it is handed, so one that stops being handed its output stops asking for
// more — and comes back from behind a confirm frozen on the frame the confirm opened
// over.
func TestASuspendedPaneKeepsReadingItsProgram(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("D"))
	if m.overlayHost != p {
		t.Fatalf("the pane is not suspended under the overlay: %v", m.overlayHost)
	}

	next, cmd := m.Update(vterm.OutputMsg{Gen: p.term.Gen()})
	m = next.(Model)

	if cmd == nil {
		t.Error("a frame arriving for the suspended pane produced no follow-up read")
	}
	if m.overlayHost != p {
		t.Error("the frame disturbed the suspension")
	}
	if _, ok := m.active.(*confirmDeleteModal); !ok {
		t.Errorf("the frame went to the wrong place: active is %T", m.active)
	}
}

// TestAProgramExitingUnderAnOverlayIsNotRestored. The pane went away while a confirm
// was over it, so there is nothing to come back to — and a dead pane put back on
// screen is a frozen copy of a program that has already quit.
func TestAProgramExitingUnderAnOverlayIsNotRestored(t *testing.T) {
	m, p := sidebarPane(t)
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("D"))

	next, _ := m.Update(vterm.ExitMsg{Gen: p.term.Gen()})
	m = next.(Model)

	if m.overlayHost != nil {
		t.Errorf("a pane that exited is still held as the overlay's host: %v", m.overlayHost)
	}

	m = pressDeck(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.active != nil {
		t.Errorf("closing the overlay restored %T — the program it showed had exited", m.active)
	}
}

// TestTheStripsCursorStopsAtTheEnds. Clamped, like the row list — not wrapped.
func TestTheStripsCursorStopsAtTheEnds(t *testing.T) {
	m, _ := sidebarPane(t)
	rows := m.sidebarRowsInOrder()
	(&m).enterSidebar()

	m.sidebarCursor = rows[0]
	(&m).moveSidebarCursor(-1)
	if !sameRow(m.sidebarCursor, rows[0]) {
		t.Errorf("k off the top landed on %s, want to stay on %s",
			m.sidebarCursor.WorkspaceName, rows[0].WorkspaceName)
	}

	last := rows[len(rows)-1]
	m.sidebarCursor = last
	(&m).moveSidebarCursor(1)
	if !sameRow(m.sidebarCursor, last) {
		t.Errorf("j off the bottom landed on %s, want to stay on %s",
			m.sidebarCursor.WorkspaceName, last.WorkspaceName)
	}
}

// Opening a program beside the pane has to hand the keyboard to what it opened.
//
// The reported path: in an agent pane, ctrl+\ to the strip, then `c` for a diff
// beside it. The split came up and could not be typed into — every key went on
// going to the strip, whose own handler reads j and k as cursor movement and
// answers most of the rest by opening a deck screen about the row it is on. So
// the diff was on screen and unreachable, and the same key from ctrl+b — which
// never involves the strip — worked, which is what made it look like a keybinding
// problem rather than a focus one.
//
// The fallback branch of openBesideFromSidebar always called leaveSidebar; the two
// branches that actually run did not.
func TestOpeningBesideFromTheStripHandsOverTheKeyboard(t *testing.T) {
	m, _ := sidebarPane(t)
	m.diffLoad = func(Item, DiffScope, int) (string, error) { return "", nil }
	// ctrl+\ from the pane is the strip's stop in the cycle.
	m = pressDeck(t, m, leaveKey())
	if !m.sidebarFocus {
		t.Fatalf("ctrl+\\ did not give the strip the keyboard (status %q)", m.status)
	}
	m.sidebarCursor = m.sidebarRowsInOrder()[0]

	m = pressDeck(t, m, runeKey("c"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("`c` from the strip opened %T, want a split with the diff beside the pane (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })
	if _, ok := s.focused().(*diffModal); !ok {
		t.Fatalf("the focused half is %T, want the diff that was just opened", s.focused())
	}

	// The keyboard is the point: the strip must have let go of it, or every key
	// from here is read against the strip's row instead of the diff.
	if m.sidebarFocus {
		t.Fatal("the strip still holds the keyboard after opening a diff beside the pane, so nothing typed can reach the diff")
	}

	// And the claim that actually matters, rather than its proxy: a key gets there.
	// `\` toggles the viewer's left column, which is visible in the frame.
	before := ansi.Strip(m.render())
	m = pressDeck(t, m, runeKey("\\"))
	if after := ansi.Strip(m.render()); after == before {
		t.Fatal("a key pressed after opening the diff changed nothing on screen — it is not reaching the viewer")
	}
}

// The `- r` picker on the path that was reported broken, end to end: agent pane,
// ctrl+\ to the strip, `c` for a diff beside it, then the scope chord and the
// revision list inside that diff.
//
// This is the test the feature shipped without. It was backed out on the strength
// of "the split diff is unusable", which turned out to be the strip keeping the
// keyboard (see TestOpeningBesideFromTheStripHandsOverTheKeyboard) and nothing to
// do with the picker — but nothing here had ever driven the picker from a split at
// all, so there was no way to tell the two apart.
func TestRevisionPickerFromASidebarSplit(t *testing.T) {
	m, _ := sidebarPane(t)
	m.diffLoad = func(Item, DiffScope, int) (string, error) { return "", nil }
	m = m.WithDiffScopes(func(Item) []ui.ScopeOption {
		return []ui.ScopeOption{
			{Key: "c", Label: "vs stack base", Load: func(int) (string, error) { return "", nil }},
			{Key: "w", Label: "working copy", Load: func(int) (string, error) { return "", nil }},
			{Key: "r", Label: "a revision…", Choices: func() ([]ui.ScopeOption, error) {
				return []ui.ScopeOption{
					{Key: "qpvuntsm", Label: "wip: the one being written",
						Load: func(int) (string, error) { return "", nil }},
					{Key: "kntqzsrx", Label: "feat: one that landed",
						Load: func(int) (string, error) { return "", nil }},
				}, nil
			}},
		}
	})
	m = pressDeck(t, m, leaveKey())
	m.sidebarCursor = m.sidebarRowsInOrder()[0]
	m = pressDeck(t, m, runeKey("c"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("`c` from the strip opened %T, want a split (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })

	// The chord, floating over the split.
	m = pressDeck(t, m, runeKey("-"))
	if _, armed := m.armedMenu(); !armed {
		t.Fatal("`-` armed no menu in the diff half of a strip-opened split")
	}
	if frame := ansi.Strip(m.render()); !strings.Contains(frame, "a revision") {
		t.Fatalf("the menu does not offer the revision picker:\n%s", frame)
	}

	// The list, and picking the second entry rather than defaulting to the first.
	m = pressDeck(t, m, runeKey("r"))
	if frame := ansi.Strip(m.render()); !strings.Contains(frame, "kntqzsrx") {
		t.Fatalf("the revision list is not on screen:\n%s", frame)
	}
	m = pressDeck(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = pressDeck(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	dm, ok := s.focused().(*diffModal)
	if !ok {
		t.Fatalf("the focused half is %T, want the diff", s.focused())
	}
	if got := dm.inner.ScopeLabel(); got != "feat: one that landed" {
		t.Fatalf("the viewer is reading %q, want the picked revision", got)
	}
}

// A program picked off the strip for *another* workspace takes you to that
// workspace, rather than pasting its diff beside the agent you were in.
//
// "Keep the pane you were in and put the program beside it" is right when the row
// is the one you are already working in — the strip is a thing you glance at while
// working, and the program is the second thing you want on screen. It is wrong
// across workspaces: it leaves workspace A's agent beside workspace B's diff, two
// halves of a split that are about different work, and the label on each says so
// only if you read it.
func TestPickingAnotherRowsProgramGoesToThatRow(t *testing.T) {
	m, p := sidebarPane(t)
	m.diffLoad = func(Item, DiffScope, int) (string, error) { return "", nil }
	current := p.workspace

	m = pressDeck(t, m, leaveKey())
	rows := m.sidebarRowsInOrder()
	var other Item
	for _, r := range rows {
		if r.WorkspaceName != current {
			other = r
			break
		}
	}
	if other.WorkspaceName == "" {
		t.Fatal("need a second workspace on the strip")
	}
	m.sidebarCursor = other

	m = pressDeck(t, m, runeKey("c"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("`c` opened %T, want a split (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })

	left, ok := s.left.(*panePopover)
	if !ok {
		t.Fatalf("the left half is %T, want the agent pane", s.left)
	}
	if left.workspace != other.WorkspaceName {
		t.Fatalf("the split pairs the agent of %q with the diff of %q — both halves should be about %q", left.workspace, other.WorkspaceName, other.WorkspaceName)
	}
	if _, ok := s.right.(*diffModal); !ok {
		t.Fatalf("the right half is %T, want the diff", s.right)
	}
}

// And the row you are already in keeps the pane you were in, which is the case
// the strip was built around: glance at it while working, and what you pick is
// the second thing you want on screen rather than a replacement for the first.
func TestPickingYourOwnRowsProgramKeepsThePane(t *testing.T) {
	m, p := sidebarPane(t)
	m.diffLoad = func(Item, DiffScope, int) (string, error) { return "", nil }
	was := p.term

	m = pressDeck(t, m, leaveKey())
	for _, r := range m.sidebarRowsInOrder() {
		if r.WorkspaceName == p.workspace {
			m.sidebarCursor = r
			break
		}
	}
	m = pressDeck(t, m, runeKey("c"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("`c` opened %T, want a split (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })

	left, ok := s.left.(*panePopover)
	if !ok {
		t.Fatalf("the left half is %T, want the agent pane", s.left)
	}
	// The same terminal, not a fresh one: re-opening the agent would repaint a
	// program you were reading mid-thought, which is what this path exists to avoid.
	if left.term != was {
		t.Error("the agent was re-opened rather than kept — picking a program for the row you are in should not restart it")
	}
}

// TestTheStripCountsWorkspacesNotLines.
//
// A row is two lines, with a blank between rows and a blank and a header per
// group, so the count read about three times the number it appears to name: a
// strip over sixty workspaces said "+124 more". The only thing on the strip worth
// counting is workspaces, which is what the notice looks like it is counting.
func TestTheStripCountsWorkspacesNotLines(t *testing.T) {
	items := make([]Item, 0, 20)
	for i := range 20 {
		items = append(items, Item{
			ProjectName: "proj", WorkspaceName: "ws" + string(rune('a'+i)),
			Path: "/tmp", RepoRoot: "/tmp", Status: "idle",
		})
	}
	m := stripDeck(items)
	m.sidebar = true
	b := box{w: sidebarDefaultWidth, h: 12}

	lines := m.sidebarLines(b)
	notice := ansi.Strip(lines[len(lines)-1].text)
	if !strings.Contains(notice, "more") {
		t.Fatalf("the strip does not overflow here, so this proves nothing: %q", notice)
	}
	n := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(notice), "+%d more", &n); err != nil {
		t.Fatalf("cannot read a count out of %q: %v", notice, err)
	}

	shown := sidebarRowsIn(lines)
	if got, want := shown+n, len(m.sidebarRowsInOrder()); got != want {
		t.Errorf("%d rows on screen plus %q accounts for %d workspaces, but the strip has %d",
			shown, notice, got, want)
	}
	// And the number the old code would have printed, so the test fails against it
	// rather than only against a wilder wrong answer.
	if lineCount := len(lines) - 1; n == lineCount {
		t.Errorf("the count is %d, which is the number of lines below the fold — it is counting lines", n)
	}
}
