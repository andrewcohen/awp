package ui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
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
	// rowComment is one display line of a comment anchored to the line above it.
	rowComment
	// rowOrphanHeader and rowOrphan are the detached section at the end of the
	// stream, holding comments whose anchor could no longer be located. They are
	// shown rather than dropped: quietly losing a reviewer's note is worse than
	// showing it out of place.
	rowOrphanHeader
	rowOrphan
	// rowEditor is one display line of the open compose box, spliced in beneath
	// whatever it is attached to (see withEditor).
	rowEditor
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
	// comment indexes into the placed comment set for rowComment / rowOrphan
	// rows, and commentLine is which display line of that comment this row is.
	comment     int
	commentLine int
	// lastComment marks the rows of the final message in a conversation. That
	// message closes the block, so it carries the trailing pad row — the pad
	// cannot belong to every message or a thread would get two blank rows
	// between each pair.
	lastComment bool
	// collapsed marks a file divider whose body is hidden.
	collapsed bool
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
	// comments is the comment set this index placed, indexed by rowRef.comment.
	comments []review.Comment
	// width and wrap are the inputs this index was built for.
	width int
	wrap  bool
}

// buildStream indexes every row of the diff at the given content width.
// commentPlacer resolves comments to the row they attach under. Passed in so
// the geometry pass stays a pure function of its inputs.
type commentPlacer func(rows []rowRef) (placed map[int][]review.Comment, orphans []review.Comment)

// commentRowCount is how many display rows a comment occupies at this width.
// Delegates to commentRows so the count cannot drift from what is rendered.
//
// last means this is the final message of its conversation, which adds the
// block's closing pad row.
func commentRowCount(c review.Comment, width int, last bool) int {
	return len(commentRows(c, width, last))
}

// withComments interleaves comment rows beneath the lines they anchor to, and
// appends any that could not be placed as a detached section.
//
// Two passes rather than one: comments are located against the *diff* rows, so
// the diff geometry has to exist before placement can run. Inserting the comment
// rows afterwards keeps the placement logic ignorant of row offsets.
func withComments(idx streamIndex, place commentPlacer) streamIndex {
	if place == nil {
		return idx
	}
	placed, orphans := place(idx.rows)
	if len(placed) == 0 && len(orphans) == 0 {
		return idx
	}

	all := make([]review.Comment, 0, len(placed)+len(orphans))
	index := func(c review.Comment) int {
		all = append(all, c)
		return len(all) - 1
	}

	rows := make([]rowRef, 0, len(idx.rows))
	// Row indices shift as comment rows are inserted, so every recorded offset
	// has to be remapped rather than reused.
	shift := make([]int, len(idx.rows))
	for i, r := range idx.rows {
		shift[i] = len(rows)
		rows = append(rows, r)
		// placed[i] is a whole conversation — the parent followed by its replies —
		// so the last entry is the one that closes the block.
		group := placed[i]
		for n, c := range group {
			ci := index(c)
			last := n == len(group)-1
			for line := 0; line < commentRowCount(c, idx.width, last); line++ {
				rows = append(rows, rowRef{
					kind: rowComment, file: r.file, hunk: -1, line: -1,
					comment: ci, commentLine: line, lastComment: last,
				})
			}
		}
	}
	if len(orphans) > 0 {
		rows = append(rows, rowRef{kind: rowOrphanHeader, file: -1, hunk: -1, line: -1})
		// The detached section is a flat list, so only its final entry closes it.
		for n, c := range orphans {
			ci := index(c)
			last := n == len(orphans)-1
			for line := 0; line < commentRowCount(c, idx.width, last); line++ {
				rows = append(rows, rowRef{
					kind: rowOrphan, file: -1, hunk: -1, line: -1,
					comment: ci, commentLine: line, lastComment: last,
				})
			}
		}
	}

	out := idx
	out.rows = rows
	out.comments = all
	out.fileStart = remap(idx.fileStart, shift)
	out.hunkStart = remap(idx.hunkStart, shift)
	return out
}

// withEditor splices the compose box into the stream as `rows` display lines.
//
// The box is part of the geometry rather than an overlay or a docked panel, so
// it appears where the remark will: under the line, or at the foot of the thread
// it answers. That costs the splice below, but the alternative — floating the box
// over the stream — hides the code being commented on, which is the one thing
// that has to stay visible while writing about it.
//
// `replacing`, when it names a comment, is that comment being revised: the box
// takes over its rows instead of landing beneath them. Appending would render the
// saved text directly above a box holding the same words, which reads as a stale
// duplicate of the thing you are in the middle of changing. A new comment and a
// reply have nothing to stand in for, so they go under row `at`.
//
// Its height must be a constant (commentEditorRows), because geometry runs before
// anything is rendered. commentEditor.view guarantees that by truncating rather
// than wrapping its header and hint.
func withEditor(idx streamIndex, at, rows int, replacing string) streamIndex {
	if rows <= 0 || at < 0 || at >= len(idx.rows) {
		return idx
	}
	editorRows := func(under rowRef) []rowRef {
		out := make([]rowRef, 0, rows)
		for line := 0; line < rows; line++ {
			// The box inherits the row's file so the file list keeps pointing at
			// the file being commented on.
			out = append(out, rowRef{
				kind: rowEditor, file: under.file, hunk: -1, line: -1,
				comment: -1, commentLine: line,
			})
		}
		return out
	}
	dropFirst, dropLast := rowsOfComment(idx, replacing)
	out := make([]rowRef, 0, len(idx.rows)+rows)
	shift := make([]int, len(idx.rows))
	for i, r := range idx.rows {
		shift[i] = len(out)
		if dropFirst >= 0 && i >= dropFirst && i <= dropLast {
			// Dropped — the box stands in for these. It goes in at the first of
			// them, so it opens where the comment was rather than wherever the
			// cursor happens to be.
			if i == dropFirst {
				out = append(out, editorRows(r)...)
			}
			continue
		}
		out = append(out, r)
		if i == at && dropFirst < 0 {
			out = append(out, editorRows(r)...)
		}
	}
	res := idx
	res.rows = out
	res.fileStart = remap(idx.fileStart, shift)
	res.hunkStart = remap(idx.hunkStart, shift)
	return res
}

// rowsOfComment is the span of display rows one comment occupies, or (-1, -1)
// when it has none — an unplaceable anchor, or an id that is not in the stream.
// A comment's rows are contiguous: withComments emits them as one run.
func rowsOfComment(idx streamIndex, id string) (first, last int) {
	first, last = -1, -1
	if id == "" {
		return first, last
	}
	for i, r := range idx.rows {
		if r.kind != rowComment && r.kind != rowOrphan {
			continue
		}
		if r.comment < 0 || r.comment >= len(idx.comments) || idx.comments[r.comment].ID != id {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	return first, last
}

func remap(offsets []int, shift []int) []int {
	if offsets == nil {
		return nil
	}
	out := make([]int, 0, len(offsets))
	for _, o := range offsets {
		if o >= 0 && o < len(shift) {
			out = append(out, shift[o])
		}
	}
	return out
}

// collapsedSet reports whether a file's body is hidden. Collapse is a geometry
// input rather than a render-time skip: skipping rows at render would leave the
// row count disagreeing with what is drawn, which is exactly the desync the
// geometry/render split exists to prevent.
type collapsedSet func(path string) bool

func buildStream(files []diff.FileDiff, width int, wrap bool, collapsed collapsedSet) streamIndex {
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
		hidden := collapsed != nil && collapsed(pathOf(f))
		idx.rows = append(idx.rows, rowRef{kind: rowFileHeader, file: fi, hunk: -1, line: -1, collapsed: hidden})

		idx.meta[fi] = make([]hunkMeta, len(f.Hunks))
		if hidden {
			// The divider stays — it is the handle for un-collapsing, and it
			// still reports what is inside.
			continue
		}
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
