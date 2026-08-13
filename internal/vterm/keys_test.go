//go:build ghosttyvt

package vterm

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestKeyPressesReachTheProcess types through SendKey — the path the deck
// actually uses — rather than writing bytes to the pty as TestTypingReaches-
// TheProcess does, which is why this hole went unnoticed.
//
// The failure it guards against is silent. x/vt's key table ends in
// `if key.Mod == 0 { seq += string(key.Code) }`, so a key carrying any
// modifier it does not list emits nothing at all. Shift+a fell into that hole,
// costing every capital letter and every shifted symbol.
func TestKeyPressesReachTheProcess(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"lowercase", tea.KeyPressMsg{Code: 'a', Text: "a"}, "a"},
		{"shifted letter", tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift}, "A"},
		{"shifted symbol", tea.KeyPressMsg{Code: '1', Text: "!", Mod: tea.ModShift}, "!"},
		{"space", tea.KeyPressMsg{Code: ' ', Text: " "}, " "},
		{"non-latin", tea.KeyPressMsg{Code: 'ß', Text: "ß"}, "ß"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// `cat` echoes what it received, so the screen reflects what
			// crossed the pty rather than anything drawn locally. The markers
			// either side make a dropped key read as a gap rather than as an
			// empty screen, which could mean anything.
			term := start(t, "cat")
			term.SendText("<")
			term.SendKey(tc.key)
			term.SendText(">")
			awaitScreen(t, term, "<"+tc.want+">")
		})
	}
}

// Keys that carry no text stay with the emulator, which knows their escape
// sequences and the terminal modes those depend on.
func TestNonTextKeysStillReachTheProcess(t *testing.T) {
	term := start(t, "cat")
	term.SendText("one")
	term.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	term.SendText("two")

	awaitScreen(t, term, "one")
	awaitScreen(t, term, "two")
}

// Ctrl and alt combinations must not take the text path: their encoding is not
// the character they would otherwise produce.
func TestModifiedKeysGoThroughTheEmulatorsEncoder(t *testing.T) {
	// `cat -v` renders control characters visibly, so ctrl+g arriving as \a
	// shows up as ^G rather than ringing a bell into the void.
	term := start(t, "cat", "-v")
	term.SendKey(tea.KeyPressMsg{Code: 'g', Text: "g", Mod: tea.ModCtrl})
	awaitScreen(t, term, "^G")
}
