package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/diff"
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
	key := rowKey{
		row:   i,
		width: width,
		// The band, not merely "is the cursor here": whether it is painted depends
		// on the focus too, and a row rendered with the band must not be served to a
		// frame that wants it without.
		band:    i == m.cursorRow && m.focus == FocusHunks,
		hscroll: m.hunkHScroll,
	}
	if out, ok := m.cache.rows[key]; ok {
		return out
	}
	out := m.renderStreamRowAt(i, width)
	m.cache.rows[key] = out
	return out
}

func (m Model) renderStreamRowAt(i, width int) string {
	atCursor := i == m.cursorRow
	// The cursorline band is painted only while this pane has the keyboard. Two
	// full-width bands at once — one here, one in the file list or comment index —
	// leaves no way to tell which selection the keys are actually driving. The ┃
	// bar stays either way, so the row you will come back to is still marked.
	band := atCursor && m.focus == FocusHunks
	kind := m.stream.rows[i].kind
	prefix := selectionPrefixBlank
	switch {
	case band:
		prefix = styleSelectedCursor.Render(selectionPrefixBar)
	case atCursor:
		prefix = styleSelected.Render(selectionPrefixBar)
	case isCommentRow(kind):
		// Paint the reserved columns too: an unpainted gap on the left would
		// break the block the comment is meant to read as.
		prefix = styleCommentFill.Render(selectionPrefixBlank)
	}
	body := m.renderStreamRow(m.stream.rows[i], width-lipgloss.Width(selectionPrefixBlank), band)
	row := prefix + body
	if !band {
		return row
	}
	// Extend the cursorline to the full pane width so it reads as a band
	// rather than as highlighting only the text.
	if pad := width - lipgloss.Width(row); pad > 0 {
		row += styleCursorFill.Render(strings.Repeat(" ", pad))
	}
	return row
}

func (m Model) renderStreamRow(r rowRef, width int, cursor bool) string {
	if width <= 0 {
		return ""
	}
	switch r.kind {
	case rowSpacer:
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
		return styleReviewHeader.Width(width).Render(" review — about the change as a whole")
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
	current := r.file == m.filesCursor
	ruleStyle, baseStyle := fileRuleStyles(current)

	lead := ruleStyle.Render(strings.Repeat(fileRuleGlyph, fileRuleLead) + " ")
	// Never banded: the divider is already a full-width rule, so a cursorline
	// behind it would add nothing (see renderStreamRow).
	badge := statusBadge(f.Status, current, false)
	summary := fmt.Sprintf(" (%d hunk%s)", len(f.Hunks), plural(len(f.Hunks)))
	if r.collapsed {
		// A collapsed file still has to say what is inside it, or the divider
		// becomes a wall you have to open to see past.
		summary = fmt.Sprintf(" ✓ reviewed · %d hunk%s, %d line%s hidden",
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

// fileRuleStyles picks the divider's hue: the accent normally, the selection
// hue for the file the cursor is in.
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
	l := h.Lines[r.line]

	avail := width - meta.prefixWidth
	text := l.Content
	if m.wrap {
		text = segmentText(text, r.seg, avail)
	} else if m.hunkHScroll > 0 {
		text = ansi.TruncateLeft(text, m.hunkHScroll, "")
	}

	added, deleted, context := styleAdded, styleDeleted, styleContext
	if cursor {
		added, deleted, context = styleAddedCursor, styleDeletedCursor, styleContextCursor
	}
	var content string
	switch l.Type {
	case '+':
		content = added.Render(text)
	case '-':
		content = deleted.Render(text)
	default:
		content = context.Render(text)
	}

	// Continuation rows carry no gutter, just its width as padding.
	if r.seg > 0 {
		pad := strings.Repeat(" ", meta.prefixWidth)
		if cursor {
			pad = styleCursorFill.Render(pad)
		}
		return truncateStyled(pad+content, width)
	}
	numbers := fmt.Sprintf("%*s %*s ", meta.oldWidth, lineNoText(r.oldNo), meta.newWidth, lineNoText(r.newNo))
	numberStyle := styleLineNo
	if cursor {
		// The bar marks the row; tinting the numbers too makes the cursor
		// readable when the bar is at the edge of vision. No background fill,
		// per the design system.
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
	prefix := numberStyle.Render(numbers) + gutterStyle.Render(gutter+" ")
	return truncateStyled(prefix+content, width)
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
	row     int
	width   int
	band    bool
	hscroll int
}

// leftKey identifies a rendered left column, which is one string because the
// whole column is rebuilt together.
type leftKey struct {
	width, height   int
	files, comments int
	focus           Focus
	entries         int
	hidden          bool
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
		return border.Width(width - 2).Height(height).Render(styleDim.Render(" No changes"))
	}
	if len(m.stream.rows) == 0 {
		return border.Width(width - 2).Height(height).Render(styleDim.Render(" rename-only, binary, or empty diff body"))
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
