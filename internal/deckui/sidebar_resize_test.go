package deckui

import (
	"errors"
	"strings"
	"testing"
)

// Dragging the strip's edge.
//
// Two things have to hold and they pull in opposite directions: the number you
// dragged to is remembered exactly, and the number the strip is drawn at is
// whatever this terminal can spare. Collapsing those into one — clamping on the way
// into storage, or storing what the clamp produced — is how a width chosen on a big
// screen gets quietly rewritten by one session on a laptop.

// TestGrabbingTheEdgeStartsADrag, at the column the edge is drawn in.
func TestGrabbingTheEdgeStartsADrag(t *testing.T) {
	m, _ := sidebarPane(t)
	edge := m.sidebarEdgeCol()
	if _, ok := m.sidebarMouse(clickAt(edge)); !ok {
		t.Fatalf("a click on the edge at column %d did not grab it", edge)
	}
	if !m.sidebarDragging {
		t.Error("the click was consumed but no drag started")
	}
}

// TestAClickInsideTheStripIsNotTheEdge. The band is a couple of columns wide; a
// press on a row is not a resize. The click is still consumed — the strip's columns
// are the strip's — but what it must not do is take hold of the border, which would
// leave every row unclickable.
func TestAClickInsideTheStripIsNotTheEdge(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarMouse(clickAt(m.sidebarEdgeCol() - 4))
	if m.sidebarDragging {
		t.Error("a click inside the strip started a drag")
	}
}

// TestDraggingMovesTheEdgeToThePointer. The column under the pointer becomes the
// strip's last, so the edge ends up where your hand is rather than one column off.
func TestDraggingMovesTheEdgeToThePointer(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarDragging = true
	const to = 60
	if _, ok := m.sidebarMouse(motionAt(to)); !ok {
		t.Fatal("a motion during a drag was not consumed")
	}
	if got := m.sidebarEdgeCol(); got != to {
		t.Errorf("dragging to column %d left the edge at %d", to, got)
	}
	// And the child gets the columns the strip stopped taking, through the one box
	// every path derives from.
	if b := m.childBox(); b.x != to+1 {
		t.Errorf("the child starts at column %d, want %d", b.x, to+1)
	}
}

// TestAMotionWithNoDragIsNotOurs. The pointer crosses the strip on its way
// somewhere; consuming that would eat events the child is owed.
func TestAMotionWithNoDragIsNotOurs(t *testing.T) {
	m, _ := sidebarPane(t)
	if _, ok := m.sidebarMouse(motionAt(10)); ok {
		t.Error("a motion with no drag under way was consumed")
	}
}

// TestTheDragEndsAndTheWidthStays.
func TestTheDragEndsAndTheWidthStays(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarDragging = true
	m.sidebarMouse(motionAt(50))
	if _, ok := m.sidebarMouse(releaseAt(50)); !ok {
		t.Error("the release that ended the drag was not consumed")
	}
	if m.sidebarDragging {
		t.Error("the drag survived the release")
	}
	if got := m.sidebarWidth(); got != 51 {
		t.Errorf("the strip is %d columns after the drag, want 51", got)
	}
	// A release with no drag under way belongs to whatever it landed on.
	if _, ok := m.sidebarMouse(releaseAt(50)); ok {
		t.Error("a release with no drag was consumed")
	}
}

// TestTheDragStopsAtTheFloor. Narrower than sidebarMinWidth the strip is a column
// of truncation, so a hand that keeps going stops rather than collapsing it into
// something you would then have to drag back out of nothing.
func TestTheDragStopsAtTheFloor(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarDragging = true
	m.sidebarMouse(motionAt(0))
	if got := m.sidebarWidth(); got != sidebarMinWidth {
		t.Errorf("dragging to the left edge left the strip %d columns, want the floor of %d",
			got, sidebarMinWidth)
	}
}

// TestTheDragLeavesRoomForAPane. The other wall: the strip may not squeeze the
// program beside it below what a pane needs, because that program is the thing you
// are working in and the strip is what you glanced at.
func TestTheDragLeavesRoomForAPane(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarDragging = true
	m.sidebarMouse(motionAt(m.width - 1))
	if got, want := m.sidebarWidth(), m.width-sidebarChildMinW; got != want {
		t.Errorf("dragging to the right edge left the strip %d columns, want %d", got, want)
	}
	if b := m.childBox(); b.w < sidebarChildMinW {
		t.Errorf("the child is %d columns, below the %d a pane needs", b.w, sidebarChildMinW)
	}
}

// TestTheEdgeIsOnlyGrabbableWhenTheStripIsUp. With no strip the column its edge
// would be in belongs to the child, and a press there has to reach it.
func TestTheEdgeIsOnlyGrabbableWhenTheStripIsUp(t *testing.T) {
	m := sidebarDeck(t)
	if _, ok := m.sidebarMouse(clickAt(sidebarDefaultWidth - 1)); ok {
		t.Error("the edge was grabbable with no strip on screen")
	}
}

// TestADragRecordsTheWidth, once per column it settles on. A drag is a stream of
// motion events and most land where the last one did; a write per event would
// rewrite the preferences file dozens of times per gesture.
func TestADragRecordsTheWidth(t *testing.T) {
	m, _ := sidebarPane(t)
	var saved []int
	m = m.WithSidebarWidthSaver(func(cols int) error {
		saved = append(saved, cols)
		return nil
	})
	m.sidebarDragging = true
	m.sidebarMouse(motionAt(49))
	m.sidebarMouse(motionAt(49)) // the same column again
	m.sidebarMouse(motionAt(59))
	want := []int{50, 60}
	if len(saved) != len(want) || saved[0] != want[0] || saved[1] != want[1] {
		t.Errorf("the drag recorded %v, want %v", saved, want)
	}
}

// TestTheRememberedWidthIsTheOneYouChose, not the one a narrow terminal could show.
//
// This is the case the two clamps exist to keep apart. A strip dragged wide on a big
// screen, opened on a small one, must come back narrow *and* still be remembered
// wide — otherwise one session on a laptop silently rewrites the preference.
func TestTheRememberedWidthIsTheOneYouChose(t *testing.T) {
	m, _ := sidebarPane(t)
	m = m.WithSidebarWidth(120)
	m.width = 100
	if got := m.sidebarWidth(); got != m.width-sidebarChildMinW {
		t.Errorf("on a %d-column terminal the strip is %d, want the clamp at %d",
			m.width, got, m.width-sidebarChildMinW)
	}
	if m.sidebarW != 120 {
		t.Errorf("the terminal rewrote the remembered width to %d", m.sidebarW)
	}
}

// TestNoRememberedWidthIsTheDefault — every build before this one wrote no such
// preference, and a deck that opened at a zero-width strip would be unusable.
func TestNoRememberedWidthIsTheDefault(t *testing.T) {
	m, _ := sidebarPane(t)
	if got := m.sidebarWidth(); got != sidebarDefaultWidth {
		t.Errorf("a deck told nothing opens the strip at %d, want the default %d",
			got, sidebarDefaultWidth)
	}
}

// TestASidebarWidthSaveThatFailsSaysSo, and leaves the strip where you dragged it —
// the same bargain the sidebar's own saver makes. What was lost is only that it will
// not be remembered.
func TestASidebarWidthSaveThatFailsSaysSo(t *testing.T) {
	m, _ := sidebarPane(t)
	m = m.WithSidebarWidthSaver(func(int) error { return errors.New("disk is full") })
	m.sidebarDragging = true
	m.sidebarMouse(motionAt(49))
	if got := m.sidebarWidth(); got != 50 {
		t.Errorf("a failed save undid the drag: the strip is %d columns", got)
	}
	if !strings.Contains(m.status, "disk is full") {
		t.Errorf("the failure is not on the status bar: %q", m.status)
	}
}

// TestASidebarWidthSaverIsOptional — the mini-deck and every test deck have none.
func TestASidebarWidthSaverIsOptional(t *testing.T) {
	m, _ := sidebarPane(t)
	m.sidebarDragging = true
	m.sidebarMouse(motionAt(49)) // must not panic
	if got := m.sidebarWidth(); got != 50 {
		t.Errorf("the drag did nothing: the strip is %d columns", got)
	}
}
