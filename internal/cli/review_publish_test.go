package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/github"
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

// A review-level remark has no line for a review comment to hang on, so it goes
// up as a comment on the PR itself rather than inline — sorted into its own group
// here, since the two take different API calls.
func TestPublishSortsReviewLevelCommentsOntoThePR(t *testing.T) {
	inline, changeWide, skipped := partitionForPublish([]review.Comment{
		{ID: "a", Body: "on a line", Anchor: review.Anchor{Path: "a.go", LineHint: 3, Side: review.SideNew}},
		{ID: "b", Body: "about the change as a whole"},
		{ID: "c", Body: "already up", State: review.Published, Anchor: review.Anchor{Path: "b.go", LineHint: 1}},
		{ID: "d", Body: "   ", Anchor: review.Anchor{Path: "c.go", LineHint: 2}},
	})
	if len(inline) != 1 || inline[0].ID != "a" {
		t.Fatalf("expected only the anchored comment inline, got %+v", inline)
	}
	if len(changeWide) != 1 || changeWide[0].ID != "b" {
		t.Fatalf("expected the review-level remark bound for the PR, got %+v", changeWide)
	}
	if skipped != 2 {
		t.Fatalf("expected the published and the empty one skipped, got %d", skipped)
	}
}

// A published review-level comment must not be reposted, the same way an inline
// one is not: the record is what makes a retry after a partial failure safe.
func TestPublishSkipsAlreadyPostedReviewLevelComments(t *testing.T) {
	inline, changeWide, skipped := partitionForPublish([]review.Comment{
		{ID: "a", Body: "summary", Publish: &review.PublishRecord{ThreadID: "IC_1"}},
		{ID: "b", Body: "another summary", State: review.Published},
	})
	if len(inline) != 0 || len(changeWide) != 0 {
		t.Fatalf("expected nothing to post, got inline=%+v changeWide=%+v", inline, changeWide)
	}
	if skipped != 2 {
		t.Fatalf("expected both skipped, got %d", skipped)
	}
}

// The flag is spelled the way GitHub's own UI labels the three buttons, since
// that is the decision the reviewer is making.
func TestParseVerdict(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ""},
		{"approve", github.EventApprove},
		{"APPROVE", github.EventApprove},
		{" approve ", github.EventApprove},
		{"comment", github.EventComment},
		{"request-changes", github.EventRequestChanges},
		{"request_changes", github.EventRequestChanges},
	} {
		got, err := parseVerdict(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
	// A typo must not silently publish without a verdict — or, worse, approve.
	if _, err := parseVerdict("lgtm"); err == nil {
		t.Fatal("expected an unknown verdict rejected")
	}
}

// With a verdict, the review-level remarks become the review's summary — which is
// what GitHub's review body is for — rather than separate comments on the PR.
func TestReviewSummaryJoinsTheChangeWideRemarks(t *testing.T) {
	remarks := []review.Comment{
		{Author: review.AuthorHuman, Body: "Scope: internal/cli only.", Kind: review.KindComment},
		{Author: "agent", Body: "Tests are missing for the publish path.", Kind: review.KindSuggestion},
		// Empty bodies contribute nothing rather than a blank paragraph.
		{Author: review.AuthorHuman, Body: "   "},
	}
	got := reviewSummary(remarks)
	if !strings.Contains(got, "Scope: internal/cli only.") {
		t.Fatalf("summary dropped the first remark: %q", got)
	}
	// Composed bodies, so an agent's remark still carries its marker and kind —
	// the same text the no-verdict path would have posted.
	if !strings.Contains(got, remarks[1].PublishBody()) {
		t.Fatalf("summary did not use the composed body: %q", got)
	}
	if strings.Count(got, "\n\n") != 1 {
		t.Fatalf("expected one paragraph break between two remarks: %q", got)
	}
	if reviewSummary(nil) != "" {
		t.Fatal("expected no summary from no remarks")
	}
}

// A verdict that asks for something has to say what, and the check has to happen
// before any comment is posted — a run that published eight comments and then
// refused the verdict would leave the reviewer guessing what landed. This asserts
// the two halves the check is made of; the ordering is in runReviewPublish.
func TestVerdictNeedingASummaryIsCaughtBeforePosting(t *testing.T) {
	for _, verdict := range []string{"comment", "request-changes"} {
		event, err := parseVerdict(verdict)
		if err != nil {
			t.Fatalf("%s: %v", verdict, err)
		}
		if !github.EventNeedsBody(event) {
			t.Fatalf("%s should require a summary", verdict)
		}
	}
	event, _ := parseVerdict("approve")
	if github.EventNeedsBody(event) {
		t.Fatal("approving should not require a summary")
	}
}

// The status bar has one row, so the publish report is squashed onto it — every
// line kept, since the part that says what failed is the part worth reading.
func TestPublishStatusLineKeepsEveryLine(t *testing.T) {
	got := publishStatusLine("posted 2, skipped 1, failed 1\n\nsubmitted the review as approve\n")
	want := "posted 2, skipped 1, failed 1 · submitted the review as approve"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if publishStatusLine("") != "" {
		t.Fatal("expected nothing from an empty report")
	}
}
