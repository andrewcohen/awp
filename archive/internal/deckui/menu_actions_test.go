package deckui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The ctrl+b menu's user actions arm.
//
// The actions themselves are covered by action_pane_test.go, which drives them from
// the row list's `x`. What is new here is reaching the same ones from inside a pane,
// where before there was no way to at all — and everything that can go wrong is in
// the reaching: whether the row is offered when it should be, whether the second key
// is read as an alias rather than typed at the program, and whether the split's own
// verbs survived `x` moving out from under them.

// actionsDeck is splitDeck with one user action configured, and a backend that will
// host it.
func actionsDeck(t *testing.T) Model {
	t.Helper()
	m := splitDeck(t)
	backend := allKinds()
	backend.handles[PaneKindForAction("dev")] = true
	return m.WithPaneBackend(backend).WithUserActions(devAction())
}

// enteredPane is a deck sitting inside the row's agent pane.
func enteredPane(t *testing.T, m Model) (Model, *panePopover) {
	t.Helper()
	m = pressDeck(t, m, agentKey())
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("entering the workspace gave %T, want a pane (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { p.close(&m) })
	return m, p
}

// TestTheMenuOffersUserActions. The row exists at all only because a pane's keys all
// belong to the program it is hosting — `x` typed inside an agent is the agent's `x`,
// so ctrl+b is the only door onto the actions from where you spend most of your time.
func TestTheMenuOffersUserActions(t *testing.T) {
	m, _ := enteredPane(t, actionsDeck(t))
	got := ansi.Strip(panePrefixMenu(&m).render(m.width))
	if !strings.Contains(got, "user actions") {
		t.Errorf("the pane menu does not offer user actions:\n%s", got)
	}
}

// TestTheMenuSaysNothingAboutActionsWhenThereAreNone. A door onto nothing is worse
// than no door: a repo with no `actions` block should see the menu it saw before.
func TestTheMenuSaysNothingAboutActionsWhenThereAreNone(t *testing.T) {
	m, _ := enteredPane(t, splitDeck(t))
	got := ansi.Strip(panePrefixMenu(&m).render(m.width))
	if strings.Contains(got, "user actions") {
		t.Errorf("a repo with no actions configured was offered them:\n%s", got)
	}
}

// TestTheSubmenuListsTheAliases. The alias is the key and the name is the
// description — the two things the config carries that a person could act on.
func TestTheSubmenuListsTheAliases(t *testing.T) {
	m, p := enteredPane(t, actionsDeck(t))
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(userActionsMenuKey))
	if len(p.actions) == 0 {
		t.Fatalf("ctrl+b x did not open the submenu (status %q)", m.status)
	}
	armed, ok := m.armedMenu()
	if !ok {
		t.Fatal("the submenu is open but no menu is armed on screen")
	}
	got := ansi.Strip(armed.render(m.width))
	if !strings.Contains(got, "dev") || !strings.Contains(got, "d") {
		t.Errorf("the submenu does not list the action by alias and name:\n%s", got)
	}
}

// TestAnAliasOpensTheActionBesideThePane. What a window key from this menu does, done
// by an action: you asked for it from inside something you were watching, so the
// thing you were watching stays.
func TestAnAliasOpensTheActionBesideThePane(t *testing.T) {
	m, _ := enteredPane(t, actionsDeck(t))
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(userActionsMenuKey))
	m = pressDeck(t, m, runeKey("d"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("the alias gave %T, want a split (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })
	right, isPane := s.right.(*panePopover)
	if !isPane {
		t.Fatalf("the right half is %T, want the action's pane", s.right)
	}
	if right.kind != PaneKindForAction("dev") {
		t.Errorf("the right half hosts %q, want %q", right.kind, PaneKindForAction("dev"))
	}
}

// TestAnAliasReplacesTheFocusedHalfInASplit. With two halves already up there is
// nowhere to put a third, which is what every other window key on a split's menu
// already means.
func TestAnAliasReplacesTheFocusedHalfInASplit(t *testing.T) {
	m := actionsDeck(t)
	m = pressDeck(t, m, runeKey("|"))
	m = pressDeck(t, m, runeKey("v"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("|v did not open a split (active=%T, status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(userActionsMenuKey))
	m = pressDeck(t, m, runeKey("d"))
	if _, stillSplit := m.active.(*splitModal); !stillSplit {
		t.Fatalf("the alias took the split down, leaving %T", m.active)
	}
	right, isPane := s.right.(*panePopover)
	if !isPane {
		t.Fatalf("the focused half is %T, want the action's pane", s.right)
	}
	if right.kind != PaneKindForAction("dev") {
		t.Errorf("the focused half hosts %q, want %q", right.kind, PaneKindForAction("dev"))
	}
}

// TestABackgroundActionOpensNoPane. The point of marking an action background is that
// it runs without one; claiming a pane for it would be the opposite of what was
// asked. It reaches the handler instead, which is where the job substrate is.
func TestABackgroundActionOpensNoPane(t *testing.T) {
	var handled []ActionRequest
	backend := allKinds()
	m := New(actionItem(), func(r ActionRequest) error { handled = append(handled, r); return nil }).
		WithPaneBackend(backend).
		WithUserActions([]UserAction{{Name: "seed", Command: "make seed", Alias: "s", Background: true}})
	m.width, m.height = 200, 40
	m.itemsAll = actionItem()
	m.keysEnhanced = true

	m, p := enteredPane(t, m)
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(userActionsMenuKey))
	// The last press directly, because the handler runs in the command it returns
	// and pressDeck throws that away.
	next, cmd := m.Update(runeKey("s"))
	m = next.(Model)
	execCmd(t, cmd)
	if m.active != p {
		t.Fatalf("a background action left %T on screen, want the pane it was started from", m.active)
	}
	if len(handled) != 1 || handled[0].Action != ActionCustom || handled[0].Arg != "seed" {
		t.Errorf("the handler saw %v, want one ActionCustom for seed", handled)
	}
}

// TestAnUnknownAliasCancelsRatherThanTyping. A mistyped alias must not fall through
// to the hosted program: a prefix whose second key sometimes lands in an agent is a
// prefix you stop trusting.
func TestAnUnknownAliasCancelsRatherThanTyping(t *testing.T) {
	m, p := enteredPane(t, actionsDeck(t))
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(userActionsMenuKey))
	m = pressDeck(t, m, runeKey("z"))
	if len(p.actions) != 0 {
		t.Error("an unknown alias left the submenu open")
	}
	if m.active != p {
		t.Fatalf("an unknown alias left %T on screen, want the pane", m.active)
	}
	if _, armed := m.armedMenu(); armed {
		t.Error("an unknown alias left a menu on screen")
	}
}

// TestTheSplitStillClosesAHalf. `x` moved out from under the split's close key, which
// is now `q`. The rebind is the one thing about this change an existing user feels.
func TestTheSplitStillClosesAHalf(t *testing.T) {
	m, _ := openedSplit(t, "v")
	got := ansi.Strip(splitPrefixMenu(&m).render(m.width))
	if !strings.Contains(got, "close the focused half") {
		t.Fatalf("the split menu no longer offers closing a half:\n%s", got)
	}
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("q"))
	p, isPane := m.active.(*panePopover)
	if !isPane {
		t.Fatalf("ctrl+b q left %T, want the surviving pane", m.active)
	}
	p.close(&m)
}
