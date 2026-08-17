package deckui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Dragging the divider, in the coordinates the pointer actually arrives in.
//
// A mouse event carries a column of the screen. The divider lives at a column of
// childBox, and those two agreed for as long as childBox started at zero — which
// it did until the sidebar took columns off its left (#333). Then the grab band
// sat sidebarDefaultWidth columns left of the visible divider, so grabbing the divider
// did nothing and clicking well inside the left half started a drag.

// dragDeck is a split whose box has been moved off the screen's left edge, which
// is the case the arithmetic has to survive.
func dragDeck(t *testing.T, sidebar bool) (Model, *splitModal) {
	t.Helper()
	m, s := openedSplit(t, "v")
	m.sidebar = sidebar
	if sidebar && !m.showsSidebar() {
		t.Fatal("the deck is too narrow to put a sidebar beside a split")
	}
	return m, s
}

// TestTheGrabBandIsAtTheDivider, wherever the split's box starts. The band is
// stated in screen columns because that is what a click carries, so with the strip
// up it has to have moved right with the divider.
func TestTheGrabBandIsAtTheDivider(t *testing.T) {
	for _, sidebar := range []bool{false, true} {
		name := "no sidebar"
		if sidebar {
			name = "sidebar"
		}
		t.Run(name, func(t *testing.T) {
			m, s := dragDeck(t, sidebar)
			b := m.childBox()
			divider := b.x + s.splitCol(b)
			if !s.dragDivider(&m, clickAt(divider)) {
				t.Errorf("a click on the divider at column %d did not grab it", divider)
			}
			s.dragging = false
			// And a column well inside the left half is not the divider. With the
			// band misplaced by the strip's width this was exactly what it caught.
			if inside := b.x + 4; s.dragDivider(&m, clickAt(inside)) {
				t.Errorf("a click at column %d, inside the left half, grabbed the divider", inside)
			}
		})
	}
}

// TestDraggingTheDividerUsesTheBoxNotTheTerminal. The fraction is of the split's
// own width, so with the strip up a pointer halfway across the split must read as
// half — measured against the terminal it reads as less, and the divider jumps
// away from the pointer the moment the drag starts.
func TestDraggingTheDividerUsesTheBoxNotTheTerminal(t *testing.T) {
	m, s := dragDeck(t, true)
	b := m.childBox()
	s.dragging = true
	mid := b.x + b.w/2
	if !s.dragDivider(&m, motionAt(mid)) {
		t.Fatal("a motion during a drag was not consumed")
	}
	// Halfway across the box, so the divider belongs at the box's midpoint.
	if got, want := s.splitCol(b), b.w/2; abs(got-want) > 1 {
		t.Errorf("dragging to column %d put the divider at %d of %d, want about %d",
			mid, got, b.w, want)
	}
}

func clickAt(x int) tea.MouseMsg {
	return tea.MouseClickMsg{X: x, Y: 5, Button: tea.MouseLeft}
}

func motionAt(x int) tea.MouseMsg {
	return tea.MouseMotionMsg{X: x, Y: 5, Button: tea.MouseLeft}
}

func releaseAt(x int) tea.MouseMsg {
	return tea.MouseReleaseMsg{X: x, Y: 5, Button: tea.MouseLeft}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
