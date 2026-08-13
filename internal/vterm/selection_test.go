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
