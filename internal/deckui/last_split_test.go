package deckui

import "testing"

// A split is something you set up — two halves and a divider where you put it —
// so leaving it and coming back has to find it, not one pane. What the deck
// remembers is the arrangement rather than a program.

// TestLeavingASplitAndComingBackFindsIt.
func TestLeavingASplitAndComingBackFindsIt(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, leaveKey())
	if m.active != nil {
		t.Fatalf("the leave key left %T open", m.active)
	}
	m = pressDeck(t, m, resumeKey())
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("coming back gave %T, want the split that was there", m.active)
	}
	t.Cleanup(func() { s.close(&m) })
	if _, isPane := s.left.(*panePopover); !isPane {
		t.Errorf("the left half came back as %T", s.left)
	}
	if _, isPane := s.right.(*panePopover); !isPane {
		t.Errorf("the right half came back as %T", s.right)
	}
}

// TestTheDividerComesBackWhereYouPutIt. A split you widened is a split you
// widened for a reason; snapping back to even on the way in undoes the adjustment
// every time you glance at the row list.
func TestTheDividerComesBackWhereYouPutIt(t *testing.T) {
	m, s := openedSplit(t, "v")
	for range 3 {
		m = pressDeck(t, m, menuKey())
		m = pressDeck(t, m, runeKey(">"))
	}
	moved := s.leftFrac
	if moved == splitEvenFrac || moved == 0 {
		t.Fatalf("the divider did not move: leftFrac is %v", moved)
	}
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, resumeKey())
	back, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("coming back gave %T", m.active)
	}
	t.Cleanup(func() { back.close(&m) })
	if back.leftFrac != moved {
		t.Errorf("the divider came back at %v, want %v", back.leftFrac, moved)
	}
}

// TestClosingAHalfIsRememberedAsOnePane. `x` takes the split apart, so coming
// back must not rebuild it — the arrangement you left is the survivor alone.
func TestClosingAHalfIsRememberedAsOnePane(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("x"))
	if _, isPane := m.active.(*panePopover); !isPane {
		t.Fatalf("closing the focused half left %T, want one pane", m.active)
	}
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, resumeKey())
	if s, isSplit := m.active.(*splitModal); isSplit {
		s.close(&m)
		t.Fatal("coming back rebuilt the split whose half had been closed")
	}
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("coming back gave %T, want the surviving pane", m.active)
	}
	p.close(&m)
}

// TestASplitIsNotAlsoTheAlternate. Splitting the pane you are in is one act, so it
// replaces that pane's memory rather than pushing it into the L slot — otherwise
// the alternate is the pane you can already see half of.
func TestASplitIsNotAlsoTheAlternate(t *testing.T) {
	m := splitDeck(t)
	next, _ := m.trigger(ActionOpenWindow, PaneKindAgent)
	m = next.(Model)
	if _, ok := m.active.(*panePopover); !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	before := m.prevPane
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey("s"))
	s, ok := m.active.(*splitModal)
	if !ok {
		t.Fatalf("the shell key opened %T, want a split (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { s.close(&m) })
	if !m.lastPane.split() {
		t.Error("the split was not remembered as an arrangement")
	}
	if m.prevPane != before {
		t.Errorf("splitting pushed %v into the alternate slot", m.prevPane)
	}
}

// TestEnteringAWorkspaceFindsTheSplitYouLeft. ctrl+\ is not the only way back in
// — enter, and the window keys, are how you get to a workspace from the row list,
// and every one of them used to open a bare pane over the split you had set up.
// Worse than not restoring it: opening the bare pane recorded itself, so the split
// was forgotten by the act of looking at the deck.
func TestEnteringAWorkspaceFindsTheSplitYouLeft(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, leaveKey())
	item, ok := m.selected()
	if !ok {
		t.Fatal("no row is selected on the deck the split was opened from")
	}
	if _, handled := m.openPaneOrArrangement(item, PaneKindAgent); !handled {
		t.Fatal("entering the workspace was refused")
	}
	s, isSplit := m.active.(*splitModal)
	if !isSplit {
		t.Fatalf("entering the workspace gave %T, want the split that was there", m.active)
	}
	t.Cleanup(func() { s.close(&m) })
}

// TestEnteringWithAnotherKeyIsThatKind. A split is an arrangement its left half is
// part of, not a thing the workspace now is: `e` on a workspace whose split was
// agent-beside-vcs means the editor.
func TestEnteringWithAnotherKeyIsThatKind(t *testing.T) {
	m, _ := openedSplit(t, "v")
	m = pressDeck(t, m, leaveKey())
	item, ok := m.selected()
	if !ok {
		t.Fatal("no row is selected on the deck the split was opened from")
	}
	if _, handled := m.openPaneOrArrangement(item, "editor"); !handled {
		t.Fatal("entering the workspace as the editor was refused")
	}
	if s, isSplit := m.active.(*splitModal); isSplit {
		s.close(&m)
		t.Fatal("the editor key rebuilt the agent's split")
	}
	p, isPane := m.active.(*panePopover)
	if !isPane {
		t.Fatalf("the editor key gave %T, want one pane", m.active)
	}
	p.close(&m)
}

// TestAHalfThatClosesItselfIsRememberedAsOnePane. ctrl+space x is not the only way a
// split comes apart — the diff viewer quits with its own key, and a pane's program
// exits on its own — and all of those leave one pane on screen. Recorded where
// every collapse goes through rather than at the one key that had it, or a split
// taken apart any other way comes back rebuilt.
func TestAHalfThatClosesItselfIsRememberedAsOnePane(t *testing.T) {
	m, s := openedSplit(t, "v")
	// The right half closing itself, which is what its own quit key amounts to
	// from the split's side: m.active no longer the child it was handed to.
	right, isPane := s.right.(*panePopover)
	if !isPane {
		t.Fatalf("the right half is %T, want a pane", s.right)
	}
	m.active = right
	right.close(&m)
	s.collapse(&m, right)
	if _, stillSplit := m.active.(*splitModal); stillSplit {
		t.Fatal("the split survived a half closing itself")
	}
	m = pressDeck(t, m, leaveKey())
	m = pressDeck(t, m, resumeKey())
	if back, isSplit := m.active.(*splitModal); isSplit {
		back.close(&m)
		t.Fatal("coming back rebuilt the split whose half had closed itself")
	}
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("coming back gave %T, want the surviving pane", m.active)
	}
	p.close(&m)
}
