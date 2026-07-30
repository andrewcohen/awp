package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

func commentModel(t *testing.T, files ...diff.FileDiff) Model {
	t.Helper()
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 14)
	m.focus = FocusHunks
	m.SaveComment = func(review.Comment) error { return nil }
	return loadWith(m, 1, files...)
}

func commentOn(path string, line int, text, body string) review.Comment {
	return review.Comment{
		ID: "c1", Author: review.AuthorHuman, Body: body, State: review.Open,
		Anchor: review.Anchor{Path: path, Side: review.SideNew, LineHint: line, Text: text},
	}
}

// rowsOfKind counts stream rows of a kind, for asserting placement.
func rowsOfKind(m Model, k rowKind) int {
	n := 0
	for _, r := range m.stream.rows {
		if r.kind == k {
			n++
		}
	}
	return n
}

func TestCommentRendersUnderItsAnchoredLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.SetComments([]review.Comment{commentOn("a.go", 2, "beta", "needs a guard")})

	if got := rowsOfKind(m, rowComment); got == 0 {
		t.Fatal("expected comment rows in the stream")
	}
	// The comment's rows must directly follow the line it anchors to.
	found := false
	for i, r := range m.stream.rows {
		if r.kind != rowLine || m.lineText(r) != "beta" {
			continue
		}
		if i+1 < len(m.stream.rows) && m.stream.rows[i+1].kind == rowComment {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the comment to sit immediately below its line")
	}
	if got := rowsOfKind(m, rowOrphanHeader); got != 0 {
		t.Fatal("a placed comment should not appear in the detached section")
	}
}

// A comment whose line moved must follow the content, not the old line number.
func TestCommentFollowsMovedLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.SetComments([]review.Comment{commentOn("a.go", 2, "beta", "note")})

	m = loadWith(m, 2, fileWith("a.go", 1, "inserted", "alpha", "beta", "gamma"))
	for i, r := range m.stream.rows {
		if r.kind == rowLine && m.lineText(r) == "beta" {
			if i+1 >= len(m.stream.rows) || m.stream.rows[i+1].kind != rowComment {
				t.Fatal("expected the comment to move with its line")
			}
			return
		}
	}
	t.Fatal("beta not found after reload")
}

// A comment that cannot be located is shown in a detached section rather than
// silently dropped.
func TestUnplaceableCommentBecomesDetached(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{commentOn("vanished.go", 9, "gone", "stale note")})

	if got := rowsOfKind(m, rowOrphanHeader); got != 1 {
		t.Fatalf("expected a detached section header, got %d", got)
	}
	if got := rowsOfKind(m, rowOrphan); got == 0 {
		t.Fatal("expected the comment to still be shown")
	}
	view := stripANSI(m.renderStreamPanel(80, 12))
	if !strings.Contains(view, "detached") {
		t.Fatalf("expected the detached section to be visible, got:\n%s", view)
	}
}

// Duplicate lines must not capture a comment by text alone — context breaks the
// tie, and an unbreakable tie falls back to the nearest line.
func TestCommentUsesContextToDisambiguateDuplicates(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "}", "middle", "}", "tail", "}"))
	c := commentOn("a.go", 3, "}", "the second brace")
	c.Anchor.ContextBefore = []string{"middle"}
	c.Anchor.ContextAfter = []string{"tail"}
	m.SetComments([]review.Comment{c})

	// Shift the file so the line hint is stale; context should still win.
	m = loadWith(m, 2, fileWith("a.go", 1, "added", "}", "middle", "}", "tail", "}"))
	for i, r := range m.stream.rows {
		if r.kind != rowComment {
			continue
		}
		// Walk back to the line this comment is under.
		j := i - 1
		for j >= 0 && m.stream.rows[j].kind == rowComment {
			j--
		}
		if j < 0 {
			t.Fatal("comment row with no line above it")
		}
		if got := m.stream.rows[j].newNo; got != 4 {
			t.Fatalf("expected the comment on the brace at line 4 (context match), got line %d", got)
		}
		return
	}
	t.Fatal("no comment row found")
}

func TestAnchorAtCursorDescribesTheLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 10, "alpha", "beta", "gamma"))
	for cursorText(m) != "beta" {
		before := m.cursorRow
		m = press(m, "j")
		if m.cursorRow == before {
			t.Fatal("never reached beta")
		}
	}
	a, ok := m.AnchorAtCursor()
	if !ok {
		t.Fatal("expected an anchor on a line row")
	}
	if a.Path != "a.go" || a.Text != "beta" || a.LineHint != 11 {
		t.Fatalf("unexpected anchor: %+v", a)
	}
	if a.Side != review.SideNew {
		t.Fatalf("expected a context line to anchor to the new side, got %q", a.Side)
	}
	if len(a.ContextBefore) == 0 || a.ContextBefore[len(a.ContextBefore)-1] != "alpha" {
		t.Fatalf("expected preceding context, got %+v", a.ContextBefore)
	}
	if len(a.ContextAfter) == 0 || a.ContextAfter[0] != "gamma" {
		t.Fatalf("expected following context, got %+v", a.ContextAfter)
	}
}

// A removed line exists only on the old side, so that is where it anchors.
func TestAnchorOnRemovedLineUsesOldSide(t *testing.T) {
	m := commentModel(t, diff.FileDiff{NewPath: "a.go", Status: "M", Hunks: []diff.Hunk{{
		OldStart: 5, NewStart: 5,
		Lines: []diff.HunkLine{{Type: '-', Content: "deleted"}},
	}}})
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	a, ok := m.AnchorAtCursor()
	if !ok {
		t.Fatal("expected an anchor")
	}
	if a.Side != review.SideOld || a.LineHint != 5 {
		t.Fatalf("expected old side line 5, got %+v", a)
	}
}

func TestAnchorAtCursorRefusesNonLineRows(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.cursorRow = 0 // the file divider
	if _, ok := m.AnchorAtCursor(); ok {
		t.Fatal("expected no anchor on the file divider")
	}
}

// The editor round trip: `c` opens, typing fills, enter saves through the sink.
func TestCommentGestureSavesThroughTheSink(t *testing.T) {
	var saved []review.Comment
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SaveComment = func(c review.Comment) error {
		saved = append(saved, c)
		return nil
	}
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	if !m.editing {
		t.Fatal("expected c to open the compose box")
	}
	for _, r := range "looks wrong" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.editing {
		t.Fatal("expected enter to close the compose box")
	}
	if len(saved) != 1 || saved[0].Body != "looks wrong" {
		t.Fatalf("expected the comment to be saved, got %+v", saved)
	}
	if saved[0].Anchor.Path != "a.go" {
		t.Fatalf("expected the anchor to carry the path, got %+v", saved[0].Anchor)
	}
	if rowsOfKind(m, rowComment) == 0 {
		t.Fatal("expected the new comment to appear in the stream")
	}
}

func TestCommentGestureCancels(t *testing.T) {
	saved := 0
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.editing || saved != 0 {
		t.Fatalf("expected esc to discard, editing=%v saved=%d", m.editing, saved)
	}
}

// An empty body is a cancel, not an empty comment.
func TestEmptyCommentIsDiscarded(t *testing.T) {
	saved := 0
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SaveComment = func(review.Comment) error { saved++; return nil }
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(Model); got.editing || saved != 0 {
		t.Fatalf("expected an empty comment to be discarded, editing=%v saved=%d", got.editing, saved)
	}
}

// A failing store must surface the error rather than pretending it saved.
func TestSaveFailureIsReported(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SaveComment = func(review.Comment) error { return errors.New("disk full") }
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.statusErr || !strings.Contains(got.status, "disk full") {
		t.Fatalf("expected the save error in status, got %q err=%v", got.status, got.statusErr)
	}
	if rowsOfKind(got, rowComment) != 0 {
		t.Fatal("a comment that failed to save must not appear as saved")
	}
}

// While composing, keys belong to the editor — the host must not treat them as
// its own bindings.
func TestEditingClaimsTheKeyboard(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	for m.stream.rows[m.cursorRow].kind != rowLine {
		m = press(m, "j")
	}
	m = press(m, "c")
	if !m.Filtering() {
		t.Fatal("expected Filtering() to report that an input owns the keyboard")
	}
	before := m.cursorRow
	m = press(m, "j")
	if m.cursorRow != before {
		t.Fatal("expected j to type into the comment, not move the cursor")
	}
}

// Comment rows shift the offsets the file and hunk indexes point at, so those
// have to be remapped or seeking lands in the wrong place.
func TestCommentRowsRemapFileAndHunkOffsets(t *testing.T) {
	m := commentModel(t,
		fileWith("a.go", 1, "alpha", "beta"),
		fileWith("b.go", 1, "one", "two"),
	)
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "note")})

	for i, start := range m.stream.fileStart {
		if got := m.stream.rows[start].kind; got != rowFileHeader {
			t.Fatalf("fileStart[%d] points at %v, not a file header", i, got)
		}
	}
	for i, start := range m.stream.hunkStart {
		if got := m.stream.rows[start].kind; got != rowHunkHeader {
			t.Fatalf("hunkStart[%d] points at %v, not a hunk header", i, got)
		}
	}
}
