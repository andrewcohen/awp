//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Where the cursor is, measured the way the deck will measure it.
//
// #339: the deck draws the cursor at an absolute column of the string View
// returns, so the only column that means anything to it is a column of that
// string. The emulator counts cells, and a grapheme's cell footprint is not
// always its rendered width — 👩‍💻 is four cells and two columns —
// so on a row whose prefix holds one, a cell column put the cursor beside the
// text instead of on it. Reported worst in a split, where a narrow half wraps a
// program's decorated status line onto the row being typed on.
//
// Each case types a payload with no trailing newline, so the cursor comes to rest
// immediately after it: the cursor's column and the rendered row's width are then
// the same number stated two ways, and the test is that they agree.

// cursorRow starts a shell that echoes s with no newline, and returns the cursor
// column the deck would be given alongside the row it would draw.
func cursorRow(t *testing.T, s string) (col int, row string) {
	t.Helper()
	term, err := Open(1, 40, 10, exec.Command("printf", "%s", s), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitExited(t, term, "printf did not finish")
	awaitScreen(t, term, lastGrapheme(s))

	lines := strings.Split(term.View(), "\n")
	cx, cy, _ := term.Cursor()
	if cy < 0 || cy >= len(lines) {
		t.Fatalf("the cursor is on row %d of a %d-row screen", cy, len(lines))
	}
	return cx, lines[cy]
}

// lastGrapheme is something from the payload to wait for that survives being
// rendered — the tail, which is past whatever prefix the case is about.
func lastGrapheme(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[len(r)-1:])
}

// TestTheCursorLandsWhereTheTextEnds for graphemes whose cell footprint and
// rendered width are the same number, and for the ones where they are not. The
// ZWJ case is the one that failed: four cells to the emulator, two columns
// rendered, so the cursor sat two columns right of the text it was supposed to
// follow.
func TestTheCursorLandsWhereTheTextEnds(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"plain", "hello"},
		{"cjk", "日本語です"},
		{"box drawing", "╭───╮ ok"},
		{"combining", "étude"},
		{"wide emoji", "ok 🚀"},
		{"zwj sequence", "ok 👩‍💻"},
		{"mixed", "│ 日本 🚀 x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			col, row := cursorRow(t, tc.payload)
			// The formatter drops a row's trailing blanks, so the row is exactly
			// the text and its width is where the text ends.
			if want := lipgloss.Width(row); col != want {
				t.Errorf("the cursor is at column %d; the text on its row ends at column %d (%q)",
					col, want, row)
			}
		})
	}
}

// TestAClickReachesTheCellItWasOver is the same translation backwards. The
// pointer was over a column of the rendered screen; the program is told about a
// cell. On a row where the two numbering schemes have drifted apart, sending the
// column through unchanged names a cell the user did not point at — so a click on
// the letter after a ZWJ sequence used to arrive two cells to its right.
func TestAClickReachesTheCellItWasOver(t *testing.T) {
	term, err := Open(1, 40, 10, exec.Command("printf", "%s", "ok 👩‍💻x"), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitExited(t, term, "printf did not finish")
	awaitScreen(t, term, "ok ")

	g, ok := term.(*ghosttyTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", term)
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	// "ok " is three cells and three columns, so there the two agree.
	for col := range 3 {
		if got := g.cellForCol(col, 0); got != col {
			t.Errorf("column %d is cell %d; plain text should need no translation", col, got)
		}
	}
	// The sequence renders in two columns and occupies four cells, and the x after
	// it is therefore cell 7 at column 5.
	if got := g.cellForCol(5, 0); got != 7 {
		t.Errorf("the x is drawn at column 5 and lives in cell 7; a click there reached cell %d", got)
	}
}

// TestTheCursorColumnIsCheapToAskFor. libghostty says of the grid-ref lookup
// behind this that it "isn't meant to be used as the core of a render loop", and
// the deck asks once a frame while a pane is up. So the answer is memoised on the
// write counter, and this is the check that the memo is actually consulted:
// nothing arrives between the two calls, so the second must not walk the row
// again.
func TestTheCursorColumnIsCheapToAskFor(t *testing.T) {
	term, err := Open(1, 40, 10, exec.Command("printf", "%s", "日本語です"), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitExited(t, term, "printf did not finish")
	awaitScreen(t, term, "です")

	first, _, _ := term.Cursor()
	g, ok := term.(*ghosttyTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", term)
	}
	g.mu.Lock()
	cached := g.colDisplay
	g.mu.Unlock()
	if cached != first {
		t.Errorf("Cursor answered %d but cached %d", first, cached)
	}
	if second, _, _ := term.Cursor(); second != first {
		t.Errorf("asked twice with nothing in between: %d then %d", first, second)
	}
}
