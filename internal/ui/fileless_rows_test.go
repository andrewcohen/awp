package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/review"
)

// Rows that belong to no file.
//
// The review-summary section at the top of the stream and the detached section at
// its foot are remarks about no particular file, and their rows say so by carrying
// file -1. The file cursor is an index into m.filtered, so copying that value into
// it produced an index that indexes nothing — and one panic inside a Bubble Tea
// program takes the whole deck down, which is what `ctrl+d` to the bottom and then
// `h` did.

// detachedModel is a viewer whose stream ends in a detached section, cursor parked
// on its last row — where holding ctrl+d leaves you.
func detachedModel(t *testing.T) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		// An anchor that cannot be located: this lands in the detached section.
		{ID: "c1", Author: review.AuthorHuman, Body: "orphan", State: review.Open,
			Anchor: review.Anchor{Path: "gone.go", Side: review.SideNew, LineHint: 99, Text: "nowhere"}},
		// And a remark about the change as a whole, which heads the stream.
		{ID: "c2", Author: review.AuthorHuman, Body: "reads well overall", State: review.Open},
	})
	m.cursorRow = len(m.stream.rows) - 1
	m.syncFileCursorToCursor()
	return m
}

// The file cursor must stay a usable index. Where you were is also the only true
// answer: you scrolled past the end of the last file, you did not move to another.
func TestAFilelessRowLeavesTheFileCursorAlone(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{{
		ID: "c1", Author: review.AuthorHuman, Body: "orphan", State: review.Open,
		Anchor: review.Anchor{Path: "gone.go", Side: review.SideNew, LineHint: 99, Text: "nowhere"},
	}})
	m.filesCursor = 0
	// Find a row in the detached section — one that belongs to no file.
	fileless := -1
	for i, r := range m.stream.rows {
		if r.file < 0 {
			fileless = i
			break
		}
	}
	if fileless < 0 {
		t.Fatal("fixture is wrong: expected a row belonging to no file")
	}
	m.cursorRow = fileless
	m.syncFileCursorToCursor()
	if m.filesCursor != 0 {
		t.Fatalf("expected the file cursor left where it was, got %d", m.filesCursor)
	}
}

// Every key, on a row belonging to no file. A sweep rather than the two keys that
// were reported: the crash was in a shared helper, so any key reaching it was
// affected, and the next one to be added would be too.
func TestNoKeyPanicsOnAFilelessRow(t *testing.T) {
	keys := []string{
		"h", "l", "0", "$", "j", "k", "g", "G", "{", "}", "[", "]",
		"w", "e", "r", "c", "i", "D", "v", "T", "R", "n", "N", "enter", "esc", "tab", "\\",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			m := detachedModel(t)
			if m.stream.rows[m.cursorRow].file >= 0 {
				t.Fatalf("fixture is wrong: the cursor is on a row of file %d",
					m.stream.rows[m.cursorRow].file)
			}
			// A panic here fails the subtest rather than the binary, which is the whole
			// difference between this test and the deck.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%q panicked on a row belonging to no file: %v", key, r)
				}
			}()
			m = press(m, key)
		})
	}
	// The half-page keys are how you get there in the first place.
	for _, k := range []tea.KeyType{tea.KeyCtrlD, tea.KeyCtrlU} {
		m := detachedModel(t)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%v panicked: %v", k, r)
				}
			}()
			updated, _ := m.Update(tea.KeyMsg{Type: k})
			m = updated.(Model)
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
			_ = updated
		}()
	}
}

// Holding ctrl+d to the foot of the change and then panning is the exact sequence
// that closed the deck.
func TestPanningAtTheBottomOfTheStreamDoesNotCloseTheDeck(t *testing.T) {
	m := detachedModel(t)
	for i := 0; i < 20; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
		m = updated.(Model)
	}
	for _, key := range []string{"h", "l", "$", "0"} {
		m = press(m, key)
	}
	// Still a working view afterwards, not just a survived keystroke.
	if out := m.renderStreamPanel(80, 20); strings.TrimSpace(stripANSI(out)) == "" {
		t.Fatal("expected the stream still to render")
	}
}
