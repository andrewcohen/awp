package vterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// styledLine is one styled cell and the emulator's serialization of it.
//
// want is exact on purpose. The emulator is the only thing standing between a
// hosted program's output and what awp can repaint, so "close enough" is not a
// property worth asserting — if the serialization changes, someone should look
// at it and decide, not have the test quietly accept it.
type styledLine struct{ name, in, want string }

var styledLines = []styledLine{
	{"truecolor-fg", "\x1b[38;2;255;100;50mX\x1b[0m", "\x1b[38;2;255;100;50mX\x1b[m"},
	{"256-fg", "\x1b[38;5;208mX\x1b[0m", "\x1b[38;5;208mX\x1b[m"},
	{"basic-fg", "\x1b[31mX\x1b[0m", "\x1b[31mX\x1b[m"},
	{"bg", "\x1b[48;5;24mX\x1b[0m", "\x1b[48;5;24mX\x1b[m"},
	{"bold", "\x1b[1mX\x1b[0m", "\x1b[1mX\x1b[m"},
	{"dim", "\x1b[2mX\x1b[0m", "\x1b[2mX\x1b[m"},
	{"italic", "\x1b[3mX\x1b[0m", "\x1b[3mX\x1b[m"},
	{"underline", "\x1b[4mX\x1b[0m", "\x1b[4mX\x1b[m"},
	{"curly-underline", "\x1b[4:3mX\x1b[0m", "\x1b[4:3mX\x1b[m"},
	// The attributes come back merged into one SGR and reordered, which is
	// equivalent — a terminal reads the params as a set.
	{"underline-color", "\x1b[4m\x1b[58;2;255;0;0mX\x1b[0m", "\x1b[58;2;255;0;0;4mX\x1b[m"},
	{"strikethrough", "\x1b[9mX\x1b[0m", "\x1b[9mX\x1b[m"},
	{"reverse", "\x1b[7mX\x1b[0m", "\x1b[7mX\x1b[m"},
	{"emoji", "\U0001F389|", "\U0001F389|"},
	{"cjk", "日|", "日|"},
	{"zwj-sequence", "\U0001F469\u200d\U0001F4BB|", "\U0001F469\u200d\U0001F4BB|"},
	{"precomposed-accent", "\u00e9|", "\u00e9|"},
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

// TestTheEmulatorDropsCombiningMarks documents a known gap: a decomposed
// character loses its combining mark, so "e" + U+0301 renders as a bare "e".
// Precomposed U+00E9 is fine, which is most text in practice.
//
// Asserting the wrong answer is deliberate. When this test starts failing,
// x/vt has fixed it and this file should simply lose the case.
func TestTheEmulatorDropsCombiningMarks(t *testing.T) {
	got := renderLines(t, []string{"e\u0301|"})
	if got[0] != "e|" {
		t.Errorf("combining marks now survive as %q — good news; delete this test", got[0])
	}
}

// TestTheEmulatorTransposesHyperlinkFields documents a known gap, and an
// upstream bug rather than a missing feature.
//
// OSC 8 is `OSC 8 ; params ; URI`, but x/vt's parser (osc.go, handleHyperlink)
// assigns parts[1] to the URL and parts[2] to the params — swapped. The
// renderer then emits them in the correct order, so a link arrives with its
// URI in the params slot and an empty URI, which a terminal reads as a link
// *close*. Every hyperlink a hosted program emits is inert.
func TestTheEmulatorTransposesHyperlinkFields(t *testing.T) {
	got := renderLines(t, []string{"\x1b]8;;https://example.com\x07X\x1b]8;;\x07"})
	const transposed = "\x1b]8;https://example.com;\aX\x1b]8;;\a"
	if got[0] != transposed {
		t.Errorf("hyperlink round-tripped as %q; if it is now correct, delete this test", got[0])
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
	term, err := Start(1, 40, len(in)+2, exec.Command("cat", path), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	select {
	case <-term.done:
	case <-time.After(5 * time.Second):
		t.Fatal("cat never finished writing the payload")
	}

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
