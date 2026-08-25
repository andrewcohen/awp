package ui

import (
	"charm.land/lipgloss/v2"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/review"
)

// rowOfKind is the first row of the given kind, or -1.
func rowOfKind(m Model, k rowKind) int {
	for i, r := range m.stream.rows {
		if r.kind == k {
			return i
		}
	}
	return -1
}

// pressC is `c` in the diff pane.
func pressC(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.startComment()
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("startComment returned %T", next)
	}
	return got
}

// The review section leads every diff, with or without anything in it. It is
// the only way to say something about the change as a whole while reading, so
// it cannot be a section that appears once you already have one.
func TestTheReviewSectionIsThereBeforeThereIsAnythingInIt(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	if rowOfKind(m, rowReviewHeader) < 0 {
		t.Fatal("a diff with no comments has no review summary header")
	}
	stand := rowOfKind(m, rowReviewEmpty)
	if stand < 0 {
		t.Fatal("the empty section has no stand-in row — a bare header reads as a render fault")
	}
	// The section leads: both its rows come before the first file.
	if file := rowOfKind(m, rowFileHeader); file >= 0 && stand > file {
		t.Errorf("the review section is at row %d, after the first file at %d", stand, file)
	}
	// And it names the key, since that is its whole content. Only the key: the row
	// used to describe what a review-level remark is, and at that length it wrapped
	// onto a second row in half a terminal — two rows of the diff spent on a
	// placeholder, where the section's own header already says what it holds.
	body := ansi.Strip(m.renderStreamRow(m.stream.rows[stand], 100, false))
	if !strings.Contains(body, "c to add") {
		t.Errorf("the stand-in row does not name the gesture: %q", body)
	}
	if got := lipgloss.Height(body); got != 1 {
		t.Errorf("the stand-in row is %d rows tall, so it wraps", got)
	}
}

// With no store there is nothing `c` could save, so the invitation is not
// offered. A reader gets the diff, not a row promising a key that refuses.
func TestAReadOnlyViewerHasNoEmptyReviewSection(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SaveComment = nil
	m.rebuildStream()
	if row := rowOfKind(m, rowReviewHeader); row >= 0 {
		t.Errorf("a viewer with no store shows the review header at row %d", row)
	}
	if row := rowOfKind(m, rowReviewEmpty); row >= 0 {
		t.Errorf("a viewer with no store invites a comment it cannot save, at row %d", row)
	}
}

// `c` there composes about the change as a whole. Both of the section's rows
// mean it: the header and the stand-in are one target, and which of the two the
// cursor happens to be on is not a distinction the reader is making.
func TestCOnTheReviewSectionComposesAboutTheWholeChange(t *testing.T) {
	for _, kind := range []struct {
		name string
		k    rowKind
	}{
		{"the header", rowReviewHeader},
		{"the stand-in", rowReviewEmpty},
	} {
		m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
		row := rowOfKind(m, kind.k)
		if row < 0 {
			t.Fatalf("%s: no such row", kind.name)
		}
		m.cursorRow = row
		m = pressC(t, m)
		if !m.editing {
			t.Fatalf("%s: c did not open the box", kind.name)
		}
		if got := m.editor.anchor.Scope(); got != review.ChangeScope {
			t.Errorf("%s: the box is scoped %v, want ChangeScope (anchor %+v)", kind.name, got, m.editor.anchor)
		}
		// The header has to say so — deciding what a remark covers is the whole point
		// of the gesture, and the box is where that decision is confirmed.
		head := ansi.Strip(m.editor.view(80))
		if !strings.Contains(head, "the whole change") {
			t.Errorf("%s: the box does not name its scope:\n%s", kind.name, head)
		}
	}
}

// A remark composed there saves change-scoped, which is what makes it a review
// summary rather than an orphan. Scope() is derived from the anchor, so the
// thing that has to hold is that no path leaked in.
func TestARemarkFromTheReviewSectionSavesChangeScoped(t *testing.T) {
	var saved []review.Comment
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SaveComment = func(c review.Comment) error { saved = append(saved, c); return nil }
	m.rebuildStream()

	m.cursorRow = rowOfKind(m, rowReviewHeader)
	m = pressC(t, m)
	m.editor.setBody("the error paths are inconsistent across this change")
	next, _ := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(Model)

	if len(saved) != 1 {
		t.Fatalf("saved %d comments, want 1", len(saved))
	}
	if got := saved[0].Anchor.Scope(); got != review.ChangeScope {
		t.Errorf("saved with scope %v and anchor %+v, want ChangeScope", got, saved[0].Anchor)
	}
}

// The cursor does not start there. The section is two rows the reader did not
// ask for; landing on them would mean every diff opens with the cursor off the
// code, and `c` meaning something other than what it has always meant.
func TestTheCursorStillOpensOnTheDiff(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	if m.cursorRow < 0 || m.cursorRow >= len(m.stream.rows) {
		t.Fatalf("cursor at %d, stream has %d rows", m.cursorRow, len(m.stream.rows))
	}
	if k := m.stream.rows[m.cursorRow].kind; k == rowReviewHeader || k == rowReviewEmpty {
		t.Errorf("the diff opened with the cursor in the review section (row %d)", m.cursorRow)
	}
}

// On an existing review-level remark, `c` still replies to it. Only the
// section's chrome carries the new-remark meaning: a comment is a comment
// wherever it sits, and answering one is what `c` does everywhere else.
func TestCOnAReviewLevelRemarkRepliesToIt(t *testing.T) {
	var replied string
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.ReplyComment = func(parent string, _ review.Comment) error { replied = parent; return nil }
	m.SetComments([]review.Comment{{
		ID: "r1", Author: review.AuthorHuman, State: review.Open,
		Body: "the error paths are inconsistent",
	}})

	row := -1
	for i, r := range m.stream.rows {
		if r.kind == rowReview {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("the review-level remark is not in the stream")
	}
	m.cursorRow = row
	m = pressC(t, m)
	if !m.editing {
		t.Fatal("c did not open a box on the review-level remark")
	}
	if m.editor.replyTo != "r1" {
		t.Errorf("the box is not a reply to r1: replyTo=%q, anchor=%+v", m.editor.replyTo, m.editor.anchor)
	}
	_ = replied
}

// And with a remark in it the section shows the remark, not the stand-in.
func TestTheStandInGoesAwayOnceThereIsARemark(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{{
		ID: "r1", Author: review.AuthorHuman, State: review.Open,
		Body: "the error paths are inconsistent",
	}})
	if row := rowOfKind(m, rowReviewEmpty); row >= 0 {
		t.Errorf("the stand-in is still at row %d beside a real remark", row)
	}
	if rowOfKind(m, rowReviewHeader) < 0 {
		t.Error("the header went away with the stand-in")
	}
}
