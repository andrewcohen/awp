package charm

import "github.com/charmbracelet/lipgloss"

// Semantic color palette for every TUI surface in the app.
//
// All values are ANSI 16 slot indices ("0"-"15") that lipgloss.Color
// accepts. ANSI 16 slots are remapped by the terminal emulator's color
// scheme, so the UIs inherit the user's terminal theme (Catppuccin
// Macchiato, in our case) instead of fighting it with hardcoded 256-color
// codes. New TUI code should route every color through one of these
// tokens; legacy 256-color call sites in internal/ui and internal/charm
// theme styles are being migrated to match.
const (
	Accent  = "6" // teal / cyan — titles, headers, primary accent
	Info    = "4" // blue — neutral info (PR numbers, async-job running)
	Success = "2" // green — working / approved / done
	Warning = "3" // yellow — waiting / pending / draft / row selection
	Danger  = "1" // red — errors, CI failing
	Spinner = "5" // magenta / pink — spinner only

	Strong  = "15" // bright white — emphasized text
	Muted   = "8"  // bright black — hints, footer, dim labels
	BgPanel = "0"  // surface — chip backgrounds (use sparingly)

	// Link is rendered identically to Info today but exists as a
	// distinct semantic token so a future theme tweak can recolor
	// underlined hyperlinks without disturbing PR numbers.
	Link = Info
)

// Cursorline is the background behind the row a line cursor is on.
//
// This is the one deliberate exception to the ANSI-16-only rule above, and
// the reason is structural rather than aesthetic: a cursorline has to sit a
// *hair* off the terminal background, and the 16-slot palette has no such
// slot. BgPanel ("0", surface) is the closest and reads as far too strong —
// it is sized for chip and badge fills, where the contrast is the point.
//
// Adaptive so it works against a light or dark terminal: lipgloss picks the
// variant from the detected background. Keep this the only non-ANSI-16 value
// in the palette; if a second one shows up, that is a signal the 16-slot
// constraint needs revisiting wholesale rather than eroding case by case.
var Cursorline = lipgloss.AdaptiveColor{Light: "254", Dark: "236"}
