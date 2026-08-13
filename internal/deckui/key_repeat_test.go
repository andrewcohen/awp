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

// TestTheMenuKeyNeedsNothingFromTheTerminal. ctrl+b is 0x02 on a plain terminal and
// under the Kitty protocol alike — so the
// menu is reachable everywhere. It was ctrl+shift+\ , which is 0x1c on a plain
// terminal exactly as ctrl+\ is: nothing to tell apart, so such a terminal had no
// menu at all. This is the test that used to say so.
func TestTheMenuKeyNeedsNothingFromTheTerminal(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	t.Cleanup(func() { p.close(&m) })
	if m.keysEnhanced {
		t.Fatal("this terminal reports event types; the plain-terminal case is untested")
	}
	if !paneMenuPressed(&m, menuKey()) {
		t.Error("the menu key was not read as the menu on a terminal with no event types")
	}
	m = pressDeck(t, m, menuKey())
	if !p.prefixArmed {
		t.Error("the menu did not arm on a plain terminal")
	}
	if hint := m.topRowHint(); !strings.Contains(hint, PaneMenuKey) {
		t.Errorf("the row does not offer the menu it can now always send: %q", hint)
	}
}

// TestTheDoorIsNotTheMenu. The two are one key apart in the hand and must not be
// one key: reading the door as the menu would open a menu instead of leaving.
func TestTheDoorIsNotTheMenu(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	m.keysEnhanced = true
	if paneMenuPressed(&m, resumeKey()) {
		t.Error("the leave key was read as the menu key")
	}
	// And the old spelling is gone rather than lingering as a second way to say it.
	for _, msg := range []tea.KeyPressMsg{
		{Code: '|', Mod: tea.ModCtrl},
		{Code: '\\', Mod: tea.ModCtrl | tea.ModShift},
	} {
		if paneMenuPressed(&m, msg) {
			t.Errorf("%q still opens the menu; it was unbound", msg.String())
		}
	}
}
