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
	Label    lipgloss.Style // plain terminal-default fg — a normal/active (un-muted) row label

	// ProjectHeader is the brightened project-header treatment for the
	// all / attention scopes (Strong + bold). Kept distinct from
	// Accent so find-mode's teal header highlight still stands out.
	ProjectHeader lipgloss.Style
	// Author / Port color the meta-line tokens that carry a semantic
	// role per the palette table (author = green, port = blue); the
	// rest of the meta line stays Muted.
	Author lipgloss.Style
	Port   lipgloss.Style
	// BucketHeader holds the urgency-colored, bold header style for
	// each inbox bucket, indexed by inboxBucket.
	BucketHeader [inboxBucketCount]lipgloss.Style
}

func newDeckStyles() deckStyles {
	s := deckStyles{
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
		Label:    lipgloss.NewStyle(),

		ProjectHeader: lipgloss.NewStyle().Foreground(lipgloss.Color(colStrong)).Bold(true),
		Author:        lipgloss.NewStyle().Foreground(lipgloss.Color(colSuccess)),
		Port:          lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo)),
	}
	for b := inboxBucket(0); b < inboxBucketCount; b++ {
		s.BucketHeader[b] = lipgloss.NewStyle().Foreground(lipgloss.Color(inboxBucketColor(b))).Bold(true)
	}
	return s
}
