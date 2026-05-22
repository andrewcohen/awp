package deckui

import "github.com/charmbracelet/lipgloss"

// deckStyles caches the base lipgloss.Style values the deck reaches
// for on every render. Each field is a "foreground + bold" base; call
// sites can chain Width(...)/Padding(...) etc. on top per use because
// lipgloss styles are immutable value types.
//
// Goal: avoid re-allocating the same style per row / per frame inside
// hot render helpers, and give us a single struct to inspect when we
// want to know "where do we use the warning-bold-selected style?". Not
// every render site has been converted yet; new code should prefer
// these over fresh lipgloss.NewStyle() chains.
type deckStyles struct {
	Muted    lipgloss.Style
	Accent   lipgloss.Style
	Warning  lipgloss.Style
	Selected lipgloss.Style // warning fg + bold — the row-selection treatment
	Success  lipgloss.Style
	Danger   lipgloss.Style
	Info     lipgloss.Style
	Spinner  lipgloss.Style
	Strong   lipgloss.Style
	Bar      lipgloss.Style // the ┃ selection bar prefix
}

func newDeckStyles() deckStyles {
	return deckStyles{
		Muted:    lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		Accent:   lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)).Bold(true),
		Warning:  lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)),
		Selected: lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true),
		Success:  lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess)),
		Danger:   lipgloss.NewStyle().Foreground(lipgloss.Color(colDanger)),
		Info:     lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo)),
		Spinner:  lipgloss.NewStyle().Foreground(lipgloss.Color(colSpinner)),
		Strong:   lipgloss.NewStyle().Foreground(lipgloss.Color(colStrong)).Bold(true),
		Bar:      lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true),
	}
}
