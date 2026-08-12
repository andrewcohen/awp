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
	return m
}

// leaveKey is the reserved key, as the terminal sends it.
func leaveKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl} }

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

// TestASplitRefusesATerminalTooNarrowForTwo, naming the width it wants. Two
// halves of a narrow terminal are two useless panes, and opening something
// technically correct that cannot be read is worse than saying no.
func TestASplitRefusesATerminalTooNarrowForTwo(t *testing.T) {
	m := splitDeck(t)
	m.width = splitMinW - 1
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
		m = pressDeck(t, m, leaveKey())
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

// TestAHeldReservedKeyDoesNotThrash is the complaint that killed the debounce:
// in a single pane ctrl+\ leaves on every press, so a key repeat flips back and
// forth. Arming a prefix is idempotent, so the same repeat does nothing at all
// until you press a verb.
func TestAHeldReservedKeyDoesNotThrash(t *testing.T) {
	m, s := openedSplit(t, "v")
	for range 8 {
		m = pressDeck(t, m, leaveKey())
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

// TestThePrefixThenQLeaves. `q` rather than the reserved key a second time,
// because a second press and a key repeat are indistinguishable — see
// TestAHeldReservedKeyDoesNotThrash, which is the test that decided this.
func TestThePrefixThenQLeaves(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("q"))
	if m.active != nil {
		t.Errorf("the prefix then q left %T open", m.active)
	}
}

// TestZoomGivesTheFocusedHalfTheWholeScreen, and giving it back does not have to
// re-open anything — which is the reason it is a zoom and not a close.
func TestZoomGivesTheFocusedHalfTheWholeScreen(t *testing.T) {
	m, s := openedSplit(t, "v")
	m = pressDeck(t, m, leaveKey())
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
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("x"))
	if m.active != agent {
		t.Fatalf("closing the focused half left %T, want the agent alone", m.active)
	}
}

// TestAKeyGoesToTheFocusedHalfAndNowhereElse. The two halves are both live
// programs, and a keystroke reaching the wrong one types into an agent.
func TestAKeyGoesToTheFocusedHalfAndNowhereElse(t *testing.T) {
	m, s := openedSplit(t, "v")
	left := s.left.(*panePopover)
	right := s.right.(*panePopover)
	eventually(t, "both halves to paint", func() bool {
		return strings.Contains(left.term.View(), "PANE-UP") && strings.Contains(right.term.View(), "PANE-UP")
	})

	pressDeck(t, m, runeKey("Q"))
	eventually(t, "the focused half to receive the key", func() bool {
		return strings.Contains(right.term.View(), "Q")
	})
	if strings.Contains(ansi.Strip(left.term.View()), "Q") {
		t.Error("the key reached the half the keyboard had left")
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

// TestTheChordSaysWhatItCanDo. A keys-only menu is undiscoverable unless the
// status bar lists it, and the `?` overlay has to carry the same set — a key
// documented in one and not the other is how they start disagreeing.
func TestTheChordSaysWhatItCanDo(t *testing.T) {
	m := splitDeck(t)
	m = pressDeck(t, m, runeKey("|"))
	for _, a := range splitActions {
		if !strings.Contains(m.status, a.key+" "+a.label) {
			t.Errorf("the chord's menu omits %q: %q", a.key, m.status)
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
// exactly like pressing a dead key — which is how it was reported. The menu is
// written over the frame's bottom row rather than given one of its own, so the
// halves' boxes do not change and no pty is resized by a modifier keypress.
func TestThePrefixIsVisibleWhileItIsArmed(t *testing.T) {
	m, s := openedSplit(t, "v")
	before := s.renderPopover(&m, m.childBox())
	if strings.Contains(ansi.Strip(before), "zoom") {
		t.Fatal("the prefix menu is on screen before the prefix was armed")
	}

	m = pressDeck(t, m, leaveKey())
	armed := ansi.Strip(s.renderPopover(&m, m.childBox()))
	for _, want := range []string{"focus", "zoom", "q leave"} {
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
	if got := lipgloss.Height(s.renderPopover(&m, m.childBox())); got != m.height {
		t.Errorf("the split is %d rows tall in a %d-row terminal", got, m.height)
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
