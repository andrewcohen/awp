package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/review"
)

// Publishing a comment makes GitHub the second holder of it: the pr-status pass
// mirrors the thread back, and the diff had both — the local record and
// `▶ github · 1 msg · you: …` right under it. Reported on a real PR where seven
// of eight mirrored threads were awp's own comments echoed back.

// publishedComment is a local comment that has been published, carrying the node
// id GitHub gave the comment it created.
func publishedComment(id, ghID, path string, line int, text, body string) review.Comment {
	return review.Comment{
		ID: id, Author: review.AuthorHuman, Body: body, State: review.Published,
		Anchor:  review.Anchor{Path: path, Side: review.SideNew, LineHint: line, Text: text},
		Publish: &review.PublishRecord{ThreadID: ghID, At: time.Unix(0, 0)},
	}
}

// echoThread is the mirrored thread GitHub reports for a comment published from
// here: our own words back, under GitHub's comment id.
func echoThread(threadID, ghCommentID, path string, line int, body string) review.Thread {
	return review.Thread{
		ID: threadID, Path: path, Side: review.SideNew, Line: line,
		Comments: []review.ThreadComment{{ID: ghCommentID, Author: "andrewcohen", Body: body}},
	}
}

func TestPublishedCommentIsDrawnOnceAsItsMirroredThread(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		publishedComment("c1", "PRRC_1", "a.go", 2, "beta", "this leaks"),
	})
	m.SetThreads([]review.Thread{echoThread("T1", "PRRC_1", "a.go", 2, "this leaks")})

	view := stripANSI(m.renderStreamPanel(90, 14))
	if n := strings.Count(view, "this leaks"); n != 1 {
		t.Fatalf("expected the conversation once, got %d copies:\n%s", n, view)
	}
	// The mirrored copy is the one kept: it is GitHub's record, and only it knows
	// whether the thread has been resolved or has drifted.
	if !strings.Contains(view, "github") {
		t.Fatalf("expected the GitHub copy kept rather than the local echo:\n%s", view)
	}
	// The comment index counts conversations too, so it must not list both either.
	if n := len(m.commentIndex); n != 1 {
		t.Fatalf("expected one index row, got %d: %+v", n, m.commentIndex)
	}
}

// An unpublished comment has no counterpart, so nothing is suppressed.
func TestUnpublishedCommentsAreUntouched(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{commentOn("a.go", 2, "beta", "still a draft")})
	m.SetThreads([]review.Thread{echoThread("T1", "PRRC_1", "a.go", 2, "someone else's point")})

	view := stripANSI(m.renderStreamPanel(90, 14))
	for _, want := range []string{"still a draft", "someone else's point"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q kept:\n%s", want, view)
		}
	}
}

// A published comment the mirror does not have — the mirror is behind, or the
// fetch failed — still renders. Suppressing it on the strength of the publish
// record alone would hide a remark that is nowhere on screen.
func TestPublishedCommentWithNoMirroredThreadStillRenders(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		publishedComment("c1", "PRRC_1", "a.go", 2, "beta", "this leaks"),
	})
	m.SetThreads(nil)

	if view := stripANSI(m.renderStreamPanel(90, 14)); !strings.Contains(view, "this leaks") {
		t.Fatalf("expected the local record shown with no mirror to defer to:\n%s", view)
	}
}

// Matched on the node id, not on the body: editing a published comment locally
// changes its text, and GitHub recomputes a thread's line as the PR moves — a
// comment filed against 47 came back reported at 53.
func TestReconcileMatchesTheIDNotTheBodyOrLine(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		publishedComment("c1", "PRRC_1", "a.go", 2, "beta", "edited since publishing"),
	})
	m.SetThreads([]review.Thread{echoThread("T1", "PRRC_1", "a.go", 99, "what was published")})

	view := stripANSI(m.renderStreamPanel(90, 14))
	if strings.Contains(view, "edited since publishing") {
		t.Fatalf("expected the local echo suppressed despite the differing body:\n%s", view)
	}
	if !strings.Contains(view, "what was published") {
		t.Fatalf("expected the mirrored copy:\n%s", view)
	}
}

// A mirror written before comment ids were carried says nothing about identity,
// so nothing is matched — the duplicate is preferable to hiding a remark on a
// guess. The next mirror refresh fills the ids in.
func TestReconcileIgnoresAMirrorWithoutIDs(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		publishedComment("c1", "PRRC_1", "a.go", 2, "beta", "this leaks"),
	})
	m.SetThreads([]review.Thread{remoteThread("T1", "a.go", 2, false, "this leaks")})

	if view := stripANSI(m.renderStreamPanel(90, 14)); strings.Count(view, "this leaks") != 2 {
		t.Fatalf("expected no match without ids, so both copies:\n%s", view)
	}
}

// Replies are never published, so they have no counterpart on GitHub. They move
// onto the mirrored thread rather than being dropped with the parent — the
// exchange with the agent is the whole reason the local record still matters.
func TestLocalRepliesMoveOntoTheMirroredThread(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	parent := publishedComment("c1", "PRRC_1", "a.go", 2, "beta", "this leaks")
	reply := review.Comment{
		ID: "c2", Author: "agent", Body: "fixed in the next commit", State: review.Open,
		ReplyTo: "c1",
		Anchor:  review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 2, Text: "beta"},
	}
	m.SetComments([]review.Comment{parent, reply})
	m.SetThreads([]review.Thread{echoThread("T1", "PRRC_1", "a.go", 2, "this leaks")})

	view := stripANSI(m.renderStreamPanel(90, 14))
	if !strings.Contains(view, "fixed in the next commit") {
		t.Fatalf("expected the local reply kept on the mirrored thread:\n%s", view)
	}
	if n := strings.Count(view, "this leaks"); n != 1 {
		t.Fatalf("expected the parent once, got %d:\n%s", n, view)
	}
	// One conversation, not a thread plus an orphaned reply in the detached
	// section.
	if strings.Contains(view, "detached") {
		t.Fatalf("the reply must not be stranded:\n%s", view)
	}
}

// Cycling `T` to none hides GitHub's conversation. Reconciling against every
// mirrored thread rather than the shown ones would take a remark of your own with
// it, so the local record comes back when its thread is not being drawn.
func TestHidingThreadsBringsBackTheLocalRecord(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{
		publishedComment("c1", "PRRC_1", "a.go", 2, "beta", "this leaks"),
	})
	m.SetThreads([]review.Thread{echoThread("T1", "PRRC_1", "a.go", 2, "this leaks")})
	m.threadVisibility = ThreadsNone
	m.rebuildStream()

	view := stripANSI(m.renderStreamPanel(90, 14))
	if strings.Contains(view, "github") {
		t.Fatalf("expected GitHub's copy hidden:\n%s", view)
	}
	if !strings.Contains(view, "this leaks") {
		t.Fatalf("expected your own remark still shown:\n%s", view)
	}
}

// The review summary's publish record holds the *review* id, which is not a
// comment id and can never match a thread's — so a summary is never mistaken for
// an echo of one.
func TestReviewSummaryIsNotReconciledAgainstAThread(t *testing.T) {
	summary := review.Comment{
		ID: "s1", Author: "agent", Body: "reviewed the publish path", State: review.Published,
		Publish: &review.PublishRecord{ThreadID: "PRR_1", At: time.Unix(0, 0)},
	}
	got := echoedByThread(
		[]review.Comment{summary},
		[]review.Thread{echoThread("T1", "PRRC_1", "a.go", 2, "unrelated")},
	)
	if len(got) != 0 {
		t.Fatalf("expected no match for a review-level remark, got %v", got)
	}
}
