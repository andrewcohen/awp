package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A child fills the box it was given.
//
// #339, finally: the popover branch of view() centres what a modal renders inside
// childBox. For a pane that is a no-op, because a pane renders exactly its box —
// but a split with the diff viewer in it came back narrower than the box, and
// centring a narrow block pads it on the left. Both halves then sit one column
// right of where boxOf says they are, so the pane's cursor was drawn one column
// left of its text. Off by one, only in a split, essentially always, and invisible
// to every test that measured a pane against its own view rather than against the
// composed frame.
//
// The rule these pin is that the centring has nothing to centre: a child renders
// its box exactly, so what reaches lipgloss.Place is already the full width.

// TestASplitFillsItsBox to the column. A narrow block is what the centring turns
// into an offset, so this is the check that there is nothing to shift.
func TestASplitFillsItsBox(t *testing.T) {
	// Both shapes: two ptys, and a pty beside one of awp's own body modals. The
	// second is the one that was short, and it was short because the body modal
	// reserved padding the panel had stopped adding — so a new half that gets its
	// width wrong fails here rather than in a cursor a column off.
	for _, tc := range []struct {
		name string
		open func(*testing.T) (Model, *splitModal)
	}{
		{"two panes", func(t *testing.T) (Model, *splitModal) { return openedSplit(t, "v") }},
		{"a pane and the diff", splitWithDiff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, s := tc.open(t)
			b := m.childBox()
			for i, row := range strings.Split(s.renderPopover(&m, b), "\n") {
				if got := lipgloss.Width(row); got != b.w {
					t.Fatalf("row %d of the split is %d columns wide, want the box's %d", i, got, b.w)
				}
			}
		})
	}
}

// TestThePaneCursorLandsOnItsOwnText is the symptom, stated against the composed
// frame rather than the pane's view — which is where it hid. The cursor's column
// has to be the column the pane's content actually starts at plus wherever the
// program put it, and the frame is the only place that can be checked.
func TestThePaneCursorLandsOnItsOwnText(t *testing.T) {
	m, s := splitWithDiff(t)
	p, ok := s.left.(*panePopover)
	if !ok {
		t.Fatalf("the left half is a %T", s.left)
	}
	f, ok := p.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", p.term)
	}
	// A marker at the cursor, so where the cursor belongs is a thing in the frame
	// rather than a number to re-derive.
	f.setView("Z")
	f.moveCursor(1, 0)

	x, y, ok := p.screenCursor(m.boxOf(p))
	if !ok {
		t.Fatal("no cursor for a visible pane")
	}
	rows := strings.Split(m.render(), "\n")
	if y < 0 || y >= len(rows) {
		t.Fatalf("the cursor is on row %d of a %d-row frame", y, len(rows))
	}
	row := []rune(ansi.Strip(rows[y]))
	marker := -1
	for i, r := range row {
		if r == 'Z' {
			marker = i
			break
		}
	}
	if marker < 0 {
		t.Fatalf("the marker is not in the frame's row %d: %q", y, string(row))
	}
	// The program parked its cursor one cell past the marker, so that is where the
	// deck has to draw it.
	if x != marker+1 {
		t.Errorf("the marker is at column %d so the cursor belongs at %d; the deck drew it at %d\nrow: %q",
			marker, marker+1, x, string(row))
	}
}
