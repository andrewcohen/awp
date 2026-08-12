package ui

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/diff"
)

// loadWith applies a diff reload carrying an explicit fingerprint, so tests can
// control whether a reload counts as a change.
func loadWith(m Model, fingerprint uint64, files ...diff.FileDiff) Model {
	updated, _ := m.Update(diffLoadedMsg{files: files, fingerprint: fingerprint})
	return updated.(Model)
}

// fileWith builds a one-hunk file whose lines are the given contents, all
// context lines so numbering is simple.
func fileWith(name string, start int, contents ...string) diff.FileDiff {
	lines := make([]diff.HunkLine, 0, len(contents))
	for _, c := range contents {
		lines = append(lines, diff.HunkLine{Type: ' ', Content: c})
	}
	return diff.FileDiff{NewPath: name, Status: "M", Hunks: []diff.Hunk{
		{OldStart: start, NewStart: start, OldCount: len(lines), NewCount: len(lines), Lines: lines},
	}}
}

func anchorModel(t *testing.T) Model {
	t.Helper()
	m := New("/repo", func(int) (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 10)
	m.focus = FocusHunks
	return m
}

// cursorText is the diff-line content under the cursor, for asserting the
// cursor stayed on the same *line* rather than the same index.
func cursorText(m Model) string {
	if len(m.stream.rows) == 0 {
		return ""
	}
	return m.lineText(m.stream.rows[m.cursorRow])
}

// The case that made auto-refresh unusable: content is inserted above the
// cursor, so every row below shifts. The cursor must stay on its line.
func TestReloadKeepsCursorOnItsLineWhenRowsShift(t *testing.T) {
	m := anchorModel(t)
	m = loadWith(m, 1, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	// Move onto "gamma".
	for cursorText(m) != "gamma" {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached gamma")
		}
	}

	m = loadWith(m, 2, fileWith("a.go", 1, "alpha", "inserted", "beta", "gamma"))
	if got := cursorText(m); got != "gamma" {
		t.Fatalf("expected the cursor to stay on gamma, got %q", got)
	}
}

// An unchanged reload must not disturb anything at all — this is what makes
// polling invisible.
func TestUnchangedReloadIsANoOp(t *testing.T) {
	m := anchorModel(t)
	files := []diff.FileDiff{fileWith("a.go", 1, "alpha", "beta", "gamma", "delta", "epsilon")}
	m = loadWith(m, 1, files...)
	m = pressTimes(m, "j", 3)
	m = press(m, "l") // pan too, to prove it survives
	cursor, scroll, pan := m.cursorRow, m.streamScroll, m.hunkHScroll

	m = loadWith(m, 1, files...)
	if m.cursorRow != cursor || m.streamScroll != scroll || m.hunkHScroll != pan {
		t.Fatalf("unchanged reload disturbed the view: cursor %d→%d scroll %d→%d pan %d→%d",
			cursor, m.cursorRow, scroll, m.streamScroll, pan, m.hunkHScroll)
	}
}

// The cursor's distance from the top of the viewport is preserved, so the
// surrounding text doesn't slide under the reader.
func TestReloadPreservesCursorScreenPosition(t *testing.T) {
	m := anchorModel(t)
	long := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		long = append(long, "line"+strings.Repeat("x", i%3))
	}
	m = loadWith(m, 1, fileWith("a.go", 1, long...))
	m = pressTimes(m, "j", 25)
	offset := m.cursorRow - m.streamScroll
	if offset <= 0 {
		t.Fatalf("fixture should have scrolled, offset %d", offset)
	}

	// Reload with one line added at the top: rows shift by one.
	m = loadWith(m, 2, fileWith("a.go", 1, append([]string{"added"}, long...)...))
	if got := m.cursorRow - m.streamScroll; got != offset {
		t.Fatalf("cursor screen offset changed: %d → %d", offset, got)
	}
}

// When the anchored line is gone, the cursor lands nearby in the same file
// rather than at the top.
func TestReloadFallsBackToNearbyLine(t *testing.T) {
	m := anchorModel(t)
	m = loadWith(m, 1, fileWith("a.go", 1, "alpha", "beta", "gamma", "delta"))
	for cursorText(m) != "gamma" {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached gamma")
		}
	}
	row := m.cursorRow

	// gamma is replaced; its neighbours survive.
	m = loadWith(m, 2, fileWith("a.go", 1, "alpha", "beta", "rewritten", "delta"))
	if m.cursorRow == 0 {
		t.Fatal("expected the cursor to stay in the file, not jump to the top")
	}
	if got := m.stream.rows[m.cursorRow].kind; got != rowLine {
		t.Fatalf("expected to land on a line row, got %v", got)
	}
	if delta := m.cursorRow - row; delta > 1 || delta < -1 {
		t.Fatalf("expected to land near row %d, got %d", row, m.cursorRow)
	}
}

// When the anchored file disappears entirely, the reload must not panic and
// must leave the cursor somewhere valid.
func TestReloadSurvivesAnchoredFileDisappearing(t *testing.T) {
	m := anchorModel(t)
	m = loadWith(m, 1, fileWith("a.go", 1, "alpha", "beta"), fileWith("b.go", 1, "one", "two"))
	m = pressTimes(m, "j", 2)

	m = loadWith(m, 2, fileWith("b.go", 1, "one", "two"))
	if m.cursorRow < 0 || m.cursorRow >= len(m.stream.rows) {
		t.Fatalf("cursor %d out of range for %d rows", m.cursorRow, len(m.stream.rows))
	}
	if !cursorVisible(m) {
		t.Fatalf("cursor %d not visible at scroll %d", m.cursorRow, m.streamScroll)
	}
}

// Anchors follow a file across a rename, since identity is the path the diff
// reports rather than the rendered "old → new" label.
func TestAnchorIdentityUsesRealPathNotDisplayPath(t *testing.T) {
	renamed := diff.FileDiff{
		OldPath: "old.go", NewPath: "new.go", Status: "R",
		Hunks: []diff.Hunk{{OldStart: 1, NewStart: 1, Lines: []diff.HunkLine{{Type: ' ', Content: "alpha"}}}},
	}
	if got := pathOf(renamed); got != "new.go" {
		t.Fatalf("expected the new path as identity, got %q", got)
	}
	deleted := diff.FileDiff{OldPath: "gone.go", Status: "D"}
	if got := pathOf(deleted); got != "gone.go" {
		t.Fatalf("expected the old path for a deletion, got %q", got)
	}
}

// Duplicate line content is ordinary in code, so a text match must be narrowed
// by line number — otherwise any edit above the cursor flings it to the first
// identical line in the file.
func TestReloadDoesNotJumpToADuplicateLine(t *testing.T) {
	m := anchorModel(t)
	// "}" appears three times; the cursor sits on the last one.
	m = loadWith(m, 1, fileWith("a.go", 1, "}", "a", "}", "b", "}", "c"))
	last := -1
	for i, r := range m.stream.rows {
		if r.kind == rowLine && m.lineText(r) == "}" {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("fixture should contain a } line")
	}
	m.cursorRow = last
	m.followCursor()
	wantNo := m.stream.rows[last].newNo

	// Insert above: every line renumbers, content is unchanged.
	m = loadWith(m, 2, fileWith("a.go", 1, "inserted", "}", "a", "}", "b", "}", "c"))
	r := m.stream.rows[m.cursorRow]
	if m.lineText(r) != "}" {
		t.Fatalf("expected to stay on a } line, got %q", m.lineText(r))
	}
	if r.newNo != wantNo+1 {
		t.Fatalf("expected to land on the shifted third } (line %d), got line %d", wantNo+1, r.newNo)
	}
}
