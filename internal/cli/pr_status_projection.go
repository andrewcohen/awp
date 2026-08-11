package cli

import (
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
)

// prStatusFromGithub projects a single github.PRStatus into the
// deckui.PRStatus shape the deck (and the persisted cache) consume.
// `queued` stamps IsInMergeQueue — supplied by ListMergeQueuedHeads in
// the bulk-fetch path, or `false` when no merge-queue info is available
// (e.g. the single-PR write-through from awp review).
//
// `viewer` is who the deck is being rendered for — the authenticated gh
// login plus the teams it belongs to, because a review request can name
// either. The viewer-relative signals (ReviewRequested, Mine) are reduced
// to bools here so the deck and the cache never need to know whose deck
// they're rendering. An unknown viewer (lookup failed, or the review
// write-through which doesn't fetch the request list) leaves them false.
//
// This is the single projection site: pr_status_job.go's bulk and
// top-up fetches, and review.go's write-through, all funnel through
// here so a future field addition is one edit.
func prStatusFromGithub(s github.PRStatus, queued bool, viewer github.Viewer) deckui.PRStatus {
	return deckui.PRStatus{
		Number:            s.Number,
		HeadRefName:       s.HeadRefName,
		HeadRefOid:        s.HeadRefOid,
		BaseRefName:       s.BaseRefName,
		Title:             s.Title,
		Author:            s.Author,
		URL:               s.URL,
		State:             deckui.PRState(s.State),
		IsDraft:           s.IsDraft,
		IsInMergeQueue:    queued,
		ReviewDecision:    deckui.PRReviewDecision(s.ReviewDecision),
		CIState:           deckui.PRCIState(s.CIState),
		MergeStateStatus:  deckui.PRMergeStateStatus(s.MergeStateStatus),
		ReviewRequested:   viewer.ReviewRequested(s),
		ReviewRerequested: viewer.ReviewRerequested(s),
		Mine:              viewer.Authored(s),
		HasReviewComments: s.HasReviewComments,
		Labels:            s.Labels,
	}
}

// prStatusMapFromGithub projects a slice of github.PRStatus into the
// repo's byHead map (headRefName → deckui.PRStatus). queuedHeads marks
// PRs whose head appears in the merge-queue set; nil or empty leaves
// every entry's IsInMergeQueue at false.
func prStatusMapFromGithub(statuses []github.PRStatus, queuedHeads map[string]bool, viewer github.Viewer) map[string]deckui.PRStatus {
	byHead := make(map[string]deckui.PRStatus, len(statuses))
	for _, s := range statuses {
		byHead[s.HeadRefName] = prStatusFromGithub(s, queuedHeads[s.HeadRefName], viewer)
	}
	return byHead
}
