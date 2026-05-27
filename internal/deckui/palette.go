package deckui

import "github.com/andrewcohen/awp/internal/charm"

// Local aliases for the shared palette in internal/charm. Keep call sites
// using these short, lowercase names — they're convenient inside this
// package, and the source of truth lives in charm/palette.go so a theme
// change is a one-file edit visible across the whole app.
const (
	colAccent  = charm.Accent
	colInfo    = charm.Info
	colSuccess = charm.Success
	colWarning = charm.Warning
	colDanger  = charm.Danger
	colSpinner = charm.Spinner
	colStrong  = charm.Strong
	colMuted   = charm.Muted
)
