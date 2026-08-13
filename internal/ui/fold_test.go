package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
)

// Folding a file, which is not marking it reviewed.
//
// The two were one thing: a file's body was hidden exactly when it carried a
// reviewed mark, so the only way to close a file was to claim you had read it and
// the only way to reopen one was to withdraw the claim. A fold is a reading
// position — "not this, not now" — and it has to be sayable without asserting
// anything about the review.

// foldModel is a viewer over two files, cursor in the diff.
func foldModel(t *testing.T) Model {
	t.Helper()
	return streamModel(t, twoFiles()...)
}

// pressEnter sends enter, which the rune helper cannot spell.
func pressEnter(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return got
}

// TestEnterFoldsTheFileAtTheCursor, and again opens it.
func TestEnterFoldsTheFileAtTheCursor(t *testing.T) {
	m := foldModel(t)
	path := pathOf(m.filtered[0])
	if m.isCollapsed(path) {
		t.Fatalf("%s starts folded, so this proves nothing", path)
	}
	m = pressEnter(t, m)
	if !m.isCollapsed(path) {
		t.Fatalf("enter did not fold %s", path)
	}
	m = pressEnter(t, m)
	if m.isCollapsed(path) {
		t.Errorf("enter again did not open %s", path)
	}
}

// TestFoldingIsNotMarkingItReviewed. The whole point of the separation: pressing
// enter must not persist a claim about having read the file, and MarkReviewed is
// how that claim leaves this package.
func TestFoldingIsNotMarkingItReviewed(t *testing.T) {
	m := foldModel(t)
	marked := 0
	m.MarkReviewed = func(string, string) error { marked++; return nil }
	m = pressEnter(t, m)
	if marked != 0 {
		t.Errorf("folding a file recorded %d reviewed mark(s)", marked)
	}
	if len(m.ReviewedFiles) != 0 {
		t.Errorf("folding a file left %v in the reviewed set", m.ReviewedFiles)
	}
}

// TestAFoldedFileDoesNotClaimToBeReviewed. The divider says why a file is closed,
// and for a folded one that reason is not "✓ reviewed" — a claim you did not make
// about a file you only put away.
func TestAFoldedFileDoesNotClaimToBeReviewed(t *testing.T) {
	m := foldModel(t)
	m = pressEnter(t, m)
	row := m.stream.rows[m.cursorRow]
	header := ansi.Strip(m.renderStreamFileHeader(row, 100))
	// It still says what is inside it, so the divider is not a wall.
	if !strings.Contains(header, "hidden") {
		t.Errorf("a folded file's divider does not say what it is hiding: %q", header)
	}
	if strings.Contains(header, "reviewed") {
		t.Errorf("a folded file's divider claims it is reviewed: %q", header)
	}
}

// TestOpeningAReviewedFileKeepsItOpen. A fold you stated outranks the reviewed
// rule — otherwise the only way to read a reviewed file again is to withdraw the
// mark, which is the coupling this exists to break.
func TestOpeningAReviewedFileKeepsItOpen(t *testing.T) {
	m := foldModel(t)
	path := pathOf(m.filtered[0])
	next, _ := m.toggleReviewed()
	m = next.(Model)
	if !m.isCollapsed(path) {
		t.Fatal("marking the file reviewed did not fold it")
	}
	// Marking a file reviewed moves on to the next one (#50), so come back to it
	// first: the fold acts on the file at the cursor.
	m.cursorRow = m.stream.fileStart[0]
	m = pressEnter(t, m)
	if m.isCollapsed(path) {
		t.Error("enter did not open the reviewed file")
	}
	if _, still := m.ReviewedFiles[path]; !still {
		t.Error("opening a reviewed file withdrew the reviewed mark")
	}
}

// TestEnterStillFoldsAConversation. The key had a meaning before this and keeps
// it: a thread at the cursor is what enter is about, and the file is what it falls
// back to.
func TestEnterStillFoldsAConversation(t *testing.T) {
	m := foldModel(t)
	if _, onThread := m.foldableThreadAtCursor(); onThread {
		t.Skip("the cursor starts on a conversation in this fixture")
	}
	path := pathOf(m.filtered[0])
	m = pressEnter(t, m)
	if !m.isCollapsed(path) {
		t.Error("with no conversation at the cursor, enter did not fall through to the file")
	}
}

// TestTheCursorSurvivesAFold. A folded file's body has no rows left to stand on,
// so a plain clamp parks the cursor wherever the rows that vanished used to be —
// in the next file, or off the end.
func TestTheCursorSurvivesAFold(t *testing.T) {
	m := foldModel(t)
	path := pathOf(m.filtered[0])
	m = pressEnter(t, m)
	if m.cursorRow < 0 || m.cursorRow >= len(m.stream.rows) {
		t.Fatalf("the cursor is at row %d of %d after folding", m.cursorRow, len(m.stream.rows))
	}
	row := m.stream.rows[m.cursorRow]
	if row.kind != rowFileHeader {
		t.Errorf("after folding, the cursor is on a %v, want the file's own divider", row.kind)
	}
	if got := pathOf(m.filtered[row.file]); got != path {
		t.Errorf("after folding %s the cursor is in %s", path, got)
	}
}

// TestFoldFilesFoldsEverythingNobodyHasStated — what a diff opened in half a
// terminal starts as.
func TestFoldFilesFoldsEverythingNobodyHasStated(t *testing.T) {
	m := foldModel(t)
	m.FoldFiles(true)
	m.rebuildStream()
	for _, f := range m.filtered {
		if !m.isCollapsed(pathOf(f)) {
			t.Errorf("%s is open in a fold-by-default viewer", pathOf(f))
		}
	}
	// And enter still opens one, which is what makes the default a starting point
	// rather than a mode.
	m.cursorRow = 0
	m = pressEnter(t, m)
	if m.isCollapsed(pathOf(m.filtered[0])) {
		t.Error("enter did not open a file the default had folded")
	}
}

// TestRWorksOnAFoldedFile is the regression. toggleReviewed asked isCollapsed —
// "is this file closed" — which was the same question as "is this reviewed" until a
// fold could close one on its own. In a fold-by-default viewer every file is
// closed, so `r` took the withdraw branch and reported "unreviewed" for a file it
// had been asked to review: the key looked dead.
func TestRWorksOnAFoldedFile(t *testing.T) {
	m := foldModel(t)
	m.FoldFiles(true)
	m.rebuildStream()
	path := pathOf(m.filtered[0])
	m.cursorRow = m.stream.fileStart[0]
	next, _ := m.toggleReviewed()
	m = next.(Model)
	if !m.isReviewed(path) {
		t.Errorf("r did not mark the folded file reviewed; status %q", m.status)
	}
	if !strings.Contains(m.status, "reviewed") || strings.Contains(m.status, "unreviewed") {
		t.Errorf("r said %q", m.status)
	}
}

// TestRHasTheLastWordOnTheFold. `r` means "read it, get it out of the way", so a
// file you had opened by hand collapses when you mark it — otherwise the key
// visibly does not collapse it. It clears the override rather than setting one, so
// enter can still reopen a reviewed file afterwards.
func TestRHasTheLastWordOnTheFold(t *testing.T) {
	m := foldModel(t)
	path := pathOf(m.filtered[0])
	m = pressEnter(t, m) // fold it by hand
	m = pressEnter(t, m) // and open it again: an explicit "open"
	if m.isCollapsed(path) {
		t.Fatal("the file is folded before r is pressed")
	}
	next, _ := m.toggleReviewed()
	m = next.(Model)
	if !m.isCollapsed(path) {
		t.Error("r did not collapse a file that had been opened by hand")
	}
	m.cursorRow = m.stream.fileStart[0]
	m = pressEnter(t, m)
	if m.isCollapsed(path) {
		t.Error("enter cannot reopen the file r collapsed")
	}
}
