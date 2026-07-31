package ui

import (
	"errors"

	"github.com/andrewcohen/awp/internal/review"
)

// Commenting on more than one line.
//
// Plenty of review remarks are about a block rather than a line — "this loop
// re-reads the file every iteration", "these three cases collapse into one". With
// only a single-line anchor the reviewer either picks a line and writes "and the
// next four", which the agent then has to interpret, or leaves four comments. So
// the anchor grew an end (see review.Anchor) and this is the gesture that fills it
// in: `v` starts a range at the cursor, moving the cursor extends it, `c` comments
// on it.
//
// `v` because that is what it is in vim, and the extension keys are just the
// movement keys — there is nothing new to learn beyond the one letter. The range
// is drawn with the cursorline band, the same band a single selected row gets, so
// what is selected is what is highlighted.
//
// The range is held as a row index, which is the one thing everything else in
// this package avoids (see search.go on why it stores no derived index): the diff
// reloads on a timer, and a row index recorded now may point at different code a
// second later. It is the right shape here anyway, because the cursor is already
// exactly that and the gesture lives between two keystrokes — you press `v`, you
// press `c`. Anything that rebuilds the rows underneath it (a reload that changed
// the diff, a wrap toggle, folding a file) drops the range rather than letting one
// end of it point at whatever moved into that slot.

// visualNone is the anchor value meaning "no range in progress". Zero is a real
// row, so absence needs its own value.
const visualNone = -1

// visualActive reports whether a range is being selected.
func (m Model) visualActive() bool { return m.visualAnchor != visualNone }

// visualSpan is the selected row span, lowest row first. Selecting upwards is as
// natural as downwards, so the anchor can be either end.
func (m Model) visualSpan() (int, int, bool) {
	if !m.visualActive() {
		return 0, 0, false
	}
	lo, hi := m.visualAnchor, m.cursorRow
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, max(0, min(hi, len(m.stream.rows)-1)), true
}

// rowSelected reports whether row i is part of the current selection: the cursor
// row, or any row of the visual range.
//
// Every row of the range carries the selection, not just its ends — a range whose
// middle looked unselected would not read as a range at all.
func (m Model) rowSelected(i int) bool {
	if i == m.cursorRow {
		return true
	}
	lo, hi, ok := m.visualSpan()
	return ok && i >= lo && i <= hi
}

// rowBanded reports whether row i is painted with the cursorline band.
//
// The band is the focus-dependent half of the selection: it appears only while
// the diff pane holds the keyboard, so there is never more than one band on
// screen to mistake for the active selection. The `┃` bar stays either way.
func (m Model) rowBanded(i int) bool {
	return m.focus == FocusHunks && m.rowSelected(i)
}

// startVisual opens a range at the cursor.
func (m *Model) startVisual() {
	m.visualAnchor = m.cursorRow
	m.status = "range: j/k extend · c comment · esc cancel"
}

// clearVisual drops the range without touching the status line, for the paths
// that have something of their own to say (or a compose box to open).
func (m *Model) clearVisual() { m.visualAnchor = visualNone }

// cancelVisual drops the range and the hint that came with it. Says nothing:
// the highlight disappearing is the whole message.
func (m *Model) cancelVisual() {
	m.clearVisual()
	m.status = ""
}

// toggleVisual is what `v` does — a second press cancels, so the key that starts
// a range also gets you out of one.
func (m *Model) toggleVisual() {
	if m.visualActive() {
		m.cancelVisual()
		return
	}
	m.startVisual()
}

var (
	// errRangeNoLines means the selection covered no diff lines — all of it was
	// headers, comments or blank rows.
	errRangeNoLines = errors.New("select diff lines to comment on")
	// errRangeSpansHunks means the two ends are in different hunks (or files).
	//
	// Rejected rather than accepted and clamped, because a range across a hunk
	// boundary cannot mean what it looks like: the lines between the hunks are not
	// in the diff at all, so "these lines" would silently cover code the reviewer
	// never saw. GitHub refuses the same shape on publish, for the same reason.
	errRangeSpansHunks = errors.New("a range comment stays inside one hunk")
)

// rangeAnchor builds the anchor for the current visual range.
func (m Model) rangeAnchor() (review.Anchor, error) {
	lo, hi, ok := m.visualSpan()
	if !ok {
		return review.Anchor{}, errRangeNoLines
	}
	rows := m.stream.rows

	// The diff lines in the span, once each: a wrapped line occupies several
	// display rows and is still one line of code, so only its first row counts.
	var sel []int
	file, hunk := -1, -1
	for i := lo; i <= hi && i < len(rows); i++ {
		r := rows[i]
		if r.kind != rowLine || r.seg != 0 || r.file < 0 || r.file >= len(m.filtered) {
			continue
		}
		if file < 0 {
			file, hunk = r.file, r.hunk
		} else if r.file != file || r.hunk != hunk {
			return review.Anchor{}, errRangeSpansHunks
		}
		sel = append(sel, i)
	}
	if len(sel) == 0 {
		return review.Anchor{}, errRangeNoLines
	}

	// Which side the range is about. A selection of nothing but removals is about
	// the old side — those lines exist nowhere else. Anything else is about the
	// resulting code, so it takes the new side, and the removals in it are dropped:
	// a removed line has no new-side number and so cannot be one of the range's
	// ends. That silently narrows what was highlighted, which is the right trade —
	// the alternative is refusing every range that starts just above an edit, which
	// is where most of them start.
	side := review.SideOld
	for _, i := range sel {
		if ln, ok := m.hunkLineAt(rows, i); ok && ln.Type != '-' {
			side = review.SideNew
			break
		}
	}
	if side == review.SideNew {
		kept := make([]int, 0, len(sel))
		for _, i := range sel {
			if rows[i].newNo > 0 {
				kept = append(kept, i)
			}
		}
		if len(kept) == 0 {
			return review.Anchor{}, errRangeNoLines
		}
		sel = kept
	}

	a, ok := m.anchorSpan(rows, sel[0], sel[len(sel)-1], side)
	if !ok {
		return review.Anchor{}, errRangeNoLines
	}
	return a, nil
}
