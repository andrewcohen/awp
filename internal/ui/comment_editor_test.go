package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/review"
)

// editorRows returns the stream rows the compose box occupies.
func editorRows(m Model) []int {
	var out []int
	for i, r := range m.stream.rows {
		if r.kind == rowEditor {
			out = append(out, i)
		}
	}
	return out
}

// The stream's geometry is computed before anything is rendered, so the box's
// height has to be a constant. If view() ever renders taller — a wrapped header
// or hint at a narrow width would do it — every row index after the box is off by
// one and the whole stream misaligns.
func TestComposeBoxHeightMatchesTheGeometryConstant(t *testing.T) {
	anchor := review.Anchor{
		Path:     "internal/some/quite/deeply/nested/package/name.go",
		LineHint: 123456,
	}
	for _, width := range []int{24, 30, 48, 80, 200} {
		e := newCommentEditor(anchor, width)
		e.area.SetValue("a remark long enough to fill the area\nand a second line\nand a third")
		if got := lipgloss.Height(e.view(width)); got != commentEditorRows {
			t.Fatalf("width %d: box is %d rows, geometry reserves %d", width, got, commentEditorRows)
		}
		if got := len(e.lines(width)); got != commentEditorRows {
			t.Fatalf("width %d: %d lines, geometry reserves %d", width, got, commentEditorRows)
		}
	}
}

// A resize while the box is open has to re-lay the text area. Left at the old
// width it wraps inside the narrower box, making it taller than the geometry
// reserved — and every row index after it wrong.
func TestResizingWhileComposingKeepsTheBoxHeight(t *testing.T) {
	e := newCommentEditor(review.Anchor{Path: "a.go", LineHint: 1}, 200)
	e.area.SetValue(strings.Repeat("wide content ", 12))
	e.setWidth(30)
	if got := lipgloss.Height(e.view(30)); got != commentEditorRows {
		t.Fatalf("box is %d rows after a resize, geometry reserves %d", got, commentEditorRows)
	}
}

// The same through the model, which is what actually receives the resize.
func TestModelResizeWhileComposingKeepsTheGeometryHonest(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.cursorRow = rowOfLine(m, "beta")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("x", 200))})
	m = updated.(Model)

	m.SetSize(60, 14)
	rows := editorRows(m)
	if len(rows) != commentEditorRows {
		t.Fatalf("expected %d box rows after a resize, got %d", commentEditorRows, len(rows))
	}
	// What is drawn must agree with what was reserved.
	if got := len(m.editor.lines(m.hunkWidth)); got != len(rows) {
		t.Fatalf("box renders %d lines but the stream reserved %d", got, len(rows))
	}
}

// And no line of the box may exceed the width it was given, or it pushes the
// stream pane's border out.
func TestComposeBoxFitsItsWidth(t *testing.T) {
	e := newCommentEditor(review.Anchor{Path: "internal/x/y/z.go", LineHint: 9999}, 60)
	for _, width := range []int{24, 40, 60} {
		for _, line := range e.lines(width) {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: a box row is %d cells: %q", width, got, line)
			}
		}
	}
}

// The box belongs in the stream, directly under the line being commented on —
// docking it below the panes hid the code it was about.
func TestComposeBoxOpensBeneathTheAnchoredLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.cursorRow = rowOfLine(m, "beta")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)

	rows := editorRows(m)
	if len(rows) != commentEditorRows {
		t.Fatalf("expected %d box rows in the stream, got %d", commentEditorRows, len(rows))
	}
	// Contiguous, and starting immediately after the anchored line.
	for i := 1; i < len(rows); i++ {
		if rows[i] != rows[i-1]+1 {
			t.Fatalf("box rows are not contiguous: %v", rows)
		}
	}
	above := m.stream.rows[rows[0]-1]
	if above.kind != rowLine || m.lineText(above) != "beta" {
		t.Fatalf("expected the box under \"beta\", got %v %q", above.kind, m.lineText(above))
	}
}

// Closing the box takes its rows back out; leaving them would shift every row
// after it and strand the cursor.
func TestClosingTheBoxRemovesItsRows(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.cursorRow = rowOfLine(m, "beta")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)
	if len(editorRows(m)) == 0 {
		t.Fatal("fixture is wrong: expected the box open")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if got := editorRows(m); len(got) != 0 {
		t.Fatalf("expected the box's rows gone after esc, got %v", got)
	}
}

// Saving also has to clear the rows — the box is closed on every exit, not only
// on cancel.
func TestSavingRemovesTheBoxRows(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.cursorRow = rowOfLine(m, "beta")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if got := editorRows(m); len(got) != 0 {
		t.Fatalf("expected no box rows after saving, got %v", got)
	}
	if got := rowsOfKind(m, rowComment); got == 0 {
		t.Fatal("expected the saved comment in the stream")
	}
}

// A reply appends to the conversation: the box goes under the last row of the
// whole exchange, not wedged between the parent and its existing replies.
func TestReplyBoxOpensAtTheFootOfTheThread(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.ReplyComment = func(string, review.Comment) error { return nil }
	parent := commentOn("a.go", 2, "beta", "needs a guard")
	reply := review.Comment{
		ID: "r1", Author: "agent", Body: "done", State: review.Open,
		ReplyTo: parent.ID, Anchor: parent.Anchor,
	}
	m.SetComments([]review.Comment{parent, reply})

	// Put the cursor on the parent and reply to it.
	m.cursorRow = firstRowOfComment(m, parent.ID)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)

	rows := editorRows(m)
	if len(rows) == 0 {
		t.Fatal("expected the reply box open")
	}
	above := m.stream.rows[rows[0]-1]
	if above.kind != rowComment {
		t.Fatalf("expected the box under a comment row, got %v", above.kind)
	}
	if got := m.stream.comments[above.comment].ID; got != "r1" {
		t.Fatalf("expected the box below the last message in the thread, got %q", got)
	}
}

// The box has to be on screen. It is spliced in below the anchor, which may sit
// well past the bottom of the viewport — without an explicit scroll you would be
// typing into something you cannot see.
func TestOpeningTheBoxScrollsItIntoView(t *testing.T) {
	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, "line "+strings.Repeat("x", i%7))
	}
	m := commentModel(t, fileWith("a.go", 1, lines...))
	m.cursorRow = len(m.stream.rows) - 1
	m.followCursor()
	// Aim the cursor at the last code line, then open the box.
	for m.cursorRow > 0 && m.stream.rows[m.cursorRow].kind != rowLine {
		m.cursorRow--
	}
	m.followCursor()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)

	rows := editorRows(m)
	if len(rows) == 0 {
		t.Fatal("expected the box open")
	}
	height := m.streamContentHeight()
	top, bottom := m.streamScroll, m.streamScroll+height-1
	if rows[0] < top || rows[0] > bottom {
		t.Fatalf("box starts at row %d, viewport shows %d..%d", rows[0], top, bottom)
	}
	// And the line being commented on stays visible above it.
	if rows[0]-1 < top {
		t.Fatalf("the anchored line (row %d) scrolled off; viewport starts at %d", rows[0]-1, top)
	}
}

// A half-written comment is not a conversation, so it must not appear in the
// left column's index — which is built from the stream the box is spliced into.
func TestTheOpenBoxIsNotListedInTheIndex(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.cursorRow = rowOfLine(m, "beta")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = updated.(Model)
	if len(m.commentIndex) != 0 {
		t.Fatalf("expected the index empty while composing, got %+v", m.commentIndex)
	}
}

// rowOfLine is the stream row showing a given line's text.
func rowOfLine(m Model, text string) int {
	for i, r := range m.stream.rows {
		if r.kind == rowLine && m.lineText(r) == text {
			return i
		}
	}
	return 0
}

// firstRowOfComment is the first display row of a comment.
func firstRowOfComment(m Model, id string) int {
	for i, r := range m.stream.rows {
		if r.kind != rowComment && r.kind != rowOrphan {
			continue
		}
		if r.comment >= 0 && r.comment < len(m.stream.comments) && m.stream.comments[r.comment].ID == id {
			return i
		}
	}
	return 0
}
