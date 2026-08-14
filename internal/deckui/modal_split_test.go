package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The `|` split.
//
// Two things that were previously one-at-a-time: the agent, and whatever you
// wanted to read while it worked. Every test here goes through the deck's own
// Update, because the parts that can go wrong are the routing ones — which half
// a key reaches, which half a click reaches, what happens when one of them
// closes — and none of those exist in a child called directly.

// splitDeck is a deck wide enough to split, on a row with a workspace.
func splitDeck(t *testing.T) Model {
	t.Helper()
	m := New([]Item{
		{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"},
	}, func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 200, 40
	m.itemsAll = []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}}
	// The menu key is only distinguishable from the leave key where the terminal
	// reports shifted control keys, so a split's verbs are only reachable there.
	m.keysEnhanced = true
	return m
}

// leaveKey is the reserved key, as the terminal sends it.
func leaveKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl} }

// menuKey opens the verb menu. ctrl+b, which every terminal can send and no
// operating system intercepts — see charm.PaneMenuKey.
func menuKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl} }

// press sends one key through the deck and returns the new model.
func pressDeck(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	next, _ := m.Update(key)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return got
}

// openedSplit arms the chord and answers it with the given key.
func openedSplit(t *testing.T, key string) (Model, *splitModal) {
	t.Helper()
	m := splitDeck(t)
	m = pressDeck(t, m, runeKey("|"))
	if _, ok := m.active.(*splitChordModal); !ok {
		t.Fatalf("| did not arm the chord (active=%T, status %q)", m.active, m.status)
	}
	m = pressDeck(t, m, runeKey(key))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("|%s did not open a split (active=%T, status %q)", key, m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })
	return m, s
}

// TestTheChordPutsTheAgentBesideTheThingYouAskedFor. `|v` is the case where both
// halves are ptys; the left is the agent whatever the second key was.
func TestTheChordPutsTheAgentBesideTheThingYouAskedFor(t *testing.T) {
	m, s := openedSplit(t, "v")
	left, ok := s.left.(*panePopover)
	if !ok {
		t.Fatalf("the left half is %T, want the agent's pane", s.left)
	}
	if !strings.Contains(left.label, PaneKindAgent) {
		t.Errorf("the left half is %q, want the agent", left.label)
	}
	if _, ok := s.right.(*panePopover); !ok {
		t.Fatalf("the right half is %T, want a pane", s.right)
	}
	// Focus starts on the right: you pressed |v because that is what you want to
	// look at, and the agent is the reference beside it.
	if !s.rightFocused {
		t.Error("the split opened with the keys in the agent, not in what you asked for")
	}
	if m.status != "" {
		t.Errorf("opening the split left a status: %q", m.status)
	}
}

// TestTheAgentCannotBeSplitAgainstItself. `|a` is the one key in the window set
// the chord has no answer for, and it says so rather than cancelling silently —
// a key that looks like it should work and does nothing reads as a broken chord.
func TestTheAgentCannotBeSplitAgainstItself(t *testing.T) {
	m := splitDeck(t)
	m = pressDeck(t, m, runeKey("|"))
	m = pressDeck(t, m, runeKey("a"))
	if m.active != nil {
		t.Errorf("|a opened %T", m.active)
	}
	if !strings.Contains(m.status, "agent") {
		t.Errorf("|a said %q, which does not explain the refusal", m.status)
	}
}

// TestAMistypedSecondKeyCancels — including esc, and without reaching the row
// list underneath and doing something else there.
func TestAMistypedSecondKeyCancels(t *testing.T) {
	for _, key := range []string{"esc", "z", "D"} {
		m := splitDeck(t)
		m = pressDeck(t, m, runeKey("|"))
		before := m.cursor
		if key == "esc" {
			m = pressDeck(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
		} else {
			m = pressDeck(t, m, runeKey(key))
		}
		if m.active != nil {
			t.Errorf("%s left %T open", key, m.active)
		}
		if m.cursor != before {
			t.Errorf("%s reached the row list and moved the cursor", key)
		}
	}
}

// TestASplitRefusesATerminalTooNarrowForTwo, naming the width it wants.
//
// The floor is a pane, not a comfortable one: a 120-column minimum used to refuse
// splits that were merely cramped, which is a judgment the person who pressed the key
// has already made. What is left is the width below which a half is a border around a
// pty no program can lay out in.
func TestASplitRefusesATerminalTooNarrowForTwo(t *testing.T) {
	m := splitDeck(t)
	m.width = 2*splitHalfMinW - 1
	m = pressDeck(t, m, runeKey("|"))
	m = pressDeck(t, m, runeKey("v"))
	if m.active != nil {
		t.Fatalf("the split opened at %d columns: %T", m.width, m.active)
	}
	if !strings.Contains(m.status, "columns") {
		t.Errorf("the refusal does not say what is wrong: %q", m.status)
	}
}

// TestBothHalvesRenderSideBySideInTheirOwnColumns. The whole feature is that the
// two are on screen together, and the frame is exactly the terminal — a split
// that renders 201 columns pushes the right half off the screen.
func TestBothHalvesRenderSideBySideInTheirOwnColumns(t *testing.T) {
	m, s := openedSplit(t, "v")
	frame := s.renderPopover(&m, m.childBox())
	if got := lipgloss.Width(frame); got != m.width {
		t.Errorf("the split rendered %d columns into a %d-column screen", got, m.width)
	}
	left, right := s.boxes(m.childBox())
	if left.w+right.w != m.width {
		t.Errorf("the halves are %d + %d columns, want %d", left.w, right.w, m.width)
	}
	// The right half's origin is what a mouse click is translated against.
	if right.x != left.w {
		t.Errorf("the right half starts at column %d, want %d", right.x, left.w)
	}
}

// TestOnlyTheFocusedHalfLooksFocused, per the design system: in a multi-pane
// screen exactly one may wear the active treatment, so the border of the half
// the keyboard has left drops a tier.
func TestOnlyTheFocusedHalfLooksFocused(t *testing.T) {
	m, s := openedSplit(t, "v")
	left, right := s.boxes(m.childBox())
	if !left.blurred {
		t.Error("the unfocused half was told the keys are in it")
	}
	if right.blurred {
		t.Error("the focused half was told the keys are elsewhere")
	}
	// And the border actually changes, or the rule is only in the box.
	focused := s.right.(*panePopover).renderPopover(&m, right)
	blurred := s.right.(*panePopover).renderPopover(&m, right.focus(false))
	if focused == blurred {
		t.Error("a blurred pane renders identically to a focused one")
	}
}

// TestThePrefixMovesTheKeysBetweenHalves. The reserved key plus a verb, so the
// halves keep their own full keymaps — h and l mean nothing on their own, which
// is why they are safe to spend.
func TestThePrefixMovesTheKeysBetweenHalves(t *testing.T) {
	m, s := openedSplit(t, "v")
	for _, tc := range []struct {
		key   string
		right bool
	}{
		{"h", false},
		{"l", true},
		{"tab", false}, // toggles off the right
		{"tab", true},
	} {
		m = pressDeck(t, m, menuKey())
		if !s.prefixArmed {
			t.Fatalf("%s: the reserved key did not arm the prefix", tc.key)
		}
		m = pressDeck(t, m, runeKey(tc.key))
		if s.prefixArmed {
			t.Errorf("%s: the prefix stayed armed", tc.key)
		}
		if s.rightFocused != tc.right {
			t.Errorf("after %s focus is right=%v, want %v", tc.key, s.rightFocused, tc.right)
		}
	}
}

// TestAHeldMenuKeyDoesNotThrash. Arming a menu is idempotent, so holding the key
// does nothing at all until you press a verb — and the menu key is not the key
// that leaves, so a held one cannot take the split down either.
func TestAHeldMenuKeyDoesNotThrash(t *testing.T) {
	m, s := openedSplit(t, "v")
	for range 8 {
		m = pressDeck(t, m, menuKey())
	}
	if _, ok := m.active.(*splitModal); !ok {
		t.Fatalf("a held key left the split: %T", m.active)
	}
	if !s.prefixArmed {
		t.Error("the prefix disarmed itself under repeat")
	}
	if s.zoomed || !s.rightFocused {
		t.Error("a held key changed the layout")
	}
}

// TestOnePressLeavesASplit. The leave key is a door in a split exactly as in a
// single pane: one press, back to the deck, no menu in front of it. It meant two
// different things depending on how many panes were up, which was the complaint.
func TestOnePressLeavesASplit(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, leaveKey())
	if m.active != nil {
		t.Errorf("one press of the leave key left %T open", m.active)
	}
}

// TestAWindowKeyReplacesTheFocusedHalf. The same keys that name a kind in a single
// pane's menu name one here; with both halves already up, the only place to put it
// is the half you are looking at. The other half is what you are keeping.
func TestAWindowKeyReplacesTheFocusedHalf(t *testing.T) {
	m, s := openedSplit(t, "v")
	keeping := s.left
	before := s.right
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("s"))
	if m.active != s {
		t.Fatalf("replacing a half left %T on screen", m.active)
	}
	if s.right == before {
		t.Error("the focused half was not replaced")
	}
	if s.left != keeping {
		t.Error("the half that was not focused was replaced too")
	}
}

// TestZoomGivesTheFocusedHalfTheWholeScreen, and giving it back does not have to
// re-open anything — which is the reason it is a zoom and not a close.
func TestZoomGivesTheFocusedHalfTheWholeScreen(t *testing.T) {
	m, s := openedSplit(t, "v")
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("o"))
	if !s.zoomed {
		t.Fatal("o did not zoom")
	}
	left, right := s.boxes(m.childBox())
	if right.w != m.width || left.w != 0 {
		t.Errorf("zoomed halves are %d and %d columns, want %d and 0", left.w, right.w, m.width)
	}
	if got := lipgloss.Width(s.renderPopover(&m, m.childBox())); got != m.width {
		t.Errorf("the zoomed half rendered %d columns, want %d", got, m.width)
	}
	// Both halves are still there.
	if s.left == nil || s.right == nil {
		t.Error("zooming discarded a half")
	}
}

// TestClosingOneHalfLeavesTheOtherAsAWholePane. `x` is the way out of a split
// that is no longer worth two halves, without leaving the thing you were reading.
func TestClosingOneHalfLeavesTheOtherAsAWholePane(t *testing.T) {
	m, s := openedSplit(t, "v")
	agent := s.left
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("x"))
	if m.active != agent {
		t.Fatalf("closing the focused half left %T, want the agent alone", m.active)
	}
}

// TestAKeyGoesToTheFocusedHalfAndNowhereElse. The two halves are both live
// programs, and a keystroke reaching the wrong one types into an agent.
func TestAKeyGoesToTheFocusedHalfAndNowhereElse(t *testing.T) {
	m, s := openedSplit(t, "v")
	left := fakeOf(t, s.left.(*panePopover))
	right := fakeOf(t, s.right.(*panePopover))

	pressDeck(t, m, runeKey("Q"))
	// Read off what each half was typed at rather than out of its screen: a key
	// reaching the wrong program is the failure, and which program received it is
	// the question — whether it then drew anything is the emulator's business.
	if got := right.keysSent(); len(got) != 1 || got[0] != "Q" {
		t.Errorf("the focused half received %v, want just Q", got)
	}
	if got := left.keysSent(); len(got) != 0 {
		t.Errorf("the half the keyboard had left received %v", got)
	}
}

// TestAClickGoesToTheHalfItLandedIn, and moves the keyboard there — which is
// what a mouse is for, and is why the right half's origin has to exist.
func TestAClickGoesToTheHalfItLandedIn(t *testing.T) {
	m, s := openedSplit(t, "v")
	if !s.rightFocused {
		t.Fatal("expected the right half focused to start")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: 4, Y: 5, Button: tea.MouseLeft})
	m = next.(Model)
	if s.rightFocused {
		t.Error("a click in the left half left the keys on the right")
	}
	next, _ = m.Update(tea.MouseClickMsg{X: m.width - 4, Y: 5, Button: tea.MouseLeft})
	m = next.(Model)
	if !s.rightFocused {
		t.Error("a click in the right half did not move the keys there")
	}
}

// TestTheDiffHalfIsAwpsOwnViewer rather than a second awp in a pty. `|c` is the
// case the whole seam was built for: one half a hosted process, the other a
// child of this program.
func TestTheDiffHalfIsAwpsOwnViewer(t *testing.T) {
	m := splitDeck(t)
	m.diffLoad = func(Item, DiffScope, int) (string, error) { return "", nil }
	m = pressDeck(t, m, runeKey("|"))
	m = pressDeck(t, m, runeKey("c"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("|c opened %T (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })
	if _, ok := s.right.(*diffModal); !ok {
		t.Errorf("the right half is %T, want the diff viewer", s.right)
	}
	if _, ok := s.left.(*panePopover); !ok {
		t.Errorf("the left half is %T, want the agent's pane", s.left)
	}
}

// TestTheChordSaysWhatItCanDo. A keys-only menu is undiscoverable unless the deck
// lists it, and the `?` overlay has to carry the same set — a key documented in
// one and not the other is how they start disagreeing.
//
// The menu is read off the top row rather than off m.status, which is where the
// menu goes: the same row an armed ctrl+b uses in a pane or a split.
func TestTheChordSaysWhatItCanDo(t *testing.T) {
	m := splitDeck(t)
	m = pressDeck(t, m, runeKey("|"))
	menu, armed := m.topRowMenu()
	if !armed {
		t.Fatalf("the chord put no menu on the top row (active=%T)", m.active)
	}
	for _, a := range splitActions {
		if !strings.Contains(menu, a.key+" "+a.label) {
			t.Errorf("the chord's menu omits %q: %q", a.key, menu)
		}
	}
	var documented []string
	for _, g := range deckKeyGroups() {
		for _, k := range g.Keys {
			documented = append(documented, k[0])
		}
	}
	help := strings.Join(documented, " ")
	for _, a := range splitActions {
		if !strings.Contains(help, "|"+a.key) {
			t.Errorf("the ? overlay omits |%s", a.key)
		}
	}
}

// TestThePrefixIsVisibleWhileItIsArmed. A split renders no deck footer, so
// arming the prefix changed only a bool and pressing the reserved key looked
// exactly like pressing a dead key — which is how it was reported. The menu
// takes over the deck's bar rather than being given a row of its own, so the
// halves' boxes do not change and no pty is resized by a modifier keypress.
func TestThePrefixIsVisibleWhileItIsArmed(t *testing.T) {
	m, _ := openedSplit(t, "v")
	before := m.render()
	if strings.Contains(ansi.Strip(before), "zoom") {
		t.Fatal("the prefix menu is on screen before the prefix was armed")
	}

	m = pressDeck(t, m, menuKey())
	armed := ansi.Strip(m.render())
	for _, want := range []string{"focus", "zoom", "replace"} {
		if !strings.Contains(armed, want) {
			t.Errorf("the armed prefix does not say %q:\n%s", want, lastLine(armed))
		}
	}
	// And it costs no rows: the frame is the same height armed or not, or the
	// halves would reflow — and a pty relaying itself out because you pressed a
	// modifier is worse than a border with a menu on it.
	if got, want := lipgloss.Height(armed), lipgloss.Height(ansi.Strip(before)); got != want {
		t.Errorf("arming the prefix changed the frame from %d rows to %d", want, got)
	}
}

// lastLine is the bottom row of a rendered frame, for an error worth reading.
func lastLine(frame string) string {
	lines := strings.Split(frame, "\n")
	return lines[len(lines)-1]
}

// TestTheDiffHalfFillsItsHeight. A body modal normally leaves room for the
// deck's footer; a split has none, so those rows are the half's. Without it the
// diff came up short and the frame ended in a band of dead rows.
func TestTheDiffHalfFillsItsHeight(t *testing.T) {
	m, s := splitWithDiff(t)
	left, right := s.boxes(m.childBox())
	agent := renderChild(&m, s.left, left)
	diff := renderChild(&m, s.right, right)
	if got, want := lipgloss.Height(diff), lipgloss.Height(agent); got != want {
		t.Errorf("the diff half is %d rows and the agent half is %d", got, want)
	}
	if got, want := lipgloss.Height(s.renderPopover(&m, m.childBox())), m.height-topRowRows; got != want {
		t.Errorf("the split is %d rows tall, want %d — the terminal less the deck's bar", got, want)
	}
	if got := lipgloss.Height(m.render()); got != m.height {
		t.Errorf("the frame is %d rows tall in a %d-row terminal", got, m.height)
	}
}

// TestTheDiffHalfOpensWithTheLeftColumnCollapsed. Half a terminal, and the file
// tree plus comment index would spend a third of it — where the diff is what the
// half was opened to read. `\` brings them back.
func TestTheDiffHalfOpensWithTheLeftColumnCollapsed(t *testing.T) {
	_, s := splitWithDiff(t)
	dm, ok := s.right.(*diffModal)
	if !ok {
		t.Fatalf("the right half is %T", s.right)
	}
	if !dm.inner.LeftColumnHidden() {
		t.Error("the diff half opened with the left column taking a third of it")
	}
}

// TestTheDiffHalfOpensWithEveryFileFolded. Same argument as the left column: in
// half a terminal the diff is opened to answer "what did it touch" first, and an
// expanded first file means scrolling past it to find out there are eight.
func TestTheDiffHalfOpensWithEveryFileFolded(t *testing.T) {
	_, s := splitWithDiff(t)
	dm, ok := s.right.(*diffModal)
	if !ok {
		t.Fatalf("the right half is %T", s.right)
	}
	if !dm.inner.FilesFoldByDefault() {
		t.Error("the diff half opened with every file expanded")
	}
}

// splitWithDiff is `|c`: the agent beside awp's own diff viewer.
func splitWithDiff(t *testing.T) (Model, *splitModal) {
	t.Helper()
	m := splitDeck(t)
	m.diffLoad = func(Item, DiffScope, int) (string, error) { return sampleSplitDiff, nil }
	m = pressDeck(t, m, runeKey("|"))
	m = pressDeck(t, m, runeKey("c"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("|c opened %T (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })
	return m, s
}

// sampleSplitDiff is a one-file change, enough for the viewer to have a body.
const sampleSplitDiff = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 alpha
-beta
+gamma
 delta
`

// TestTheSplitLeavesTheBarItsRowAndFillsTheRest. The row above both halves is
// the deck's (see host_bar.go and TestTheBarIsInTheSameCellsWhicheverArrangementIsUp),
// so what the split owes is the rest of the terminal exactly — a row short leaves
// a band of dead cells, a row over pushes a half's border off the bottom.
func TestTheSplitLeavesTheBarItsRowAndFillsTheRest(t *testing.T) {
	m, s := openedSplit(t, "v")
	m.itemsAll = waitingRows()

	body := ansi.Strip(s.renderPopover(&m, m.childBox()))
	if got, want := lipgloss.Height(body), m.height-topRowRows; got != want {
		t.Errorf("the halves are %d rows, want %d", got, want)
	}
	rows := strings.Split(ansi.Strip(m.render()), "\n")
	if len(rows) != m.height {
		t.Fatalf("the frame is %d rows, the terminal is %d", len(rows), m.height)
	}
	for _, i := range []int{0, len(rows) - 1} {
		if got := lipgloss.Width(rows[i]); got != m.width {
			t.Errorf("frame row %d is %d columns in a %d-column terminal: %q", i, got, m.width, rows[i])
		}
	}
}

// TestTheDividerMoves. The keys are behind the prefix, and the halves have to
// follow — a ratio nothing renders from is a number that only looks like a
// resize.
func TestTheDividerMoves(t *testing.T) {
	m, s := openedSplit(t, "v")
	even := s.splitCol(m.childBox())
	if want := m.width / 2; even != want {
		t.Fatalf("a fresh split divides at %d, want %d", even, want)
	}
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(">"))
	wider := s.splitCol(m.childBox())
	if wider <= even {
		t.Errorf("> put the divider at %d, no further right than %d", wider, even)
	}
	if got := lipgloss.Width(renderChild(&m, s.left, mustBox(t, s, &m, s.left))); got != wider {
		t.Errorf("the left half rendered %d columns with the divider at %d", got, wider)
	}
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("="))
	if got := s.splitCol(m.childBox()); got != even {
		t.Errorf("= left the divider at %d, want %d", got, even)
	}
}

// TestTheDividerIsDraggable. A press on it starts a drag, motion moves it, and
// the release ends one — and none of the three reaches a half, since a program
// being typed at by a resize gesture is the failure mode this guards.
func TestTheDividerIsDraggable(t *testing.T) {
	m, s := openedSplit(t, "v")
	col := s.splitCol(m.childBox())
	m = pressMouse(t, m, tea.MouseClickMsg{X: col, Y: 5, Button: tea.MouseLeft})
	if !s.dragging {
		t.Fatal("a press on the divider did not start a drag")
	}
	target := col + 20
	m = pressMouse(t, m, tea.MouseMotionMsg{X: target, Y: 5, Button: tea.MouseLeft})
	if got := s.splitCol(m.childBox()); got != target {
		t.Errorf("dragging to column %d left the divider at %d", target, got)
	}
	m = pressMouse(t, m, tea.MouseReleaseMsg{X: target, Y: 5, Button: tea.MouseLeft})
	if s.dragging {
		t.Error("the release did not end the drag")
	}
	// Motion after the release is an ordinary event again, and must not move the
	// divider just because the pointer crossed the screen.
	settled := s.splitCol(m.childBox())
	pressMouse(t, m, tea.MouseMotionMsg{X: 10, Y: 5})
	if got := s.splitCol(m.childBox()); got != settled {
		t.Errorf("motion after the release moved the divider to %d, want %d", got, settled)
	}
}

// TestADragDoesNotMoveTheKeyboard. Grabbing the divider is not choosing a half:
// the press is consumed by the divider, so halfAt never sees it.
func TestADragDoesNotMoveTheKeyboard(t *testing.T) {
	m, s := openedSplit(t, "v")
	if !s.rightFocused {
		t.Fatal("expected the right half focused to start")
	}
	col := s.splitCol(m.childBox())
	// The left border of the divider pair, which is inside the left half's box.
	pressMouse(t, m, tea.MouseClickMsg{X: col - 1, Y: 5, Button: tea.MouseLeft})
	if !s.rightFocused {
		t.Error("grabbing the divider moved the keyboard to the left half")
	}
	if !s.dragging {
		t.Error("a press on the divider's left column did not start a drag")
	}
}

// TestASplitAsksForTheMouseItself. The deck otherwise only requests mouse
// reporting when the focused pane's own program wants it, which would leave the
// divider undraggable beside a plain shell.
func TestASplitAsksForTheMouseItself(t *testing.T) {
	m, _ := openedSplit(t, "v")
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("a split declared mouse mode %v, want cell motion", got)
	}
}

// pressMouse sends one mouse event through the deck.
func pressMouse(t *testing.T, m Model, msg tea.MouseMsg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return got
}

// TestTheDividerStopsBeforeAHalfDisappears. Held at the wall the key does
// nothing, rather than squeezing a half down to a border around nothing and
// resizing a pty to a width its program cannot lay out.
func TestTheDividerStopsBeforeAHalfDisappears(t *testing.T) {
	m, s := openedSplit(t, "v")
	for range 40 {
		s.resize(&m, splitResizeStep)
	}
	col := s.splitCol(m.childBox())
	if col > m.width-splitHalfMinW {
		t.Errorf("the right half was squeezed to %d columns, minimum %d", m.width-col, splitHalfMinW)
	}
	// And back the same number of taps: a clamped tap must not bank a fraction
	// that has to be spent again on the way out.
	for range 40 {
		s.resize(&m, -splitResizeStep)
	}
	if got := s.splitCol(m.childBox()); got > splitHalfMinW+int(splitResizeStep*float64(m.width))+1 {
		t.Errorf("coming back left the divider at %d, want it against the left wall", got)
	}
}

// TestTheDividerHoldsItsPlaceAcrossAResize. The fraction is stored, not the
// column, so a terminal that gets wider keeps the proportion you chose.
func TestTheDividerHoldsItsPlaceAcrossAResize(t *testing.T) {
	m, s := openedSplit(t, "v")
	s.resize(&m, splitResizeStep*2)
	before := float64(s.splitCol(m.childBox())) / float64(m.width)
	m.width = 300
	after := float64(s.splitCol(m.childBox())) / float64(m.width)
	if diff := after - before; diff > 0.01 || diff < -0.01 {
		t.Errorf("the divider was at %.2f of the width and is now at %.2f", before, after)
	}
}

// mustBox is boxOf, failing the test rather than returning nowhere.
func mustBox(t *testing.T, s *splitModal, m *Model, child modal) box {
	t.Helper()
	b := s.boxOf(child, m.childBox())
	if b.w <= 0 {
		t.Fatalf("the half is addressed as nowhere: %+v", b)
	}
	return b
}

// TestAHalfSitsWhereTheCursorThinksItDoes. boxOf is what the hosted program's
// cursor and its mouse translation are derived from, and renderPopover is where
// the half is actually drawn. They have to subtract the bar's row the same
// number of times: the renderer remembered it and boxOf did not, so a program's
// cursor was drawn one row above the cell it was in, and a click was reported
// one row below the cell it hit. Both derive from childBox now, which is the one
// place that subtraction happens.
func TestAHalfSitsWhereTheCursorThinksItDoes(t *testing.T) {
	m, s := openedSplit(t, "v")
	full := m.childBox()
	wantLeft, wantRight := s.boxes(full)
	for _, half := range []struct {
		name  string
		child modal
		want  box
	}{
		{"left", s.left, wantLeft},
		{"right", s.right, wantRight},
	} {
		if got := s.boxOf(half.child, full); got != half.want {
			t.Errorf("the %s half is drawn in %+v but addressed as %+v", half.name, half.want, got)
		}
	}
	if got := wantLeft.y; got != topRowRows {
		t.Errorf("the halves start on row %d, want %d — below the deck's bar", got, topRowRows)
	}
	if got, want := wantLeft.h, m.height-topRowRows; got != want {
		t.Errorf("the halves are %d rows tall, want %d", got, want)
	}
}

// TestAHalfHasNoHeaderOfItsOwn. Everything a pane's header used to carry is on
// the deck's bar above both halves, so a half that still drew one would be
// spending a row of someone else's program on a second copy — which is what the
// first cut of the split did, and why neither copy could be relied on.
//
// Asserted as a height: the box is the border plus the terminal, and a header
// would be one row more.
func TestAHalfHasNoHeaderOfItsOwn(t *testing.T) {
	m, s := openedSplit(t, "v")
	m.itemsAll = waitingRows()

	left := mustBox(t, s, &m, s.left)
	p := s.left.(*panePopover)
	rendered := p.renderPopover(&m, left)
	tw, th := paneDims(left.w, left.h)
	if _, wantH := paneBox(tw, th); lipgloss.Height(rendered) != wantH {
		t.Errorf("the half rendered %d rows, want %d — the border and the program, nothing else",
			lipgloss.Height(rendered), wantH)
	}
	if th != left.h-borderCells {
		t.Errorf("the program got %d of the half's %d rows, want %d", th, left.h, left.h-borderCells)
	}
}
