package deckui

import (
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/charm"
)

// The deck's menus: an armed ctrl+b over a pane or a split, and the row list's own
// chords (`|`, `p`).
//
// A centered popover floating over whatever is on screen, which is #344. They lived
// on the deck's top row before that (#328), whose argument was that one row is where
// every menu should be and that a menu long enough to wrap costs the frame a row.
// Both objections are better answered here than there: a floating box consumes no
// frame row at all, and being unbound by one line it can spell each verb out on its
// own line instead of packing them into a ribbon read left to right.
//
// The top row goes back to saying what it says the rest of the time — the attention
// badge and what is on screen — so nothing about it moves as a menu opens.

// deckMenu is a menu as data: the verbs, and nothing else.
//
// Data rather than an assembled string, because the layout is no longer one line and
// the thing that knows how to set a key beside its description is charm.KeyHelpView
// — the same renderer behind the `?` overlay and the diff viewer's. A menu that
// formatted itself would be a third opinion about how a keymap looks.
//
// No title. Each one carried its subject for a while ("this pane", "this split") and
// before that the key that opened it, which told you the one thing you already knew.
// A bordered box of key-and-verb rows that appeared when you pressed a key is
// self-evidently a menu of what that key can do, and every row of a heading is a row
// the box is taller than the thing it is worth.
type deckMenu struct {
	verbs [][2]string
}

// menu builds one, dropping verbs whose key is empty so a caller can leave a row out
// by condition rather than by building the slice twice.
func menu(verbs ...[2]string) deckMenu {
	out := make([][2]string, 0, len(verbs))
	for _, v := range verbs {
		if v[0] != "" {
			out = append(out, v)
		}
	}
	return deckMenu{verbs: out}
}

// menuCancelVerb closes every menu's list.
//
// On the list rather than implied, because it is the only verb that is a property of
// the menu rather than of what is on screen: any unbound key cancels, and `esc` is
// the one to reach for when you have decided you meant nothing.
var menuCancelVerb = [2]string{"esc", "cancel"}

// render draws the menu as a bordered box, sized to its content.
//
// Padding(1, 2) inside a border, per the design system's rule for popovers: they
// float over a canvas rather than competing with the frame for its height, and
// content flush against a border reads as a mistake.
func (mn deckMenu) render(width int) string {
	body := charm.KeyHelpView([]charm.KeyGroup{{Keys: mn.verbs}})
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colAccent)).
		Padding(1, 2)
	// Only bounded, never padded out: a menu of six short verbs in a 200-column
	// terminal should be the width of its longest line, not of the terminal. The
	// clamp is for the narrow half of a split, where the box would otherwise be
	// wider than the screen it floats over.
	if w := lipgloss.Width(body) + menuBoxCols; w > width {
		box = box.Width(max(menuBoxCols+1, width))
	}
	return box.Render(body)
}

// menuBoxCols is what the box costs around its content: two border columns and
// Padding(1, 2)'s four.
const menuBoxCols = borderCells + 4

// armedMenu is the menu of whatever is waiting for a key, and whether anything is.
//
// One answer for every menu the deck has — asked once here rather than tested
// wherever each of them renders, which is how the chords once came to print theirs
// somewhere else.
func (m *Model) armedMenu() (deckMenu, bool) {
	switch c := m.active.(type) {
	case *splitModal:
		// The submenu first: `x` moved the menu on rather than resolving it, so the
		// box on screen has to move with it.
		if len(c.actions) > 0 {
			return userActionsMenu(c.actions), true
		}
		if c.prefixArmed {
			return splitPrefixMenu(m), true
		}
	case *panePopover:
		if len(c.actions) > 0 {
			return userActionsMenu(c.actions), true
		}
		if c.prefixArmed {
			return panePrefixMenu(m), true
		}
	}
	if c, ok := m.active.(chordModal); ok {
		return c.chordMenu(), true
	}
	return deckMenu{}, false
}

// overlayMenu floats the menu over a rendered frame, centered.
//
// A real composite rather than a modal that replaces the screen: the menu names
// things to do to what is on screen, so what is on screen has to stay visible while
// you read it. lipgloss's canvas is what makes that possible — layers drawn in order,
// the later one on top — and it is why this is not the popoverModal path, which
// paints a blank canvas first.
//
// The frame is measured rather than trusted to be m.width × m.height: a frame short
// of its own height would otherwise put the menu below the bottom of the terminal.
func overlayMenu(frame, menuBox string) string {
	w, h := lipgloss.Width(frame), lipgloss.Height(frame)
	mw, mh := lipgloss.Width(menuBox), lipgloss.Height(menuBox)
	if mw > w || mh > h {
		// Nowhere to float it that would not clip. The menu is the thing being read,
		// so it wins the screen rather than being drawn half off it.
		return menuBox
	}
	// One compositor of two layers, not two Compose calls: a canvas composes each
	// drawable at its own origin, so composing the layers separately drew the menu at
	// 0,0 over an empty canvas and lost the frame entirely. A compositor is the thing
	// that stacks them.
	return lipgloss.NewCanvas(w, h).Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(frame),
		lipgloss.NewLayer(menuBox).X((w-mw)/2).Y((h-mh)/2),
	)).Render()
}
