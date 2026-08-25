//go:build ghosttyvt

package vterm

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestShiftEnterReachesTheProgram is the bug: in a pane, shift+enter sent
// nothing a hosted program saw, so an agent that binds it to "newline, don't
// submit" had no way to accept a multi-line message.
//
// The pane's program is echoed through `cat -v`, so the screen shows the bytes
// that crossed the pty rather than anything drawn locally. `^[` is how cat -v
// renders ESC.
func TestShiftEnterReachesTheProgram(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request string // what the program asks its terminal for first
		want    string
	}{
		{
			// Ghostty encodes shift+enter this way with nothing asked for, and a pane
			// is a Ghostty terminal: `input/function_keys.zig` gives enter+shift that
			// sequence with no condition on any protocol, which is why binding
			// shift+enter works in Ghostty and not in xterm. #334 looked at this
			// output and read it as an invented sequence; it is the emulator's own
			// answer, and matching it is the point of hosting that emulator.
			name: "no request", request: "", want: "<^[[27;2;13~>",
		},
		{
			name: "kitty keyboard", request: "\x1b[>1u", want: "<^[[13;2u>",
		},
		{
			name: "modifyOtherKeys", request: "\x1b[>4;2m", want: "<^[[27;2;13~>",
		},
		{
			// Claude Code asks for both, in this order. Kitty is the better
			// protocol and asking for both means it prefers that one.
			name: "both", request: "\x1b[>1u\x1b[>4;2m", want: "<^[[13;2u>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The request comes out of the hosted program, which is the only path
			// that exercises the sniffer — and READY, printed after it, is how the
			// test knows the chunk carrying it has been read: the sniffer scans a
			// chunk before forwarding it, so a marker on screen means every
			// earlier byte has been through.
			term := start(t, "sh", "-c", "printf '"+shEscape(tc.request)+"'; echo READY; exec cat -v")
			awaitScreen(t, term, "READY")

			term.SendText("<")
			term.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
			term.SendText(">")
			awaitScreen(t, term, tc.want)
		})
	}
}

// TestModifyOtherKeysIsOffUntilAskedFor is what #334 actually was, found by
// chasing the shift+enter report: the encoder was being told modifyOtherKeys
// state 2 was on in every pane, because the library's
// setopt_from_terminal answers `true` whether the program asked for the mode,
// asked for it to be turned off, or never mentioned it.
//
// shift+enter is the one key that looks the same either way, which is how this
// hid. The keys below are the ones that do not: with the mode wrongly on, alt+b
// and alt+f — word-motion in readline, so in every shell and every agent prompt —
// arrive as ESC[27;3;98~ instead of ESC b, and typing a capital A sends
// ESC[27;2;65~ instead of an A.
func TestModifyOtherKeysIsOffUntilAskedFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		off  string // what the program gets having asked for nothing
		on   string // and having asked for modifyOtherKeys state 2
	}{
		{"alt+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt, Text: "b"}, "\x1bb", "\x1b[27;3;98~"},
		{"alt+f", tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt, Text: "f"}, "\x1bf", "\x1b[27;3;102~"},
		{"shift+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModShift, Text: "A"}, "A", "\x1b[27;2;65~"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "\x1b[Z", "\x1b[27;2;9~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quiet := ghosttyEncoderWithModes(t, "")
			if got := string(quiet.encodeKey(tc.key)); got != tc.off {
				t.Errorf("with nothing asked, %s encoded to %q, want %q", tc.name, got, tc.off)
			}
			// And the mode still works when it is asked for, which is the other half:
			// awp overrides the library's answer rather than pinning it to false.
			asked := ghosttyEncoderWithModes(t, `\033[>4;2m`)
			if got := string(asked.encodeKey(tc.key)); got != tc.on {
				t.Errorf("with the mode asked for, %s encoded to %q, want %q", tc.name, got, tc.on)
			}
		})
	}
}

// TestTheModeCanBeWithdrawn. A program may turn it back off, and the pane has to
// hear that too — otherwise a shell that enabled it for one prompt leaves every
// later keystroke in the wrong encoding.
func TestTheModeCanBeWithdrawn(t *testing.T) {
	g := ghosttyEncoderWithModes(t, `\033[>4;2m\033[>4;0m`)
	key := tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt, Text: "b"}
	if got := string(g.encodeKey(key)); got != "\x1bb" {
		t.Errorf("after the mode was withdrawn, alt+b encoded to %q, want %q", got, "\x1bb")
	}
}

// shEscape renders an escape sequence for a single-quoted sh printf format: the
// bytes reach the program's stdout, not our own.
func shEscape(seq string) string {
	return strings.ReplaceAll(seq, "\x1b", `\033`)
}

// Plain enter must stay a CR whatever was asked for. It is the key everything
// submits with, and an escape sequence in its place would break every program in
// a pane rather than one chord in one of them.
func TestPlainEnterIsStillACarriageReturn(t *testing.T) {
	term := start(t, "sh", "-c", `printf '\033[>1u'; echo READY; exec cat -v`)
	awaitScreen(t, term, "READY")
	term.SendText("one")
	term.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	term.SendText("two")
	awaitScreen(t, term, "one")
	awaitScreen(t, term, "two")
}
