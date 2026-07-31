package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
)

// The `?` overlay.
//
// The footer used to carry a key legend, which cost a row of width on every
// frame to answer a question asked once. It also could not list everything —
// there are more bindings than fit — so the keys that did appear were whichever
// ones happened to be picked, and the rest were undiscoverable.
//
// viewerKeyGroups is the canonical binding surface for this view, the same
// contract deckKeyGroups has for the deck: a key added to the switch in
// handleKey without a row here is a key nobody will find.
//
// The body scrolls, in a bubbles/viewport. There are more bindings than fit a
// short terminal, and clipping them would put the overlay back in the position
// the footer legend was in — showing an arbitrary subset and hiding the rest,
// except now it would look complete.

// helpBoxHOverhead is what the panel's chrome costs horizontally: 2 columns of
// border plus 2 of padding on each side.
//
// There is no vertical equivalent. The panes this overlay stands in for render
// `height` rows *inside* their border (renderStreamPanel does Height(height) on a
// bordered style), so the whole body is height+2 rows and the overlay has to do
// the same or the footer moves when the help opens.
const helpBoxHOverhead = 6

func viewerKeyGroups() []charm.KeyGroup {
	return []charm.KeyGroup{
		{
			Title: "Move",
			Keys: [][2]string{
				{"j/k ↓/↑", "move the cursor a row"},
				{"ctrl+d / ctrl+u", "half a page down / up"},
				{"{ / }", "previous / next hunk, anywhere in the change"},
				{"g / G", "first / last row"},
				{"tab / shift+tab", "cycle pane: files → comments → diff"},
				{"enter", "hand the keyboard to the diff (from either list)"},
			},
		},
		{
			Title: "Read",
			Keys: [][2]string{
				{"h/l ←/→", "pan horizontally (no-op under wrap)"},
				{"0 / $", "start / end of the line"},
				{"w", "toggle line wrap"},
				{`\`, "show / hide the left column"},
				{"/", "filter files · esc clears"},
				{"e", "open the file in $EDITOR at the cursor's line"},
				{"ctrl+r", "refresh now (the view also refreshes itself)"},
			},
		},
		{
			Title: "Review",
			Keys: [][2]string{
				{"c", "comment on the line · on a comment, reply to it"},
				{"i", "edit your own comment in place"},
				{"D", "delete the comment and every reply beneath it"},
				{"r", "mark the file reviewed and collapse it"},
				{"R", "resolve / reopen the GitHub thread at the cursor"},
				{"T", "cycle GitHub threads shown: unresolved → all → none"},
			},
		},
		{
			Title: "In the compose box",
			Keys: [][2]string{
				{"enter", "save"},
				{"ctrl+s", "save and send to the workspace's agent"},
				{"ctrl+g", "write it in $EDITOR instead"},
				{"tab", "cycle kind: comment → suggestion → question"},
				{"alt+enter", "newline"},
				{"esc", "discard"},
			},
		},
		{
			Title: "Here",
			Keys: [][2]string{
				{"j/k ctrl+d/ctrl+u", "scroll this reference"},
				{"? esc q", "close it"},
			},
		},
	}
}

// newHelpViewport builds the overlay's scroll region for a body of this size.
func newHelpViewport(width, height int) viewport.Model {
	vp := viewport.New(helpContentWidth(width), max(1, height))
	vp.SetContent(helpContent(helpContentWidth(width)))
	return vp
}

// helpContentWidth is the columns left for text inside the panel.
func helpContentWidth(width int) int {
	return max(20, width-helpBoxHOverhead)
}

// helpContent is the reference itself, clipped to width.
//
// Truncated rather than wrapped: a wrapped line adds a row, and the viewport
// sizes its scroll against the line count, so wrapping would make the scrollbar
// disagree with what is on screen.
func helpContent(width int) string {
	title := lipgloss.NewStyle().Bold(true).Render("Keys")
	body := charm.KeyHelpView(viewerKeyGroups())
	return clipToWidth(title+"\n\n"+body, width)
}

// renderHelpOverlay draws the key reference in place of the diff panes.
//
// Sized to the body it replaces rather than floating over it: the body is the
// whole width and height the host gave us, so there is nothing to float above,
// and a panel filling the same box keeps the footer where it was.
func renderHelpOverlay(vp viewport.Model, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	// Padding(1, 2) inside a rounded border is the modal convention (see the
	// design system). lipgloss Width covers padding but not border, so the box
	// renders 2 columns wider than what is set here.
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(charm.Muted)).
		Padding(0, 2).
		Width(max(1, width-2)).
		Render(vp.View())
}

// clipToWidth truncates every line to w, ellipsising what it cuts. Without it
// lipgloss wraps long binding descriptions onto extra rows.
func clipToWidth(block string, w int) string {
	lines := strings.Split(block, "\n")
	for i, ln := range lines {
		lines[i] = ansi.Truncate(ln, w, "…")
	}
	return strings.Join(lines, "\n")
}
