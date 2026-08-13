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
	// Out through the menu, since one press no longer leaves.
	next, _ = m.Update(resumeKey())
	m = next.(Model)
	next, _ = m.Update(runeKey("q"))
	m = next.(Model)
	if m.active != nil {
		t.Fatalf("ctrl+\\ then q did not leave the pane (active=%T)", m.active)
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

// TestAHeldLeaveKeyInAPaneStaysInIt. The pane's half of the same flap. Arming a
// menu is idempotent, so the repeats behind the press do nothing at all — and
// they are swallowed rather than forwarded, because a held ctrl+\ passed through
// would spray it at whatever is running.
func TestAHeldLeaveKeyInAPaneStaysInIt(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	defer p.close(&m)
	// The burst as the terminal sends it, on a terminal that reports repeats: the
	// press and every repeat behind it. Even the double tap that leaves must not
	// be spelled by holding the key.
	m.keysEnhanced = true
	next, _ = m.Update(resumeKey())
	m = next.(Model)
	for range 8 {
		next, _ = m.Update(repeatKey(resumeKey()))
		m = next.(Model)
	}
	if m.active != p {
		t.Errorf("a held ctrl+\\ closed the pane (active=%T)", m.active)
	}
	if !p.prefixArmed {
		t.Error("the repeats disarmed the menu the press armed")
	}
}

// TestADoubleTapLeavesAPane is what the event-types flag buys: the gesture the
// prefix wanted from the start, two taps of the reserved key, the way tmux
// spells its own prefix twice.
func TestADoubleTapLeavesAPane(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	m.keysEnhanced = true
	for range 2 {
		next, _ = m.Update(resumeKey())
		m = next.(Model)
	}
	if m.active != nil {
		// Reported before the cleanup, since closing it sets m.active to nil and the
		// message would name the state the test just created.
		left := m.active
		if p, ok := left.(*panePopover); ok {
			p.close(&m)
		}
		t.Errorf("two taps of ctrl+\\ did not leave the pane (active=%T)", left)
	}
}

// TestADoubleTapDoesNotLeaveWithoutEventTypes. On a terminal that grants
// nothing, the second press is indistinguishable from a repeat, so it re-arms
// and `q` is the way out. The menu says so — see panePrefixHint — rather than
// offering a verb that would leave whenever the key was held.
func TestADoubleTapDoesNotLeaveWithoutEventTypes(t *testing.T) {
	m := twoRowPanes(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	defer p.close(&m)
	if m.keysEnhanced {
		t.Fatal("this terminal reports event types; the fallback is untested")
	}
	for range 4 {
		next, _ = m.Update(resumeKey())
		m = next.(Model)
	}
	if m.active != p {
		t.Fatalf("taps of ctrl+\\ left the pane on a terminal that cannot tell them from a repeat (active=%T)", m.active)
	}
	if hint := panePrefixHint(&m); !strings.Contains(hint, "q deck") {
		t.Errorf("the menu %q does not offer q, which is the only way out here", hint)
	}
}
