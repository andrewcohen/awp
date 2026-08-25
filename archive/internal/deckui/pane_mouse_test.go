package deckui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestAMouseEventArrivesInTheProgramsOwnGrid is the bug: the event was
// forwarded in the deck's coordinates, so a drag highlighted two rows below the
// pointer — the program was told the pointer was there and obliged.
//
// Two rows and one column, because paneBox makes the popover exactly the
// terminal's size, leaving the chrome inset as the whole of the error.
func TestAMouseEventArrivesInTheProgramsOwnGrid(t *testing.T) {
	const deckW, deckH = 100, 30
	got, ok := paneMouse(tea.MouseClickMsg{X: 10, Y: 5}, box{w: deckW, h: deckH})
	if !ok {
		t.Fatal("a click inside the terminal was dropped")
	}
	m := got.Mouse()
	if m.X != 10-paneInsetX || m.Y != 5-paneInsetY {
		t.Errorf("click landed at (%d,%d), want (%d,%d)", m.X, m.Y, 10-paneInsetX, 5-paneInsetY)
	}
}

// TestTheTranslationIsScreenCursorBackwards pins the two directions against
// each other. They are the same arithmetic, and the failure when they drift is
// silent: a cursor drawn where the program did not put it, or a click delivered
// to a cell the user did not point at.
func TestTheTranslationIsScreenCursorBackwards(t *testing.T) {
	const deckW, deckH = 100, 30
	w, h := paneDims(deckW, deckH)
	for _, tc := range []struct{ x, y int }{{0, 0}, {1, 1}, {w - 1, h - 1}, {w / 2, h / 2}} {
		// Where the program's cell (x,y) lands on screen.
		screenX, screenY := screenCursorFor(deckW, deckH, tc.x, tc.y)
		// And back again.
		got, ok := paneMouse(tea.MouseClickMsg{X: screenX, Y: screenY}, box{w: deckW, h: deckH})
		if !ok {
			t.Fatalf("cell (%d,%d) mapped to screen (%d,%d), which translated back to nothing", tc.x, tc.y, screenX, screenY)
		}
		if m := got.Mouse(); m.X != tc.x || m.Y != tc.y {
			t.Errorf("cell (%d,%d) round-tripped to (%d,%d)", tc.x, tc.y, m.X, m.Y)
		}
	}
}

// screenCursorFor is screenCursor's arithmetic without needing a live terminal
// to ask for a cursor position. It deliberately re-derives from the same
// helpers, so if those change both this and screenCursor move together.
func screenCursorFor(deckW, deckH, cx, cy int) (int, int) {
	w, h := paneDims(deckW, deckH)
	boxW, boxH := paneBox(w, h)
	originX, originY := (deckW-boxW)/2, (deckH-boxH)/2
	return originX + paneInsetX + cx, originY + paneInsetY + cy
}

// TestChromeClicksAreNotTheProgramsBusiness: the border is awp's cells.
// Forwarding one would send the program a negative coordinate, which is not a
// position it can do anything sensible with. (The header row used to be awp's
// too; it is the deck's own bar now, above the box entirely — and a click on it
// never reaches paneMouse, because the box it is handed starts below it.)
func TestChromeClicksAreNotTheProgramsBusiness(t *testing.T) {
	const deckW, deckH = 100, 30
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"top border", 5, 0},
		{"left border", 0, 5},
		{"past the right edge", deckW - 1, 5},
		{"past the bottom edge", 5, deckH - 1},
	} {
		if _, ok := paneMouse(tea.MouseClickMsg{X: tc.x, Y: tc.y}, box{w: deckW, h: deckH}); ok {
			t.Errorf("%s at (%d,%d) was forwarded to the program", tc.name, tc.x, tc.y)
		}
	}
}

// TestTheEventKindSurvives: each concrete message is a defined type over the
// same struct, so the kind has to be carried across by hand. A click that
// arrives as motion is a click the program never sees.
func TestTheEventKindSurvives(t *testing.T) {
	const deckW, deckH = 100, 30
	at := tea.Mouse{X: 10, Y: 5}
	for _, tc := range []struct {
		name string
		in   tea.MouseMsg
		want any
	}{
		{"click", tea.MouseClickMsg(at), tea.MouseClickMsg{}},
		{"release", tea.MouseReleaseMsg(at), tea.MouseReleaseMsg{}},
		{"wheel", tea.MouseWheelMsg(at), tea.MouseWheelMsg{}},
		{"motion", tea.MouseMotionMsg(at), tea.MouseMotionMsg{}},
	} {
		got, ok := paneMouse(tc.in, box{w: deckW, h: deckH})
		if !ok {
			t.Fatalf("%s was dropped", tc.name)
		}
		if gotT, wantT := typeName(got), typeName(tc.want); gotT != wantT {
			t.Errorf("%s arrived as %s, want %s", tc.name, gotT, wantT)
		}
	}
}

func typeName(v any) string {
	switch v.(type) {
	case tea.MouseClickMsg:
		return "click"
	case tea.MouseReleaseMsg:
		return "release"
	case tea.MouseWheelMsg:
		return "wheel"
	case tea.MouseMotionMsg:
		return "motion"
	}
	return "unknown"
}
