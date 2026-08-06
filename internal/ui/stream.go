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
	// rowReviewHeader and rowReview are the review-level section at the top of
	// the stream: remarks about the change as a whole, which anchor to no file.
	// Distinct from the detached section below, which means something else —
	// these are deliberately unanchored, those lost their anchor.
	rowReviewHeader
	rowReview
	// rowOrphanHeader and rowOrphan are the detached section at the end of the
	// stream, holding comments whose anchor could no longer be located. They are
	// shown rather than dropped: quietly losing a reviewer's note is worse than
	// showing it out of place.
	rowOrphanHeader
	rowOrphan
	// rowEditor is one display line of the open compose box, spliced in beneath
	// whatever it is attached to (see withEditor).
	rowEditor
	// rowCommentGap is the empty row between two conversations that landed on the
	// same line.
	//
	// Empty in the strong sense: no gutter, no bar, no painted columns. Each card
	// already pads itself top and bottom, but those pad rows carry the kind-coloured
	// bar, so two adjacent conversations ran into each other as one block with a
	// continuous left edge. A row with nothing on it is what makes the break read as
	// a break.
	rowCommentGap
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
	// paired marks a side-by-side row, on which oldLine and newLine name the two
	// source lines it shows — indices into the hunk's Lines, -1 where that column
	// is empty.
	//
	// A flag rather than a -1 sentinel on the indices. rowRef is built in a dozen
	// places, most of them not about diff lines at all, and a zero oldLine would
	// read as "line 0 of the hunk" in every one of them that forgot to set it.
	// False by default means no call site can claim a pair by omission.
	paired  bool
	oldLine int
	newLine int
	// comment indexes into the placed comment set for comment rows of any
	// section (see isCommentRow), and commentLine is which display line of that
	// comment this row is.
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
//
// prefixWidth is the unified layout's, and it is cached here because it is
// per-hunk and the geometry pass exists to not recompute per-hunk facts per row
// per frame. The side-by-side cell geometry is deliberately *not* cached
// alongside it: it depends on the width the frame is actually being drawn at,
// which the host can change without rebuilding the index, so a stored copy would
// be a number that goes quietly stale. splitGeometry derives it from oldWidth and
// newWidth at render time instead.
type hunkMeta struct {
	oldWidth    int
	newWidth    int
	prefixWidth int
}

// sideBySideDivider separates the two cells. A space either side of the rule, so
// content never butts against it.
const sideBySideDivider = " │ "

// sideBySideMinWidth is the narrowest pane the split is offered on.
//
// Two columns of thirty is not a diff, it is two truncated diffs. Refusing is
// better than falling back to unified, which would leave `|` looking broken at
// the one width where the reader most needs to be told what to do instead.
const sideBySideMinWidth = 100

// splitGeometry divides a pane between two cells.
func splitGeometry(width, oldWidth, newWidth int) (colWidth, oldPrefix, newPrefix int) {
	// Each cell shows one number and one gutter glyph: "%*s " + glyph + " ".
	oldPrefix = oldWidth + 3
	newPrefix = newWidth + 3
	colWidth = max(1, (width-len([]rune(sideBySideDivider)))/2)
	return colWidth, oldPrefix, newPrefix
}

// linePair is one row of a side-by-side hunk: an index into the hunk's lines for
// each column, -1 where that column has nothing to show.
type linePair struct{ old, new int }

// pairHunkLines lays a hunk's lines out two abreast.
//
// A maximal run of removals is zipped against the additions immediately
// following it, which is how a rewritten line comes to sit opposite the thing it
// was rewritten into — the whole point of the layout. Runs of unequal length
// leave the shorter side's surplus rows with one cell empty. Anything else is a
// context line, which exists on both sides and so pairs with itself.
//
// Removals before additions is the order git emits a change block in. A `+` run
// reached first is therefore not part of a pair: it takes the right column alone
// rather than being held back to see whether removals follow, since guessing
// wrong would put a line opposite something it has nothing to do with.
func pairHunkLines(lines []diff.HunkLine) []linePair {
	out := make([]linePair, 0, len(lines))
	for i := 0; i < len(lines); {
		switch lines[i].Type {
		case '-':
			dels := i
			for i < len(lines) && lines[i].Type == '-' {
				i++
			}
			adds := i
			for i < len(lines) && lines[i].Type == '+' {
				i++
			}
			nDel, nAdd := adds-dels, i-adds
			for k := range max(nDel, nAdd) {
				p := linePair{old: -1, new: -1}
				if k < nDel {
					p.old = dels + k
				}
				if k < nAdd {
					p.new = adds + k
				}
				out = append(out, p)
			}
		case '+':
			out = append(out, linePair{old: -1, new: i})
			i++
		default:
			out = append(out, linePair{old: i, new: i})
			i++
		}
	}
	return out
}

// anchorLine is the source line a row is *about* — what a comment on it attaches
// to and what a range covering it selects.
//
// The new side when the row has one, the old side otherwise. Not a new rule: it
// is what a mixed `v` range already does, since a removed line has no new-side
// number to anchor to and an added one has no old. Side-by-side makes an existing
// rule visible rather than introducing a second one, which is why this is the
// only place either layout asks the question.
func (r rowRef) anchorLine() int {
	if !r.paired {
		return r.line
	}
	if r.newLine >= 0 {
		return r.newLine
	}
	return r.oldLine
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
	// width, wrap and sideBySide are the inputs this index was built for.
	width      int
	wrap       bool
	sideBySide bool
}

// buildStream indexes every row of the diff at the given content width.
// commentPlacer resolves comments to the row they attach under. Passed in so
// the geometry pass stays a pure function of its inputs.
type commentPlacer func(rows []rowRef) commentPlacement

// commentPlacement is where each comment in the set ended up. A struct rather
// than a tuple of return values: the three destinations are one answer, and
// every consumer wants all of them.
type commentPlacement struct {
	// byRow maps a diff row to the conversation annotating the line it shows.
	byRow map[int][]review.Comment
	// review are remarks about the change as a whole — no file anchor at all —
	// shown in their own section above the first file.
	review []review.Comment
	// orphans are remarks that name a file but could no longer be located in it,
	// shown in the detached section at the foot.
	orphans []review.Comment
}

func (p commentPlacement) empty() bool {
	return len(p.byRow) == 0 && len(p.review) == 0 && len(p.orphans) == 0
}

// isCommentRow reports whether a row is part of a comment block, wherever that
// block sits: beneath the line it annotates, in the review-level section at the
// top, or in the detached section at the foot.
//
// Everything acting on "the comment at the cursor" — the index, reply, edit,
// delete, the cursorline's fill — has to accept all three, or a whole section
// becomes something you can look at but not touch.
func isCommentRow(k rowKind) bool {
	return k == rowComment || k == rowReview || k == rowOrphan
}

// commentRowCount is how many display rows a comment occupies at this width.
// Delegates to commentRows so the count cannot drift from what is rendered.
//
// last means this is the final message of its conversation, which adds the
// block's closing pad row.
func commentRowCount(c review.Comment, width int, last, collapsed bool) int {
	return len(commentRows(c, width, last, collapsed))
}

// commentFolder reports whether a comment renders folded to one line. Passed into
// the geometry pass the same way commentPlacer is, so counting rows and drawing
// them ask one function and cannot disagree about how tall a thread is.
type commentFolder func(review.Comment) bool

// folds is the predicate with a nil check, since a stream can be built without
// one (nothing folds then).
func (f commentFolder) folds(c review.Comment) bool {
	return f != nil && f(c)
}

// withComments interleaves comment rows beneath the lines they anchor to, with
// the review-level remarks in a section above the first file and the ones that
// could not be placed in a section below the last.
//
// Two passes rather than one: comments are located against the *diff* rows, so
// the diff geometry has to exist before placement can run. Inserting the comment
// rows afterwards keeps the placement logic ignorant of row offsets.
func withComments(idx streamIndex, place commentPlacer, folded commentFolder) streamIndex {
	if place == nil {
		return idx
	}
	p := place(idx.rows)
	if p.empty() {
		return idx
	}

	all := make([]review.Comment, 0, len(p.byRow)+len(p.review)+len(p.orphans))
	index := func(c review.Comment) int {
		all = append(all, c)
		return len(all) - 1
	}

	rows := make([]rowRef, 0, len(idx.rows))
	// Review-level remarks lead the stream: they are about the change as a whole,
	// so they belong before the first thing they are about rather than after
	// everything.
	rows = appendCommentSection(rows, p.review, rowReviewHeader, rowReview, idx.width, index, folded)
	// Row indices shift as comment rows are inserted, so every recorded offset
	// has to be remapped rather than reused.
	shift := make([]int, len(idx.rows))
	for i, r := range idx.rows {
		shift[i] = len(rows)
		rows = append(rows, r)
		// byRow[i] is a whole conversation — the parent followed by its replies —
		// so the last entry is the one that closes the block.
		group := p.byRow[i]
		for n, c := range group {
			ci := index(c)
			// The last message of the *conversation*, not of the group: several
			// conversations can be anchored to one line, and each closes its own card.
			last := n == len(group)-1 || group[n+1].ReplyTo == ""
			if n > 0 && c.ReplyTo == "" {
				// A new conversation starts here, so break from the one above it.
				rows = append(rows, rowRef{kind: rowCommentGap, file: r.file, hunk: -1, line: -1})
			}
			for line := 0; line < commentRowCount(c, idx.width, last, folded.folds(c)); line++ {
				rows = append(rows, rowRef{
					kind: rowComment, file: r.file, hunk: -1, line: -1,
					comment: ci, commentLine: line, lastComment: last,
				})
			}
		}
	}
	rows = appendCommentSection(rows, p.orphans, rowOrphanHeader, rowOrphan, idx.width, index, folded)

	out := idx
	out.rows = rows
	out.comments = all
	out.fileStart = remap(idx.fileStart, shift)
	out.hunkStart = remap(idx.hunkStart, shift)
	return out
}

// appendCommentSection emits a headed run of conversations — the review-level
// section and the detached section are the same shape, differing only in their
// header and row kind.
//
// Grouped through review.Threads rather than emitted flat: closing only the
// section's final entry runs every thread in it into the next one, and a reply
// whose parent is in the same section is not necessarily adjacent to it in the
// comment set. Threads answers both — parents in order with their replies
// gathered, a reply whose parent is absent standing alone.
func appendCommentSection(
	rows []rowRef,
	cs []review.Comment,
	header, body rowKind,
	width int,
	index func(review.Comment) int,
	folded commentFolder,
) []rowRef {
	if len(cs) == 0 {
		return rows
	}
	rows = append(rows, rowRef{kind: header, file: -1, hunk: -1, line: -1})
	for i, th := range review.Threads(cs) {
		if i > 0 {
			// Same break the anchored conversations get: without it, a section of several
			// detached threads read as one wall of text with a single left edge.
			rows = append(rows, rowRef{kind: rowCommentGap, file: -1, hunk: -1, line: -1})
		}
		group := append([]review.Comment{th.Parent}, th.Replies...)
		for n, c := range group {
			ci := index(c)
			last := n == len(group)-1
			for line := 0; line < commentRowCount(c, width, last, folded.folds(c)); line++ {
				rows = append(rows, rowRef{
					kind: body, file: -1, hunk: -1, line: -1,
					comment: ci, commentLine: line, lastComment: last,
				})
			}
		}
	}
	return rows
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
		if !isCommentRow(r.kind) {
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

// lineNumbers is a hunk's per-line old/new numbering, resolved once.
//
// Its own pass because side-by-side needs a line's number out of order — the
// addition opposite a removal is several lines further down the hunk — and
// walking forward to find it per row would put the O(hunk) work back that the
// geometry pass exists to do once.
func hunkLineNumbers(h diff.Hunk) []rowRef {
	out := make([]rowRef, len(h.Lines))
	oldNo, newNo := h.OldStart, h.NewStart
	for li, l := range h.Lines {
		switch l.Type {
		case '+':
			out[li].newNo = newNo
			newNo++
		case '-':
			out[li].oldNo = oldNo
			oldNo++
		default:
			out[li].oldNo, out[li].newNo = oldNo, newNo
			oldNo++
			newNo++
		}
	}
	return out
}

func buildStream(files []diff.FileDiff, width int, wrap bool, collapsed collapsedSet) streamIndex {
	return buildStreamLayout(files, width, wrap, false, collapsed)
}

// buildStreamLayout is buildStream with the layout named. Side-by-side never
// wraps (see the spec's decision 1): one line-pair is always exactly one row, so
// the wrap argument is ignored rather than half-honoured.
func buildStreamLayout(files []diff.FileDiff, width int, wrap, sideBySide bool, collapsed collapsedSet) streamIndex {
	if sideBySide {
		wrap = false
	}
	return buildStreamRows(files, width, wrap, sideBySide, collapsed)
}

func buildStreamRows(files []diff.FileDiff, width int, wrap, sideBySide bool, collapsed collapsedSet) streamIndex {
	// Row counts must be right even before the first size message, or
	// scrolling is dead until a resize. At width 1 nothing wraps, so the
	// geometry is one row per line — correct, just unreadable, which is moot
	// at that size.
	width = max(1, width)
	idx := streamIndex{
		width:      width,
		wrap:       wrap,
		sideBySide: sideBySide,
		fileStart:  make([]int, 0, len(files)),
		meta:       make([][]hunkMeta, len(files)),
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

			nums := hunkLineNumbers(h)
			if sideBySide {
				for _, p := range pairHunkLines(h.Lines) {
					ref := rowRef{
						kind: rowLine, file: fi, hunk: hi,
						// line is the row's anchor, so everything that already reads it —
						// placement, the range gesture, the agent prompt's context — keeps
						// working without knowing which layout drew the row.
						line:    max(p.old, p.new),
						paired:  true,
						oldLine: p.old,
						newLine: p.new,
					}
					if p.new >= 0 {
						ref.line = p.new
						ref.newNo = nums[p.new].newNo
					}
					if p.old >= 0 {
						ref.oldNo = nums[p.old].oldNo
						if p.new < 0 {
							ref.line = p.old
						}
					}
					idx.rows = append(idx.rows, ref)
				}
				continue
			}

			avail := width - meta.prefixWidth
			for li, l := range h.Lines {
				ref := rowRef{
					kind: rowLine, file: fi, hunk: hi, line: li,
					oldNo: nums[li].oldNo, newNo: nums[li].newNo,
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
