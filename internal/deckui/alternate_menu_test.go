package deckui

import (
	"strings"
	"testing"
)

// L in the pane menu, which is #360.
//
// The deck's row list has had this key since #286, and inside a program it was the
// one arrangement verb the menu did not carry — so switching panes meant leaving to
// the deck, pressing L, and being somewhere you had not asked to be on the way. The
// verb is the same act as the row list's; what is new is that it has to take down
// what is on screen before the other thing can come up.

// paneKindEditor is `e`'s kind. A literal here, as in allKinds: unlike the agent's,
// this kind is not named in production code — the backend maps the key to it — so
// there is no const to reach for.
const paneKindEditor = "editor"

// TestTheMenusLGoesToTheArrangementBefore is the whole feature: two panes deep,
// the menu's L is in the other one.
func TestTheMenusLGoesToTheArrangementBefore(t *testing.T) {
	m := twoPanesDeep(t)
	if kind := activePaneKind(t, &m); kind != paneKindEditor {
		t.Fatalf("precondition: the second pane is %q", kind)
	}

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(alternateKey))

	if kind := activePaneKind(t, &m); kind != PaneKindAgent {
		t.Errorf("%s %s landed in %q, want the pane before this one", PaneMenuKey, alternateKey, kind)
	}
}

// TestTheMenusLIsItsOwnWayBack. The point of alternating rather than resuming: the
// two most recent things are one keypress apart, so pressing it twice is where you
// started. It only holds if arriving somewhere records where you came from.
func TestTheMenusLIsItsOwnWayBack(t *testing.T) {
	m := twoPanesDeep(t)
	// The midpoint is asserted because without it this test passes on a key that
	// does nothing at all: staying put twice ends up where it started too.
	want := []string{PaneKindAgent, paneKindEditor}
	for i := range 2 {
		m = pressDeck(t, m, menuKey())
		m = pressDeck(t, m, runeKey(alternateKey))
		if kind := activePaneKind(t, &m); kind != want[i] {
			t.Fatalf("press %d landed in %q, want %q", i+1, kind, want[i])
		}
	}
	if kind := activePaneKind(t, &m); kind != paneKindEditor {
		t.Errorf("pressing %s twice landed in %q, want back where it started", alternateKey, kind)
	}
}

// TestAlternatingAwayClosesThePaneItLeft. The pane's terminal is a live process
// with a pty; installing the next arrangement over the top of it without closing
// it leaks both, and nothing on screen would say so.
func TestAlternatingAwayClosesThePaneItLeft(t *testing.T) {
	m := twoPanesDeep(t)
	left, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("active is %T", m.active)
	}
	term, ok := left.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the pane's terminal is %T", left.term)
	}

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(alternateKey))

	if !term.closed {
		t.Error("the pane that was left is still running its process")
	}
}

// TestAlternatingFromASplitClosesBothHalves. A split is one arrangement, so
// leaving it leaves all of it — a half left open would be a pty painting nothing.
func TestAlternatingFromASplitClosesBothHalves(t *testing.T) {
	m := splitDeck(t)
	// A pane to alternate back to, then the split that replaces it on screen.
	m = pressDeck(t, m, runeKey("e"))
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("|"))
	m = pressDeck(t, m, runeKey("v"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("|v opened %T (status %q)", m.active, m.status)
	}
	terms := make([]*fakeTerm, 0, 2)
	for _, half := range []modal{s.left, s.right} {
		p, isPane := half.(*panePopover)
		if !isPane {
			t.Fatalf("a half is %T, want two panes", half)
		}
		term, isFake := p.term.(*fakeTerm)
		if !isFake {
			t.Fatalf("a half's terminal is %T", p.term)
		}
		terms = append(terms, term)
	}

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(alternateKey))

	for i, term := range terms {
		if !term.closed {
			t.Errorf("half %d is still running after the split was left", i)
		}
	}
	if kind := activePaneKind(t, &m); kind != paneKindEditor {
		t.Errorf("landed in %q, want the pane the split replaced", kind)
	}
}

// TestTheMenusLSaysSoWithNowhereToGo. One pane deep there is no other arrangement,
// and the answer is a message — not the pane closing, which would be the key doing
// half of itself and leaving you looking at the deck.
func TestTheMenusLSaysSoWithNowhereToGo(t *testing.T) {
	m := splitDeck(t)
	m = pressDeck(t, m, runeKey("a"))
	before, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("`a` opened %T (status %q)", m.active, m.status)
	}

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(alternateKey))

	if m.active != before {
		t.Errorf("%s with nowhere to go left the pane for %T", alternateKey, m.active)
	}
	if !strings.Contains(m.status, "only one pane") {
		t.Errorf("status is %q, want it to say there is nothing to alternate with", m.status)
	}
}

// TestTheMenusAlternateKeyIsTheDecksL. Two spellings of one verb: the row list
// binds it through the keymap, the menus through a const, and a change to either
// that left the other behind would mean the key depended on which screen you were
// looking at.
func TestTheMenusAlternateKeyIsTheDecksL(t *testing.T) {
	keys := newDeckKeyMap().LastSession.Keys()
	for _, k := range keys {
		if k == alternateKey {
			return
		}
	}
	t.Errorf("the menus use %q, the deck's row list binds %q", alternateKey, keys)
}

// TestBothMenusOfferTheKey. It is discoverable only from the menu, since the ?
// overlay is a screen away from the program you are in.
func TestBothMenusOfferTheKey(t *testing.T) {
	m := splitDeck(t)
	for name, mn := range map[string]deckMenu{
		"a pane": panePrefixMenu(&m),
		"split":  splitPrefixMenu(&m),
	} {
		if !menuBinds(mn, alternateKey) {
			t.Errorf("%s's menu does not offer %q: %+v", name, alternateKey, mn.verbs)
		}
	}
}

// menuBinds reports whether the menu lists this key.
func menuBinds(mn deckMenu, key string) bool {
	for _, v := range mn.verbs {
		if v[0] == key {
			return true
		}
	}
	return false
}

// twoPanesDeep leaves the editor's pane on screen with the agent's behind it, which
// is the shallowest state where alternating has an answer.
func twoPanesDeep(t *testing.T) Model {
	t.Helper()
	m := splitDeck(t)
	m = pressDeck(t, m, runeKey("a"))
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, runeKey("e"))
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("`e` opened %T (status %q)", m.active, m.status)
	}
	return m
}

// activePaneKind names the pane on screen, or fails saying what is there instead.
func activePaneKind(t *testing.T, m *Model) string {
	t.Helper()
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("active is %T (status %q)", m.active, m.status)
	}
	return p.kind
}
