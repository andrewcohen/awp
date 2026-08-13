package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// repeatKey is the same key arriving because it is being held, which a terminal
// only reports as distinct from a press when it has granted the Kitty keyboard
// protocol's event-types flag — see View, which asks for it.
func repeatKey(msg tea.KeyPressMsg) tea.KeyPressMsg {
	msg.IsRepeat = true
	return msg
}

// TestTheDeckAsksForKeyEventTypes. Everything below depends on IsRepeat being
// set, which only happens if the view asks the terminal to report event types.
// The request is what makes the distinction exist at all, so it is the first
// thing to pin: the guards are dead code without it.
func TestTheDeckAsksForKeyEventTypes(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	if !m.View().KeyboardEnhancements.ReportEventTypes {
		t.Error("the deck does not ask the terminal to report key repeats")
	}
}

// TestTheDeckRemembersWhetherTheTerminalObliged. A terminal that grants nothing
// never sets IsRepeat, and a gesture that needs the distinction should be able
// to say so rather than behave differently for reasons the user cannot see.
func TestTheDeckRemembersWhetherTheTerminalObliged(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	if m.keysEnhanced {
		t.Error("the deck assumed enhanced keys before the terminal answered")
	}
	next, _ := m.Update(tea.KeyboardEnhancementsMsg{Flags: 0b10})
	if got := next.(Model); !got.keysEnhanced {
		t.Error("the terminal granted event types and the deck did not record it")
	}
}

// TestAHeldLeaveKeyDoesNotFlapTheDeck is #307: ctrl+\ used to leave a pane and,
// from the row list, to go back into one, so a repeat treated as a press opened
// what the next repeat closed for as long as the key was down. This is the row
// list's half — a repeat arriving there must not re-enter the pane just left.
func TestAHeldLeaveKeyDoesNotFlapTheDeck(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	next, _ = m.Update(resumeKey())
	m = next.(Model)
	if m.active != nil {
		t.Fatalf("ctrl+\\ did not leave the pane (active=%T)", m.active)
	}
	for range 8 {
		next, _ = m.Update(repeatKey(resumeKey()))
		m = next.(Model)
		if m.active != nil {
			opened := m.active
			if p, ok := opened.(*panePopover); ok {
				p.close(&m)
			}
			t.Fatalf("a repeat re-entered the pane the press had left (active=%T)", opened)
		}
	}
}

// TestAHeldLeaveKeyInAPaneStaysInIt. The pane's half of the same flap, and the
// reason the repeat is swallowed rather than forwarded: a held ctrl+\ passed
// through to the program would spray it at whatever is running.
func TestAHeldLeaveKeyInAPaneStaysInIt(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	defer p.close(&m)
	for range 8 {
		next, _ = m.Update(repeatKey(resumeKey()))
		m = next.(Model)
	}
	if m.active != p {
		t.Errorf("a held ctrl+\\ closed the pane (active=%T)", m.active)
	}
	// And one real press still leaves, so the guard has not taken the key.
	next, _ = m.Update(resumeKey())
	if got := next.(Model); got.active != nil {
		t.Errorf("a pressed ctrl+\\ did not leave the pane (active=%T)", got.active)
	}
}

// TestTheMenuKeyNeedsEventTypes. ctrl+shift+\ is 0x1c on a plain terminal, exactly
// as ctrl+\ is, so there is nothing to tell apart: reading it as the menu there
// would swallow the key that leaves. The menu is unavailable rather than
// approximated, and the pane still leaves on the key it always did.
func TestTheMenuKeyNeedsEventTypes(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	if m.keysEnhanced {
		t.Fatal("this terminal reports event types; the fallback is untested")
	}
	// What such a terminal actually sends for the menu key.
	next, _ = m.Update(resumeKey())
	m = next.(Model)
	if m.active != nil {
		t.Errorf("ctrl+\\ stopped leaving the pane on a terminal with no menu (active=%T)", m.active)
	}
	if hint := m.topRowHint(); strings.Contains(hint, PaneMenuKey) {
		t.Errorf("the row offers %q, which this terminal cannot send: %q", PaneMenuKey, hint)
	}
}

// TestTheMenuKeyIsBothSpellings. A shifted backslash arrives as `|` with ctrl from
// a terminal that resolves the shift, and as `\` with ctrl and shift from one that
// reports it — the same keypress either way.
func TestTheMenuKeyIsBothSpellings(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	m.keysEnhanced = true
	for _, msg := range []tea.KeyPressMsg{
		{Code: '|', Mod: tea.ModCtrl},
		{Code: '\\', Mod: tea.ModCtrl | tea.ModShift},
	} {
		if !paneMenuPressed(&m, msg) {
			t.Errorf("%q was not read as the menu key", msg.String())
		}
	}
	// And the door is not the menu, or pressing it would open one instead of leaving.
	if paneMenuPressed(&m, resumeKey()) {
		t.Error("the leave key was read as the menu key")
	}
}
