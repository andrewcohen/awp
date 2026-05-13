package charm

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
	Accent   = "6"  // teal / cyan — titles, headers, primary accent
	Info     = "4"  // blue — neutral info (PR numbers, async-job running)
	Success  = "2"  // green — working / approved / done
	Warning  = "3"  // yellow — waiting / pending / draft / row selection
	Danger   = "1"  // red — errors, CI failing
	Spinner  = "5"  // magenta / pink — spinner only

	Strong   = "15" // bright white — emphasized text
	Muted    = "8"  // bright black — hints, footer, dim labels
	BgPanel  = "0"  // surface — chip backgrounds (use sparingly)
)
