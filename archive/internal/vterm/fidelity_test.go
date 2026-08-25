//go:build ghosttyvt

package vterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// styledLine is one styled cell and the emulator's serialization of it.
//
// want is exact on purpose. The emulator is the only thing standing between a
// hosted program's output and what awp can repaint, so "close enough" is not a
// property worth asserting — if the serialization changes, someone should look
// at it and decide, not have the test quietly accept it.
//
// Exact means exact to libghostty-vt, which is the emulator. These strings were
// x/vt's until it was deleted, and the two disagree about spelling while agreeing
// about appearance: a line opens with an explicit reset, a reset is `ESC[0m` rather
// than `ESC[m`, and a basic colour is normalised to its 256-colour index. None of
// that is visible on screen. Re-baselining rather than normalising in the test is
// deliberate — a comparison that forgave spelling would also forgive an attribute
// quietly changing meaning.
type styledLine struct{ name, in, want string }

// reset is how the emulator spells "no attributes", which it emits both before a
// styled line and after it.
const reset = "\x1b[0m"

var styledLines = []styledLine{
	{"truecolor-fg", "\x1b[38;2;255;100;50mX\x1b[0m", reset + "\x1b[38;2;255;100;50mX" + reset},
	{"256-fg", "\x1b[38;5;208mX\x1b[0m", reset + "\x1b[38;5;208mX" + reset},
	// A basic colour comes back as its palette index: 31 is palette 1, and the
	// terminal resolves either spelling through the same slot.
	{"basic-fg", "\x1b[31mX\x1b[0m", reset + "\x1b[38;5;1mX" + reset},
	{"bg", "\x1b[48;5;24mX\x1b[0m", reset + "\x1b[48;5;24mX" + reset},
	{"bold", "\x1b[1mX\x1b[0m", reset + "\x1b[1mX" + reset},
	{"dim", "\x1b[2mX\x1b[0m", reset + "\x1b[2mX" + reset},
	{"italic", "\x1b[3mX\x1b[0m", reset + "\x1b[3mX" + reset},
	{"underline", "\x1b[4mX\x1b[0m", reset + "\x1b[4mX" + reset},
	{"curly-underline", "\x1b[4:3mX\x1b[0m", reset + "\x1b[4:3mX" + reset},
	// Two SGRs, in the order they were fed. x/vt merged these into one and
	// reordered the params; this is the more faithful of the two.
	{"underline-color", "\x1b[4m\x1b[58;2;255;0;0mX\x1b[0m", reset + "\x1b[4m\x1b[58;2;255;0;0mX" + reset},
	{"strikethrough", "\x1b[9mX\x1b[0m", reset + "\x1b[9mX" + reset},
	{"reverse", "\x1b[7mX\x1b[0m", reset + "\x1b[7mX" + reset},
	// Unstyled text needs no reset, so these are the bytes that went in.
	{"emoji", "\U0001F389|", "\U0001F389|"},
	{"cjk", "日|", "日|"},
	{"zwj-sequence", "\U0001F469\u200d\U0001F4BB|", "\U0001F469\u200d\U0001F4BB|"},
	{"precomposed-accent", "\u00e9|", "\u00e9|"},
	// A grapheme built from more than one code point comes back with a trailing
	// reset, where a single-code-point one does not.
	{"decomposed-accent", "e\u0301|", "e\u0301|" + reset},
}

// TestTheEmulatorKeepsTheStylingItIsGiven pins how much of a hosted program's
// appearance survives being turned into a string awp can place in a layout.
//
// This is the ceiling on how good a pane can ever look: nothing downstream —
// not lipgloss, not the deck — can restore an attribute the emulator dropped.
func TestTheEmulatorKeepsTheStylingItIsGiven(t *testing.T) {
	got := renderLines(t, lineInputs(styledLines))
	for i, tc := range styledLines {
		if got[i] != tc.want {
			t.Errorf("%s:\n  fed      %q\n  rendered %q\n  want     %q",
				tc.name, tc.in, got[i], tc.want)
		}
	}
}

// TestTheEmulatorKeepsCombiningMarks. x/vt dropped them — "e" + U+0301 rendered
// as a bare "e" — and that was one of the defects that decided this emulator.
// It is in the corpus above as a case rather than here as a known gap; this is
// the one that says so out loud, because a regression would be invisible in a
// diff of the table.
func TestTheEmulatorKeepsCombiningMarks(t *testing.T) {
	got := renderLines(t, []string{"e\u0301|"})
	if got[0] != "e\u0301|"+reset {
		t.Errorf("a combining mark rendered as %q, want it kept", got[0])
	}
}

// TestTheEmulatorDropsHyperlinks documents a known gap: OSC 8 does not survive
// the round trip at all. The text of the link is there and the link is not, so a
// hosted program's hyperlinks are inert in a pane.
//
// x/vt had the same outcome by a different route — its parser assigned the URI to
// the params slot and the params to the URI, which a terminal reads as a link
// *close* — so this is not a regression, and no surface awp has today depends on
// it. Asserting the wrong answer is deliberate: when this starts failing the
// emulator has grown the feature, and the case belongs in the corpus above.
func TestTheEmulatorDropsHyperlinks(t *testing.T) {
	got := renderLines(t, []string{"\x1b]8;;https://example.com\x07X\x1b]8;;\x07"})
	if want := "X" + reset; got[0] != want {
		t.Errorf("hyperlink round-tripped as %q, want %q — if the link now survives, "+
			"move the case into styledLines and delete this test", got[0], want)
	}
}

func lineInputs(lines []styledLine) []string {
	in := make([]string, len(lines))
	for i, l := range lines {
		in[i] = l.in
	}
	return in
}

// renderLines feeds each input as its own line through a real pty and returns
// the emulator's rendering of it, trailing padding removed.
func renderLines(t *testing.T, in []string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(strings.Join(in, "\r\n")+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	term, err := Open(1, 40, len(in)+2, exec.Command("cat", path), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitExited(t, term, "cat never finished writing the payload")

	lines := strings.Split(term.View(), "\n")
	if len(lines) < len(in) {
		t.Fatalf("the emulator rendered %d lines for %d inputs", len(lines), len(in))
	}
	out := make([]string, len(in))
	for i := range in {
		out[i] = strings.TrimRight(lines[i], " ")
	}
	return out
}
