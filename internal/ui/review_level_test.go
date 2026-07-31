package ui

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// reviewLevelComment is a remark about the change as a whole: no file anchor at
// all, which is what distinguishes it from one whose anchor was lost.
func reviewLevelComment(id, body string) review.Comment {
	return review.Comment{ID: id, Author: review.AuthorHuman, Body: body, State: review.Open}
}

// The section leads the stream: a remark about the whole change belongs before
// the thing it is about, not after everything.
func TestReviewLevelCommentsLeadTheStream(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{reviewLevelComment("r1", "this needs a test")})

	firstFile, firstReview := -1, -1
	for i, r := range m.stream.rows {
		if firstReview < 0 && r.kind == rowReviewHeader {
			firstReview = i
		}
		if firstFile < 0 && r.kind == rowFileHeader {
			firstFile = i
		}
	}
	if firstReview < 0 {
		t.Fatal("expected a review-level section")
	}
	if firstFile < 0 || firstReview > firstFile {
		t.Fatalf("expected the section above the first file: review at %d, file at %d", firstReview, firstFile)
	}
	view := stripANSI(m.renderStreamPanel(90, 16))
	if !strings.Contains(view, "this needs a test") {
		t.Fatalf("expected the remark rendered:\n%s", view)
	}
	if !strings.Contains(view, "review") {
		t.Fatalf("expected the section headed:\n%s", view)
	}
}

// It must not read as a failure. A deliberate summary remark filed under
// "their anchor could not be found" tells the reader something untrue.
func TestReviewLevelCommentsAreNotDetached(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{reviewLevelComment("r1", "overall this is fine")})

	for _, r := range m.stream.rows {
		if r.kind == rowOrphan || r.kind == rowOrphanHeader {
			t.Fatalf("a review-level comment landed in the detached section:\n%s",
				stripANSI(m.renderStreamPanel(90, 16)))
		}
	}
	if view := stripANSI(m.renderStreamPanel(90, 16)); strings.Contains(view, "anchor could not be found") {
		t.Fatalf("expected no detached header:\n%s", view)
	}
}

// A comment that names a file it can no longer be found in still goes to the
// detached section — the two sections mean different things and both have to
// keep working.
func TestUnplaceableCommentStillDetachesAlongsideReviewLevel(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{
		reviewLevelComment("r1", "change-wide remark"),
		{
			ID: "d1", Author: review.AuthorHuman, Body: "lost remark", State: review.Open,
			Anchor: review.Anchor{Path: "gone.go", Side: review.SideNew, LineHint: 3, Text: "vanished"},
		},
	})
	kinds := map[rowKind]bool{}
	for _, r := range m.stream.rows {
		kinds[r.kind] = true
	}
	if !kinds[rowReviewHeader] || !kinds[rowOrphanHeader] {
		t.Fatalf("expected both sections, got review=%t detached=%t",
			kinds[rowReviewHeader], kinds[rowOrphanHeader])
	}
	view := stripANSI(m.renderStreamPanel(90, 24))
	if strings.Index(view, "change-wide remark") > strings.Index(view, "lost remark") {
		t.Fatalf("expected the review section above the detached one:\n%s", view)
	}
}

// The section is reachable: the cursor lands on it, and the comment it holds is
// the one the reply / edit / delete keys act on.
func TestReviewLevelCommentIsReachable(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{reviewLevelComment("r1", "a change-wide remark")})

	at := -1
	for i, r := range m.stream.rows {
		if r.kind == rowReview {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("expected review-level rows")
	}
	m.cursorRow = at
	c, ok := m.localCommentAtCursor()
	if !ok {
		t.Fatal("expected the review-level comment to be the comment at the cursor")
	}
	if c.ID != "r1" {
		t.Fatalf("got comment %q at the cursor", c.ID)
	}
}

// And it appears in the comment index, labelled "review" — there is no file to
// name, and the label has to say where selecting it will land.
func TestReviewLevelCommentAppearsInTheIndex(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetComments([]review.Comment{reviewLevelComment("r1", "a change-wide remark")})
	if len(m.commentIndex) != 1 {
		t.Fatalf("expected one index entry, got %d", len(m.commentIndex))
	}
	e := m.commentIndex[0]
	if !e.changeWide || e.detached {
		t.Fatalf("expected a change-wide, non-detached entry: %+v", e)
	}
	if got := entryLocation(e); got != "review" {
		t.Fatalf("entryLocation = %q, want \"review\"", got)
	}
	// Selecting it seeks the diff to it, the same as any other conversation.
	m.seekToComment(0)
	if m.stream.rows[m.cursorRow].kind != rowReview {
		t.Fatalf("expected the cursor on a review row, got kind %v", m.stream.rows[m.cursorRow].kind)
	}
}

// A reply to a review-level remark stays in the section with its parent rather
// than falling into detached, which is where an unplaceable reply goes.
func TestReplyToAReviewLevelCommentStaysWithIt(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	parent := reviewLevelComment("r1", "needs a test")
	m.SetComments([]review.Comment{parent, {
		ID: "r2", Author: "agent", Body: "added one", State: review.Open, ReplyTo: parent.ID,
	}})
	for _, r := range m.stream.rows {
		if r.kind == rowOrphan {
			t.Fatalf("a reply to a review-level remark detached:\n%s",
				stripANSI(m.renderStreamPanel(90, 20)))
		}
	}
	view := stripANSI(m.renderStreamPanel(90, 20))
	if !strings.Contains(view, "added one") {
		t.Fatalf("expected the reply rendered:\n%s", view)
	}
	// One conversation, so one index row with the reply folded in.
	if len(m.commentIndex) != 1 || m.commentIndex[0].replies != 1 {
		t.Fatalf("expected one entry with one reply, got %+v", m.commentIndex)
	}
}
