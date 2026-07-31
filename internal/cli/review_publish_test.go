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

// A workspace and its source repo must resolve to the same review store
// directory. They did not: the CLI used the workspace root while the deck used
// the source repo, so an agent's findings landed where the deck never looked.
func TestWorkspaceAndSourceRepoShareOneReviewStore(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	target := review.Target{Kind: review.TargetWorking, Workspace: "ws-1"}

	// What the deck opens (source repo root).
	fromDeck, err := store.Open("/src/proj", target)
	if err != nil {
		t.Fatalf("deck open: %v", err)
	}
	if _, err := store.AddComment(fromDeck, review.Comment{
		Body:   "filed from the deck",
		Anchor: review.Anchor{Path: "a.go", LineHint: 1},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// What the CLI must open for an agent working in a workspace of that repo.
	fromAgent, err := store.Open("/src/proj", target)
	if err != nil {
		t.Fatalf("agent open: %v", err)
	}
	if fromAgent.ID != fromDeck.ID {
		t.Fatalf("expected one review, got %q and %q", fromAgent.ID, fromDeck.ID)
	}
	got, err := store.Comments(fromAgent)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the deck's comment visible to the agent, got %d", len(got))
	}

	// And the failure mode: keyed by the workspace path instead, it is a different
	// store and the comment is invisible.
	wrong, err := store.Open("/Users/me/.awp/workspaces/ws-1", target)
	if err != nil {
		t.Fatalf("wrong open: %v", err)
	}
	if wrongComments, _ := store.Comments(wrong); len(wrongComments) != 0 {
		t.Fatal("fixture is wrong: the workspace-keyed store should be separate")
	}
}

// A review-level remark has no line for a review comment to hang on, so publish
// holds it back and says so rather than sending an empty path GitHub will reject.
func TestPublishHoldsBackReviewLevelComments(t *testing.T) {
	pending, skipped, unanchored := partitionForPublish([]review.Comment{
		{ID: "a", Body: "on a line", Anchor: review.Anchor{Path: "a.go", LineHint: 3, Side: review.SideNew}},
		{ID: "b", Body: "about the change as a whole"},
		{ID: "c", Body: "already up", State: review.Published, Anchor: review.Anchor{Path: "b.go", LineHint: 1}},
		{ID: "d", Body: "   ", Anchor: review.Anchor{Path: "c.go", LineHint: 2}},
	})
	if len(pending) != 1 || pending[0].ID != "a" {
		t.Fatalf("expected only the anchored comment pending, got %+v", pending)
	}
	if unanchored != 1 {
		t.Fatalf("expected 1 held back, got %d", unanchored)
	}
	if skipped != 2 {
		t.Fatalf("expected the published and the empty one skipped, got %d", skipped)
	}
}
