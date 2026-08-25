//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ghosttyEncoder is a pane reduced to the thing these tests are about: what an
// input event turns into on the wire. The pane runs cat because it has to run
// something — the encoders are synced from a real terminal's modes.
func ghosttyEncoder(t *testing.T) *ghosttyTerm {
	t.Helper()
	return ghosttyEncoderWithModes(t, "")
}

// ghosttyEncoderWithModes is the same, for a program that has turned some modes
// on. modes is written into the pane by the program itself rather than into the
// terminal directly, because that is the only way the terminal learns a mode in
// the first place, and what the encoders read back is the point of the test.
func ghosttyEncoderWithModes(t *testing.T, modes string) *ghosttyTerm {
	t.Helper()
	script := "echo ready; exec cat"
	if modes != "" {
		script = "printf '" + modes + "'; " + script
	}
	term := ghosttyPane(t, paneTestW, paneTestH, exec.Command("sh", "-c", script))
	waitForScreen(t, term, "ready")
	g, ok := term.(*ghosttyTerm)
	if !ok {
		t.Fatalf("a ghostty pane is a %T, not a *ghosttyTerm", term)
	}
	return g
}

// The pane these tests run on. Big enough that a pointer at 3,4 is inside it and
// the coordinates in the assertions are not on a boundary.
const (
	paneTestW = 40
	paneTestH = 12
)

// The DEC private modes a program sets to ask for mouse reporting, spelled as the
// bytes it writes. 1000 is press and release only; 1003 adds bare motion; 1006 is
// the SGR report format, which is what everything modern uses.
const (
	modeMouseNormal = "\\033[?1000h\\033[?1006h"
	modeMouseAny    = "\\033[?1003h\\033[?1006h"
)

// TestCtrlLetterEncodesItsControlByte covers the whole C0 range in one loop,
// because the range is the rule: ctrl+letter is the letter's position in the
// alphabet, so ctrl+a is 0x01 and ctrl+z is 0x1a with nothing to decide in
// between.
//
// This is the test that was missing. ctrl+a, ctrl+e and ctrl+w — start of line,
// end of line, delete word, the three every shell and every agent's prompt binds
// — encoded to nothing at all, and the only symptom was that the keys did
// nothing, which reads as the hosted program ignoring them.
func TestCtrlLetterEncodesItsControlByte(t *testing.T) {
	g := ghosttyEncoder(t)
	for c := 'a'; c <= 'z'; c++ {
		if _, collides := ctrlKeyCollisions[c]; collides {
			continue
		}
		want := byte(c-'a') + 1
		got := g.encodeKey(tea.KeyPressMsg{Code: c, Mod: tea.ModCtrl})
		if len(got) != 1 || got[0] != want {
			t.Errorf("ctrl+%c encoded as %q, want the single byte %#02x", c, got, want)
		}
	}
}

// ctrlKeyCollisions are the three ctrl combinations whose byte is also a key of
// its own: ctrl+i is 0x09 is tab, ctrl+m is 0x0d is enter, ctrl+[ is 0x1b is
// escape. Each maps to the key awp is actually handed when someone presses it.
//
// libghostty encodes none of the three, and that is not a hole here, because the
// combination never reaches awp as itself. The keyboard's bytes are parsed by
// Bubble Tea before the deck sees anything, and a terminal sends 0x09 for both
// tab and ctrl+i — so what arrives is tab, which encodes. Ghostty-the-app gets
// these from AppKit already resolved to text, which is the same division of
// labour one layer up.
//
// It would become a hole if awp ever asked its own terminal for the Kitty
// keyboard protocol, since that is what makes ctrl+i distinguishable from tab.
// TestTheCollidingCombinationsArriveAsTheirOwnKeys is the guard: it pins the
// path that does work, so this stays a decision rather than an omission.
var ctrlKeyCollisions = map[rune]rune{
	'i': tea.KeyTab,
	'm': tea.KeyEnter,
	'[': tea.KeyEscape,
}

func TestTheCollidingCombinationsArriveAsTheirOwnKeys(t *testing.T) {
	g := ghosttyEncoder(t)
	want := map[rune]byte{tea.KeyTab: 0x09, tea.KeyEnter: 0x0d, tea.KeyEscape: 0x1b}
	for ctrl, named := range ctrlKeyCollisions {
		got := g.encodeKey(tea.KeyPressMsg{Code: named})
		if len(got) != 1 || got[0] != want[named] {
			t.Errorf("the key ctrl+%c arrives as, %q, encoded as %q, want %#02x",
				ctrl, tea.Key{Code: named}.String(), got, want[named])
		}
	}
}

// TestCtrlPunctuationEncodesItsControlByte is the other half of the C0 range, and
// the reason the punctuation keys are in the map at all.
func TestCtrlPunctuationEncodesItsControlByte(t *testing.T) {
	g := ghosttyEncoder(t)
	for code, want := range map[rune]byte{']': 0x1d, '\\': 0x1c, '/': 0x1f} {
		got := g.encodeKey(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl})
		if len(got) != 1 || got[0] != want {
			t.Errorf("ctrl+%c encoded as %q, want the single byte %#02x", code, got, want)
		}
	}
}

// TestAltPrefixesWithEscape is the second defect this file exists for, and it was
// invisible until the keys above started working.
//
// libghostty's encoder has one option it cannot read off the terminal —
// macos-option-as-alt — and setopt_from_terminal resets it to false on every
// call, so alt was being discarded after the sync. alt+b and alt+f are
// word-motion in readline: dead in every shell and every agent prompt in a pane.
func TestAltPrefixesWithEscape(t *testing.T) {
	g := ghosttyEncoder(t)
	for _, tc := range []struct {
		code rune
		want string
	}{
		{'b', "\x1bb"},
		{'f', "\x1bf"},
		{'d', "\x1bd"},
	} {
		if got := string(g.encodeKey(tea.KeyPressMsg{Code: tc.code, Mod: tea.ModAlt})); got != tc.want {
			t.Errorf("alt+%c encoded as %q, want %q", tc.code, got, tc.want)
		}
	}
}

// TestAPlainLetterEncodesAsItself guards the direction the fix could have broken.
// Naming the physical key changes what the encoder has to work with, and the keys
// that already worked went through the same call.
func TestAPlainLetterEncodesAsItself(t *testing.T) {
	g := ghosttyEncoder(t)
	for _, tc := range []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'a', Text: "a"}, "a"},
		{tea.KeyPressMsg{Code: 'a', ShiftedCode: 'A', Mod: tea.ModShift, Text: "A"}, "A"},
		{tea.KeyPressMsg{Code: '7', Text: "7"}, "7"},
		{tea.KeyPressMsg{Code: '.', Text: "."}, "."},
	} {
		if got := string(g.encodeKey(tc.key)); got != tc.want {
			t.Errorf("%v encoded as %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestANamedKeyStillEncodes, because the named keys are the ones the map held
// before and they must not have been disturbed by the letters moving in beside
// them. Only that something is sent: the exact bytes depend on modes the program
// sets, which is the encoder's business rather than this map's.
func TestANamedKeyStillEncodes(t *testing.T) {
	g := ghosttyEncoder(t)
	for _, k := range []rune{tea.KeyEnter, tea.KeyTab, tea.KeyEscape, tea.KeyUp, tea.KeyF5, tea.KeyBackspace} {
		if got := g.encodeKey(tea.KeyPressMsg{Code: k}); len(got) == 0 {
			t.Errorf("the key %q encoded to nothing", tea.Key{Code: k}.String())
		}
	}
}

// TestAnUnshiftedCodepointIsSuppliedForEveryPrintableKey pins the half of the fix
// that is not the key map. The codepoint used to be sent only when Bubble Tea
// supplied a BaseCode, which it does for a layout that renames the key — so on a
// US keyboard, never.
func TestAnUnshiftedCodepointIsSuppliedForEveryPrintableKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.Key
		want rune
	}{
		{"a plain letter", tea.Key{Code: 'a', Text: "a"}, 'a'},
		{"a modified letter, which carries no text", tea.Key{Code: 'w', Mod: tea.ModCtrl}, 'w'},
		{"shift, whose codepoint is still the unshifted one", tea.Key{Code: 'a', ShiftedCode: 'A', Mod: tea.ModShift, Text: "A"}, 'a'},
		{"a renamed key, where the layout's answer wins", tea.Key{Code: 'й', BaseCode: 'q'}, 'q'},
		{"a named key, which types nothing", tea.Key{Code: tea.KeyF5}, 0},
		{"an arrow, likewise", tea.Key{Code: tea.KeyUp}, 0},
	} {
		if got := unshiftedCodepoint(tc.key); got != tc.want {
			t.Errorf("%s: unshifted codepoint %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestTheWheelReachesTheProgram. Scrolling over a pane did nothing, and so did
// clicking, and the cause was neither the wheel nor the button: libghostty's mouse
// encoder is written for a renderer, so it takes positions in surface pixels and
// divides by a cell geometry the embedder supplies. awp supplied none, leaving the
// cell size zero — every event collapsed onto cell 1;1 and a press encoded to
// nothing at all.
//
// The numbers are SGR reports: 64 and 65 are the wheel's two buttons, 0 is the
// left button, and the coordinates are 1-based, so a pointer at 3,4 is 4;5.
func TestTheWheelReachesTheProgram(t *testing.T) {
	g := ghosttyEncoderWithModes(t, modeMouseNormal)
	for _, tc := range []struct {
		name string
		msg  tea.MouseMsg
		want string
	}{
		{"wheel up", tea.MouseWheelMsg{X: 3, Y: 4, Button: tea.MouseWheelUp}, "\x1b[<64;4;5M"},
		{"wheel down", tea.MouseWheelMsg{X: 3, Y: 4, Button: tea.MouseWheelDown}, "\x1b[<65;4;5M"},
		{"left click", tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft}, "\x1b[<0;4;5M"},
		{"release", tea.MouseReleaseMsg{X: 3, Y: 4, Button: tea.MouseLeft}, "\x1b[<0;4;5m"},
	} {
		if got := string(g.encodeMouse(tc.msg)); got != tc.want {
			t.Errorf("%s encoded as %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAMouseReportCarriesThePointersOwnCell is the same fix stated as the symptom
// it would have been caught by: with no geometry every report said 1;1, so a click
// anywhere in a pane landed in the top-left corner of the program's own idea of
// where you clicked.
func TestAMouseReportCarriesThePointersOwnCell(t *testing.T) {
	g := ghosttyEncoderWithModes(t, modeMouseNormal)
	for _, tc := range []struct {
		x, y int
		want string
	}{
		{0, 0, "\x1b[<0;1;1M"},
		{1, 0, "\x1b[<0;2;1M"},
		{0, 1, "\x1b[<0;1;2M"},
		{paneTestW - 1, paneTestH - 1, "\x1b[<0;40;12M"},
	} {
		msg := tea.MouseClickMsg{X: tc.x, Y: tc.y, Button: tea.MouseLeft}
		if got := string(g.encodeMouse(msg)); got != tc.want {
			t.Errorf("a click at %d,%d encoded as %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
}

// TestMotionIsReportedOnlyWhenTheProgramAsked, which is the encoder reading the
// terminal rather than awp deciding: 1000 is press and release, and bare motion
// under it is not an event the program wants to be woken for.
func TestMotionIsReportedOnlyWhenTheProgramAsked(t *testing.T) {
	normal := ghosttyEncoderWithModes(t, modeMouseNormal)
	if got := normal.encodeMouse(tea.MouseMotionMsg{X: 3, Y: 4}); len(got) != 0 {
		t.Errorf("motion under press/release tracking encoded as %q, want nothing", got)
	}
	any := ghosttyEncoderWithModes(t, modeMouseAny)
	if got := string(any.encodeMouse(tea.MouseMotionMsg{X: 3, Y: 4})); got != "\x1b[<35;4;5M" {
		t.Errorf("motion under any-event tracking encoded as %q, want %q", got, "\x1b[<35;4;5M")
	}
}
