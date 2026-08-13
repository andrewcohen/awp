package deckui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
)

// Selecting text in a pane with the mouse.
//
// awp has to own this. A pane's selection cannot be the host terminal's, because
// the host terminal selects screen rows and knows nothing about the divider: a drag
// over one half of a split takes the other half's text on every line, which is what
// it did. Only the deck knows where the panes are.
//
// It applies to a pane whose program has not asked for the mouse — a shell, a
// pager, `less`. A program that did ask (an agent, nvim, jjui) gets the drag,
// because a drag is one of the things it asked for, and awp taking it would break
// the selection that program implements itself.
//
// Copy happens on release, without a key, which is what the terminals this sits
// inside do. The highlight stays until the next click, so you can see what went to
// the clipboard rather than watching it vanish at the moment it mattered.

// paneSelection is a range of the pane's screen, in the display columns the
// terminal's View renders and its Cursor answers in — see vterm.Hosted.Cursor for
// why those are not cell columns.
//
// anchor is where the drag started and cursor is where the pointer is now, kept
// unordered: which came first is what makes extending a selection upwards work, and
// SelectionText takes either order.
type paneSelection struct {
	anchorX, anchorY int
	cursorX, cursorY int
	// dragging is true between press and release. A selection outlives the drag —
	// it stays highlighted — so "is there a selection" and "is one being made" are
	// different questions.
	dragging bool
	// active is whether there is a selection at all. Not derivable from the
	// endpoints: a click with no drag is a real event that clears the last
	// selection, and (0,0)-(0,0) is a legitimate one-cell range.
	active bool
}

// paneSelects reports whether this pane handles the mouse itself rather than
// forwarding it.
func (p *panePopover) paneSelects() bool { return !p.term.WantsMouse() }

// selectMouse handles a mouse event for a pane that selects. It reports whether
// the event was consumed, and a command to run — the clipboard write, on release.
//
// Motion is only meaningful while dragging: a pointer crossing a pane it has not
// pressed in is not selecting anything, and treating it as such would highlight
// text as you moved past on the way somewhere else.
func (p *panePopover) selectMouse(m *Model, msg tea.MouseMsg) (tea.Cmd, bool) {
	inner, ok := paneMouse(msg, m.boxOf(p))
	if !ok {
		// Outside the terminal — the border, or another region. A drag that runs off
		// the edge keeps the selection it had rather than dropping it, because the
		// pointer coming back is a continuation of the same gesture.
		return nil, false
	}
	at := inner.Mouse()

	switch msg.(type) {
	case tea.MouseClickMsg:
		p.sel = paneSelection{
			anchorX: at.X, anchorY: at.Y,
			cursorX: at.X, cursorY: at.Y,
			dragging: true,
			// Not active yet: a click that never becomes a drag is how you clear a
			// selection, and one cell highlighted under the pointer on every click
			// would be noise.
		}
		return nil, true

	case tea.MouseMotionMsg:
		if !p.sel.dragging {
			return nil, false
		}
		p.sel.cursorX, p.sel.cursorY = at.X, at.Y
		p.sel.active = true
		return nil, true

	case tea.MouseReleaseMsg:
		if !p.sel.dragging {
			return nil, false
		}
		p.sel.dragging = false
		if !p.sel.active {
			// A click, not a drag. Nothing selected, nothing copied.
			return nil, true
		}
		text := p.term.SelectionText(p.sel.anchorX, p.sel.anchorY, p.sel.cursorX, p.sel.cursorY)
		if text == "" {
			p.sel.active = false
			return nil, true
		}
		m.status = fmt.Sprintf("copied %d characters", len([]rune(text)))
		return tea.SetClipboard(text), true
	}
	// A wheel, or anything else. Left alone: scrolling is not selecting, and a
	// pane whose program does not want the mouse has nothing to do with it either.
	return nil, false
}

// clearSelection drops the highlight. Called when a key is pressed, because typing
// into a pane means you are done with what you had picked out — and a highlight
// left over a screen the program is redrawing marks whatever text has since moved
// under it.
func (p *panePopover) clearSelection() { p.sel = paneSelection{} }

// selectionRows is the selected span on each row it covers, as start and end
// display columns, inclusive. Empty when nothing is selected.
//
// One entry per row rather than a single rectangle: a selection is linear, so the
// first row runs from its column to the end of the screen and the last from the
// start, which is what makes a multi-row drag read as text rather than as a block.
func (p *panePopover) selectionRows(w int) map[int][2]int {
	if !p.sel.active {
		return nil
	}
	x0, y0, x1, y1 := p.sel.anchorX, p.sel.anchorY, p.sel.cursorX, p.sel.cursorY
	if y1 < y0 || (y1 == y0 && x1 < x0) {
		x0, y0, x1, y1 = x1, y1, x0, y0
	}
	rows := make(map[int][2]int, y1-y0+1)
	for y := y0; y <= y1; y++ {
		from, to := 0, w-1
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = x1
		}
		if to >= from {
			rows[y] = [2]int{from, to}
		}
	}
	return rows
}

// tintSelection paints the selected span of each row of a rendered screen.
//
// The screen arrives as a block of styled lines, so each affected row is cut into
// three by display column and the middle is re-wrapped in the selection
// background. ansi.Cut is style-aware, which is what makes this safe over a
// program's own colours — the alternative, walking the string counting escapes by
// hand, is the thing it exists to avoid.
func tintSelection(screen string, rows map[int][2]int, w int) string {
	if len(rows) == 0 {
		return screen
	}
	style := lipgloss.NewStyle().Background(charm.SelectionBg)
	lines := strings.Split(screen, "\n")
	for y, span := range rows {
		if y < 0 || y >= len(lines) {
			continue
		}
		line := lines[y]
		from, to := max(span[0], 0), min(span[1], w-1)
		if to < from {
			continue
		}
		// Cut is [left, right), so the inclusive end column is right = to+1.
		head := ansi.Cut(line, 0, from)
		mid := ansi.Cut(line, from, to+1)
		tail := ansi.Cut(line, to+1, lipgloss.Width(line))
		lines[y] = head + style.Render(ansi.Strip(mid)) + tail
	}
	return strings.Join(lines, "\n")
}
