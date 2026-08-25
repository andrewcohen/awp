//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"strings"
	"testing"
)

// The text of a range of cells, which is what a selection copies.
//
// It has to come from the grid rather than from the string View returns: the
// string has already lost the soft wraps and the padding that answering "what did
// I select" needs. A shell that wrapped one command over two rows selected as two
// lines is not what you pasted into it.

// selectionTerm runs printf and waits for the screen.
func selectionTerm(t *testing.T, w, h int, s, await string) Hosted {
	t.Helper()
	term, err := Open(1, w, h, exec.Command("printf", "%s", s), HostColors{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitExited(t, term, "printf did not finish")
	awaitScreen(t, term, await)
	return term
}

// TestSelectionTextTakesTheCellsBetweenTwoPoints.
func TestSelectionTextTakesTheCellsBetweenTwoPoints(t *testing.T) {
	term := selectionTerm(t, 40, 6, "hello world", "hello world")
	for _, tc := range []struct {
		name           string
		x0, y0, x1, y1 int
		want           string
	}{
		{"one word", 0, 0, 4, 0, "hello"},
		{"one character", 0, 0, 0, 0, "h"},
		{"across the space", 4, 0, 6, 0, "o w"},
		{"the whole line", 0, 0, 10, 0, "hello world"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := term.SelectionText(tc.x0, tc.y0, tc.x1, tc.y1); got != tc.want {
				t.Errorf("selecting (%d,%d)-(%d,%d) gave %q, want %q",
					tc.x0, tc.y0, tc.x1, tc.y1, got, tc.want)
			}
		})
	}
}

// TestSelectionTextReadsTheSameBackwards. A drag rightwards and the same drag
// leftwards select the same text, so the endpoints cannot be assumed ordered.
func TestSelectionTextReadsTheSameBackwards(t *testing.T) {
	term := selectionTerm(t, 40, 6, "hello world", "hello world")
	forward := term.SelectionText(0, 0, 4, 0)
	back := term.SelectionText(4, 0, 0, 0)
	if forward != back {
		t.Errorf("forwards gave %q and backwards %q", forward, back)
	}
	if forward != "hello" {
		t.Errorf("selected %q, want hello", forward)
	}
}

// TestSelectionTextDropsTheBlanksPaddingAShortLine. Every row of a terminal is
// the width of the screen; the spaces past the text are not something you selected.
func TestSelectionTextDropsTheBlanksPaddingAShortLine(t *testing.T) {
	term := selectionTerm(t, 40, 6, "short", "short")
	if got := term.SelectionText(0, 0, 39, 0); got != "short" {
		t.Errorf("selecting a whole row gave %q, want short with no padding", got)
	}
}

// TestSelectionTextSpansRows.
func TestSelectionTextSpansRows(t *testing.T) {
	term := selectionTerm(t, 40, 6, "one\r\ntwo\r\nthree", "three")
	got := term.SelectionText(0, 0, 4, 2)
	if want := "one\ntwo\nthree"; got != want {
		t.Errorf("selecting three rows gave %q, want %q", got, want)
	}
	// And the first row is cut at its column, not taken whole.
	if got := term.SelectionText(1, 0, 2, 1); !strings.HasPrefix(got, "ne") {
		t.Errorf("a selection starting mid-row gave %q, want it to start at ne", got)
	}
}

// TestSelectionTextOfAClosedTerminalIsEmpty rather than a crash: a pane can be
// torn down between a drag and the release that copies it.
func TestSelectionTextOfAClosedTerminalIsEmpty(t *testing.T) {
	term := selectionTerm(t, 40, 6, "hello", "hello")
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if got := term.SelectionText(0, 0, 4, 0); got != "" {
		t.Errorf("a closed terminal selected %q", got)
	}
}

// The word and the line a click lands on, which the emulator answers rather than
// awp (#357). Ghostty ships boundary rules and a logical-line notion; a second
// opinion derived from View's string would disagree with the text the same
// selection copies.

// TestWordAtIsTheWordUnderThePoint, with Ghostty's own boundaries.
func TestWordAtIsTheWordUnderThePoint(t *testing.T) {
	term := selectionTerm(t, 40, 3, "hello world", "hello world")
	for _, tc := range []struct {
		name   string
		at     int
		x0, x1 int
	}{
		{"inside the second word", 8, 6, 10},
		{"on its first character", 6, 6, 10},
		{"on its last", 10, 6, 10},
		{"inside the first word", 2, 0, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x0, y0, x1, y1, ok := term.WordAt(tc.at, 0)
			if !ok {
				t.Fatal("no word under a point that has one")
			}
			if x0 != tc.x0 || x1 != tc.x1 || y0 != 0 || y1 != 0 {
				t.Errorf("word spans (%d,%d)..(%d,%d), want (%d,0)..(%d,0)", x0, y0, x1, y1, tc.x0, tc.x1)
			}
			if got := term.SelectionText(x0, y0, x1, y1); got == "" {
				t.Error("the span the emulator gave back reads as empty text")
			}
		})
	}
}

// TestWordAtOnBlankSpaceTakesTheRunOfBlanks, which is Ghostty's answer and every
// other terminal's: a run of whitespace is a unit you can double-click, and the
// alternative — a click that selects nothing — reads as the gesture having missed.
//
// Recorded here because it is the kind of thing awp would otherwise "fix" back to
// its own idea of a word, and then the pane would disagree with the terminal it is
// running in about what a double-click does.
func TestWordAtOnBlankSpaceTakesTheRunOfBlanks(t *testing.T) {
	term := selectionTerm(t, 40, 3, "hi     there", "hi     there")
	x0, _, x1, _, ok := term.WordAt(4, 0)
	if !ok {
		t.Fatal("blank space selected nothing")
	}
	if x0 != 2 || x1 != 6 {
		t.Errorf("the blank run spans %d..%d, want the gap 2..6", x0, x1)
	}
}

// TestLineAtIsTheWholeLine, trimmed of the blanks that pad a short row out to the
// width of the screen.
func TestLineAtIsTheWholeLine(t *testing.T) {
	term := selectionTerm(t, 40, 3, "hello world", "hello world")
	x0, y0, x1, y1, ok := term.LineAt(8, 0)
	if !ok {
		t.Fatal("no line under a point on one")
	}
	if x0 != 0 || y0 != 0 || y1 != 0 {
		t.Fatalf("line spans (%d,%d)..(%d,%d), want a single row from column 0", x0, y0, x1, y1)
	}
	if got := term.SelectionText(x0, y0, x1, y1); strings.TrimSpace(got) != "hello world" {
		t.Errorf("the line reads as %q, want `hello world`", got)
	}
}

// TestLineAtCrossesASoftWrap. The logical line: a command too long for the width
// is one line, and selecting it means every row it wrapped over. Without this the
// highlight and the copied text would disagree about what a line is —
// SelectionText already unwraps.
func TestLineAtCrossesASoftWrap(t *testing.T) {
	const wide = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 30 chars into a 20-column screen
	term := selectionTerm(t, 20, 4, wide, "aaaa")
	_, y0, _, y1, ok := term.LineAt(2, 0)
	if !ok {
		t.Fatal("no line under a point on one")
	}
	if y1 == y0 {
		t.Errorf("the line stopped at row %d; a soft-wrapped line spans the rows it wrapped over", y0)
	}
}
