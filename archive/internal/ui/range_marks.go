package ui

import (
	"github.com/andrewcohen/awp/internal/review"
)

// Showing which lines a ranged comment covers.
//
// The comment's header says "a.go:12-18", but a header is a claim about lines
// you then have to go and count. Once the visual selection is gone there is
// nothing in the diff that says where the block ends — so the covered lines keep
// a left bar, in the comment's kind colour, for as long as the comment is there.
//
// The bar sits in the same two columns the selection bar uses. That is deliberate
// reuse rather than a new gutter: a third column of markers would cost every row
// two more cells of width to say something only some rows have to say. The cursor
// still wins its own row — losing track of the cursor is worse than losing one
// row of a marker, and the row above and below still carry it, so the range reads
// as continuous.

// rangeMarks maps a stream row to the kind whose bar it carries. Absent means no
// bar. Built per rebuild, against the final row set.
type rangeMarks map[int]review.Kind

// rangeMark is the kind marking row i, if any.
func (m Model) rangeMark(i int) (review.Kind, bool) {
	k, ok := m.marks[i]
	return k, ok
}

// buildRangeMarks marks the lines every ranged comment covers, plus the range the
// open compose box is writing about.
//
// Computed against the rows it is handed rather than against m.stream, because it
// runs after the compose box has been spliced in — that shifts every index after
// the box, so marks resolved against the pre-splice rows would sit a few lines off.
func (m Model) buildRangeMarks(rows []rowRef) rangeMarks {
	var marks rangeMarks
	mark := func(a review.Anchor, kind review.Kind) {
		if !a.Multiline() {
			return
		}
		start, ok := m.locateAnchorStart(rows, review.Comment{Anchor: a})
		if !ok || rows[start].kind != rowLine {
			return
		}
		end := m.rangeEndRow(rows, a, start)
		if marks == nil {
			marks = rangeMarks{}
		}
		for i := start; i <= end && i < len(rows); i++ {
			// Only the lines themselves: a comment block or a hunk header inside the
			// span is not code the remark is about, and painting the box's own border
			// rows with it would read as the box being selected.
			if rows[i].kind == rowLine {
				marks[i] = kind.OrDefault()
			}
		}
	}
	for _, c := range m.comments {
		// Replies inherit nothing: the range belongs to the remark that opened the
		// thread, and a reply's own anchor is a copy of it. Marking both would just
		// set the same rows twice.
		if c.ReplyTo == "" {
			mark(c.Anchor, c.Kind)
		}
	}
	// The box last, so a range being written about wins over whatever a saved
	// comment says about the same lines — it is the one you are looking at, and its
	// colour changes under `tab` as you choose what the remark is asking for.
	if m.editing {
		mark(m.editor.anchor, m.editor.kind)
	}
	return marks
}
