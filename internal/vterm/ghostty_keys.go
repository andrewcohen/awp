//go:build ghosttyvt

package vterm

/*
#include <ghostty/vt.h>
*/
import "C"

import (
	tea "charm.land/bubbletea/v2"
)

// The translation between Bubble Tea's key vocabulary and libghostty's.
//
// libghostty's GhosttyKey is a physical-key enum in the W3C style — 181 entries,
// one per key on a keyboard. Only the ones a terminal program reads as a named
// key are mapped: for everything printable the encoder works from the event's
// utf8 and unshifted codepoint, which is what a terminal actually puts on the
// wire. An unmapped key is GHOSTTY_KEY_UNIDENTIFIED, which is honest — it says
// "text, no named key" rather than naming the wrong one.
var ghosttyKeys = map[rune]C.GhosttyKey{
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

func ghosttyKey(code rune) C.GhosttyKey {
	if k, ok := ghosttyKeys[code]; ok {
		return k
	}
	return C.GHOSTTY_KEY_UNIDENTIFIED
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
