package deckui

import (
	tea "charm.land/bubbletea/v2"
)

// Dragging the strip's right edge, and remembering where you left it.
//
// The strip shipped at a fixed 36 columns with an argument for the number (see
// sidebarDefaultWidth) and the argument is still right about the default — but it
// was answering "what is a good width" when the real question is "whose choice is
// it". What a row needs depends on how long your workspaces' names are, which awp
// cannot know and you can see. So the number is a preference now, and the way to
// change it is to grab the edge, which is the gesture the split's divider already
// taught in this deck.
//
// Columns rather than a fraction, unlike the split. The split divides a fixed
// budget between two programs, so the interesting quantity is the ratio; the strip
// takes columns from a pane whose appetite does not grow with the screen, so the
// interesting quantity is how many. A fraction here would widen the strip every
// time you moved to a bigger monitor, which is the behaviour the fixed number was
// chosen to avoid.

// sidebarGrabCols is how far either side of the strip's edge counts as grabbing it,
// mirroring splitGrabCols and for the same reason: a one-column target is one you
// miss. The cells it borrows are the strip's last column and the child's border,
// neither of which is a cell of a program — paneMouse already refuses a border.
const sidebarGrabCols = 1

// SidebarWidthSaver records how wide the strip should be next time, in columns.
//
// Its own hook, like SidebarSaver and SplitFracSaver, and for the reason given
// there: each saver takes exactly the value its key stores, so deckui never has to
// know the shape of a preferences file.
//
// What it saves is the width you dragged to, not m.sidebarWidth() — a strip
// clamped narrow by today's terminal must not overwrite the width you chose on a
// wider one.
type SidebarWidthSaver func(int) error

// WithSidebarWidth opens the deck with the strip as wide as it was left. Zero, or
// anything from a build that never wrote the preference, means the default.
func (m Model) WithSidebarWidth(cols int) Model {
	m.sidebarW = cols
	return m
}

// WithSidebarWidthSaver sets the hook called when the edge is dragged.
func (m Model) WithSidebarWidthSaver(save SidebarWidthSaver) Model {
	m.saveSidebarWidth = save
	return m
}

// sidebarMouse is the strip's whole claim on the mouse, reporting whether it took
// the event.
//
// One entry point so Update asks the strip once rather than testing each gesture in
// an order Update would then own. The edge goes first: its grab band overlaps the
// strip's own last column, and a drag has to win there — resizing is the gesture you
// are in the middle of, and a press on the edge that fell through to a row would
// open a pane instead of taking hold of the border.
func (m *Model) sidebarMouse(msg tea.MouseMsg) bool {
	return m.dragSidebarEdge(msg)
}

// sidebarEdgeCol is the screen column the strip's last column occupies. Only
// meaningful while the strip is up.
func (m *Model) sidebarEdgeCol() int { return m.sidebarWidth() - 1 }

// dragSidebarEdge handles a mouse event that belongs to the strip's edge rather
// than to anything under it, reporting whether it consumed the event.
//
// The same shape as splitModal.dragDivider: a press on the edge starts a drag,
// motion resizes, and anything else ends one. Checked before the strip's rows and
// before the child, because the edge overlaps both — its grab band covers the
// strip's last column and the child's first.
//
// While dragging, every motion is consumed wherever the pointer is. A hand that
// runs past the clamp must not start typing into the pane it ran into.
func (m *Model) dragSidebarEdge(msg tea.MouseMsg) bool {
	if !m.showsSidebar() {
		return false
	}
	x := msg.Mouse().X
	switch msg.(type) {
	case tea.MouseMotionMsg:
		if !m.sidebarDragging {
			return false
		}
		// The pointer is on the column that should become the strip's last, so the
		// width is one more than the column index.
		m.setSidebarWidth(x + 1)
		return true
	case tea.MouseClickMsg:
		edge := m.sidebarEdgeCol()
		if x < edge-sidebarGrabCols || x > edge+sidebarGrabCols {
			return false
		}
		m.sidebarDragging = true
		return true
	default:
		// A release, a wheel, anything else ends the drag — and is consumed only if
		// there was one, so an ordinary click still reaches whatever it landed on.
		if m.sidebarDragging {
			m.sidebarDragging = false
			return true
		}
		return false
	}
}

// setSidebarWidth puts the strip at cols and remembers it.
//
// Clamped here as well as in sidebarWidth, and the two clamps are not the same
// one: this bounds the number that gets *stored*, so a drag against the wall does
// not bank columns the clamp is hiding and then need undoing one at a time on the
// way back. sidebarWidth bounds what a given terminal *shows*, which is a question
// asked again on every resize.
//
// A width that did not change writes nothing. A drag is a stream of motion events
// and most of them land on the column the last one did.
func (m *Model) setSidebarWidth(cols int) {
	cols = min(max(cols, sidebarMinWidth), max(m.width-sidebarChildMinW, sidebarMinWidth))
	if cols == m.sidebarW {
		return
	}
	m.sidebarW = cols
	if m.saveSidebarWidth == nil {
		return
	}
	if err := m.saveSidebarWidth(cols); err != nil {
		// The same treatment the sidebar's own saver gets: the strip is already the
		// width you dragged it to, and the only thing lost is that it will not be
		// next time. Worth saying, not worth refusing.
		m.status = "sidebar: " + err.Error()
	}
}
