package charm

import (
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	catppuccingo "github.com/catppuccin/go"
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

// Syntax colours, for code shown inside a diff body.
//
// Catppuccin rather than the ANSI 16 slots above, and the distinction is worth
// naming because it is not an exception. Everything above is awp's **chrome** — a
// status dot, a project header, a selection bar. Chrome should follow whatever
// theme the terminal is set to, which is exactly what the 16 slots buy. Code in a
// diff is not chrome, it is **content**, and it should look the way the same code
// looks in the editor the reader is about to open it in.
//
// Sixteen slots also cannot carry a syntax palette. Six are already spoken for by
// status roles, so at ANSI 16 a token either shares its hue with "CI failing" or
// gets none at all — which is why punctuation and operators had none. Catppuccin
// has fourteen accents plus graded overlays, which is what a lexer's output needs.
//
// Latte against a light terminal, Macchiato against a dark one. The assignment is
// Catppuccin's own conventional one, so a diff matches an editor wearing the theme
// rather than approximating it. catppuccingo.Color implements color.Color, so
// these need no conversion to reach lipgloss.
//
// Plain — a bare identifier — deliberately has no entry. It keeps the terminal's
// default foreground, which under this theme is already Catppuccin's Text.
var (
	SyntaxKeyword  = syntax(catppuccingo.Latte.Mauve, catppuccingo.Macchiato.Mauve)       // keywords, builtins, constants
	SyntaxType     = syntax(catppuccingo.Latte.Yellow, catppuccingo.Macchiato.Yellow)     // types, classes, tags, YAML keys, markdown headings
	SyntaxFunc     = syntax(catppuccingo.Latte.Blue, catppuccingo.Macchiato.Blue)         // function names
	SyntaxAttr     = syntax(catppuccingo.Latte.Teal, catppuccingo.Macchiato.Teal)         // attributes, properties, variables, decorators
	SyntaxString   = syntax(catppuccingo.Latte.Green, catppuccingo.Macchiato.Green)       // strings and other non-numeric literals
	SyntaxNumber   = syntax(catppuccingo.Latte.Peach, catppuccingo.Macchiato.Peach)       // numeric literals
	SyntaxComment  = syntax(catppuccingo.Latte.Overlay1, catppuccingo.Macchiato.Overlay1) // comments
	SyntaxOperator = syntax(catppuccingo.Latte.Sky, catppuccingo.Macchiato.Sky)           // = => ?? |
	SyntaxPunct    = syntax(catppuccingo.Latte.Overlay2, catppuccingo.Macchiato.Overlay2) // ( ) { } , ;
)

// syntax pairs a Catppuccin colour's light and dark flavours.
//
// Takes the accessors rather than the colours so a call site names the accent once
// — `catppuccingo.Latte.Mauve, catppuccingo.Macchiato.Mauve` reads as "mauve", and
// a pair that disagreed would be a typo this shape makes visible.
func syntax(light, dark func() catppuccingo.Color) compat.AdaptiveColor {
	return compat.AdaptiveColor{Light: light(), Dark: dark()}
}

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
