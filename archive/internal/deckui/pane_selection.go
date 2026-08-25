package deckui

import (
	"fmt"
	"strings"
	"time"

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
	// clicks is how many presses this one is part of — 1 for a click, 2 for a
	// double, 3 for a triple, and back to 1 after that. A terminal's own gesture
	// vocabulary stops at three, and a fourth click starting over is what makes a
	// mis-aimed flurry recoverable rather than leaving the pane in a mode.
	clicks int
	// lastClick is when and where the previous press was, which is the whole of what
	// "the same click again" means: near enough in time, and on the same cell.
	lastClick    time.Time
	lastX, lastY int
}

// paneClickInterval is how long after a press a second one still counts as a
// double-click.
//
// Half a second, which is the middle of what the platforms use (macOS defaults to
// 500ms, and X11 toolkits to 400). Not read from the OS: this is a pane inside a
// terminal inside someone else's window manager, and there is no answer to ask for
// that would be about this pane.
const paneClickInterval = 500 * time.Millisecond

// paneNow is the clock the click counter reads, swapped in tests. A gesture defined
// by an interval cannot be tested against a real clock without either sleeping or
// being flaky, and this is the smallest seam that avoids both.
var paneNow = time.Now

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
	inner, ok := paneMouse(msg, p.lastBox)
	if !ok {
		// Outside the terminal — the border, or another region. A drag that runs off
		// the edge keeps the selection it had rather than dropping it, because the
		// pointer coming back is a continuation of the same gesture.
		return nil, false
	}
	at := inner.Mouse()

	switch msg.(type) {
	case tea.MouseClickMsg:
		now := paneNow()
		clicks := p.sel.nextClick(now, at.X, at.Y)
		p.sel = paneSelection{
			anchorX: at.X, anchorY: at.Y,
			cursorX: at.X, cursorY: at.Y,
			dragging:  true,
			clicks:    clicks,
			lastClick: now, lastX: at.X, lastY: at.Y,
			// Not active yet: a click that never becomes a drag is how you clear a
			// selection, and one cell highlighted under the pointer on every click
			// would be noise.
		}
		// A second or third click on the spot selects something immediately, which is
		// the difference between them and the first: a word or a line is picked out by
		// the press itself rather than by the drag that may follow.
		p.selectAround(clicks, at.X, at.Y)
		return nil, true

	case tea.MouseMotionMsg:
		if !p.sel.dragging {
			return nil, false
		}
		if p.sel.clicks > 1 {
			// Dragging out of a word or a line selection. Left as it was rather than
			// collapsing to the pointer: the gesture already said what unit you are
			// picking, and extending by that unit is a bigger feature than this
			// (libghostty ships select_word_between for exactly it). Ignoring the motion
			// keeps what the double-click gave you instead of silently throwing it away.
			return nil, true
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

// nextClick is how many presses this one continues: a second press near the first,
// soon enough, is a double, a third is a triple, and a fourth starts over.
//
// Same cell rather than a pixel radius, because a terminal has no pixels — the
// pointer either moved off the character or it did not, and there is no smaller
// distance for a threshold to be about.
func (s paneSelection) nextClick(now time.Time, x, y int) int {
	if s.clicks == 0 || s.clicks >= 3 {
		return 1
	}
	if x != s.lastX || y != s.lastY {
		return 1
	}
	if now.Sub(s.lastClick) > paneClickInterval {
		return 1
	}
	return s.clicks + 1
}

// selectAround applies the unit a double- or triple-click picks out.
//
// The emulator answers both — see vterm.Hosted.WordAt — so this is the routing and
// nothing else, deliberately: Ghostty ships the boundary rules, and a second opinion
// here would make a pane disagree with the terminal it is running inside about what
// a double-click picks out. That includes the cases that look like edge cases from
// here: a run of blanks is a word, because it is one everywhere else.
//
// A gap therefore does get taken, and then dropped on release, because its text is
// empty and an empty selection is not copied. The two rules meet at the right
// answer — double-clicking between words does nothing — without either of them
// having to know about the other.
func (p *panePopover) selectAround(clicks, x, y int) {
	var x0, y0, x1, y1 int
	var ok bool
	switch clicks {
	case 2:
		x0, y0, x1, y1, ok = p.term.WordAt(x, y)
	case 3:
		x0, y0, x1, y1, ok = p.term.LineAt(x, y)
	default:
		return
	}
	if !ok {
		return
	}
	p.sel.anchorX, p.sel.anchorY = x0, y0
	p.sel.cursorX, p.sel.cursorY = x1, y1
	p.sel.active = true
}
