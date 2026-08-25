package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/review"
)

// publishedFinding is one of our own remarks after a publish went through, in the
// window before the mirror has caught up and taken over drawing it.
//
// A real window, not a contrived one: the mirror is refreshed by the pr-status
// job, so between pressing P and that job's next pass the local record is what the
// stream shows — and if GitHub never handed back the comment's node id, or the job
// never runs, it is what the stream shows indefinitely.
func publishedFinding() review.Comment {
	c := commentOn("a.go", 1, "alpha", "these words are on the PR")
	c.State = review.Published
	c.Publish = &review.PublishRecord{ThreadID: "PRRT_abc", At: time.Unix(1700000000, 0)}
	return c
}

func onPublished(t *testing.T) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.UpdateComment = func(review.Comment) error { return nil }
	m.DeleteComment = func(string) error { return nil }
	m.SetComments([]review.Comment{publishedFinding()})
	row := firstRowOfComment(m, "c1")
	if row < 0 {
		t.Fatal("fixture is wrong: the published comment should still be placed")
	}
	m.cursorRow = row
	return m
}

// `i` on a published finding used to open the editor, and the edit went nowhere:
// GitHub keeps the original, and once the mirror catches up the mirrored copy is
// what gets drawn — so the reviewer's revision was invisible to everyone including
// themselves. A published *reply* was already refused for exactly this reason; the
// two disagreed only because each spelled the question out of nearby fields.
func TestEditingAPublishedFindingIsRefused(t *testing.T) {
	m := onPublished(t)
	m = press(m, "i")
	if m.editing {
		t.Fatal("expected i to refuse a comment that is already on github")
	}
	if !strings.Contains(m.status, "on github") {
		t.Fatalf("status should say why it refused, got %q", m.status)
	}
	// Named for what the cursor is on. "that reply" pointed at a finding sends the
	// reader hunting for a reply they never wrote.
	if !strings.Contains(m.status, "comment") {
		t.Fatalf("status should name the record as a comment, got %q", m.status)
	}
	if !strings.Contains(m.status, "edit") {
		t.Fatalf("status should name what was attempted, got %q", m.status)
	}
}

// Same rule, same reason: the local record would go, the words would stay up, and
// the mirror would go on drawing them — a delete that deleted nothing except our
// own record of having said it.
func TestDeletingAPublishedFindingIsRefused(t *testing.T) {
	m := onPublished(t)
	deleted := false
	m.DeleteComment = func(string) error { deleted = true; return nil }

	m = press(m, "D")
	if deleted {
		t.Fatal("expected D to refuse a comment that is already on github")
	}
	if !strings.Contains(m.status, "on github") || !strings.Contains(m.status, "delete") {
		t.Fatalf("status should say what was refused and why, got %q", m.status)
	}
	if len(m.comments) != 1 {
		t.Fatalf("the comment should still be here, got %d", len(m.comments))
	}
}

// A published reply says "reply", not "comment" — the refusal points at the row
// the cursor is on.
func TestTheRefusalNamesWhichRecordItMeans(t *testing.T) {
	reply := publishedFinding()
	reply.ReplyToThread = "PRRT_abc"
	if got := onGitHubRefusal(reply, "edit"); !strings.Contains(got, "reply") {
		t.Errorf("a posted reply should be named a reply, got %q", got)
	}
	if got := onGitHubRefusal(publishedFinding(), "delete"); !strings.Contains(got, "comment") {
		t.Errorf("a published finding should be named a comment, got %q", got)
	}
}

// The rule is "not yet anywhere else", not "not published" — a draft is still
// yours to change, and refusing one would make the whole surface read-only.
func TestADraftIsStillEditable(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.UpdateComment = func(review.Comment) error { return nil }
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "still a draft")})
	m.cursorRow = firstRowOfComment(m, "c1")
	m = press(m, "i")
	if !m.editing {
		t.Fatalf("expected i to open the editor on a draft, status %q", m.status)
	}
}
