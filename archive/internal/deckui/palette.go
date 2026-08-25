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

// bgCursorline is charm.Cursorline: the low-contrast background behind the row you
// are on.
//
// A var and not part of the const block above because it is an adaptive colour
// rather than an ANSI index — it has to be one, since the 16 slots carry no
// low-contrast background at all (the nearest, BgPanel, is sized for chip fills and
// reads far too strong to read text through). charm/palette.go argues that at
// length; this is the alias.
var bgCursorline = charm.Cursorline

// borderCells is charm.BorderCells under this package's naming convention:
// the columns a rounded border adds to a style's width, which lipgloss v2
// counts inside Width rather than outside it.
const borderCells = charm.BorderCells
