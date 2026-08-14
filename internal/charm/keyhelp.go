package charm

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// Key bindings, rendered for a `?` overlay.
//
// Lives here rather than in either TUI because both the deck and the diff viewer
// need it and internal/ui cannot import internal/deckui — deckui imports ui, not
// the other way round. Sharing the renderer is also what keeps the two overlays
// looking like the same application: one place decides how a key and its
// description are set next to each other.

// KeyGroup is a labeled set of (key, description) rows. Declaring bindings as
// data rather than as strings assembled at render time is what lets one slice be
// the single source of truth for a surface's keymap and its help.
//
// Title may be empty, which renders the bindings alone and unindented. A `?`
// overlay stacks several groups and needs each one labeled; the deck's action
// menus are one group on a screen of their own, where a heading would only name
// what the box already is.
type KeyGroup struct {
	Title string
	Keys  [][2]string
}

// KeyHelpView renders groups as a stack of titled sections, each binding on its
// own line.
//
// Built on bubbles/help so key and description styling matches everywhere else
// that uses NewHelp. FullKey is overridden to the selection hue because a help
// overlay is read by scanning the key column, and the accent it would otherwise
// take is the same hue as the section titles.
func KeyHelpView(groups []KeyGroup) string {
	h := NewHelp()
	h.ShowAll = true
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(lipgloss.Color(Warning)).Bold(true)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(lipgloss.Color(Muted))
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(lipgloss.Color(Muted))
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(Accent))
	indent := lipgloss.NewStyle().Padding(0, 0, 0, 2)

	lines := make([]string, 0, len(groups)*3)
	for i, g := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		body := indent
		if g.Title != "" {
			lines = append(lines, titleStyle.Render(g.Title))
		} else {
			// Nothing to indent under, so nothing to indent. The 2 columns exist to
			// set the bindings beneath their heading.
			body = lipgloss.NewStyle()
		}
		bindings := make([]key.Binding, 0, len(g.Keys))
		for _, kr := range g.Keys {
			bindings = append(bindings, key.NewBinding(
				key.WithKeys(kr[0]),
				key.WithHelp(kr[0], kr[1]),
			))
		}
		// FullHelpView lays out one column per []key.Binding. Passing a single
		// column keeps each binding on its own "key   description" line, which is
		// what makes the key column scannable top to bottom.
		lines = append(lines, body.Render(h.FullHelpView([][]key.Binding{bindings})))
	}
	return strings.Join(lines, "\n")
}
