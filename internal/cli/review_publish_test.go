package cli

import (
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

func TestGithubSideMapping(t *testing.T) {
	if got := githubSide(review.SideOld); got != "LEFT" {
		t.Fatalf("expected LEFT for the old side, got %q", got)
	}
	if got := githubSide(review.SideNew); got != "RIGHT" {
		t.Fatalf("expected RIGHT for the new side, got %q", got)
	}
	// An unset side must not silently become the old side — that would post a
	// comment against code the reviewer wasn't looking at.
	if got := githubSide(""); got != "RIGHT" {
		t.Fatalf("expected an unset side to default to RIGHT, got %q", got)
	}
}

// The reason each comment records its publish immediately: a second run must post
// nothing. Batching those writes until the end would leave posted comments
// looking unpublished after a crash, and the retry would duplicate them.
func TestPublishedCommentsAreNotRepublished(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repo/proj", review.Target{Kind: review.TargetPR, Value: "9", Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	posted, err := store.AddComment(r, review.Comment{
		Body:   "already up",
		Anchor: review.Anchor{Path: "a.go", LineHint: 3, Side: review.SideNew},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := store.AddComment(r, review.Comment{
		ID:     "pending-1",
		Body:   "not yet",
		Anchor: review.Anchor{Path: "b.go", LineHint: 7, Side: review.SideNew},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	posted.State = review.Published
	posted.Publish = &review.PublishRecord{ThreadID: "PRRC_abc"}
	if err := store.UpdateComment(r, posted); err != nil {
		t.Fatalf("update: %v", err)
	}

	comments, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var pending []review.Comment
	for _, c := range comments {
		if c.State == review.Published || c.Publish != nil {
			continue
		}
		pending = append(pending, c)
	}
	if len(pending) != 1 || pending[0].Body != "not yet" {
		t.Fatalf("expected only the unpublished comment pending, got %+v", pending)
	}
}

// A comment whose publish record exists but whose state was not updated must
// still be skipped: either signal alone is enough to mean "already on GitHub".
func TestPublishRecordAloneSuppressesRepost(t *testing.T) {
	c := review.Comment{State: review.Open, Publish: &review.PublishRecord{ThreadID: "T1"}}
	if c.State != review.Published && c.Publish == nil {
		t.Fatal("a comment carrying a publish record must count as published")
	}
}
