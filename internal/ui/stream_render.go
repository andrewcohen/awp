package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

// renderStreamRow styles one row of the diff stream. Called only for rows
// currently on screen — see the note in stream.go on why geometry and
// rendering are separate.
// renderStreamRowAt renders the stream row at index i, including the selection
// prefix slot. Every row reserves the prefix columns whether or not it is the
// cursor, so content stays aligned down the pane.
// cachedStreamRow is renderStreamRowAt with the result kept between frames.
//
// This is what makes scrolling cheap: a keypress changes which rows are on
// screen, but the rows themselves are identical to the ones the last frame drew —
// only the two that gained and lost the cursorline are actually new work.
func (m Model) cachedStreamRow(i, width int) string {
	if m.cache == nil {
		return m.renderStreamRowAt(i, width)
	}
	// The compose box is never cached. What makes caching safe here is that a
	// cached row is a pure function of (the stream, the width, the selection) and
	// the stream is replaced in exactly one place, which drops the cache — but the
	// box's content is the text being typed *right now*, and a keystroke into it
	// rebuilds nothing. Cached, its rows were served from the first frame forever:
	// the box kept showing its "comment…" placeholder while the textarea filled up
	// underneath, and the text only appeared once something else forced a rebuild.
	//
	// Skipped rather than keyed on the body: a box is at most commentEditorRows
	// rows and only exists while you are typing into it, so there is no frame cost
	// worth the risk of a key that has to be remembered to include every field the
	// box can draw.
	if m.stream.rows[i].kind == rowEditor {
		return m.renderStreamRowAt(i, width)
	}
	key := m.rowKeyAt(i, width)
	if out, ok := m.cache.rows[key]; ok {
		return out
	}
	out := m.renderStreamRowAt(i, width)
	m.cache.rows[key] = out
	return out
}

// rowKeyAt is everything row i's appearance depends on that the stream itself does
// not fix. Separate from cachedStreamRow so a new dependency can be tested as a
// key that changes, which is the property that matters — a stale entry shows the
// previous frame's styling with nothing to hint that it is wrong.
func (m Model) rowKeyAt(i, width int) rowKey {
	r := m.stream.rows[i]
	return rowKey{
		row:   i,
		width: width,
		// Both halves of the selection treatment, because both vary independently
		// of the row's content: the bar depends on the selection, and the band on
		// the selection *and* the focus. A row rendered with either must not be
		// served to a frame that wants it without — which is why this is not simply
		// "is the cursor here".
		selected: m.rowSelected(i),
		band:     m.rowBanded(i),
		// The ranged-comment bar and its colour. `tab` in the compose box recolours a
		// range without moving a row, so a key without this would keep serving the
		// old hue.
		mark: m.marks[i],
		// Whether this row is a file divider wearing the selection hue. It follows
		// filesCursor and the focus, and neither goes through rebuildStream —
		// seekToFile moves the cursor into another file without touching the stream,
		// so without this the old file's divider stayed highlighted. Left false for
		// every other row rather than derived from the file alone, so moving focus
		// does not split the cache entry of every row in the current file.
		fileRule: r.kind == rowFileHeader && m.fileRuleActive(r.file),
		hscroll:  m.hunkHScroll,
	}
}

// selectionBarStyle is the `┃` marker's hue on a selected row.
//
// Banded means the diff pane has the keyboard, so the marker is the app-wide
// selection treatment. Unbanded it is the same row seen from another pane: the
// marker stays, because it is where the keys go back to, but muted — in the
// selection hue it competed with the pane actually being driven, and it slid down
// the diff as the file list or comment index seeked.
//
// Factored out so the choice is assertable: lipgloss strips colour with no TTY, so
// it cannot be read back out of rendered output.
func selectionBarStyle(band bool) lipgloss.Style {
	if band {
		return styleSelectedCursor
	}
	return styleSelectedIdle
}

func (m Model) renderStreamRowAt(i, width int) string {
	// The selection is the cursor row, or every row of a visual range. The
	// cursorline band is painted only while this pane has the keyboard: two
	// full-width bands at once — one here, one in the file list or comment index —
	// leaves no way to tell which selection the keys are actually driving. The ┃
	// bar stays either way, so the rows you will come back to are still marked —
	// muted while the keyboard is elsewhere, see selectionBarStyle.
	selected, band := m.rowSelected(i), m.rowBanded(i)
	kind := m.stream.rows[i].kind
	mark, marked := m.rangeMark(i)
	prefix := selectionPrefixBlank
	switch {
	case selected:
		prefix = selectionBarStyle(band).Render(selectionPrefixBar)
	// A ranged comment's own bar, in its kind's hue — so the block a remark is
	// about is visible while reading, not only stated in its header. Below the
	// cursor in this switch because the cursor is one row and the range's other
	// rows still carry the mark, which is enough to read it as continuous.
	case marked:
		prefix = kindStyles(mark).Render(selectionPrefixBar)
	case isCommentRow(kind):
		// Left as reserved blank columns. They used to be painted so the block's
		// wash reached the pane's left edge; with no wash there is nothing for them
		// to continue, and the gutter's ▌ sits just inside them where the block's
		// edge belongs.
		prefix = styleCommentPlain.Render(selectionPrefixBlank)
	}
	body := m.renderStreamRow(m.stream.rows[i], width-lipgloss.Width(selectionPrefixBlank), band)
	row := prefix + body

	// The cursorline is extended to the full pane width so it reads as a band rather
	// than as highlighting only the text. A syntax-painted added or removed line is
	// filled for the same reason: highlighting spends the foreground on the lexer, so
	// the change type lives in the background, and a background that stops where the
	// code does is not a property of the line. On the cursor's row the two fills are
	// the same thing — the brighter variant of the tint.
	fill, filled := styleCursorFill, band
	if t, ok := m.paintedLine(i); ok && (t == '+' || t == '-') {
		fill, filled = paintTable().line(t, band).fill, true
	}
	if !filled {
		return row
	}
	if pad := width - lipgloss.Width(row); pad > 0 {
		row += fill.Render(strings.Repeat(" ", pad))
	}
	return row
}

// paintedLine is the change type of row i when it is a syntax-painted code line,
// and false otherwise.
//
// Asked by the row filler rather than derived inside the line renderer, because
// the fill is the row's business: renderStreamRow is handed a width and returns
// text of whatever length it needs, and the padding out to the pane's edge happens
// one level up.
//
// False for a paired row. Side by side, a row is an old line and a new one with
// different change types and no single answer — and it needs none, because each
// cell is already padded to its own column width.
func (m Model) paintedLine(i int) (byte, bool) {
	if m.hl == nil || i < 0 || i >= len(m.stream.rows) {
		return 0, false
	}
	r := m.stream.rows[i]
	if r.kind != rowLine || r.paired {
		return 0, false
	}
	h, _, ok := m.stream.hunkAt(m.filtered, r)
	if !ok || r.line < 0 || r.line >= len(h.Lines) {
		return 0, false
	}
	l := h.Lines[r.line]
	if len(m.hl.spansFor(lexPath(m.filtered[r.file]), l)) == 0 {
		return 0, false
	}
	return l.Type, true
}

func (m Model) renderStreamRow(r rowRef, width int, cursor bool) string {
	if width <= 0 {
		return ""
	}
	switch r.kind {
	case rowSpacer:
		return ""
	case rowCommentGap:
		// Nothing, and nothing is the point: the prefix switch above paints the
		// reserved columns for comment rows, and this kind is deliberately not one —
		// so the two columns stay unpainted and the break is a real break.
		return ""
	case rowFileHeader:
		// The divider is already a full-width band, so a cursorline behind it
		// would add nothing; the bar marks it.
		return m.renderStreamFileHeader(r, width)
	case rowHunkHeader:
		h, _, ok := m.stream.hunkAt(m.filtered, r)
		if !ok {
			return ""
		}
		header := fmt.Sprintf(" @@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		style := styleHunkHeader
		if cursor {
			style = styleHunkHeaderCursor
		}
		return style.Width(width).Render(header)
	case rowLine:
		return m.renderStreamLine(r, width, cursor)
	case rowComment, rowReview, rowOrphan:
		if r.comment < 0 || r.comment >= len(m.stream.comments) {
			return ""
		}
		lines := m.commentBlock(r, width, cursor)
		if r.commentLine < 0 || r.commentLine >= len(lines) {
			return ""
		}
		return lines[r.commentLine]
	case rowReviewHeader:
		// Accent rather than the detached section's warning yellow: a remark about
		// the change as a whole is ordinary, where a lost anchor wants attention.
		return styleReviewHeader.Width(width).Render(" review summary")
	case rowReviewEmpty:
		// Says what the section is for and how to fill it. The header alone above a
		// file divider reads as a section that failed to render.
		return styleReviewEmpty.Width(width).Render("   nothing yet — c to say something about the whole change")
	case rowOrphanHeader:
		return styleOrphanHeader.Width(width).Render(" detached comments — their anchor could not be found")
	case rowEditor:
		lines := m.editor.lines(width)
		if r.commentLine < 0 || r.commentLine >= len(lines) {
			return ""
		}
		return lines[r.commentLine]
	}
	return ""
}

// renderStreamFileHeader is the divider that opens each file's section of the
// stream: a rule drawn the full width of the pane with the filename set into
// it. In a continuous scroll the file boundary is the main landmark, so it has
// to be unmissable — a badge and a path alone read as just another row.
//
// The rule is `═` rather than `─` because the pane is already inside a rounded
// `─` border; a single rule reads as more border instead of a section break.
func (m Model) renderStreamFileHeader(r rowRef, width int) string {
	if r.file < 0 || r.file >= len(m.filtered) {
		return ""
	}
	f := m.filtered[r.file]
	current := m.fileRuleActive(r.file)
	ruleStyle, baseStyle := fileRuleStyles(current)

	lead := ruleStyle.Render(strings.Repeat(fileRuleGlyph, fileRuleLead) + " ")
	// Never banded: the divider is already a full-width rule, so a cursorline
	// behind it would add nothing (see renderStreamRow).
	badge := statusBadge(f.Status, current, false)
	summary := fmt.Sprintf(" (%d hunk%s)", len(f.Hunks), plural(len(f.Hunks)))
	if r.collapsed {
		// A collapsed file still has to say what is inside it, or the divider
		// becomes a wall you have to open to see past.
		//
		// "reviewed" is on it and "folded" is not. The mark is a claim about the file
		// that outlives the view and has to be visible; being folded is a fact the
		// reader is looking at — the body is not there — so saying it spends a word on
		// what the divider already is. What matters is that a merely folded file does
		// not wear the badge, which is the whole reason the two came apart.
		summary = fmt.Sprintf(" %s%d hunk%s, %d line%s hidden",
			reviewedBadge(m.isReviewed(pathOf(f))),
			len(f.Hunks), plural(len(f.Hunks)), countChangedLines(f), plural(countChangedLines(f)))
	}
	meta := styleMuted.Render(summary)
	reserved := lipgloss.Width(lead) + lipgloss.Width(badge) + 1 + lipgloss.Width(meta)
	label := renderPathWith(diff.DisplayPath(f), max(10, width-reserved), ruleStyle, baseStyle, styleMuted)

	head := lead + badge + " " + label + meta
	// Fill whatever is left with the rule, so every file divider spans the
	// pane regardless of how long its path is.
	if fill := width - lipgloss.Width(head) - 1; fill > 0 {
		head += " " + ruleStyle.Render(strings.Repeat(fileRuleGlyph, fill))
	}
	return truncateStyled(head, width)
}

// fileRuleActive reports whether file i's divider should carry the selection hue.
//
// Only while the diff pane holds the keyboard. A full-width yellow rule is the
// loudest thing this pane draws, and keyed off filesCursor alone it jumped from
// divider to divider as the file list was scanned — motion in a pane the keys were
// not driving, next to the file list's own highlight on the same file. The `┃` bar
// still marks the cursor's row, so nothing is lost by letting the rule go quiet.
func (m Model) fileRuleActive(file int) bool {
	return m.focus == FocusHunks && file == m.filesCursor
}

// fileRuleStyles picks the divider's hue: the accent normally, the selection
// hue for the file the cursor is in while the diff pane is focused.
func fileRuleStyles(current bool) (rule, base lipgloss.Style) {
	if current {
		return styleFileRuleCurrent, styleFileRuleCurBase
	}
	return styleFileRule, styleFileRuleBase
}

// renderStreamLine renders one diff line, or one hard-wrapped slice of it.
// The gutter — line numbers plus the +/- marker — is only drawn on a line's
// first row, so continuations sit under the code and the gutter column stays
// readable. The horizontal pan applies here, to the visible row, rather than
// during layout.
func (m Model) renderStreamLine(r rowRef, width int, cursor bool) string {
	h, meta, ok := m.stream.hunkAt(m.filtered, r)
	if !ok || r.line < 0 || r.line >= len(h.Lines) {
		return ""
	}
	if r.paired {
		return m.renderSplitLine(h, meta, r, width, cursor)
	}
	l := h.Lines[r.line]

	avail := width - meta.prefixWidth

	added, deleted, context := styleAdded, styleDeleted, styleContext
	if cursor {
		added, deleted, context = styleAddedCursor, styleDeletedCursor, styleContextCursor
	}
	base := context
	switch l.Type {
	case '+':
		base = added
	case '-':
		base = deleted
	}

	// Two orders, because the two treatments need opposite ones.
	//
	// Unpainted — the path this pane has always taken — cuts the plain text and
	// styles it once, which is why every width calculation here is ANSI-free.
	//
	// Painted has to style the whole line first, because the spans are byte offsets
	// into the whole line. visibleSlice is ANSI-aware, so cutting after is safe; the
	// alternative is re-deriving in bytes where a wrap segment or a pan starts, which
	// is a second copy of arithmetic that already exists here and would have to be
	// kept in step with it by hand.
	spans := m.hl.spansFor(lexPath(m.filtered[r.file]), l)
	var content string
	if len(spans) > 0 {
		content = m.visibleSlice(paintCode(l.Content, spans, l.Type, cursor), r.seg, avail)
	} else {
		content = base.Render(m.visibleSlice(l.Content, r.seg, avail))
	}

	numberStyle := styleLineNo
	if cursor {
		// The bar marks the row; tinting the numbers too makes the cursor
		// readable when the bar is at the edge of vision.
		numberStyle = styleCursorLineNo
	}
	gutter := string(l.Type)
	gutterStyle := context
	switch l.Type {
	case '+':
		gutterStyle = added
	case '-':
		gutterStyle = deleted
	default:
		gutter = "│"
	}
	// Painted, the columns either side of the code come from the paint table so they
	// carry the line's background as well as their own hue — the backwash is a
	// property of the whole row, and starting it at the code left the gutter as an
	// untinted notch.
	padStyle := styleCursorFill
	if len(spans) > 0 {
		lp := paintTable().line(l.Type, cursor)
		numberStyle, gutterStyle, padStyle = lp.number, lp.glyph, lp.fill
	}

	// Continuation rows carry no gutter, just its width as padding — in the line's own
	// background, so a wrapped change is one field rather than a tinted body under an
	// untinted indent.
	if r.seg > 0 {
		pad := strings.Repeat(" ", meta.prefixWidth)
		if cursor || len(spans) > 0 {
			pad = padStyle.Render(pad)
		}
		return truncateStyled(pad+content, width)
	}
	numbers := fmt.Sprintf("%*s %*s ", meta.oldWidth, lineNoText(r.oldNo), meta.newWidth, lineNoText(r.newNo))
	prefix := numberStyle.Render(numbers) + gutterStyle.Render(gutter+" ")
	return truncateStyled(prefix+content, width)
}

// renderSplitLine draws one side-by-side row: the old text on the left, the new
// on the right, a rule between them.
//
// Each cell goes through splitCell, which is the same number + gutter + content
// shape the unified renderer uses. Deliberately one function per cell rather than
// two layouts sharing nothing: the gutter glyph, the number style and the cursor
// treatment are design-system decisions, and two copies of them would drift the
// moment either layout was touched.
func (m Model) renderSplitLine(h diff.Hunk, meta hunkMeta, r rowRef, width int, cursor bool) string {
	// Derived from the width this frame is being drawn at, not from one stored when
	// the index was built: the host hands the viewer a width per frame and can
	// narrow it without a rebuild, and a cell sized to a stale width leaves the
	// divider off centre or pushes the right column past the border.
	colWidth, oldPrefix, newPrefix := splitGeometry(width, meta.oldWidth, meta.newWidth)
	path := lexPath(m.filtered[r.file])
	left := m.splitCell(h, path, r.oldLine, r.oldNo, oldPrefix, colWidth, cursor)
	right := m.splitCell(h, path, r.newLine, r.newNo, newPrefix, colWidth, cursor)

	rule := styleLineNo.Render(sideBySideDivider)
	if cursor {
		rule = styleCursorLineNo.Render(sideBySideDivider)
	}
	return truncateStyled(left+rule+right, width)
}

// splitCell is one column of a paired row, padded to exactly colWidth.
//
// An empty cell — the left of an addition, the right of a deletion — is spaces,
// not a repeat of the other side. Echoing the counterpart is what a unified diff
// already does; the whole reason to split is that "nothing was here" and "this
// is unchanged" are different facts and should not look alike.
func (m Model) splitCell(h diff.Hunk, path string, li, no, prefixWidth, colWidth int, cursor bool) string {
	avail := max(1, colWidth-prefixWidth)
	if li < 0 || li >= len(h.Lines) {
		blank := strings.Repeat(" ", colWidth)
		if cursor {
			return styleCursorFill.Render(blank)
		}
		return blank
	}
	l := h.Lines[li]

	added, deleted, context := styleAdded, styleDeleted, styleContext
	if cursor {
		added, deleted, context = styleAddedCursor, styleDeletedCursor, styleContextCursor
	}
	body, gutterStyle := context, context
	gutter := "│"
	switch l.Type {
	case '+':
		body, gutterStyle, gutter = added, added, "+"
	case '-':
		body, gutterStyle, gutter = deleted, deleted, "-"
	}

	// Painted whole and cut after, for the reason renderStreamLine gives. The cutters
	// below are ANSI-aware in both directions, and ansi.StringWidth measures the
	// painted text correctly — what the old comment here warned against was measuring
	// with something that is not.
	spans := m.hl.spansFor(path, l)
	text := l.Content
	if len(spans) > 0 {
		text = paintCode(text, spans, l.Type, cursor)
	}
	if m.hunkHScroll > 0 {
		text = ansi.TruncateLeft(text, m.hunkHScroll, "")
	}
	text = ansi.Truncate(text, avail, "")
	pad := strings.Repeat(" ", max(0, avail-ansi.StringWidth(text)))

	numberStyle := styleLineNo
	if cursor {
		numberStyle = styleCursorLineNo
	}
	numbers := fmt.Sprintf("%*s ", max(0, prefixWidth-3), lineNoText(no))
	if len(spans) == 0 {
		// One Render over text and its padding together, as before.
		return numberStyle.Render(numbers) + gutterStyle.Render(gutter+" ") + body.Render(text+pad)
	}
	// Painted: the columns carry the line's background too, and so does the padding —
	// otherwise the fill stops where the code happens to end, which reads as a
	// rendering fault.
	lp := paintTable().line(l.Type, cursor)
	prefix := lp.number.Render(numbers) + lp.glyph.Render(gutter+" ")
	if pad != "" {
		pad = lp.fill.Render(pad)
	}
	return prefix + text + pad
}

// A comment block is rendered once, not once per row of it and not once per
// frame.
//
// commentLines styles a conversation's *whole* block and the caller keeps one
// line, so a block H rows tall costs H work for each of its H visible rows —
// quadratic in the length of the conversation, and a screen full of comment rows
// is exactly what `T` → all threads produces. Measured at 20ms a frame against
// 1ms over code, felt the moment you hold a scroll key.
//
// The cache lives until the comment set changes, which is rebuildStream and only
// rebuildStream: it is the one place m.stream — and with it m.stream.comments —
// is replaced, so clearing there covers every path that can invalidate an entry.
// A stale comment body is the worst thing this surface could show, and that
// single choke point is what makes it impossible.
//
// It was originally scoped to one frame, which was safe but not enough: parked
// inside a 205-row conversation, a frame still wrapped and styled all 205 rows to
// display 46 of them, and 3.3MB of garbage per frame turned into GC pauses that
// read as stutter. Surviving frames only became worthwhile once SetSize stopped
// rebuilding on every one of them — before that, the cache was cleared per frame
// anyway.
type blockKey struct {
	comment int
	width   int
	cursor  bool
	last    bool
}

// rowKey identifies a rendered stream row. Everything a row's appearance depends
// on that is *not* fixed by the stream itself: the pane width, whether the
// cursorline band is on it, and the horizontal pan. The stream's own contents are
// covered by the cache being dropped whenever it is rebuilt.
type rowKey struct {
	row      int
	width    int
	selected bool
	band     bool
	// mark is the kind of the ranged comment marking this row, empty for none.
	mark review.Kind
	// fileRule is set on a file divider drawn in the selection hue.
	fileRule bool
	hscroll  int
}

// leftKey identifies a rendered left column, which is one string because the
// whole column is rebuilt together.
type leftKey struct {
	width, height   int
	files, comments int
	focus           Focus
	entries         int
	// hidden is the `\` toggle: the whole column is dropped.
	hidden bool
	// hiddenThreads is the count the index's header reports. In the key because the
	// header is text this cache holds, and `T` changes it — the same rule rowKey's
	// fileRule exists for. It nearly always moves `entries` too, which would have
	// masked this until the one arrangement where it does not.
	hiddenThreads int
}

// renderCache holds rendered fragments between frames.
//
// Every lipgloss Style.Render allocates an ansi parser buffer of about 20KB
// regardless of how little text it is given, and a frame makes upwards of eighty
// of them — one per stream row plus a handful per file-list row. That was 1.7MB of
// garbage per frame, and at scrolling speed the GC pauses it caused are what was
// left of the stutter after the geometry fixes.
//
// What makes caching safe here is that everything cached is a pure function of
// (the stream, the width, the cursor) and the stream is replaced in exactly one
// place — rebuildStream — which drops the whole cache. Anything that changes what
// a row looks like either goes through there or is part of a key.
type renderCache struct {
	blocks map[blockKey][]string
	rows   map[rowKey]string
	left   struct {
		key leftKey
		out string
		ok  bool
	}
}

func newRenderCache() *renderCache {
	return &renderCache{
		blocks: map[blockKey][]string{},
		rows:   map[rowKey]string{},
	}
}

// drop forgets everything. Called from rebuildStream, the one place the stream
// these fragments were rendered from is replaced.
func (c *renderCache) drop() {
	if c == nil {
		return
	}
	clear(c.blocks)
	clear(c.rows)
	c.left.ok = false
}

// commentBlock is the rendered lines of the conversation this row belongs to.
// Fold state is not in the cache key. It cannot change without the stream being
// rebuilt — folding a thread and resolving one both go through rebuildStream,
// which drops the cache — and the block's height depends on it, so a stale entry
// would be a geometry mismatch rather than a cosmetic one.
func (m Model) commentBlock(r rowRef, width int, cursor bool) []string {
	c := m.stream.comments[r.comment]
	collapsed := m.threadCollapsed(c)
	if m.cache == nil {
		// No cache installed (a bare Model, or a single row rendered directly by a
		// test): correct, just not memoized.
		return commentLines(c, width, cursor, r.lastComment, collapsed)
	}
	key := blockKey{comment: r.comment, width: width, cursor: cursor, last: r.lastComment}
	if lines, ok := m.cache.blocks[key]; ok {
		return lines
	}
	lines := commentLines(c, width, cursor, r.lastComment, collapsed)
	m.cache.blocks[key] = lines
	return lines
}

// renderStreamPanel draws the visible window of the stream.
func (m Model) renderStreamPanel(width, height int) string {
	border := styleNormalBorder
	if m.focus == FocusHunks {
		border = styleFocusBorder
	}
	if len(m.filtered) == 0 {
		return panelBox([]string{styleDim.Render(" No changes")}, width, height, border)
	}
	if len(m.stream.rows) == 0 {
		return panelBox([]string{styleDim.Render(" rename-only, binary, or empty diff body")}, width, height, border)
	}

	contentWidth := width - 4
	rows := make([]string, 0, height)
	end := min(len(m.stream.rows), m.streamScroll+height)
	for i := max(0, m.streamScroll); i < end; i++ {
		rows = append(rows, m.cachedStreamRow(i, contentWidth))
	}
	return panelBox(rows, width, height, border)
}

// countChangedLines is how many added or removed lines a file's diff holds, for
// the collapsed divider's summary.
// reviewedBadge is the divider's mark for a reviewed file, and nothing at all for
// one that is merely folded.
func reviewedBadge(reviewed bool) string {
	if reviewed {
		return "✓ reviewed · "
	}
	return ""
}

func countChangedLines(f diff.FileDiff) int {
	n := 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Type == '+' || l.Type == '-' {
				n++
			}
		}
	}
	return n
}

// panelBox draws a rounded border around already-sized rows, without handing the
// whole block to lipgloss.
//
// The lipgloss equivalent — border.Width(w).Height(h).Render(join(rows)) — is a
// single Render over the entire pane, and it was the largest allocation left in a
// frame: 940KB, because every Style.Render builds an ansi parser buffer sized to
// its input and this one is given the whole screen. Here the border pieces are
// styled once (they are two distinct strings, whatever the pane's height) and the
// rows are padded by measuring, so a frame's panel costs a handful of allocations
// instead of a megabyte.
//
// rows are padded to inner width and truncated past it, so the right edge lands in
// the same column on every line — which is the one thing a hand-drawn border gets
// wrong if you let it.
func panelBox(rows []string, width, height int, border lipgloss.Style) string {
	inner := width - 2
	if inner < 1 || height < 1 {
		return ""
	}
	b := lipgloss.RoundedBorder()
	// The border style carries its colour on BorderForeground, which only applies
	// to a border lipgloss draws itself — so recolour it as a foreground here.
	edge := lipgloss.NewStyle().Foreground(border.GetBorderTopForeground())
	side := edge.Render(b.Left)
	rule := strings.Repeat(b.Top, inner)
	var out strings.Builder
	out.Grow((width + 8) * (height + 2))
	out.WriteString(edge.Render(b.TopLeft + rule + b.TopRight))
	for i := 0; i < height; i++ {
		out.WriteByte('\n')
		out.WriteString(side)
		var row string
		if i < len(rows) {
			row = rows[i]
		}
		if w := ansi.StringWidth(row); w > inner {
			row = ansi.Truncate(row, inner, "")
		} else if w < inner {
			row += strings.Repeat(" ", inner-w)
		}
		out.WriteString(row)
		out.WriteString(side)
	}
	out.WriteByte('\n')
	out.WriteString(edge.Render(b.BottomLeft + rule + b.BottomRight))
	return out.String()
}
