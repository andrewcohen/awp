package deckui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The ctrl+b verb for a shell on the whole screen. `s` beside the agent and `S`
// instead of what is on screen are the same key in its two cases, which is the
// reason the sidebar had to move off `S` (see sidebarKey).

// TestTheMenuOpensAShellOnTheWholeScreen. From a single pane: the pane you were in
// goes, and what replaces it is one whole-screen shell rather than a split.
func TestTheMenuOpensAShellOnTheWholeScreen(t *testing.T) {
	m, was := openedPane(t, allKinds())
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(fullscreenShellKey))

	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("ctrl+b %s left %T, want a pane (status %q)", fullscreenShellKey, m.active, m.status)
	}
	if p == was {
		t.Fatalf("ctrl+b %s left the pane it was pressed in", fullscreenShellKey)
	}
	if p.kind != PaneKindShell {
		t.Errorf("the new pane is kind %q, want the shell's %q", p.kind, PaneKindShell)
	}
	p.close(&m)
}

// TestAShellOnTheWholeScreenTakesBothHalves. From a split it is one arrangement
// replacing another, the way ctrl+b L is — not a third region.
func TestAShellOnTheWholeScreenTakesBothHalves(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(fullscreenShellKey))

	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("ctrl+b %s left %T, want a single pane (status %q)", fullscreenShellKey, m.active, m.status)
	}
	if p.kind != PaneKindShell {
		t.Errorf("the surviving pane is kind %q, want the shell's %q", p.kind, PaneKindShell)
	}
	p.close(&m)
}

// TestBothMenusOfferAShellOnTheWholeScreen. A key you can only find by pressing it
// is not discoverable, and both ctrl+b menus answer this key.
func TestBothMenusOfferAShellOnTheWholeScreen(t *testing.T) {
	m, _ := openedSplit(t, "v")
	for _, tc := range []struct {
		what string
		menu deckMenu
	}{
		{"a pane's", panePrefixMenu(&m)},
		{"a split's", splitPrefixMenu(&m)},
	} {
		if !menuBinds(tc.menu, fullscreenShellKey) {
			t.Errorf("%s menu does not list %s:\n%s", tc.what, fullscreenShellKey,
				ansi.Strip(tc.menu.render(m.width)))
		}
	}
}

// TestTheSidebarKeepsItsOwnKey. The sidebar moved to `A` when the shell took `S`,
// and the two verbs share both menus — so a collision here is silent: one of them
// simply stops answering.
func TestTheSidebarKeepsItsOwnKey(t *testing.T) {
	if sidebarKey == fullscreenShellKey {
		t.Fatalf("the sidebar and the full-screen shell are both %q", sidebarKey)
	}
	if _, taken := splitKindFor(sidebarKey); taken {
		t.Fatalf("%q is a window key as well as the sidebar's", sidebarKey)
	}
	if got := sidebarVerb(true); got[0] != sidebarKey {
		t.Fatalf("the menus list the sidebar under %q, not %q", got[0], sidebarKey)
	}
}
