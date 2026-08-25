//go:build ghosttyvt

package vterm

/*
#include <ghostty/vt.h>
*/
import "C"

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// The translation between Bubble Tea's key vocabulary and libghostty's.
//
// libghostty's GhosttyKey is a physical-key enum in the W3C style — 181 entries,
// one per key on a keyboard — and it is not only for the keys with names. A
// modified printable key is encoded from the key rather than from its text,
// because there is no text: ctrl+w produces no character, so an event that names
// no key and carries no codepoint gives the encoder nothing to work from and it
// emits nothing at all. That is exactly how ctrl+a, ctrl+e and ctrl+w came to do
// nothing in a pane while unmodified typing worked.
//
// So the writing-system keys are mapped too, spelled out rather than derived
// from the enum's order. GHOSTTY_KEY_A through _Z are contiguous today and
// arithmetic over them would be shorter, but this is another project's ABI: a
// key inserted mid-block would not fail here, it would silently send the letter
// next to the one that was pressed.
var ghosttyKeys = map[rune]C.GhosttyKey{
	'a': C.GHOSTTY_KEY_A,
	'b': C.GHOSTTY_KEY_B,
	'c': C.GHOSTTY_KEY_C,
	'd': C.GHOSTTY_KEY_D,
	'e': C.GHOSTTY_KEY_E,
	'f': C.GHOSTTY_KEY_F,
	'g': C.GHOSTTY_KEY_G,
	'h': C.GHOSTTY_KEY_H,
	'i': C.GHOSTTY_KEY_I,
	'j': C.GHOSTTY_KEY_J,
	'k': C.GHOSTTY_KEY_K,
	'l': C.GHOSTTY_KEY_L,
	'm': C.GHOSTTY_KEY_M,
	'n': C.GHOSTTY_KEY_N,
	'o': C.GHOSTTY_KEY_O,
	'p': C.GHOSTTY_KEY_P,
	'q': C.GHOSTTY_KEY_Q,
	'r': C.GHOSTTY_KEY_R,
	's': C.GHOSTTY_KEY_S,
	't': C.GHOSTTY_KEY_T,
	'u': C.GHOSTTY_KEY_U,
	'v': C.GHOSTTY_KEY_V,
	'w': C.GHOSTTY_KEY_W,
	'x': C.GHOSTTY_KEY_X,
	'y': C.GHOSTTY_KEY_Y,
	'z': C.GHOSTTY_KEY_Z,

	'0': C.GHOSTTY_KEY_DIGIT_0,
	'1': C.GHOSTTY_KEY_DIGIT_1,
	'2': C.GHOSTTY_KEY_DIGIT_2,
	'3': C.GHOSTTY_KEY_DIGIT_3,
	'4': C.GHOSTTY_KEY_DIGIT_4,
	'5': C.GHOSTTY_KEY_DIGIT_5,
	'6': C.GHOSTTY_KEY_DIGIT_6,
	'7': C.GHOSTTY_KEY_DIGIT_7,
	'8': C.GHOSTTY_KEY_DIGIT_8,
	'9': C.GHOSTTY_KEY_DIGIT_9,

	// The punctuation keys carry control codes of their own: ctrl+[ is escape,
	// ctrl+/ is 0x1f, ctrl+] is 0x1d.
	'`':  C.GHOSTTY_KEY_BACKQUOTE,
	'-':  C.GHOSTTY_KEY_MINUS,
	'=':  C.GHOSTTY_KEY_EQUAL,
	'[':  C.GHOSTTY_KEY_BRACKET_LEFT,
	']':  C.GHOSTTY_KEY_BRACKET_RIGHT,
	'\\': C.GHOSTTY_KEY_BACKSLASH,
	';':  C.GHOSTTY_KEY_SEMICOLON,
	'\'': C.GHOSTTY_KEY_QUOTE,
	',':  C.GHOSTTY_KEY_COMMA,
	'.':  C.GHOSTTY_KEY_PERIOD,
	'/':  C.GHOSTTY_KEY_SLASH,

	tea.KeyEnter:     C.GHOSTTY_KEY_ENTER,
	tea.KeyTab:       C.GHOSTTY_KEY_TAB,
	tea.KeyBackspace: C.GHOSTTY_KEY_BACKSPACE,
	tea.KeyEscape:    C.GHOSTTY_KEY_ESCAPE,
	tea.KeySpace:     C.GHOSTTY_KEY_SPACE,
	tea.KeyDelete:    C.GHOSTTY_KEY_DELETE,
	tea.KeyInsert:    C.GHOSTTY_KEY_INSERT,
	tea.KeyUp:        C.GHOSTTY_KEY_ARROW_UP,
	tea.KeyDown:      C.GHOSTTY_KEY_ARROW_DOWN,
	tea.KeyLeft:      C.GHOSTTY_KEY_ARROW_LEFT,
	tea.KeyRight:     C.GHOSTTY_KEY_ARROW_RIGHT,
	tea.KeyHome:      C.GHOSTTY_KEY_HOME,
	tea.KeyEnd:       C.GHOSTTY_KEY_END,
	tea.KeyPgUp:      C.GHOSTTY_KEY_PAGE_UP,
	tea.KeyPgDown:    C.GHOSTTY_KEY_PAGE_DOWN,
	tea.KeyF1:        C.GHOSTTY_KEY_F1,
	tea.KeyF2:        C.GHOSTTY_KEY_F2,
	tea.KeyF3:        C.GHOSTTY_KEY_F3,
	tea.KeyF4:        C.GHOSTTY_KEY_F4,
	tea.KeyF5:        C.GHOSTTY_KEY_F5,
	tea.KeyF6:        C.GHOSTTY_KEY_F6,
	tea.KeyF7:        C.GHOSTTY_KEY_F7,
	tea.KeyF8:        C.GHOSTTY_KEY_F8,
	tea.KeyF9:        C.GHOSTTY_KEY_F9,
	tea.KeyF10:       C.GHOSTTY_KEY_F10,
	tea.KeyF11:       C.GHOSTTY_KEY_F11,
	tea.KeyF12:       C.GHOSTTY_KEY_F12,
}

// ghosttyKey names the physical key an event came from, or
// GHOSTTY_KEY_UNIDENTIFIED for one this does not know — which is honest, and says
// "text, no named key" rather than naming the wrong key.
//
// Lowercased first because a key is a place on the keyboard, not a character:
// there is no upper-case key, and shift is already carried in the mods.
func ghosttyKey(code rune) C.GhosttyKey {
	if k, ok := ghosttyKeys[unicode.ToLower(code)]; ok {
		return k
	}
	return C.GHOSTTY_KEY_UNIDENTIFIED
}

// unshiftedCodepoint is what the key would produce with no modifiers held, which
// is the other half of what the encoder needs to derive a control byte: ctrl+w is
// 0x17 because the key under it is 'w'.
//
// BaseCode when Bubble Tea supplies one — it is the PC-101 key under a layout
// that renames it, which is precisely this question — and otherwise the code
// itself, since for a latin layout the key and what it would type are the same.
// Returning it for every printable key rather than only the renamed ones is the
// fix: the codepoint used to be sent only when BaseCode was set, which on a US
// layout is never.
//
// Zero for a named key. Bubble Tea's sentinels live above unicode.MaxRune
// exactly so they cannot be mistaken for characters, and passing one through as
// a codepoint would claim ctrl+F5 types something.
func unshiftedCodepoint(k tea.Key) rune {
	if k.BaseCode != 0 {
		return k.BaseCode
	}
	if k.Code > unicode.MaxRune {
		return 0
	}
	return unicode.ToLower(k.Code)
}

// ghosttyMods drops the side bits: awp is told a modifier is held, not which of
// the two was held, and the encoder only reads sides when the base bit is set.
func ghosttyMods(mod tea.KeyMod) C.GhosttyMods {
	var out C.GhosttyMods
	if mod&tea.ModShift != 0 {
		out |= C.GHOSTTY_MODS_SHIFT
	}
	if mod&tea.ModCtrl != 0 {
		out |= C.GHOSTTY_MODS_CTRL
	}
	if mod&tea.ModAlt != 0 {
		out |= C.GHOSTTY_MODS_ALT
	}
	if mod&tea.ModSuper != 0 {
		out |= C.GHOSTTY_MODS_SUPER
	}
	if mod&tea.ModCapsLock != 0 {
		out |= C.GHOSTTY_MODS_CAPS_LOCK
	}
	if mod&tea.ModNumLock != 0 {
		out |= C.GHOSTTY_MODS_NUM_LOCK
	}
	return out
}

// ghosttyMouseAction reports which kind of event this is, and false for one
// libghostty has no action for — a release with no button, say. Sending nothing
// beats sending a press the program never made.
func ghosttyMouseAction(msg tea.MouseMsg) (C.GhosttyMouseAction, bool) {
	switch msg.(type) {
	case tea.MouseClickMsg:
		return C.GHOSTTY_MOUSE_ACTION_PRESS, true
	case tea.MouseReleaseMsg:
		return C.GHOSTTY_MOUSE_ACTION_RELEASE, true
	case tea.MouseWheelMsg:
		// A wheel notch is a press of one of the wheel buttons; there is no release,
		// which is why it is not paired.
		return C.GHOSTTY_MOUSE_ACTION_PRESS, true
	case tea.MouseMotionMsg:
		return C.GHOSTTY_MOUSE_ACTION_MOTION, true
	default:
		return 0, false
	}
}

func ghosttyMouseButton(b tea.MouseButton) (C.GhosttyMouseButton, bool) {
	switch b {
	case tea.MouseLeft:
		return C.GHOSTTY_MOUSE_BUTTON_LEFT, true
	case tea.MouseMiddle:
		return C.GHOSTTY_MOUSE_BUTTON_MIDDLE, true
	case tea.MouseRight:
		return C.GHOSTTY_MOUSE_BUTTON_RIGHT, true
	case tea.MouseWheelUp:
		return C.GHOSTTY_MOUSE_BUTTON_FOUR, true
	case tea.MouseWheelDown:
		return C.GHOSTTY_MOUSE_BUTTON_FIVE, true
	case tea.MouseWheelLeft:
		return C.GHOSTTY_MOUSE_BUTTON_SIX, true
	case tea.MouseWheelRight:
		return C.GHOSTTY_MOUSE_BUTTON_SEVEN, true
	case tea.MouseBackward:
		return C.GHOSTTY_MOUSE_BUTTON_EIGHT, true
	case tea.MouseForward:
		return C.GHOSTTY_MOUSE_BUTTON_NINE, true
	default:
		return C.GHOSTTY_MOUSE_BUTTON_UNKNOWN, false
	}
}
