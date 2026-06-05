package cli

import (
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
)

func TestPrStatusFromGithubPreservesFields(t *testing.T) {
	src := github.PRStatus{
		Number:           42,
		HeadRefName:      "andrew/foo",
		HeadRefOid:       "feedface",
		Title:            "feat: foo",
		URL:              "https://github.com/o/r/pull/42",
		State:            github.PRStateOpen,
		IsDraft:          true,
		ReviewDecision:   github.ReviewApproved,
		CIState:          github.CIFailing,
		MergeStateStatus: github.MergeStateDirty,
	}
	got := prStatusFromGithub(src, true, "")
	want := deckui.PRStatus{
		Number:           42,
		HeadRefName:      "andrew/foo",
		HeadRefOid:       "feedface",
		Title:            "feat: foo",
		URL:              "https://github.com/o/r/pull/42",
		State:            deckui.PRStateOpen,
		IsDraft:          true,
		IsInMergeQueue:   true,
		ReviewDecision:   deckui.PRReviewApproved,
		CIState:          deckui.PRCIFailing,
		MergeStateStatus: deckui.PRMergeStateDirty,
	}
	if got != want {
		t.Fatalf("projection mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestPrStatusFromGithubViewerSignals(t *testing.T) {
	src := github.PRStatus{
		Number:         7,
		HeadRefName:    "coworker/feat",
		Author:         "CoWorker",
		State:          github.PRStateOpen,
		ReviewRequests: []string{"AndrewCohen"},
	}
	// Someone else's PR requesting my review for the first time. Login
	// matches are case-insensitive.
	got := prStatusFromGithub(src, false, "andrewcohen")
	if !got.ReviewRequested || got.ReviewRerequested || got.Mine {
		t.Errorf("their PR: ReviewRequested=%v ReviewRerequested=%v Mine=%v, want true/false/false", got.ReviewRequested, got.ReviewRerequested, got.Mine)
	}
	// Requested again after I already reviewed → re-request.
	src.Reviewers = []string{"AndrewCohen"}
	got = prStatusFromGithub(src, false, "andrewcohen")
	if !got.ReviewRequested || !got.ReviewRerequested {
		t.Errorf("re-request: ReviewRequested=%v ReviewRerequested=%v, want true/true", got.ReviewRequested, got.ReviewRerequested)
	}
	src.Reviewers = nil
	// The author's own view of the same PR.
	got = prStatusFromGithub(src, false, "coworker")
	if got.ReviewRequested || !got.Mine {
		t.Errorf("author's view: ReviewRequested=%v Mine=%v, want false/true", got.ReviewRequested, got.Mine)
	}
	// Unknown viewer → both signals off.
	got = prStatusFromGithub(src, false, "")
	if got.ReviewRequested || got.Mine {
		t.Errorf("empty viewer: ReviewRequested=%v Mine=%v, want false/false", got.ReviewRequested, got.Mine)
	}
	// Uninvolved viewer → both off.
	got = prStatusFromGithub(src, false, "thirdparty")
	if got.ReviewRequested || got.Mine {
		t.Errorf("uninvolved viewer: ReviewRequested=%v Mine=%v, want false/false", got.ReviewRequested, got.Mine)
	}
}

func TestPrStatusMapFromGithubKeysByHeadAndStampsQueue(t *testing.T) {
	statuses := []github.PRStatus{
		{Number: 1, HeadRefName: "a", State: github.PRStateOpen},
		{Number: 2, HeadRefName: "b", State: github.PRStateOpen},
	}
	queued := map[string]bool{"b": true}
	got := prStatusMapFromGithub(statuses, queued, "")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got["a"].IsInMergeQueue {
		t.Errorf("'a' should not be marked queued")
	}
	if !got["b"].IsInMergeQueue {
		t.Errorf("'b' should be marked queued")
	}
}
