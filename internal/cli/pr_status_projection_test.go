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
		Title:            "feat: foo",
		URL:              "https://github.com/o/r/pull/42",
		State:            github.PRStateOpen,
		IsDraft:          true,
		ReviewDecision:   github.ReviewApproved,
		CIState:          github.CIFailing,
		MergeStateStatus: github.MergeStateDirty,
	}
	got := prStatusFromGithub(src, true)
	want := deckui.PRStatus{
		Number:           42,
		HeadRefName:      "andrew/foo",
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

func TestPrStatusMapFromGithubKeysByHeadAndStampsQueue(t *testing.T) {
	statuses := []github.PRStatus{
		{Number: 1, HeadRefName: "a", State: github.PRStateOpen},
		{Number: 2, HeadRefName: "b", State: github.PRStateOpen},
	}
	queued := map[string]bool{"b": true}
	got := prStatusMapFromGithub(statuses, queued)
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
