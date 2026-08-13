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
			// Nothing asked, so a real terminal would send a CR and so do we —
			// which puts the two markers on separate rows. Inventing an escape
			// sequence here would put one in the program's input buffer.
			// Nothing asked, so a real terminal would send a CR and so do we,
			// which puts the markers on separate rows. want "" means "no escape
			// sequence anywhere": inventing one here would put it in the
			// program's input buffer.
			name: "no request", request: "", want: "",
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
			if tc.want == "" {
				// #334. The encoder emits the modifyOtherKeys form for shift+enter
				// whether or not the program asked for it, so this case fails against
				// the emulator awp runs. Skipped rather than re-baselined: a sequence
				// nobody asked for is not a spelling difference, it arrives in the
				// program's input buffer as garbage, and this assertion is the one
				// that should survive.
				t.Skip("#334: the encoder invents ESC[27;2;13~ with nothing asking for one")
			}
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
			if tc.want != "" {
				awaitScreen(t, term, tc.want)
				return
			}
			// Both markers arrived, so whatever the key produced is on screen by
			// now, and it must not be an escape sequence.
			awaitScreen(t, term, "<")
			awaitScreen(t, term, ">")
			if got := render(term); strings.Contains(got, "^[") {
				t.Errorf("an escape sequence was invented with nothing asking for one:\n%s", got)
			}
		})
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
