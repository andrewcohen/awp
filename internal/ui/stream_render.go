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

// renderStreamFileHeader is the inline separator that opens each file's
// section of the stream. The file the cursor is in is marked, so scrolling
// tells you where you are without consulting the file list.
func (m Model) renderStreamFileHeader(r rowRef, width int) string {
	if r.file < 0 || r.file >= len(m.filtered) {
		return ""
	}
	f := m.filtered[r.file]
	current := r.file == m.filesCursor
	badge := statusBadge(f.Status, current)
	label := renderPath(diff.DisplayPath(f), max(10, width-lipgloss.Width(badge)-1), current)
	meta := styleMuted.Render(fmt.Sprintf(" (%d hunk%s)", len(f.Hunks), plural(len(f.Hunks))))
	return truncateStyled(badge+" "+label+meta, width)
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
