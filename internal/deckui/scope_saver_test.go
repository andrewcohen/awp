package deckui

import (
	"errors"
	"strings"
	"testing"
)

// The scope is a choice you make with a key and expect to still be true tomorrow.
// The deck does not know where preferences live — it calls a saver — so what these
// pin is that it calls one, with the scope it actually switched to.

// TestCyclingTheScopeRemembersIt.
func TestCyclingTheScopeRemembersIt(t *testing.T) {
	var saved []Scope
	m := splitDeck(t).WithScopeSaver(func(s Scope) error {
		saved = append(saved, s)
		return nil
	})
	before := m.Scope()
	m = pressDeck(t, m, runeKey("P"))
	if m.Scope() == before {
		t.Fatal("P did not change the scope")
	}
	if len(saved) != 1 {
		t.Fatalf("P saved the scope %d times, want once", len(saved))
	}
	if saved[0] != m.Scope() {
		// The saved value and the one on screen have to be the same, or the deck
		// re-opens on a scope you were only passing through.
		t.Errorf("saved %v, but the deck is showing %v", saved[0], m.Scope())
	}
}

// TestADeckWithNoSaverStillCycles. The mini-deck and every test deck have no
// saver; a nil hook is "this deck does not remember", not a crash.
func TestADeckWithNoSaverStillCycles(t *testing.T) {
	m := splitDeck(t)
	before := m.Scope()
	m = pressDeck(t, m, runeKey("P"))
	if m.Scope() == before {
		t.Error("P did nothing on a deck with no scope saver")
	}
}

// TestAFailedSaveSaysSo. The scope did change — only remembering it failed — so
// the message is appended to what you pressed the key for rather than replacing
// it. Swallowing it means the next deck opens on the old scope with no explanation.
func TestAFailedSaveSaysSo(t *testing.T) {
	m := splitDeck(t).WithScopeSaver(func(Scope) error {
		return errors.New("disk is full")
	})
	m = pressDeck(t, m, runeKey("P"))
	if !strings.Contains(m.status, scopeLabel(m.Scope())) {
		t.Errorf("the status %q does not say which scope is now showing", m.status)
	}
	if !strings.Contains(m.status, "disk is full") {
		t.Errorf("the status %q does not say the scope was not remembered", m.status)
	}
}
