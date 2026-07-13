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

	// Title is the "awp deck" heading — bold, terminal-default fg
	// (white). Kept plain so the teal project headers carry the hue
	// without the title competing.
	Title lipgloss.Style
	// ProjectHeader is the project-header treatment for the all /
	// attention scopes — teal (Accent) + bold, so the structural
	// skeleton of every screen carries a hue instead of reading as
	// bright-white-on-gray.
	ProjectHeader lipgloss.Style
	// FindHeader highlights the find-mode target project header. Project
	// headers are teal now, so the find target moves to Warning + bold
	// (the selection hue) to stay distinct while find mode is up.
	FindHeader lipgloss.Style
	// SubHeader is the inbox project subheader nested under a bucket
	// header: a non-bold muted blue (Info), subordinate to the
	// urgency-colored bucket header above it.
	SubHeader lipgloss.Style
	// Port colors the meta-line :port token blue; the rest of the meta
	// line stays Muted. Author (teal) and branch (green) tints were
	// tried and read as too much color repeated on every row.
	Port lipgloss.Style
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

		Title:         lipgloss.NewStyle().Bold(true),
		ProjectHeader: lipgloss.NewStyle().Foreground(lipgloss.Color(colAccent)).Bold(true),
		FindHeader:    lipgloss.NewStyle().Foreground(lipgloss.Color(colWarning)).Bold(true),
		SubHeader:     lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo)),
		Port:          lipgloss.NewStyle().Foreground(lipgloss.Color(colInfo)),
	}
	for b := inboxBucket(0); b < inboxBucketCount; b++ {
		s.BucketHeader[b] = lipgloss.NewStyle().Foreground(lipgloss.Color(inboxBucketColor(b))).Bold(true)
	}
	return s
}
