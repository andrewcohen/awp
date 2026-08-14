package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The menu as a floating popover, which is #344.
//
// What it has to be is three things at once: over what is on screen rather than
// instead of it, costing the frame no rows, and one verb per line. The row it used to
// take could manage the third only by not doing it.

// TestTheMenuFloatsOverWhatIsOnScreen. The verbs name things to do to what you are
// looking at, so what you are looking at has to still be there while you read them.
func TestTheMenuFloatsOverWhatIsOnScreen(t *testing.T) {
	m, s := openedSplit(t, "v")
	behind := ansi.Strip(m.render())
	s.prefixArmed = true
	armed := ansi.Strip(m.render())

	if !strings.Contains(armed, "zoom") {
		t.Fatalf("the armed menu is not on screen:\n%s", armed)
	}
	// The frame's own corners are the halves' borders. A menu that replaced the screen
	// rather than floating over it would take them with it.
	if got, want := strings.Count(armed, "╰"), strings.Count(behind, "╰"); got <= want {
		t.Errorf("the armed frame has %d bottom corners and the quiet one %d — the menu did not"+
			" land on top of anything", got, want)
	}
}

// TestTheMenuCostsTheFrameNoRows. The reason a floating box beats the row: the halves
// are ptys, and a frame that changed height would relay them out — because you pressed
// a modifier.
func TestTheMenuCostsTheFrameNoRows(t *testing.T) {
	m, s := openedSplit(t, "v")
	quiet := m.render()
	s.prefixArmed = true
	armed := m.render()
	if got, want := lipgloss.Height(armed), lipgloss.Height(quiet); got != want {
		t.Errorf("arming the menu took the frame from %d rows to %d", want, got)
	}
	if got, want := lipgloss.Width(ansi.Strip(armed)), lipgloss.Width(ansi.Strip(quiet)); got != want {
		t.Errorf("arming the menu took the frame from %d columns to %d", want, got)
	}
}

// TestEveryVerbIsOnItsOwnLine, which is what the row could not do. A ribbon read left
// to right is how the verbs were described before; the point of the box is that each
// one gets the room to say what it does.
func TestEveryVerbIsOnItsOwnLine(t *testing.T) {
	m := splitDeck(t)
	mn := panePrefixMenu(&m)
	rows := strings.Split(ansi.Strip(mn.render(m.width)), "\n")
	for _, v := range mn.verbs {
		n := 0
		for _, row := range rows {
			if strings.Contains(row, v[1]) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%q appears on %d lines of the menu, want exactly 1:\n%s",
				v[1], n, strings.Join(rows, "\n"))
		}
	}
}

// TestTheMenuStaysInsideANarrowTerminal. It floats over the frame, so a box wider than
// the frame would be drawn off the side of it — and half a menu is worse than none.
func TestTheMenuStaysInsideANarrowTerminal(t *testing.T) {
	m := splitDeck(t)
	for _, width := range []int{40, 60, 100, 200} {
		m.width = width
		got := lipgloss.Width(ansi.Strip(panePrefixMenu(&m).render(width)))
		if got > width {
			t.Errorf("at %d columns the menu is %d wide", width, got)
		}
	}
}

// TestTheMenuIsNotWiderThanItNeeds. The other half of the width rule: a box padded out
// to a 200-column terminal is a menu you have to move your eyes across to read.
func TestTheMenuIsNotWiderThanItNeeds(t *testing.T) {
	m := splitDeck(t)
	m.width = 200
	if got := lipgloss.Width(ansi.Strip(prMenu().render(m.width))); got >= m.width {
		t.Errorf("the pr menu is %d columns wide on a %d-column terminal", got, m.width)
	}
}

// TestEveryMenuNamesWhatItActsOn. Four menus reach the same box, and two of them are
// armed by the same key — so the title is the only thing that says whether the verbs
// are about this pane, this split, this workspace or this row.
func TestEveryMenuNamesWhatItActsOn(t *testing.T) {
	m := splitDeck(t)
	for _, mn := range []deckMenu{panePrefixMenu(&m), splitPrefixMenu(&m), splitChordMenu(), prMenu()} {
		if mn.title == "" {
			t.Errorf("a menu has no title: %+v", mn.verbs)
		}
		if len(mn.verbs) == 0 {
			t.Errorf("menu %q has no verbs", mn.title)
		}
		if !menuBinds(mn, "esc") {
			t.Errorf("menu %q does not offer esc", mn.title)
		}
	}
}

// TestAMenuDropsAVerbWithNoKey. menu() skips empty keys so a caller can leave a row
// out by condition rather than building the slice twice — and a blank key would
// otherwise render as a description with nothing to press.
func TestAMenuDropsAVerbWithNoKey(t *testing.T) {
	mn := menu("t", [2]string{"a", "keep"}, [2]string{"", "drop"})
	if len(mn.verbs) != 1 || mn.verbs[0][1] != "keep" {
		t.Errorf("menu kept a keyless verb: %+v", mn.verbs)
	}
}
