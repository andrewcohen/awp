package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// searchModel is a viewer over one file whose lines are distinguishable, tall
// enough that a match can be centred.
func searchModel(t *testing.T) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 20)
	m.focus = FocusHunks
	return loadWith(m, 1, fileWith("a.go", 1,
		"package main",
		"func alpha() {}",
		"func beta() {}",
		"// alpha again",
		"func gamma() {}",
	))
}

// typing feeds a query one rune at a time, the way the prompt receives it — an
// incremental search has to be right at every prefix, not only at the end.
func typing(m Model, q string) Model {
	for _, r := range q {
		m = press(m, string(r))
	}
	return m
}

// `/` from the diff searches its content. From the lists it still filters files —
// which is the pane where filtering is what you want.
func TestSlashSearchesFromTheDiffAndFiltersFromTheLists(t *testing.T) {
	m := searchModel(t)
	if got := press(m, "/").focus; got != FocusSearch {
		t.Fatalf("expected `/` in the diff to open search, got %v", got)
	}
	m.focus = FocusFiles
	if got := press(m, "/").focus; got != FocusFilter {
		t.Fatalf("expected `/` in the file list to open the filter, got %v", got)
	}
}

// The match is found through the row set and the cursor moves to it.
func TestSearchMovesTheCursorToTheMatch(t *testing.T) {
	m := typing(press(searchModel(t), "/"), "beta")
	if got := m.lineText(m.stream.rows[m.cursorRow]); got != "func beta() {}" {
		t.Fatalf("expected the cursor on the matching line, got %q", got)
	}
	if !strings.Contains(m.status, "1 of 1") {
		t.Fatalf("expected the count in the status, got %q", m.status)
	}
}

// Case-insensitive, like every other search in a terminal.
func TestSearchIgnoresCase(t *testing.T) {
	m := typing(press(searchModel(t), "/"), "BETA")
	if got := m.lineText(m.stream.rows[m.cursorRow]); got != "func beta() {}" {
		t.Fatalf("expected a case-insensitive match, got %q", got)
	}
}

// n/N step matches and wrap at the ends. Two lines hold "alpha", so stepping has
// somewhere to go.
func TestNAndNStepMatchesAndWrap(t *testing.T) {
	m := typing(press(searchModel(t), "/"), "alpha")
	m = press(m, "enter")
	if m.focus != FocusHunks {
		t.Fatalf("expected enter to hand the keyboard back to the diff, got %v", m.focus)
	}
	first := m.cursorRow

	m = press(m, "n")
	second := m.cursorRow
	if second == first {
		t.Fatal("expected n to advance to the next match")
	}
	if !strings.Contains(m.status, "2 of 2") {
		t.Fatalf("expected to be on match 2 of 2, got %q", m.status)
	}
	// Wraps back to the first rather than stopping at the end.
	if m = press(m, "n"); m.cursorRow != first {
		t.Fatalf("expected n to wrap to the first match, got row %d want %d", m.cursorRow, first)
	}
	// And backwards wraps the other way.
	if m = press(m, "N"); m.cursorRow != second {
		t.Fatalf("expected N to wrap to the last match, got row %d want %d", m.cursorRow, second)
	}
}

// The query outlives the prompt: n/N after confirming is the whole point of
// having typed one.
func TestSearchKeepsTheQueryAfterEnter(t *testing.T) {
	m := press(typing(press(searchModel(t), "/"), "gamma"), "enter")
	if m.searchQuery != "gamma" {
		t.Fatalf("expected the query kept, got %q", m.searchQuery)
	}
}

// esc abandons: no query, and the cursor back where it started. A search you
// changed your mind about should leave no trace.
func TestEscapeAbandonsTheSearchAndRestoresTheCursor(t *testing.T) {
	m := searchModel(t)
	m.cursorRow = 2
	origin := m.cursorRow

	m = typing(press(m, "/"), "gamma")
	if m.cursorRow == origin {
		t.Fatal("fixture is wrong: the search should have moved the cursor")
	}
	m = press(m, "esc")

	if m.searchQuery != "" {
		t.Fatalf("expected the query cleared, got %q", m.searchQuery)
	}
	if m.cursorRow != origin {
		t.Fatalf("expected the cursor back at %d, got %d", origin, m.cursorRow)
	}
	if m.status != "" {
		t.Fatalf("expected no leftover status, got %q", m.status)
	}
	if m.focus != FocusHunks {
		t.Fatalf("expected the keyboard back on the diff, got %v", m.focus)
	}
}

// Each keystroke searches from where the search started, not from the last
// match — otherwise narrowing a query walks the cursor further down the file
// with every rune.
func TestIncrementalSearchRestartsFromTheOrigin(t *testing.T) {
	m := searchModel(t)
	m.cursorRow = 0

	// "alpha" matches line 2 first; typing the whole word must land there rather
	// than at the second "alpha" further down, which is where searching from each
	// successive match would end up.
	m = typing(press(m, "/"), "alpha")
	if got := m.lineText(m.stream.rows[m.cursorRow]); got != "func alpha() {}" {
		t.Fatalf("expected the first match, got %q", got)
	}
	if !strings.Contains(m.status, "1 of 2") {
		t.Fatalf("expected to be on the first of two matches, got %q", m.status)
	}
}

// A query matching nothing says so, and leaves the cursor alone rather than
// jumping somewhere arbitrary.
func TestNoMatchSaysSo(t *testing.T) {
	m := searchModel(t)
	m.cursorRow = 3
	at := m.cursorRow
	m = typing(press(m, "/"), "zzz")
	if !strings.Contains(m.status, "no match") {
		t.Fatalf("expected a no-match message, got %q", m.status)
	}
	if m.cursorRow != at {
		t.Fatalf("expected the cursor to stay at %d, got %d", at, m.cursorRow)
	}
}

// A folded file contributes no line rows, so its content is genuinely not
// searchable. Say that, rather than letting a hit inside a file you reviewed read
// as an absence.
func TestNoMatchAccountsForFoldedFiles(t *testing.T) {
	m := searchModel(t)
	m.ReviewedFiles = map[string]string{"a.go": fileContentHash(m.filtered[0])}
	m.rebuildStream()

	m = typing(press(m, "/"), "beta")
	if !strings.Contains(m.status, "no match") {
		t.Fatalf("expected no match with the file folded, got %q", m.status)
	}
	if !strings.Contains(m.status, "1 file folded") {
		t.Fatalf("expected the folded file accounted for, got %q", m.status)
	}
}

// A wrapped line occupies several rows; matching each would make n step through
// one long line as though it were several hits.
func TestSearchMatchesAWrappedLineOnce(t *testing.T) {
	long := strings.Repeat("needle and more text ", 12)
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(80, 20)
	m.focus = FocusHunks
	m.wrap = true
	m = loadWith(m, 1, fileWith("a.go", 1, long))

	m.searchQuery = "needle"
	if got := len(m.searchMatches()); got != 1 {
		t.Fatalf("expected one match for one wrapped line, got %d", got)
	}
}

// The host has to keep its hands off the keyboard while a query is being typed,
// or `q` closes the whole view mid-search.
func TestSearchTakesTheKeyboardFromTheHost(t *testing.T) {
	m := press(searchModel(t), "/")
	if !m.Filtering() {
		t.Fatal("expected the search prompt to claim the keyboard")
	}
	if m = press(m, "enter"); m.Filtering() {
		t.Fatal("expected the keyboard released once the search is confirmed")
	}
}

// n with no query is not a mistake worth a message.
func TestStepWithNoQueryIsANoOp(t *testing.T) {
	m := searchModel(t)
	m.cursorRow = 2
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := updated.(Model)
	if got.cursorRow != 2 || got.status != "" {
		t.Fatalf("expected n to do nothing, got row %d status %q", got.cursorRow, got.status)
	}
}
