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

func TestTheSnifferReadsWhatAProgramAsksFor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   keyEncoding
	}{
		{"nothing asked", []string{"hello"}, encodingLegacy},
		{"kitty", []string{"\x1b[>1u"}, encodingKitty},
		{"kitty with other flags", []string{"\x1b[>5u"}, encodingKitty},
		{"modifyOtherKeys 2", []string{"\x1b[>4;2m"}, encodingModifyOtherKeys},
		{"modifyOtherKeys 1", []string{"\x1b[>4;1m"}, encodingModifyOtherKeys},
		{"both, kitty wins", []string{"\x1b[>1u\x1b[>4;2m"}, encodingKitty},
		{"both, either order", []string{"\x1b[>4;2m\x1b[>1u"}, encodingKitty},
		// Programs pop what they pushed on the way out, and a pane outlives one
		// program when its session is reattached to another.
		{"kitty then off", []string{"\x1b[>1u", "\x1b[>0u"}, encodingLegacy},
		{"modifyOtherKeys then 0", []string{"\x1b[>4;2m", "\x1b[>4;0m"}, encodingLegacy},
		{"modifyOtherKeys then bare", []string{"\x1b[>4;2m", "\x1b[>4m"}, encodingLegacy},
		// A request arrives in whatever chunks the pty hands over. A scan with no
		// carry misses one whenever the split lands inside it, which reads as the
		// protocol working sometimes.
		{"split mid-sequence", []string{"junk\x1b[>", "1u more"}, encodingKitty},
		{"split after ESC", []string{"\x1b", "[>1u"}, encodingKitty},
		{"split after CSI", []string{"\x1b[", ">1u"}, encodingKitty},
		{"split in the params", []string{"\x1b[>4;", "2m"}, encodingModifyOtherKeys},
		// Private modes we have no opinion about must not be mistaken for one.
		{"another private mode", []string{"\x1b[>0;1c\x1b[>c"}, encodingLegacy},
		{"a lone escape", []string{"\x1b[>"}, encodingLegacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var keys keyRequests
			s := &modeSniffer{next: discard{}, keys: &keys}
			for _, c := range tc.chunks {
				if _, err := s.Write([]byte(c)); err != nil {
					t.Fatal(err)
				}
			}
			if got := keys.encoding(); got != tc.want {
				t.Errorf("encoding is %v, want %v", got, tc.want)
			}
		})
	}
}

// The sniffer is on the path to the emulator, so anything it does to the byte
// count or the bytes themselves corrupts the screen. It reads and forwards.
func TestTheSnifferForwardsEveryByteUntouched(t *testing.T) {
	var keys keyRequests
	sink := &collector{}
	s := &modeSniffer{next: sink, keys: &keys}

	chunks := []string{"hello \x1b[>1u world", "\x1b[>4;2m", "plain"}
	for _, c := range chunks {
		n, err := s.Write([]byte(c))
		if err != nil {
			t.Fatal(err)
		}
		// io.Copy checks the count against what it handed over, so a count that
		// describes the carry-prefixed buffer instead of p reports a short write.
		if n != len(c) {
			t.Errorf("wrote %d of %d bytes for %q", n, len(c), c)
		}
	}
	want := "hello \x1b[>1u world\x1b[>4;2mplain"
	if got := string(sink.b); got != want {
		t.Errorf("the emulator received %q, want %q", got, want)
	}
}

// The carry is bounded: a program that writes a lone `ESC [ >` and then a lot of
// digits must not make a pane grow a buffer for its lifetime.
func TestAnUnfinishedSequenceDoesNotGrowForever(t *testing.T) {
	var keys keyRequests
	s := &modeSniffer{next: discard{}, keys: &keys}
	long := "\x1b[>"
	for range maxCarry * 4 {
		long += "1"
	}
	if _, err := s.Write([]byte(long)); err != nil {
		t.Fatal(err)
	}
	if len(s.carry) > maxCarry+3 {
		t.Errorf("the carry is holding %d bytes", len(s.carry))
	}
}

func TestEnterEncodesEveryModifier(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  tea.KeyMod
		want string
	}{
		{"none", 0, ""},
		{"shift", tea.ModShift, "\x1b[13;2u"},
		{"alt", tea.ModAlt, "\x1b[13;3u"},
		{"ctrl", tea.ModCtrl, "\x1b[13;5u"},
		{"shift+ctrl", tea.ModShift | tea.ModCtrl, "\x1b[13;6u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := enterKeyBytes(tc.mod, encodingKitty); got != tc.want {
				t.Errorf("enterKeyBytes = %q, want %q", got, tc.want)
			}
		})
	}
	// Unmodified enter returns "" whatever the encoding, so the ordinary path
	// keeps handling the key everything submits with.
	for _, enc := range []keyEncoding{encodingLegacy, encodingModifyOtherKeys, encodingKitty} {
		if got := enterKeyBytes(0, enc); got != "" {
			t.Errorf("plain enter under %v returned %q", enc, got)
		}
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

type collector struct{ b []byte }

func (c *collector) Write(p []byte) (int, error) {
	c.b = append(c.b, p...)
	return len(p), nil
}
