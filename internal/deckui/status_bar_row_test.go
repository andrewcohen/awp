package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The frame's height is budgeted with the status bar as exactly one line, so a bar
// that wraps costs a row nothing gave it and the whole deck is pushed up off the
// top of the screen. These pin the one-row guarantee at the two places a long
// status is easy to produce.

// TestTheStatusBarIsAlwaysOneRow, whatever it is asked to say.
func TestTheStatusBarIsAlwaysOneRow(t *testing.T) {
	long := strings.Repeat("a very long status message that will not fit ", 6)
	for _, width := range []int{20, 40, 80, 120} {
		bar := composeStatusBar(nil, "", long, "? help", width)
		if got := lipgloss.Height(bar); got != 1 {
			t.Errorf("width %d: the bar is %d rows:\n%s", width, got, bar)
		}
		if got := lipgloss.Width(bar); got > width {
			t.Errorf("width %d: the bar is %d columns wide", width, got)
		}
	}
}

// TestAnArmedChordDoesNotPushTheDeckUp. `|` and `p` are menus over the row list,
// and the row list has to stay exactly where it was while one is up — a chord that
// costs the frame a row moves every workspace row under the cursor's own reading
// position, which is the sort of jump you feel before you can name it.
//
// Pinned as the rows themselves rather than as the frame's height: the frame is
// m.height either way, so a chord that drops the deck's top row scrolls the list
// up without changing a single measurement.
func TestAnArmedChordDoesNotPushTheDeckUp(t *testing.T) {
	arm := map[string]tea.KeyPressMsg{"|": runeKey("|"), "p": runeKey("p")}
	for key, press := range arm {
		for _, width := range []int{60, 100, 200} {
			m := splitDeck(t)
			m.width = width
			before := rowLineIndex(t, m.render())
			m = pressDeck(t, m, press)
			if m.active == nil {
				t.Fatalf("%s did not arm a menu", key)
			}
			if _, ok := m.active.(chordModal); !ok {
				t.Fatalf("%s armed a %T, which is not a chord", key, m.active)
			}
			frame := m.render()
			if got := lipgloss.Height(frame); got != m.height {
				t.Errorf("%s at width %d: the frame is %d rows, want %d", key, width, got, m.height)
			}
			if got := rowLineIndex(t, frame); got != before {
				t.Errorf("%s at width %d: arming moved the workspace row from line %d to %d:\n%s",
					key, width, before, got, frame)
			}
		}
	}
}

// rowLineIndex is which line of the frame the deck's one workspace row is on.
//
// Found by the selection bar rather than by the workspace's name, which is "ws" —
// a substring of "browser", in the PR menu, on the row above it.
func rowLineIndex(t *testing.T, frame string) int {
	t.Helper()
	for i, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "┃") {
			return i
		}
	}
	t.Fatalf("no workspace row in the frame:\n%s", frame)
	return -1
}
