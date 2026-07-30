package ui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/diff"
)

// The diff is presented as one continuous stream of rows spanning every
// changed file, rather than a pane scoped to the selected file. Reading a
// change is a single scroll; the file list is a jump index into that stream.
//
// Geometry is separated from rendering, which is what makes a whole-diff
// stream cheap. streamIndex answers "how many rows are there, and what does
// row N show" without building a single string — so scroll clamping, the
// file-cursor sync and hunk jumps are lookups rather than re-renders. Only
// the rows actually on screen get styled, each frame.
//
// The index depends on (files, width, wrap) and is rebuilt only when those
// change. Notably the horizontal pan is NOT an input: it's applied to the
// visible window at render time, so panning never invalidates geometry.

// The file divider's rule: a short lead-in before the filename, then a fill
// to the pane's right edge.
const (
	fileRuleGlyph = "═"
	fileRuleLead  = 2
)

type rowKind uint8

const (
	// rowSpacer is the blank line separating one file from the next.
	rowSpacer rowKind = iota
	rowFileHeader
	rowHunkHeader
	rowLine
)

// rowRef says what a single stream row shows. Line numbers are resolved
// during the build pass — walking a hunk to work out which numbers a line
// carries is O(hunk) and would otherwise happen per row, per frame.
type rowRef struct {
	kind rowKind
	file int
	hunk int
	line int
	// seg is which slice of a hard-wrapped line this row shows; 0 when the
	// line fits or wrap is off.
	seg int
	// oldNo / newNo are the line's numbers on each side, 0 where it has none
	// (an added line has no old number, a removed line no new one).
	oldNo int
	newNo int
}

// hunkMeta is the gutter geometry for one hunk: how wide its line-number
// columns are, and therefore how much width is left for content.
type hunkMeta struct {
	oldWidth    int
	newWidth    int
	prefixWidth int
}

// streamIndex is the row geometry of a whole diff.
type streamIndex struct {
	rows []rowRef
	// fileStart[i] is the row index of file i's header.
	fileStart []int
	// hunkStart holds the row index of every hunk header, in stream order.
	hunkStart []int
	// meta[file][hunk] is that hunk's gutter geometry.
	meta [][]hunkMeta
	// width and wrap are the inputs this index was built for.
	width int
	wrap  bool
}

// buildStream indexes every row of the diff at the given content width.
func buildStream(files []diff.FileDiff, width int, wrap bool) streamIndex {
	// Row counts must be right even before the first size message, or
	// scrolling is dead until a resize. At width 1 nothing wraps, so the
	// geometry is one row per line — correct, just unreadable, which is moot
	// at that size.
	width = max(1, width)
	idx := streamIndex{
		width:     width,
		wrap:      wrap,
		fileStart: make([]int, 0, len(files)),
		meta:      make([][]hunkMeta, len(files)),
	}
	for fi, f := range files {
		if fi > 0 {
			// The separator belongs to the file it follows, not the one it
			// precedes, so the file cursor changes exactly when the next
			// file's header reaches the top rather than a row early.
			idx.rows = append(idx.rows, rowRef{kind: rowSpacer, file: fi - 1, hunk: -1, line: -1})
		}
		idx.fileStart = append(idx.fileStart, len(idx.rows))
		idx.rows = append(idx.rows, rowRef{kind: rowFileHeader, file: fi, hunk: -1, line: -1})

		idx.meta[fi] = make([]hunkMeta, len(f.Hunks))
		for hi, h := range f.Hunks {
			oldWidth, newWidth := hunkLineNumberWidths(h)
			meta := hunkMeta{
				oldWidth:    oldWidth,
				newWidth:    newWidth,
				prefixWidth: oldWidth + 1 + newWidth + 1 + 2,
			}
			idx.meta[fi][hi] = meta
			idx.hunkStart = append(idx.hunkStart, len(idx.rows))
			idx.rows = append(idx.rows, rowRef{kind: rowHunkHeader, file: fi, hunk: hi, line: -1})

			avail := width - meta.prefixWidth
			oldNo, newNo := h.OldStart, h.NewStart
			for li, l := range h.Lines {
				ref := rowRef{kind: rowLine, file: fi, hunk: hi, line: li}
				switch l.Type {
				case '+':
					ref.newNo = newNo
					newNo++
				case '-':
					ref.oldNo = oldNo
					oldNo++
				default:
					ref.oldNo, ref.newNo = oldNo, newNo
					oldNo++
					newNo++
				}
				segs := 1
				if wrap {
					segs = wrappedSegments(l.Content, avail)
				}
				for s := 0; s < segs; s++ {
					ref.seg = s
					idx.rows = append(idx.rows, ref)
				}
			}
		}
	}
	return idx
}

// wrappedSegments is how many rows a line occupies under hard wrap. Hard
// wrapping (rather than word wrapping) is what keeps this arithmetic: the
// count follows from the width, with no trial layout. It also suits code,
// where reflowing at word boundaries breaks up tokens misleadingly.
func wrappedSegments(content string, avail int) int {
	if avail < 1 {
		return 1
	}
	w := ansi.StringWidth(content)
	if w <= avail {
		return 1
	}
	return (w + avail - 1) / avail
}

// segmentText is the slice of content this row shows: the whole line when
// unwrapped, or one hard-wrapped chunk. Cutting the *unstyled* text and
// styling afterwards keeps this a plain cell-range operation — every diff
// line carries a single style, so the result is identical to slicing styled
// text and much harder to get wrong.
func segmentText(content string, seg, avail int) string {
	if avail < 1 || seg == 0 && ansi.StringWidth(content) <= avail {
		return content
	}
	return ansi.Cut(content, seg*avail, (seg+1)*avail)
}

// hunkAt returns the hunk a row belongs to, or false for rows that aren't
// inside one.
func (idx streamIndex) hunkAt(files []diff.FileDiff, r rowRef) (diff.Hunk, hunkMeta, bool) {
	if r.hunk < 0 || r.file < 0 || r.file >= len(files) {
		return diff.Hunk{}, hunkMeta{}, false
	}
	f := files[r.file]
	if r.hunk >= len(f.Hunks) {
		return diff.Hunk{}, hunkMeta{}, false
	}
	return f.Hunks[r.hunk], idx.meta[r.file][r.hunk], true
}

// nextHunkStart is the row of the first hunk header strictly after row, or
// -1 when there is none.
func (idx streamIndex) nextHunkStart(row int) int {
	for _, s := range idx.hunkStart {
		if s > row {
			return s
		}
	}
	return -1
}

// prevHunkStart is the row of the last hunk header strictly before row, or
// -1 when there is none.
func (idx streamIndex) prevHunkStart(row int) int {
	for i := len(idx.hunkStart) - 1; i >= 0; i-- {
		if idx.hunkStart[i] < row {
			return idx.hunkStart[i]
		}
	}
	return -1
}

// fileAt is the index of the file owning a row, clamped to the stream.
func (idx streamIndex) fileAt(row int) int {
	if len(idx.rows) == 0 {
		return 0
	}
	return idx.rows[min(max(row, 0), len(idx.rows)-1)].file
}
