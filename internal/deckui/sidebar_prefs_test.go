package deckui

import (
	"errors"
	"strings"
	"testing"
)

// Remembering whether the strip is up.
//
// It is a property of the deck, not of a workspace or an arrangement: the answer
// to "do I want to see what is waiting" does not change when you switch panes, so
// it must not change when you restart either.

// TestTogglingTheSidebarRecordsIt, in both directions. Off is a choice — you
// pressed the key to turn it off — so it has to be recorded as deliberately as on,
// which is the whole reason the stored field carries no omitempty.
func TestTogglingTheSidebarRecordsIt(t *testing.T) {
	var saved []bool
	m := sidebarDeck(t)
	m = m.WithSidebarSaver(func(on bool) error {
		saved = append(saved, on)
		return nil
	})
	m.toggleSidebar()
	m.toggleSidebar()
	want := []bool{true, false}
	if len(saved) != len(want) || saved[0] != want[0] || saved[1] != want[1] {
		t.Errorf("toggling twice recorded %v, want %v", saved, want)
	}
}

// TestARefusedSidebarToggleRecordsNothing. The strip needs columns, and a terminal
// too narrow to give them leaves it alone — so there is no new state to save, and
// saving the old one would rewrite the file on a keypress that did nothing.
func TestARefusedSidebarToggleRecordsNothing(t *testing.T) {
	m := sidebarDeck(t)
	m.width = sidebarWidth + sidebarChildMinW - 1
	saved := false
	m = m.WithSidebarSaver(func(bool) error { saved = true; return nil })
	m.toggleSidebar()
	if saved {
		t.Error("a refused toggle was recorded")
	}
	if m.sidebar {
		t.Error("the strip came up on a terminal too narrow for it")
	}
}

// TestTheDeckOpensWithTheRememberedSidebar.
func TestTheDeckOpensWithTheRememberedSidebar(t *testing.T) {
	m := sidebarDeck(t).WithSidebar(true)
	if !m.sidebar {
		t.Error("a deck told the strip was up opened without it")
	}
}

// TestASaveThatFailsSaysSo, and leaves the strip where you put it. The toggle
// already happened; what was lost is only that it will not be remembered, and a
// deck that refused to open a strip because a file would not write would be
// answering the wrong question.
func TestASaveThatFailsSaysSo(t *testing.T) {
	m := sidebarDeck(t)
	m = m.WithSidebarSaver(func(bool) error { return errors.New("disk is full") })
	m.toggleSidebar()
	if !m.sidebar {
		t.Error("a failed save undid the toggle")
	}
	if !strings.Contains(m.status, "disk is full") {
		t.Errorf("the failure is not on the status bar: %q", m.status)
	}
}

// TestASidebarSaverIsOptional — the mini-deck and every test deck have none, the
// same way they have no ScopeSaver.
func TestASidebarSaverIsOptional(t *testing.T) {
	m := sidebarDeck(t)
	m.toggleSidebar() // must not panic
	if !m.sidebar {
		t.Error("the toggle did nothing")
	}
}
