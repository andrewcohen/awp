package ui

import "github.com/andrewcohen/awp/internal/diff"

// Keeping the reader's place across a reload.
//
// A live-refreshing diff rebuilds its whole row index whenever the underlying
// change moves, and row indices are meaningless across that rebuild: inserting
// one line above the cursor shifts every row below it. Restoring the cursor by
// index is what made auto-refresh unusable in April 2026 (see the 2026-04-10
// entry in specs/20260410-1l07-diff-ui-spec.md) and why it stayed disabled.
//
// So the cursor is restored the same way a comment will be (phase 3 of the
// review-surface spec): by *content*, falling back down a ladder of
// progressively weaker matches. The two problems are the same problem, and
// solving it here first means commenting inherits a tested implementation.

// pathOf is the file's identity for anchoring: the new path where it has one,
// the old path for a deletion. Deliberately not DisplayPath, which renders a
// rename as "old → new" and so would change identity under a rename.
func pathOf(f diff.FileDiff) string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// viewAnchor is where the cursor was, described so it can be found again in a
// rebuilt stream.
type viewAnchor struct {
	kind rowKind
	// path identifies the file, which survives everything short of a rename.
	path string
	// hunk is the hunk's ordinal within the file, for hunk-header rows.
	hunk int
	// text is the diff line's content — the strongest signal for a line row,
	// since line numbers shift but content usually doesn't.
	text string
	// textOrdinal is which occurrence of that text this was within the file,
	// counting from the top. It disambiguates duplicate lines better than a
	// line number does: inserting unrelated text above shifts every number but
	// leaves the ordinal intact.
	textOrdinal int
	oldNo       int
	newNo       int
	seg         int
}

// captureAnchor describes the cursor's current row. Reports false when there is
// nothing to anchor to (empty stream), in which case a reload should just reset.
func (m Model) captureAnchor() (viewAnchor, bool) {
	if len(m.stream.rows) == 0 || m.cursorRow < 0 || m.cursorRow >= len(m.stream.rows) {
		return viewAnchor{}, false
	}
	r := m.stream.rows[m.cursorRow]
	if r.file < 0 || r.file >= len(m.filtered) {
		return viewAnchor{}, false
	}
	a := viewAnchor{
		kind:  r.kind,
		path:  pathOf(m.filtered[r.file]),
		hunk:  r.hunk,
		oldNo: r.oldNo,
		newNo: r.newNo,
		seg:   r.seg,
	}
	if r.kind == rowLine {
		if h, _, ok := m.stream.hunkAt(m.filtered, r); ok && r.line >= 0 && r.line < len(h.Lines) {
			a.text = h.Lines[r.line].Content
			a.textOrdinal = m.textOrdinalAt(m.cursorRow, a.path, a.text, r.seg)
		}
	}
	return a, true
}

// restoreAnchor rebuilds the stream and puts the cursor back on the row the
// anchor describes, preserving its distance from the top of the viewport so the
// text the reader was looking at stays where it was on screen.
func (m *Model) restoreAnchor(a viewAnchor, screenOffset int) {
	m.rebuildStream()
	if row, ok := m.findAnchor(a); ok {
		m.cursorRow = row
	}
	m.clampCursor()
	// Restore the cursor's screen position rather than just making it visible:
	// re-centring or scrolling to an edge would move the surrounding text under
	// the reader even though the cursor itself landed correctly.
	m.streamScroll = m.cursorRow - max(0, screenOffset)
	m.clampStreamScroll()
	m.followCursor()
	m.syncFileCursorToCursor()
}

// findAnchor locates the anchored row in the current stream, weakening the
// match until something fits.
func (m Model) findAnchor(a viewAnchor) (int, bool) {
	if len(m.stream.rows) == 0 {
		return 0, false
	}
	// Rows belonging to the anchor's file. Everything below searches within
	// them, so a file that vanished falls straight through to the caller's
	// clamp rather than matching text somewhere unrelated.
	var inFile []int
	fileHeader := -1
	for i, r := range m.stream.rows {
		if r.file < 0 || r.file >= len(m.filtered) || pathOf(m.filtered[r.file]) != a.path {
			continue
		}
		if r.kind == rowFileHeader && fileHeader < 0 {
			fileHeader = i
		}
		inFile = append(inFile, i)
	}
	if len(inFile) == 0 {
		return 0, false
	}

	switch a.kind {
	case rowFileHeader, rowSpacer:
		if fileHeader >= 0 {
			return fileHeader, true
		}
	case rowHunkHeader:
		for _, i := range inFile {
			if r := m.stream.rows[i]; r.kind == rowHunkHeader && r.hunk == a.hunk {
				return i, true
			}
		}
	case rowLine:
		// Same content and same line number: the line did not move.
		for _, i := range inFile {
			r := m.stream.rows[i]
			if r.kind == rowLine && r.seg == a.seg && r.newNo == a.newNo && r.oldNo == a.oldNo && m.lineText(r) == a.text {
				return i, true
			}
		}
		// Same content, renumbered: the line moved but is still there — the
		// case that matters most, an edit above the cursor.
		//
		// Candidates must be narrowed by line number rather than taking the
		// first match. Duplicate line content is completely ordinary in code
		// (`}`, blank lines, repeated calls), and picking the first would fling
		// the cursor to the top of the file the moment anything above it moved.
		var textMatches []int
		for _, i := range inFile {
			r := m.stream.rows[i]
			if r.kind == rowLine && r.seg == a.seg && m.lineText(r) == a.text {
				textMatches = append(textMatches, i)
			}
		}
		// Prefer the same occurrence of that text within the file. Line-number
		// distance cannot break a tie between two equidistant duplicates, and
		// guessing wrong lands the cursor on a different line that merely looks
		// identical.
		if a.textOrdinal >= 0 && a.textOrdinal < len(textMatches) {
			return textMatches[a.textOrdinal], true
		}
		if best, ok := m.nearestByLineNo(textMatches, a); ok {
			return best, true
		}
		// Content changed too: fall back to the nearest line number anywhere in
		// the file, so the cursor stays in the right neighbourhood.
		if best, ok := m.nearestByLineNo(inFile, a); ok {
			return best, true
		}
	}
	if fileHeader >= 0 {
		return fileHeader, true
	}
	return inFile[0], true
}

// textOrdinalAt counts how many earlier rows in the same file carry the same
// text, giving the row's occurrence index.
func (m Model) textOrdinalAt(row int, path, text string, seg int) int {
	n := 0
	for i := 0; i < row && i < len(m.stream.rows); i++ {
		r := m.stream.rows[i]
		if r.kind != rowLine || r.seg != seg {
			continue
		}
		if r.file < 0 || r.file >= len(m.filtered) || pathOf(m.filtered[r.file]) != path {
			continue
		}
		if m.lineText(r) == text {
			n++
		}
	}
	return n
}

// nearestByLineNo finds the line row in the file whose number is closest to the
// anchor's.
func (m Model) nearestByLineNo(inFile []int, a viewAnchor) (int, bool) {
	want, pick := a.newNo, func(r rowRef) int { return r.newNo }
	if want == 0 {
		want, pick = a.oldNo, func(r rowRef) int { return r.oldNo }
	}
	if want == 0 {
		return 0, false
	}
	best, bestDist := -1, 0
	for _, i := range inFile {
		r := m.stream.rows[i]
		if r.kind != rowLine {
			continue
		}
		n := pick(r)
		if n == 0 {
			continue
		}
		d := n - want
		if d < 0 {
			d = -d
		}
		if best < 0 || d < bestDist {
			best, bestDist = i, d
		}
	}
	return best, best >= 0
}

// lineText is the content of a line row.
func (m Model) lineText(r rowRef) string {
	h, _, ok := m.stream.hunkAt(m.filtered, r)
	if !ok || r.line < 0 || r.line >= len(h.Lines) {
		return ""
	}
	return h.Lines[r.line].Content
}
