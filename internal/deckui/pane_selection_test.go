package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Selecting text in a pane with the mouse.
//
// The pane owns which cells are selected, because it is the only thing that knows
// where its panes are — the host terminal's own selection takes screen rows and
// hands back both halves of a split interleaved, which is why this exists at all.
// What text is in those cells is the emulator's answer (vterm.SelectionText), so
// these tests are about the geometry, the gate, and the highlight.

// selectPane opens a pane whose program has not asked for the mouse, with a known
// screen on it.
func selectPane(t *testing.T, screen string) (Model, *panePopover, *fakeTerm) {
	t.Helper()
	m, p := sidebarPane(t)
	m.sidebar = false // the strip shifts the box; these tests are about the pane
	f, ok := p.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", p.term)
	}
	f.setView(screen)
	return m, p, f
}

// drag presses at one cell of the pane, moves to another, and releases.
func drag(m *Model, p *panePopover, x0, y0, x1, y1 int) tea.Cmd {
	b := m.boxOf(p)
	sx, sy := b.x+paneInsetX, b.y+paneInsetY
	p.update(m, tea.MouseClickMsg{X: sx + x0, Y: sy + y0, Button: tea.MouseLeft})
	p.update(m, tea.MouseMotionMsg{X: sx + x1, Y: sy + y1, Button: tea.MouseLeft})
	return p.update(m, tea.MouseReleaseMsg{X: sx + x1, Y: sy + y1, Button: tea.MouseLeft})
}

// TestADragSelectsAndCopiesOnRelease. No key: the terminals awp runs inside copy
// on release, and a selection you have to confirm is one you have to learn about.
func TestADragSelectsAndCopiesOnRelease(t *testing.T) {
	m, p, _ := selectPane(t, "hello world")
	cmd := drag(&m, p, 0, 0, 4, 0)
	if cmd == nil {
		t.Fatal("releasing a drag returned no command, so nothing reached the clipboard")
	}
	if !p.sel.active {
		t.Error("the selection is not active after a drag")
	}
	if !strings.Contains(m.status, "copied") {
		t.Errorf("the status bar does not say anything was copied: %q", m.status)
	}
}

// TestAClickWithNoDragSelectsNothing, and clears what was selected. It is the only
// gesture that can mean "never mind", so it has to.
func TestAClickWithNoDragSelectsNothing(t *testing.T) {
	m, p, _ := selectPane(t, "hello world")
	drag(&m, p, 0, 0, 4, 0)
	if !p.sel.active {
		t.Fatal("the drag did not select")
	}

	b := m.boxOf(p)
	sx, sy := b.x+paneInsetX, b.y+paneInsetY
	p.update(&m, tea.MouseClickMsg{X: sx + 2, Y: sy, Button: tea.MouseLeft})
	if p.sel.active {
		t.Error("a click did not clear the selection")
	}
	cmd := p.update(&m, tea.MouseReleaseMsg{X: sx + 2, Y: sy, Button: tea.MouseLeft})
	if cmd != nil {
		t.Error("releasing a click with no drag copied something")
	}
}

// TestTypingClearsTheSelection. The program is about to redraw the screen, and a
// highlight left behind marks whatever text moves under it.
func TestTypingClearsTheSelection(t *testing.T) {
	m, p, _ := selectPane(t, "hello world")
	drag(&m, p, 0, 0, 4, 0)
	p.update(&m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if p.sel.active {
		t.Error("typing left the selection highlighted")
	}
}

// TestAProgramThatWantedTheMouseKeepsIt. An agent, nvim, jjui: a drag is one of the
// things they asked for, and awp turning it into a selection would break the one
// they implement themselves.
func TestAProgramThatWantedTheMouseKeepsIt(t *testing.T) {
	m, p, f := selectPane(t, "hello world")
	f.askForMouse()
	if p.paneSelects() {
		t.Fatal("a pane whose program wants the mouse is selecting instead")
	}
	drag(&m, p, 0, 0, 4, 0)
	if p.sel.active {
		t.Error("the drag became a selection")
	}
	if len(f.miceSeen()) == 0 {
		t.Error("the drag did not reach the program either")
	}
}

// TestTheSelectionIsHighlightedInTheFrame. Painted over the program's own screen,
// because the selection is not the program's — awp made it out of cells the program
// had already drawn.
func TestTheSelectionIsHighlightedInTheFrame(t *testing.T) {
	m, p, _ := selectPane(t, "hello world")
	plain := p.renderPopover(&m, m.boxOf(p))
	drag(&m, p, 0, 0, 4, 0)
	tinted := p.renderPopover(&m, m.boxOf(p))

	if plain == tinted {
		t.Fatal("the frame is unchanged by a selection")
	}
	// The text survives being highlighted — checking what you selected is the whole
	// point of showing it.
	if !strings.Contains(ansi.Strip(tinted), "hello world") {
		t.Errorf("the highlighted row lost its text:\n%s", ansi.Strip(tinted))
	}
	// And the row is still the width it was: a tint must not add or eat columns.
	if got, want := lineWidths(tinted), lineWidths(plain); !equalInts(got, want) {
		t.Errorf("highlighting changed the row widths: %v, want %v", got, want)
	}
}

// TestASelectionSpansRowsLinearly, not as a block: the first row runs to the end of
// the screen and the last from its start, which is what makes a multi-row drag read
// as text.
func TestASelectionSpansRowsLinearly(t *testing.T) {
	m, p, _ := selectPane(t, "one\ntwo\nthree")
	p.sel = paneSelection{anchorX: 1, anchorY: 0, cursorX: 2, cursorY: 2, active: true}

	w, _ := paneDims(m.boxOf(p).w, m.boxOf(p).h)
	rows := p.selectionRows(w)
	if len(rows) != 3 {
		t.Fatalf("a drag over three rows selected %d of them: %v", len(rows), rows)
	}
	if got := rows[0]; got != [2]int{1, w - 1} {
		t.Errorf("the first row is %v, want from column 1 to the end", got)
	}
	if got := rows[1]; got != [2]int{0, w - 1} {
		t.Errorf("the middle row is %v, want the whole width", got)
	}
	if got := rows[2]; got != [2]int{0, 2} {
		t.Errorf("the last row is %v, want from the start to column 2", got)
	}
}

func lineWidths(s string) []int {
	lines := strings.Split(s, "\n")
	out := make([]int, len(lines))
	for i, l := range lines {
		out[i] = ansi.StringWidth(l)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
