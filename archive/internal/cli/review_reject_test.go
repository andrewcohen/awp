package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

// A publish that refuses over a bad anchor used to leave nothing behind but a
// message on the way past. On alpha #2348 the only way to clear the block was
// to delete the finding — someone did, and the body went with it.

// rejectFixture is a store holding one finding, ready to publish.
func rejectFixture(t *testing.T, c review.Comment) (review.Store, review.Review, review.Comment) {
	t.Helper()
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repos/theirs", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	saved, err := store.AddComment(r, c)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return store, r, saved
}

// only reads back the review's single finding.
func only(t *testing.T, store review.Store, r review.Review) review.Comment {
	t.Helper()
	got, err := store.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the finding kept, got %d: %+v", len(got), got)
	}
	return got[0]
}

// publishFixture runs a real publish against the fake gh, whose PR diff covers
// lines 1-9 of a.go, b.go and c.go.
func publishFixture(t *testing.T, store review.Store, r review.Review, comments []review.Comment) error {
	t.Helper()
	var out bytes.Buffer
	return publishReview(&dirRecordingRunner{}, publishRequest{
		Store:    store,
		Review:   r,
		Comments: comments,
		PR:       54,
		Event:    github.EventApprove,
		Verdict:  "approve",
		Dir:      "/workspaces/theirs-pr-54",
	}, &out)
}

// The finding survives the refusal, carrying the reason it was refused.
func TestARefusedFindingIsKeptWithItsReason(t *testing.T) {
	store, r, _ := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		// A file the PR does not touch, so there is nothing to relocate onto either.
		Anchor: review.Anchor{Path: "d.go", Side: review.SideNew, LineHint: 688, Text: "leak()"},
	})
	comments, err := store.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if err := publishFixture(t, store, r, comments); err == nil {
		t.Fatal("expected the run refused over an anchor outside the diff")
	}

	got := only(t, store, r)
	if got.Body != "this leaks" {
		t.Fatalf("expected the words kept, got %q", got.Body)
	}
	reason, refused := got.Rejected()
	if !refused {
		t.Fatal("expected the refusal recorded on the finding")
	}
	if reason != "file is not in the PR's diff" {
		t.Fatalf("expected the check's own words, got %q", reason)
	}
	// Still open, still ours: the next run has to retry it rather than skip it.
	if got.State != review.Open || got.OnGitHub() {
		t.Fatalf("expected it still unpublished, got state=%q onGitHub=%v", got.State, got.OnGitHub())
	}
}

// The refusal says the finding survived and where to look. An error that only
// names what is wrong leaves the reader to guess whether anything was lost —
// which is the guess that got one deleted.
func TestTheRefusalSaysTheFindingIsKept(t *testing.T) {
	store, r, _ := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		Anchor: review.Anchor{Path: "d.go", Side: review.SideNew, LineHint: 688, Text: "leak()"},
	})
	comments, _ := store.Comments(r)
	err := publishFixture(t, store, r, comments)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"d.go", "kept", "awp review list", "publish again"} {
		if !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// Repair the anchor and the reason goes. A rejection that outlives the anchor it
// was about is the same lie in the other direction.
func TestPublishingClearsAnEarlierRefusal(t *testing.T) {
	store, r, saved := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "line 3"},
	})
	saved.Reject = &review.RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	comments, _ := store.Comments(r)
	if err := publishFixture(t, store, r, comments); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := only(t, store, r)
	if _, refused := got.Rejected(); refused {
		t.Fatalf("expected the stale refusal cleared, got %+v", got.Reject)
	}
	if !got.OnGitHub() {
		t.Fatal("expected the finding published")
	}
}

// recordAnchorVerdicts is where both directions meet, so the cases that are hard
// to reach through a whole publish are checked on it directly.

// A check that could not run decides nothing, so it must not retire a rejection a
// run that *could* see the diff had recorded.
func TestNoVerdictsLeavesAnEarlierRefusalAlone(t *testing.T) {
	store, r, saved := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "line 3"},
	})
	saved.Reject = &review.RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	var out bytes.Buffer
	stored, _ := store.Comments(r)
	recordAnchorVerdicts(store, r, stored, stored, nil, &out)

	if _, refused := only(t, store, r).Rejected(); !refused {
		t.Fatal("expected the refusal kept when the check could not run")
	}
}

// The write must not smuggle a relocation into the store. b.Threads carries the
// anchors the run would *send*, which for a drifted comment is not where it was
// filed — and the store deliberately keeps what was filed, since a hint is a hint
// and the viewer locates a comment by its text.
func TestClearingARefusalDoesNotPersistARelocation(t *testing.T) {
	store, r, saved := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 3, Text: "line 3"},
	})
	saved.Reject = &review.RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	stored, _ := store.Comments(r)
	// What the preflight would hand on: the same finding, moved to line 7.
	moved := stored[0]
	moved.Anchor.LineHint = 7
	var out bytes.Buffer
	recordAnchorVerdicts(store, r, stored, []review.Comment{moved},
		[]anchorVerdict{{State: anchorOK, Anchor: moved.Anchor, Note: "relocated: 3 → 7"}}, &out)

	got := only(t, store, r)
	if _, refused := got.Rejected(); refused {
		t.Fatal("expected the refusal cleared")
	}
	if got.Anchor.LineHint != 3 {
		t.Fatalf("expected the filed line kept, got %d", got.Anchor.LineHint)
	}
}

// Re-running against the same broken anchor must not keep moving the timestamp:
// when it was decided is a fact about the decision, not about the last attempt.
func TestAnUnchangedRefusalIsNotRewritten(t *testing.T) {
	store, r, saved := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		Anchor: review.Anchor{Path: "d.go", Side: review.SideNew, LineHint: 688, Text: "leak()"},
	})
	first := time.Unix(1700000000, 0)
	saved.Reject = &review.RejectRecord{Reason: "file is not in the PR's diff", At: first}
	if err := store.UpdateComment(r, saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	stored, _ := store.Comments(r)
	var out bytes.Buffer
	recordAnchorVerdicts(store, r, stored, stored,
		[]anchorVerdict{{State: anchorMissingFile, Note: "file is not in the PR's diff"}}, &out)

	got := only(t, store, r)
	if !got.Reject.At.Equal(first) {
		t.Fatalf("expected the original decision time kept, got %v", got.Reject.At)
	}
}

// A reason that changed is rewritten — the anchor was repaired into a different
// kind of wrong, and the old reason would send the reader to the wrong problem.
func TestAChangedReasonReplacesTheOldOne(t *testing.T) {
	store, r, saved := rejectFixture(t, review.Comment{
		Author: review.AuthorHuman, Body: "this leaks",
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 688, Text: "leak()"},
	})
	saved.Reject = &review.RejectRecord{Reason: "file is not in the PR's diff", At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	stored, _ := store.Comments(r)
	var out bytes.Buffer
	recordAnchorVerdicts(store, r, stored, stored,
		[]anchorVerdict{{State: anchorMissingLine, Note: "line 688 is not in the diff"}}, &out)

	reason, refused := only(t, store, r).Rejected()
	if !refused || reason != "line 688 is not in the diff" {
		t.Fatalf("expected the new reason, got %q / %v", reason, refused)
	}
}

// The listing is where a refusal has to surface. Without it a refused finding
// reads as one of the open ones, and a run that refused looks identical to one
// that never happened — which is how a real refusal went unnoticed for two days.
func TestReviewListSaysAFindingWasRefused(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	store, r := reviewFor(t, runner, svc)
	finding.Reject = &review.RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, finding); err != nil {
		t.Fatalf("update: %v", err)
	}

	out := listing(t, runner, svc)
	// The reason travels with the mark. A bare `refused` says a reason exists
	// somewhere else, and somewhere else is awp.log — the arrangement that failed.
	if !strings.Contains(out, "refused: line 688 is not in the diff") {
		t.Fatalf("the listing does not say why it was refused:\n%s", out)
	}
	// Its own column, on every row, so the fields line up and a reader can index
	// them (see proposalColumn).
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, finding.ID) {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 || !strings.HasPrefix(fields[4], "refused: ") {
			t.Fatalf("the refusal is not in its own column: %q", line)
		}
	}
}

// A finding nothing refused says so in the same column, rather than leaving it out
// and shifting every field after it.
func TestReviewListMarksTheUnrefusedRows(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	for _, line := range strings.Split(strings.TrimSpace(listing(t, runner, svc)), "\n") {
		if !strings.HasPrefix(line, finding.ID) {
			continue
		}
		if fields := strings.Split(line, "\t"); len(fields) < 5 || fields[4] != "-" {
			t.Fatalf("expected `-` in the refusal column, got %q", line)
		}
	}
}

// The machine channel carries it too, so an agent re-reading its own findings does
// not have to scrape the human table for the one field that says why a publish
// would not take one.
func TestARefusalSurvivesTheJSONChannel(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	store, r := reviewFor(t, runner, svc)
	finding.Reject = &review.RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(0, 0)}
	if err := store.UpdateComment(r, finding); err != nil {
		t.Fatalf("update: %v", err)
	}

	for _, c := range listComments(t, runner, svc) {
		if c.ID != finding.ID {
			// Omitted rather than written null on everything that was never refused.
			if c.Reject != nil {
				t.Errorf("%s carries a refusal it never had: %+v", c.ID, c.Reject)
			}
			continue
		}
		reason, refused := c.Rejected()
		if !refused || reason != "line 688 is not in the diff" {
			t.Fatalf("expected the reason through JSON, got %q / %v", reason, refused)
		}
	}
}
