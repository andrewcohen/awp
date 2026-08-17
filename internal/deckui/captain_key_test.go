package deckui

import (
	"strings"
	"testing"
)

// The captain: `a` from the row list, and the pane menu's `a` from inside a pane.
//
// What these are about is that it is reachable from both, and that it is not a
// row's pane — it opens with the cursor on nothing in particular, and it carries no
// workspace with it. The captain's own behaviour, once open, is its agent's.

// captainDeck is a deck with a pane backend that can host the captain.
func captainDeck(t *testing.T) Model {
	t.Helper()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 200, 40
	m.itemsAll = []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}}
	return m
}

// captainPane is the captain's pane if it is up.
//
// Read from m.captain rather than from m.active: the captain floats over whatever
// the deck is showing, so what is in active is whatever it was opened over.
func captainPane(t *testing.T, m *Model) *panePopover {
	t.Helper()
	p := m.captain
	if p == nil {
		t.Fatalf("the captain is not up; active is %T (status %q)", m.active, m.status)
	}
	if p.kind != PaneKindCaptain {
		t.Fatalf("the captain's pane is %q", p.kind)
	}
	return p
}

func TestAOpensTheCaptain(t *testing.T) {
	m := captainDeck(t)
	m = pressDeck(t, m, runeKey(captainKey))
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })
}

// It is not the selected row's pane, so it opens from a deck with no rows at all.
// A captain that needed a workspace selected would be unreachable in exactly the
// situation you most want to ask it something — an empty scope, or a filter that
// matched nothing.
func TestTheCaptainOpensWithNoRowSelected(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil }).WithPaneBackend(allKinds())
	m.width, m.height = 200, 40

	m = pressDeck(t, m, runeKey(captainKey))
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })
}

// The captain carries no workspace. If it borrowed the selected row's identity its
// session would collide with that workspace's, and closing one would take the
// other's process with it.
func TestTheCaptainBorrowsNoWorkspace(t *testing.T) {
	m := captainDeck(t)
	m = pressDeck(t, m, runeKey(captainKey))
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })

	if p.workspace == "ws" || p.project == "proj" {
		t.Errorf("the captain opened as %s/%s — it took the selected row's identity", p.project, p.workspace)
	}
	if p.project != CaptainProject || p.workspace != CaptainWorkspace {
		t.Errorf("the captain opened as %s/%s, want %s/%s", p.project, p.workspace, CaptainProject, CaptainWorkspace)
	}
}

// From inside a pane, the menu is the only door: `a` on its own is the hosted
// program's key. This is the case the feature exists for, since a pane is where you
// spend most of your time.
func TestThePaneMenuReachesTheCaptain(t *testing.T) {
	m := splitDeck(t)
	m = pressDeck(t, m, agentKey())
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("precondition: enter opened %T (status %q)", m.active, m.status)
	}

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(captainKey))

	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })
}

// And the pane it was opened over stays exactly where it was.
//
// This is what the captain being a modal buys: you ask awp a question about the
// agent you are watching without taking the agent's screen down, and dismissing the
// question puts you back in it rather than back at the row list. Closing it used to
// be the contract — TestOpeningTheCaptainClosesThePaneItReplaced — and coming back
// to the wrong place is what that cost. See #385.
func TestTheCaptainLeavesThePaneItOpenedOver(t *testing.T) {
	m := splitDeck(t)
	m = pressDeck(t, m, agentKey())
	under := m.active.(*panePopover)
	term, ok := under.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the pane's terminal is %T, want the fake", under.term)
	}

	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(captainKey))

	p := captainPane(t, &m)
	if term.closed {
		t.Error("the pane the captain floated over had its process closed")
	}
	if m.active != under {
		t.Errorf("the pane behind the captain is %T, want the one that was there", m.active)
	}

	// And dismissing the captain gives it the keys back, rather than dropping to the
	// row list.
	p.close(&m)
	if m.captain != nil {
		t.Error("the captain is still up after its leave key")
	}
	if m.active != under {
		t.Errorf("dismissing the captain left %T on screen, want the pane it floated over", m.active)
	}
	under.close(&m)
}

// The menu says so. A verb that works but is not listed is one you find by
// accident, which for the captain's only door from a pane is the whole problem.
func TestBothPaneMenusListTheCaptain(t *testing.T) {
	m := captainDeck(t)
	for name, menu := range map[string]deckMenu{
		"the pane menu":  panePrefixMenu(&m),
		"the split menu": splitPrefixMenu(&m),
	} {
		var found bool
		for _, v := range menu.verbs {
			if v[0] == captainKey {
				found = true
				if !strings.Contains(strings.ToLower(v[1]), "captain") {
					t.Errorf("%s lists %q as %q, which does not name the captain", name, v[0], v[1])
				}
			}
		}
		if !found {
			t.Errorf("%s has no %q verb", name, captainKey)
		}
	}
}

// A deck with no pane backend says why rather than doing nothing. There is no tmux
// fallback for the captain the way there is for a review window, so the refusal is
// the entire answer and it has to be visible.
func TestTheCaptainSaysSoWithNowhereToRun(t *testing.T) {
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(ActionRequest) error { return nil })
	m.width, m.height = 200, 40

	m = pressDeck(t, m, runeKey(captainKey))

	if m.active != nil {
		t.Fatalf("`a` opened %T with no pane backend", m.active)
	}
	if !strings.Contains(strings.ToLower(m.status), "captain") {
		t.Errorf("the refusal does not name the captain: %q", m.status)
	}
}

// The `?` overlay carries it, in both spellings. The menu only appears once you
// have pressed the key, so the overlay is the only place to learn the captain
// exists without being told.
func TestTheHelpOverlayNamesTheCaptain(t *testing.T) {
	var rowKey, menuRow bool
	for _, g := range deckKeyGroups() {
		for _, k := range g.Keys {
			if !strings.Contains(strings.ToLower(k[1]), "captain") {
				continue
			}
			switch k[0] {
			case captainKey:
				rowKey = true
			case PaneMenuKey + " " + captainKey:
				menuRow = true
			}
		}
	}
	if !rowKey {
		t.Errorf("the ? overlay never says %q opens the captain", captainKey)
	}
	if !menuRow {
		t.Errorf("the ? overlay never says %s %s opens the captain", PaneMenuKey, captainKey)
	}
}
