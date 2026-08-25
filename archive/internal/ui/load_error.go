package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/awplog"
	"github.com/andrewcohen/awp/internal/charm"
)

// The panel that stands in for the panes when the diff would not load.
//
// What made this necessary: a failed `jj diff` reported "error: load diff: …
// exited 1" on the footer and nothing else, so the one thing that says *why* —
// jj's own stderr, which the runner already captures and attaches underneath
// the first line — was in the error and nowhere on screen. The footer is one
// row and cannot show a second line, so the detail needs a surface of its own.
//
// Not scrollable. A command's complaint is a handful of lines; the last resort
// for one longer than the body is the log, which this panel names.

// renderLoadErrorOverlay draws the load failure in place of the diff panes,
// sized exactly like the help overlay so the footer does not move.
func renderLoadErrorOverlay(err error, width, height int) string {
	if err == nil || width <= 0 || height <= 0 {
		return ""
	}
	inner := helpContentWidth(width)
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(charm.Danger)).Render("Could not load the diff")
	hint := styleDim.Render("full text: " + awplog.Path())
	// The body is height rows inside the border (see helpBoxHOverhead); the title,
	// the blank under it and the hint take three of them.
	body := loadErrorBody(err.Error(), inner, height-3)
	block := strings.Join([]string{title, "", body, "", hint}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Muted)).
		Padding(0, 2).
		Width(max(1, width)).
		Height(max(1, height)).
		Render(block)
}

// loadErrorBody is the error's text wrapped to width and clipped to rows,
// marking the clip so a reader knows to go to the log rather than assuming they
// have the whole complaint.
func loadErrorBody(text string, width, rows int) string {
	wrapped := lipgloss.NewStyle().Width(max(1, width)).Render(strings.TrimSpace(text))
	lines := strings.Split(wrapped, "\n")
	if rows > 0 && len(lines) > rows {
		lines = lines[:max(1, rows-1)]
		lines = append(lines, styleDim.Render("… truncated"))
	}
	return strings.Join(lines, "\n")
}
