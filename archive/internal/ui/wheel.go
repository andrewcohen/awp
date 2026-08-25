package ui

// The wheel.
//
// #340's rule, which the deck already applies to a pane: the wheel scrolls what
// it is *over*, and does not move the keyboard there. So a notch over the file
// list while the diff has the keys moves the file list and leaves the keys in the
// diff — pointing at something is not the same gesture as going there.
//
// The pointer is the only thing that says which pane a notch belongs to, so the
// coordinates have to be the body's own. Every host renders Body somewhere
// different — the deck's modal at its box's origin, a split's half further right
// again, standalone `awp diff` under its header — and a host that passed screen
// coordinates would scroll the pane at the mirror-image position instead. One
// entry point, taking body-local cells, so there is one place the translation can
// be got wrong rather than one per host.

// wheelRows is how far one notch moves a pane that has a scroll of its own.
//
// Three, which is what a terminal emulator's own default is, and for the reason
// paneWheelRows is three in the deck: one row per notch makes a long scroll a
// wrist exercise, and a screenful per notch leaves no overlap to read continuity
// from.
const wheelRows = 3

// wheelSelectionRows is a notch over a list whose scroll *is* its selection — the
// file list and the comment index, both of which derive what they show from where
// their cursor is (see visibleRange).
//
// One, not three. Moving a view by three rows lands three rows away; moving a
// selection by three lands on the fourth file down, which is past the one you were
// aiming at. And a file selection is a seek, so three notches of overshoot is three
// rebuilt stream positions.
const wheelSelectionRows = 1

// WheelAt scrolls the pane under the pointer, reporting whether it took the
// event.
//
// x, y are cells of the block Body returned — 0,0 is its top-left, border
// included. up says which way the notch went.
//
// Not a tea.MouseWheelMsg: this model is embedded as often as it runs standalone,
// and an embedded one is handed events in its host's coordinates. Taking the
// translated position as two ints is what makes the host do the translation, at the
// one point in the host that knows where it put the body.
func (m Model) WheelAt(x, y int, up bool) (Model, bool) {
	if m.width <= 0 {
		return m, false
	}
	// An overlay stands in place of the panes rather than over them (see Body), so
	// while one is up there is no pane under the pointer to scroll. The `?`
	// reference is the exception: it is a viewport, and it is long enough that
	// reaching the end of it is exactly what someone reads it with a mouse for.
	switch {
	case m.showHelp:
		if up {
			m.helpVP.ScrollUp(wheelRows)
		} else {
			m.helpVP.ScrollDown(wheelRows)
		}
		return m, true
	case m.publishing, m.merging:
		return m, false
	}
	height := max(minBodyHeight, m.bodyHeight)
	leftWidth, _ := m.paneWidthsFor(m.width)
	if leftWidth > 0 && x >= 0 && x < leftWidth {
		return m.wheelLeftColumn(y, height, up)
	}
	step := wheelRows
	if up {
		step = -step
	}
	// The cursor is deliberately left where it is, which is what makes this a scroll
	// rather than a move: it may end up above or below the view, and coming back is
	// one press of j or k. clampStreamScroll bounds the far end.
	m.streamScroll += step
	m.clampStreamScroll()
	return m, true
}

// wheelLeftColumn routes a notch inside the left column, which is the file list
// stacked over the comment index.
//
// The split comes from buildLeftColumn: the index takes the last commentPaneHeight
// rows plus its border, and the file list has the rest. Derived from the same
// function the renderer uses rather than measured again here, so a change to how
// the column is stacked moves both.
func (m Model) wheelLeftColumn(y, height int, up bool) (Model, bool) {
	step := wheelSelectionRows
	if up {
		step = -step
	}
	indexRows := commentPaneHeight(len(m.commentIndex), m.hiddenThreads(), height)
	// height - indexRows, not (height+2) - (indexRows+2): each panel's border adds a
	// row above and below its content, and the two cancel.
	if indexRows > 0 && y >= height-indexRows {
		if len(m.commentIndex) == 0 {
			// The header-only notice, which has no rows to select.
			return m, false
		}
		m.seekToComment(min(len(m.commentIndex)-1, max(0, m.commentsCursor+step)))
		return m, true
	}
	if len(m.filtered) == 0 {
		return m, false
	}
	m.seekToFile(min(len(m.filtered)-1, max(0, m.filesCursor+step)))
	return m, true
}
