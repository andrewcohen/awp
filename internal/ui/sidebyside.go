package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// The `|` layout toggle.
//
// Side-by-side is a way of reading the same stream, not a different view: the
// row set changes shape but every key still acts on rows, and the review store
// never learns which layout a comment was written in. See
// specs/20260806-35ph-side-by-side-diff-layout-spec.md for the decisions.

// toggleSideBySide switches the layout, keeping the reader where they were.
func (m Model) toggleSideBySide() (tea.Model, tea.Cmd) {
	if !m.sideBySide && m.hunkWidth < sideBySideMinWidth {
		// Named numbers, because "too narrow" leaves the reader guessing how much
		// wider and whether it is even worth trying. `\` is the cheapest way to find
		// the columns without touching the terminal, so it is the thing suggested.
		m.fail("side-by-side needs a wider pane — %d columns, this is %d (try `\\` to hide the left column)",
			sideBySideMinWidth, m.hunkWidth)
		return m, nil
	}

	// Where the cursor is, in terms that survive the rebuild. A row index does not:
	// pairing changes how many rows a hunk has, so the same number lands somewhere
	// else — often in a different file.
	at, held := m.cursorAnchorPoint()

	m.sideBySide = !m.sideBySide
	if m.sideBySide {
		// The two are mutually exclusive (see the spec's decision 1). Turned off
		// rather than refused, since `|` is the key that was pressed and honouring it
		// while silently keeping wrap on would give a layout neither key asked for.
		m.wrap = false
	}
	m.rebuildStream()
	if held {
		m.restoreCursorAnchor(at)
	}
	m.followCursor()
	m.syncFileCursorToCursor()
	if m.sideBySide {
		m.status = "side-by-side · | for unified"
	} else {
		m.status = "unified · | for side-by-side"
	}
	return m, nil
}

// anchorPoint names a position in the diff in terms the geometry cannot
// invalidate: which file, which hunk, and which source line inside it.
type anchorPoint struct {
	file, hunk, line int
}

// cursorAnchorPoint is where the cursor is, or false when it is not on a diff
// line — in a comment, on a header, in the detached section. Those are left to
// the ordinary clamp: there is no source line to come back to.
func (m Model) cursorAnchorPoint() (anchorPoint, bool) {
	if m.cursorRow < 0 || m.cursorRow >= len(m.stream.rows) {
		return anchorPoint{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if r.kind != rowLine {
		return anchorPoint{}, false
	}
	return anchorPoint{file: r.file, hunk: r.hunk, line: r.anchorLine()}, true
}

// restoreCursorAnchor puts the cursor back on the row showing that source line.
//
// Matched on the anchor rather than on either column, so a removal the reader was
// sitting on in unified lands on the pair that holds it — which is the row that
// answers for it in the split layout too.
func (m *Model) restoreCursorAnchor(at anchorPoint) {
	for i, r := range m.stream.rows {
		if r.kind != rowLine || r.file != at.file || r.hunk != at.hunk {
			continue
		}
		if r.anchorLine() == at.line || (r.paired && (r.oldLine == at.line || r.newLine == at.line)) {
			m.cursorRow = i
			return
		}
	}
	// The line has no row in the new layout — it cannot happen for a line that had
	// one before, since pairing drops nothing, but a clamp is cheaper than trusting
	// that forever.
	m.clampCursor()
}

// LayoutLabel names the current layout for a host's chrome, empty for the
// default. Unified says nothing: it is what the viewer has always looked like,
// and a permanent label for the normal state is noise.
func (m Model) LayoutLabel() string {
	if m.sideBySide {
		return "side-by-side"
	}
	return ""
}
