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
func (m Model) renderStreamRow(r rowRef, width int) string {
	if width <= 0 {
		return ""
	}
	switch r.kind {
	case rowSpacer:
		return ""
	case rowFileHeader:
		return m.renderStreamFileHeader(r, width)
	case rowHunkHeader:
		h, _, ok := m.stream.hunkAt(m.filtered, r)
		if !ok {
			return ""
		}
		header := fmt.Sprintf(" @@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		return styleHunkHeader.Width(width).Render(header)
	case rowLine:
		return m.renderStreamLine(r, width)
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
	badge := statusBadge(f.Status, current)
	meta := styleMuted.Render(fmt.Sprintf(" (%d hunk%s)", len(f.Hunks), plural(len(f.Hunks))))
	reserved := lipgloss.Width(lead) + lipgloss.Width(badge) + 1 + lipgloss.Width(meta)
	label := renderPathWith(diff.DisplayPath(f), max(10, width-reserved), ruleStyle, baseStyle)

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
func (m Model) renderStreamLine(r rowRef, width int) string {
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

	var content string
	switch l.Type {
	case '+':
		content = styleAdded.Render(text)
	case '-':
		content = styleDeleted.Render(text)
	default:
		content = styleContext.Render(text)
	}

	// Continuation rows carry no gutter, just its width as padding.
	if r.seg > 0 {
		return truncateStyled(strings.Repeat(" ", meta.prefixWidth)+content, width)
	}
	numbers := fmt.Sprintf("%*s %*s ", meta.oldWidth, lineNoText(r.oldNo), meta.newWidth, lineNoText(r.newNo))
	gutter := string(l.Type)
	gutterStyle := styleContext
	switch l.Type {
	case '+':
		gutterStyle = styleAdded
	case '-':
		gutterStyle = styleDeleted
	default:
		gutter = "│"
	}
	prefix := styleLineNo.Render(numbers) + gutterStyle.Render(gutter+" ")
	return truncateStyled(prefix+content, width)
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
		rows = append(rows, m.renderStreamRow(m.stream.rows[i], contentWidth))
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}
