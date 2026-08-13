package deckui

import (
	"strings"
	"testing"

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

// TestAnArmedChordDoesNotPushTheDeckUp. `|` puts the whole window-key menu in the
// status bar, which is the longest thing the deck ever says there — and it says it
// while the row list is on screen, so a second row moves everything.
func TestAnArmedChordDoesNotPushTheDeckUp(t *testing.T) {
	for _, width := range []int{60, 100, 200} {
		m := splitDeck(t)
		m.width = width
		before := lipgloss.Height(m.render())
		m = pressDeck(t, m, runeKey("|"))
		if _, ok := m.active.(*splitChordModal); !ok {
			t.Fatalf("| did not arm the chord (active=%T)", m.active)
		}
		if got := lipgloss.Height(m.render()); got != before {
			t.Errorf("width %d: arming the chord took the frame from %d rows to %d", width, before, got)
		}
	}
}
