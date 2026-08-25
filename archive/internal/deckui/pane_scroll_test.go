package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Scrolling a pane's history with the wheel.
//
// What a terminal does with the rows above its viewport is the emulator's half,
// tested against a real one in internal/vterm/scroll_test.go. This is the deck's
// half: which events become a scroll, what puts the view back on the tail, and
// the row that says the pane is not live.

// wheel sends a notch over the pane's own cells, after a frame — the mouse
// translates against the box the pane was drawn in.
func wheel(m *Model, p *panePopover, button tea.MouseButton) {
	m.render()
	b := m.boxOf(p)
	p.update(m, tea.MouseWheelMsg{
		X: b.x + paneInsetX + 2, Y: b.y + paneInsetY + 2, Button: button,
	})
}

// longScreen is more lines than the pane can show, so there is a history to be
// above.
func longScreen(rows int) string {
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = "line" + string(rune('a'+i%26))
	}
	return strings.Join(lines, "\n")
}

// TestTheWheelScrollsAPaneBack. The bug this closes is not that the wheel did
// nothing — it is that a shell pane had no way at all to show what had scrolled
// off, so output past the window was gone as far as awp was concerned.
func TestTheWheelScrollsAPaneBack(t *testing.T) {
	m, p, f := selectPane(t, longScreen(200))
	tail := f.View()

	wheel(&m, p, tea.MouseWheelUp)
	if back := f.View(); back == tail {
		t.Error("a wheel-up left the pane on the same rows")
	}
	if above, behind := p.paneBehind(); !behind || above <= 0 {
		t.Errorf("after scrolling up the pane reports above=%d behind=%v", above, behind)
	}
}

// TestTheWheelComesBackDown, and the tail is exactly where it was — a round trip
// that lands one row off is worse than one that does not move.
func TestTheWheelComesBackDown(t *testing.T) {
	m, p, f := selectPane(t, longScreen(200))
	tail := f.View()

	wheel(&m, p, tea.MouseWheelUp)
	wheel(&m, p, tea.MouseWheelDown)
	if got := f.View(); got != tail {
		t.Errorf("scrolling up and back down did not return to the tail:\ngot\n%s\nwant\n%s", got, tail)
	}
	if _, behind := p.paneBehind(); behind {
		t.Error("back at the tail, the pane still reports itself behind")
	}
}

// TestTypingReturnsToTheTail. The program answers on the bottom row, so reading
// history is over the moment you type — a pane that stayed put would drop the
// response into rows above the view.
func TestTypingReturnsToTheTail(t *testing.T) {
	m, p, f := selectPane(t, longScreen(200))
	tail := f.View()

	wheel(&m, p, tea.MouseWheelUp)
	p.update(&m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := f.View(); got != tail {
		t.Error("typing left the pane up in its history")
	}
}

// TestScrollingClearsTheSelection. The highlight is a set of rows of the view, and
// the view has just moved out from under it — keeping it marks whatever text
// scrolled into those rows.
func TestScrollingClearsTheSelection(t *testing.T) {
	m, p, _ := selectPane(t, longScreen(200))
	drag(&m, p, 0, 0, 4, 0)
	if !p.sel.active {
		t.Fatal("the drag did not select")
	}
	wheel(&m, p, tea.MouseWheelUp)
	if p.sel.active {
		t.Error("scrolling left the selection highlighted over different text")
	}
}

// TestAProgramThatWantedTheMouseKeepsTheWheel. An agent, nvim, a pager: each has
// its own idea of what scrolling means, and awp scrolling the emulator underneath
// would move the whole screen the program is drawing on.
func TestAProgramThatWantedTheMouseKeepsTheWheel(t *testing.T) {
	m, p, f := selectPane(t, longScreen(200))
	f.askForMouse()
	tail := f.View()

	wheel(&m, p, tea.MouseWheelUp)
	if got := f.View(); got != tail {
		t.Error("awp scrolled a pane whose program wanted the mouse itself")
	}
	if len(f.miceSeen()) == 0 {
		t.Error("the wheel did not reach the program either")
	}
}

// TestTheTopRowSaysThePaneIsBehind. A scrolled-back pane is indistinguishable from
// a pane whose program has stopped printing — same border, same frame, output
// apparently frozen — so the row is the only thing that can say which it is.
func TestTheTopRowSaysThePaneIsBehind(t *testing.T) {
	m, p, _ := selectPane(t, longScreen(200))
	if strings.Contains(ansi.Strip(m.renderTopRow(m.width)), scrollbackGlyph) {
		t.Fatal("a pane on its live tail is reported as behind")
	}

	wheel(&m, p, tea.MouseWheelUp)
	bar := ansi.Strip(m.renderTopRow(m.width))
	if !strings.Contains(bar, scrollbackGlyph) {
		t.Errorf("the row does not say the pane is behind: %q", bar)
	}

	wheel(&m, p, tea.MouseWheelDown)
	if bar := ansi.Strip(m.renderTopRow(m.width)); strings.Contains(bar, scrollbackGlyph) {
		t.Errorf("back on the tail, the row still says the pane is behind: %q", bar)
	}
}

// TestAWheelOutsideThePaneIsNotItsToScroll — the border, or a region beside it in a
// split. Consuming one would scroll a pane the pointer is not over.
func TestAWheelOutsideThePaneIsNotItsToScroll(t *testing.T) {
	m, p, f := selectPane(t, longScreen(200))
	m.render()
	tail := f.View()

	b := m.boxOf(p)
	// The popover's own top-left cell, which is the border rather than a row of the
	// terminal inside it.
	p.update(&m, tea.MouseWheelMsg{X: b.x, Y: b.y, Button: tea.MouseWheelUp})
	if got := f.View(); got != tail {
		t.Error("a wheel event on the pane's border scrolled the pane")
	}
}
