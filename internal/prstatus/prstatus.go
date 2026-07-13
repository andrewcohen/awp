// Package prstatus holds the per-PR projection the deck renders and
// caches. It lives below internal/deckui and internal/deckdata (and is
// imported by internal/cli) so the PR type cluster has a home that does
// not pull in Bubble Tea — letting the pure read model (internal/deckdata)
// reference PR status without importing the TUI package.
//
// The deckui package keeps type/const aliases (PRStatus, PRState, …) so
// existing references there and in internal/cli compile unchanged. The
// struct is moved verbatim, so the on-disk pr-status-cache.json format is
// unaffected.
package prstatus

// PRState mirrors gh's pr.state field for the row-glyph projection.
type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
)

// PRReviewDecision mirrors gh's reviewDecision; "" means no review yet.
type PRReviewDecision string

const (
	PRReviewApproved         PRReviewDecision = "APPROVED"
	PRReviewChangesRequested PRReviewDecision = "CHANGES_REQUESTED"
	PRReviewRequired         PRReviewDecision = "REVIEW_REQUIRED"
)

// PRCIState rolls up statusCheckRollup into one signal. NONE = no checks yet.
type PRCIState string

const (
	PRCINone    PRCIState = "NONE"
	PRCIPending PRCIState = "PENDING"
	PRCIPassing PRCIState = "PASSING"
	PRCIFailing PRCIState = "FAILING"
)

// PRMergeStateStatus mirrors gh's mergeStateStatus. BEHIND only surfaces when
// the repo's branch protection requires up-to-date branches; otherwise an
// out-of-date PR is reported as CLEAN.
type PRMergeStateStatus string

const (
	PRMergeStateBehind   PRMergeStateStatus = "BEHIND"
	PRMergeStateBlocked  PRMergeStateStatus = "BLOCKED"
	PRMergeStateClean    PRMergeStateStatus = "CLEAN"
	PRMergeStateDirty    PRMergeStateStatus = "DIRTY"
	PRMergeStateDraft    PRMergeStateStatus = "DRAFT"
	PRMergeStateHasHooks PRMergeStateStatus = "HAS_HOOKS"
	PRMergeStateUnknown  PRMergeStateStatus = "UNKNOWN"
	PRMergeStateUnstable PRMergeStateStatus = "UNSTABLE"
)

// PRStatus is the per-PR projection consumed by the row glyph.
// HeadRefOid is the head commit SHA at the time the PR sync ran; callers
// compare it against a local commit-id to detect "PR has moved since I
// last looked," which feeds the re-review signal.
type PRStatus struct {
	Number      int
	HeadRefName string
	HeadRefOid  string
	// BaseRefName is the branch this PR merges into. When it matches
	// another open PR's HeadRefName in the same repo, this PR is stacked
	// on that one — the edge deckdata's inbox stack layout is built from.
	BaseRefName      string
	Title            string
	Author           string
	URL              string
	State            PRState
	IsDraft          bool
	IsInMergeQueue   bool
	ReviewDecision   PRReviewDecision
	CIState          PRCIState
	MergeStateStatus PRMergeStateStatus
	// ReviewRequested: the deck owner's review is currently requested on
	// this PR. ReviewRerequested narrows it: they already reviewed and
	// the author asked again. Mine: the deck owner authored this PR.
	// All three are computed against the gh viewer login at fetch time
	// (cli/pr_status_projection.go), so the cache stays viewer-agnostic
	// at render time.
	ReviewRequested   bool
	ReviewRerequested bool
	Mine              bool
	// HasReviewComments: a reviewer left COMMENTED or CHANGES_REQUESTED
	// feedback on this PR. Distinct from ReviewDecision — plain review
	// comments never flip GitHub's branch-protection verdict off
	// REVIEW_REQUIRED, so this is the only signal that catches "someone
	// gave you feedback to look at" on your own PR.
	HasReviewComments bool
}
