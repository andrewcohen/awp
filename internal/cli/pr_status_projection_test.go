package cli

import (
	"reflect"
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
		Labels:           []string{"bug", "enhancement"},
	}
	got := prStatusFromGithub(src, true, github.Viewer{})
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
		Labels:           []string{"bug", "enhancement"},
	}
	if !reflect.DeepEqual(got, want) {
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
	me := github.Viewer{Login: "andrewcohen"}
	// Someone else's PR requesting my review for the first time. Login
	// matches are case-insensitive.
	got := prStatusFromGithub(src, false, me)
	if !got.ReviewRequested || got.ReviewRerequested || got.Mine {
		t.Errorf("their PR: ReviewRequested=%v ReviewRerequested=%v Mine=%v, want true/false/false", got.ReviewRequested, got.ReviewRerequested, got.Mine)
	}
	// Requested again after I already reviewed → re-request.
	src.Reviewers = []string{"AndrewCohen"}
	got = prStatusFromGithub(src, false, me)
	if !got.ReviewRequested || !got.ReviewRerequested {
		t.Errorf("re-request: ReviewRequested=%v ReviewRerequested=%v, want true/true", got.ReviewRequested, got.ReviewRerequested)
	}
	src.Reviewers = nil
	// The author's own view of the same PR.
	got = prStatusFromGithub(src, false, github.Viewer{Login: "coworker"})
	if got.ReviewRequested || !got.Mine {
		t.Errorf("author's view: ReviewRequested=%v Mine=%v, want false/true", got.ReviewRequested, got.Mine)
	}
	// Unknown viewer → both signals off.
	got = prStatusFromGithub(src, false, github.Viewer{})
	if got.ReviewRequested || got.Mine {
		t.Errorf("empty viewer: ReviewRequested=%v Mine=%v, want false/false", got.ReviewRequested, got.Mine)
	}
	// Uninvolved viewer → both off.
	got = prStatusFromGithub(src, false, github.Viewer{Login: "thirdparty"})
	if got.ReviewRequested || got.Mine {
		t.Errorf("uninvolved viewer: ReviewRequested=%v Mine=%v, want false/false", got.ReviewRequested, got.Mine)
	}
}

// A PR requested from a team the viewer is in, naming nobody. This is the
// deck-facing end of the bug: the glyph, the attention scope's "wants your
// review" arm and `p r`'s pending-request issue all read the projected bool,
// so if it is false here none of them can fire no matter what GitHub says.
func TestPrStatusFromGithubReadsATeamRequest(t *testing.T) {
	src := github.PRStatus{
		Number:             557,
		HeadRefName:        "coworker/feat",
		Author:             "coworker",
		State:              github.PRStateOpen,
		ReviewRequestTeams: []string{"acme-corp/consumer-team", "acme-corp/enterprise-team"},
	}
	inTheTeam := github.Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/consumer-team"}}
	if got := prStatusFromGithub(src, false, inTheTeam); !got.ReviewRequested {
		t.Error("a review requested from the viewer's team does not reach the deck as requested")
	}
	// The same PR read by someone in neither team wants nothing from them.
	outside := github.Viewer{Login: "thirdparty", Teams: []string{"acme-corp/platform-team"}}
	if got := prStatusFromGithub(src, false, outside); got.ReviewRequested {
		t.Error("a team the viewer is not in reads as their request")
	}
	// A login-only viewer is the pre-read:org state: the teams are
	// unavailable, so the signal stays off rather than guessing.
	noTeams := github.Viewer{Login: "andrewcohen"}
	if got := prStatusFromGithub(src, false, noTeams); got.ReviewRequested {
		t.Error("teams unknown should leave the signal off, not on")
	}
}

func TestPrStatusMapFromGithubKeysByHeadAndStampsQueue(t *testing.T) {
	statuses := []github.PRStatus{
		{Number: 1, HeadRefName: "a", State: github.PRStateOpen},
		{Number: 2, HeadRefName: "b", State: github.PRStateOpen},
	}
	queued := map[string]bool{"b": true}
	got := prStatusMapFromGithub(statuses, queued, github.Viewer{})
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
