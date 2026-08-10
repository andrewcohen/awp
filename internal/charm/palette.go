package charm

import (
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

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

// Syntax token roles, for code shown inside a diff body.
//
// Named separately from the status tokens above even where they land on the same
// ANSI slot, because the two are retuned for different reasons: changing what a
// keyword looks like must not change what a failing check looks like. Same 16
// slots, so the user's terminal theme still supplies every hue — a chroma style
// would emit its own 256/truecolour codes, which is the thing this file exists to
// prevent.
//
// There is no token for punctuation or for an identifier. Both stay at the base
// colour of the line they are on: at ANSI-16 resolution the palette runs out of
// distinguishable hues long before a lexer runs out of token types, and the ones
// worth spending a hue on are the ones you scan for.
const (
	SyntaxKeyword = "5" // magenta — keywords, and the builtins that read like them
	SyntaxType    = "3" // yellow — type names, tags, YAML keys, markdown headings
	SyntaxFunc    = "4" // blue — function names
	SyntaxAttr    = "6" // teal — attributes, properties, variables, decorators
	SyntaxString  = "2" // green — string and other non-numeric literals
	SyntaxNumber  = "5" // magenta — literals read as one family with keywords
	SyntaxComment = "8" // bright black — the same dimming hints wear
)

// Background tints. These are the exception to the ANSI-16 rule above, and the
// rule is narrower than it first looked: it is about *foreground* semantics,
// where being remapped by the user's theme is the whole point. A low-contrast
// background has no ANSI 16 slot at all — the nearest, BgPanel ("0", surface),
// is sized for chip and badge fills where contrast is the point, and reads far
// too strong for a tint you are meant to read code through.
//
// So: foregrounds are ANSI 16, background tints are adaptive and off-palette.
// Cursorline was the first, and its comment used to say a second one should
// trigger revisiting the constraint wholesale rather than eroding it case by
// case. That is this block — the revisit, done once, with the tints named here
// beside everything else rather than inlined at a call site.
//
// Adaptive so they work against a light or a dark terminal; lipgloss picks the
// variant from the detected background.
var (
	// Cursorline is the background behind the row a line cursor is on.
	Cursorline = compat.AdaptiveColor{Light: lipgloss.Color("254"), Dark: lipgloss.Color("236")}

	// AddedBg and RemovedBg sit behind a syntax-highlighted diff line, because
	// highlighting spends the foreground on the lexer and the change type has to go
	// somewhere. Without them a + line and a - line differ only in the gutter glyph.
	//
	// Not used when the body is unpainted: there the change type is already the
	// foreground of every character on the line, and a tint under it is two signals
	// for one fact.
	AddedBg   = compat.AdaptiveColor{Light: lipgloss.Color("#e4f5e4"), Dark: lipgloss.Color("#23331f")}
	RemovedBg = compat.AdaptiveColor{Light: lipgloss.Color("#f8e4e6"), Dark: lipgloss.Color("#361f24")}

	// AddedBgCursor and RemovedBgCursor are the same lines with the cursor on them.
	//
	// A step brighter rather than replaced by Cursorline: the cheap version lets the
	// cursorline win, and then a + row loses its tint for exactly as long as the
	// cursor is on it, so the tint blinks off and on down the whole file as you
	// scroll. Brighter variants keep one rule — the cursor's row is a step up from
	// the row beneath it — true of added, removed and unchanged alike.
	AddedBgCursor   = compat.AdaptiveColor{Light: lipgloss.Color("#cceccc"), Dark: lipgloss.Color("#2f4429")}
	RemovedBgCursor = compat.AdaptiveColor{Light: lipgloss.Color("#f2ccd2"), Dark: lipgloss.Color("#472830")}
)

// BorderCells is how many columns a single-cell border adds to a style's
// width.
//
// It exists because lipgloss changed what Width means. In v1 Width was the
// content-plus-padding box and the border was drawn outside it, so a bordered
// panel rendered two columns wider than the number you set. In v2 Width is the
// total rendered width, border included. Every bordered panel that wants to
// span a known number of columns therefore passes that number directly, and
// anything still thinking in v1 terms adds this to what it used to set.
const BorderCells = 2
