package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type PRInfo struct {
	Number  int    `json:"number"`
	HeadRef string `json:"headRefName"`
	BaseRef string `json:"baseRefName"`
	HeadSHA string `json:"headRefOid"`
	BaseSHA string `json:"baseRefOid"`
	Title   string `json:"title"`
	Author  string `json:"-"`
	Body    string `json:"body"`
	URL     string `json:"url"`
	// HeadRepoOwner / HeadRepoName identify the repo the head branch
	// lives in. For non-fork PRs this matches the origin repo. For fork
	// PRs the branch is on a different repo and origin won't have it —
	// the review flow uses these to fetch the branch directly from the
	// fork before trying to align a workspace to it.
	HeadRepoOwner string
	HeadRepoName  string
	HeadRepoURL   string

	// Status fields mirror what ListPRStatus pulls per PR — populated
	// from the same `gh pr view --json` call so the review flow can
	// write the projection into the PR-status cache without a second
	// network hop.
	State            PRState
	IsDraft          bool
	ReviewDecision   ReviewDecision
	CIState          CIState
	MergeStateStatus MergeStateStatus
}

// prViewResponse mirrors `gh pr view --json` output. PRInfo flattens it
// because the nested struct only exists at the gh-call boundary.
type prViewResponse struct {
	Number         int    `json:"number"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	HeadRefOid     string `json:"headRefOid"`
	BaseRefOid     string `json:"baseRefOid"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	URL            string `json:"url"`
	HeadRepository struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"headRepository"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State             PRState          `json:"state"`
	IsDraft           bool             `json:"isDraft"`
	ReviewDecision    ReviewDecision   `json:"reviewDecision"`
	StatusCheckRollup []rawCheck       `json:"statusCheckRollup"`
	MergeStateStatus  MergeStateStatus `json:"mergeStateStatus"`
}

type Client struct {
	runner Runner
}

func New(runner Runner) *Client {
	return &Client{runner: runner}
}

type PRSummary struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HeadRef string `json:"headRefName"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	IsDraft bool `json:"isDraft"`
}

func (c *Client) ListPRs() ([]PRSummary, error) {
	out, err := c.runner.Run(
		context.Background(), "",
		"gh", "pr", "list",
		"--json", "number,title,headRefName,author,isDraft",
		"--limit", "100",
	)
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w: %s", err, out)
	}
	var prs []PRSummary
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}
	return prs, nil
}

// PRState mirrors gh's PR state field.
type PRState string

const (
	PRStateOpen   PRState = "OPEN"
	PRStateClosed PRState = "CLOSED"
	PRStateMerged PRState = "MERGED"
)

// ReviewDecision mirrors gh's reviewDecision field. The field is "" when no
// review has been requested or rendered yet.
type ReviewDecision string

const (
	ReviewApproved         ReviewDecision = "APPROVED"
	ReviewChangesRequested ReviewDecision = "CHANGES_REQUESTED"
	ReviewRequired         ReviewDecision = "REVIEW_REQUIRED"
)

// CIState rolls up statusCheckRollup into a single signal.
// NONE means there are no checks (yet) on the PR.
type CIState string

const (
	CINone    CIState = "NONE"
	CIPending CIState = "PENDING"
	CIPassing CIState = "PASSING"
	CIFailing CIState = "FAILING"
)

// MergeStateStatus mirrors gh's mergeStateStatus field. BEHIND only fires when
// the repo's branch protection requires up-to-date branches before merging;
// repos without that rule report CLEAN even when the PR is behind.
type MergeStateStatus string

const (
	MergeStateBehind   MergeStateStatus = "BEHIND"
	MergeStateBlocked  MergeStateStatus = "BLOCKED"
	MergeStateClean    MergeStateStatus = "CLEAN"
	MergeStateDirty    MergeStateStatus = "DIRTY"
	MergeStateDraft    MergeStateStatus = "DRAFT"
	MergeStateHasHooks MergeStateStatus = "HAS_HOOKS"
	MergeStateUnknown  MergeStateStatus = "UNKNOWN"
	MergeStateUnstable MergeStateStatus = "UNSTABLE"
)

// PRStatus is the per-PR projection the deck consumes to render a glyph.
// HeadRefOid carries the head commit SHA so callers can detect whether
// what they're looking at locally matches what's actually on the PR right
// now — important for collaborator PRs where the head is on a branch the
// local jj repo doesn't track.
type PRStatus struct {
	Number      int
	HeadRefName string
	HeadRefOid  string
	// BaseRefName is the branch this PR merges into. When it matches
	// another open PR's HeadRefName, the two PRs form a stack edge
	// (this PR is stacked on that one); otherwise the base is trunk.
	BaseRefName      string
	Title            string
	Author           string
	URL              string
	State            PRState
	IsDraft          bool
	IsInMergeQueue   bool
	ReviewDecision   ReviewDecision
	CIState          CIState
	MergeStateStatus MergeStateStatus
	// ReviewRequests is the logins whose review is currently requested
	// (team requests are skipped — they can't match a viewer login).
	// GitHub puts a reviewer back in this set when the author
	// re-requests their review, so "viewer ∈ ReviewRequests" covers both
	// first requests and re-requests.
	ReviewRequests []string
	// Reviewers is the logins with a latest review on record. A login in
	// both Reviewers and ReviewRequests has been asked to review AGAIN —
	// the re-request signal.
	Reviewers []string
	// HasReviewComments is true when a reviewer has left COMMENTED or
	// CHANGES_REQUESTED feedback. Unlike ReviewDecision (the
	// branch-protection verdict, which ignores plain comments), this
	// catches review feedback the author should look at even when no
	// formal "request changes" was submitted.
	HasReviewComments bool
}

// rawCheck is the (partial) shape of an entry in statusCheckRollup. gh returns
// a heterogeneous list of CheckRun and StatusContext rows. Both expose a
// Conclusion (CheckRun) or State (StatusContext) field plus a Status field; we
// reduce them to a single CIState via rollupCIState. Name (CheckRun) /
// Context (StatusContext) and the timestamps let the rollup keep only the
// latest run per check when superseded runs linger in the list.
type rawCheck struct {
	Name        string `json:"name"`
	Context     string `json:"context"`
	Conclusion  string `json:"conclusion"`
	Status      string `json:"status"`
	State       string `json:"state"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
}

type rawPRStatus struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	State             PRState          `json:"state"`
	IsDraft           bool             `json:"isDraft"`
	ReviewDecision    ReviewDecision   `json:"reviewDecision"`
	StatusCheckRollup []rawCheck       `json:"statusCheckRollup"`
	MergeStateStatus  MergeStateStatus `json:"mergeStateStatus"`
	// reviewRequests mixes Users (login) and Teams (name/slug only).
	ReviewRequests []struct {
		Login string `json:"login"`
	} `json:"reviewRequests"`
	LatestReviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"latestReviews"`
	// Reviews is the full review history (not just the latest per
	// author). gh's reviewDecision only flips to CHANGES_REQUESTED on a
	// formal "request changes" review — a reviewer who leaves COMMENTED
	// feedback never moves it off REVIEW_REQUIRED. We read the review
	// states directly so "a reviewer left feedback on your PR" can be
	// surfaced even when the branch-protection verdict hasn't changed.
	Reviews []struct {
		State string `json:"state"`
	} `json:"reviews"`
}

// hasReviewComments reports whether any review carries actionable
// feedback — a COMMENTED or CHANGES_REQUESTED review. DISMISSED (the
// review was superseded/dismissed), APPROVED, and PENDING are excluded:
// none represents open feedback the author still has to look at.
func (r rawPRStatus) hasReviewComments() bool {
	for _, rv := range r.Reviews {
		switch rv.State {
		case "COMMENTED", "CHANGES_REQUESTED":
			return true
		}
	}
	return false
}

// requestedLogins extracts the user logins whose review is requested,
// dropping empties (team review requests carry no login).
func (r rawPRStatus) requestedLogins() []string {
	var out []string
	for _, rr := range r.ReviewRequests {
		if l := strings.TrimSpace(rr.Login); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// reviewerLogins extracts the authors of the PR's latest reviews.
func (r rawPRStatus) reviewerLogins() []string {
	var out []string
	for _, rv := range r.LatestReviews {
		if l := strings.TrimSpace(rv.Author.Login); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ListPRStatus fetches OPEN PRs for the repo at repoDir via gh and
// returns a normalized status projection per PR. repoDir scopes the runner's
// working directory; gh derives the owner/name from that repo's remote.
//
// Only open PRs are listed because the deck only ever displays bulk-list
// PRs that are open: workspace rows show open PRs' status, the inbox's
// virtual rows filter to open, and the review picker defaults to open.
// Terminal (merged/closed) status for a workspace's PR is learned the
// cheap way — the per-PR top-up (GetPRStatus on a pinned PR number) and
// the post-merge write-through — never by listing every recently-closed
// PR. Listing `--state all` forced GitHub to compute the expensive
// statusCheckRollup for 100 PRs (mostly closed) that nothing rendered;
// `--state open` cuts a busy repo's fetch from ~7s to ~2s.
func (c *Client) ListPRStatus(repoDir string) ([]PRStatus, error) {
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "pr", "list",
		"--state", "open",
		"--limit", "100",
		"--json", "number,headRefName,headRefOid,baseRefName,title,url,author,state,isDraft,reviewDecision,statusCheckRollup,mergeStateStatus,reviewRequests,latestReviews,reviews",
	)
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w: %s", err, out)
	}
	var raws []rawPRStatus
	if err := json.Unmarshal([]byte(out), &raws); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}
	statuses := make([]PRStatus, len(raws))
	for i, r := range raws {
		statuses[i] = PRStatus{
			Number:            r.Number,
			HeadRefName:       r.HeadRefName,
			HeadRefOid:        r.HeadRefOid,
			BaseRefName:       r.BaseRefName,
			Title:             r.Title,
			Author:            r.Author.Login,
			URL:               r.URL,
			State:             r.State,
			IsDraft:           r.IsDraft,
			ReviewDecision:    r.ReviewDecision,
			CIState:           rollupCIState(r.StatusCheckRollup),
			MergeStateStatus:  r.MergeStateStatus,
			ReviewRequests:    r.requestedLogins(),
			Reviewers:         r.reviewerLogins(),
			HasReviewComments: r.hasReviewComments(),
		}
	}
	return statuses, nil
}

// PRStatusFromInfo projects a PRInfo (the FetchPR result, augmented with
// status fields in its `gh pr view --json` call) into the same PRStatus
// shape ListPRStatus produces. Used by the review flow to write the
// just-fetched PR into the PR-status cache without a second gh call.
func PRStatusFromInfo(p PRInfo) PRStatus {
	return PRStatus{
		Number:           p.Number,
		HeadRefName:      p.HeadRef,
		HeadRefOid:       p.HeadSHA,
		BaseRefName:      p.BaseRef,
		Title:            p.Title,
		Author:           p.Author,
		URL:              p.URL,
		State:            p.State,
		IsDraft:          p.IsDraft,
		ReviewDecision:   p.ReviewDecision,
		CIState:          p.CIState,
		MergeStateStatus: p.MergeStateStatus,
	}
}

// ProgressReporter receives optional, human-readable progress updates
// from long-running Client calls. Step marks a discrete action; Log
// appends a narrative line. A nil reporter is fine — updates are
// dropped. (deckui.Reporter satisfies this structurally.)
type ProgressReporter interface {
	Step(string)
	Log(string)
}

func progStep(r ProgressReporter, s string) {
	if r != nil {
		r.Step(s)
	}
}

func progLog(r ProgressReporter, s string) {
	if r != nil {
		r.Log(s)
	}
}

// MergePR merges a PR by number via `gh pr merge`. gh has no
// non-interactive "use the repo default" mode for ordinary branches —
// it errors without an explicit method flag — so we squash, the common
// feature-branch default. The merge is immediate (no --auto): gh fails
// fast if the PR isn't mergeable (checks pending/failing, review not
// approved, conflicts).
//
// Merge-queue branches are the exception: `gh pr merge` rejects an
// explicit strategy (the queue dictates it) and tries to enqueue via the
// `enablePullRequestAutoMerge` mutation — which fails outright when the
// repo has auto-merge disabled (cli/cli#13398): gh has no code path that
// calls the correct `enqueuePullRequest` mutation. So when we see the
// merge-queue / auto-merge-blocked signature, we bypass gh's broken path
// and enqueue the PR directly via the GraphQL mutation.
//
// rep (optional) narrates the path actually taken so the caller's
// progress UI shows squash-vs-queue accurately. The combined output is
// returned for both success and failure so the caller can surface gh's
// own message.
func (c *Client) MergePR(repoDir string, n int, rep ProgressReporter) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("MergePR: invalid PR number %d", n)
	}
	progLog(rep, fmt.Sprintf("Trying squash merge (gh pr merge %d --squash)", n))
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "pr", "merge", strconv.Itoa(n), "--squash",
	)
	if err == nil {
		progStep(rep, fmt.Sprintf("Squash-merged PR #%d", n))
		return out, nil
	}
	if mentionsMergeQueue(out) || mentionsAutoMergeBlocked(out) {
		progLog(rep, "Merge queue detected — squash strategy rejected; enqueuing via the merge queue")
		progStep(rep, fmt.Sprintf("Add PR #%d to the merge queue", n))
		return c.enqueuePR(repoDir, n)
	}
	return out, fmt.Errorf("gh pr merge %d: %w: %s", n, err, strings.TrimSpace(out))
}

// enqueuePR adds a PR to the repo's merge queue via the GraphQL
// `enqueuePullRequest` mutation — the path `gh pr merge` is missing for
// repos whose merge queue is configured without `allow_auto_merge`
// (cli/cli#13398). It resolves the PR's node id, runs the mutation, and
// returns a human-readable confirmation (queue state/position) for the
// deck's progress log.
func (c *Client) enqueuePR(repoDir string, n int) (string, error) {
	idOut, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "pr", "view", strconv.Itoa(n), "--json", "id",
	)
	if err != nil {
		return idOut, fmt.Errorf("enqueue PR %d: gh pr view --json id: %w: %s", n, err, strings.TrimSpace(idOut))
	}
	var idResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(idOut), &idResp); err != nil {
		return idOut, fmt.Errorf("enqueue PR %d: parse PR id: %w", n, err)
	}
	if idResp.ID == "" {
		return idOut, fmt.Errorf("enqueue PR %d: empty PR node id", n)
	}
	const mutation = `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){mergeQueueEntry{position state}}}`
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "api", "graphql", "-f", "query="+mutation, "-f", "id="+idResp.ID,
	)
	if err != nil {
		return out, fmt.Errorf("enqueue PR %d: %w: %s", n, err, strings.TrimSpace(out))
	}
	return enqueueSummary(n, out), nil
}

// enqueueSummary turns the enqueuePullRequest mutation response into a
// one-line confirmation, falling back to the raw output if the shape is
// unexpected.
func enqueueSummary(n int, out string) string {
	var resp struct {
		Data struct {
			EnqueuePullRequest struct {
				MergeQueueEntry *struct {
					Position int    `json:"position"`
					State    string `json:"state"`
				} `json:"mergeQueueEntry"`
			} `json:"enqueuePullRequest"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return strings.TrimSpace(out)
	}
	e := resp.Data.EnqueuePullRequest.MergeQueueEntry
	if e == nil {
		return fmt.Sprintf("added PR #%d to the merge queue", n)
	}
	state := strings.TrimSpace(e.State)
	if state == "" {
		state = "queued"
	}
	return fmt.Sprintf("added PR #%d to the merge queue (state %s, position %d)", n, state, e.Position)
}

// mentionsMergeQueue reports whether gh's output indicates the target
// branch requires a merge queue (so an explicit merge strategy must be
// dropped).
func mentionsMergeQueue(s string) bool {
	return strings.Contains(strings.ToLower(s), "merge queue")
}

// mentionsAutoMergeBlocked reports whether gh failed because it tried to
// enable auto-merge on a repo that doesn't allow it — the signature
// (cli/cli#13398) of a merge-queue repo configured without
// `allow_auto_merge`.
func mentionsAutoMergeBlocked(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "auto merge is not allowed") ||
		strings.Contains(l, "enablepullrequestautomerge")
}

// GetPRStatus fetches a single PR by number via `gh pr view` and returns
// the same projection as ListPRStatus. Used by the deck to top up
// PR-pinned workspaces (entries with PRNumber > 0) that fell outside the
// bulk `gh pr list --limit 100` window — common in busy repos with high
// PR churn.
func (c *Client) GetPRStatus(repoDir string, n int) (PRStatus, error) {
	if n <= 0 {
		return PRStatus{}, fmt.Errorf("GetPRStatus: invalid PR number %d", n)
	}
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "pr", "view", fmt.Sprintf("%d", n),
		"--json", "number,headRefName,headRefOid,baseRefName,title,url,author,state,isDraft,reviewDecision,statusCheckRollup,mergeStateStatus,reviewRequests,latestReviews,reviews",
	)
	if err != nil {
		return PRStatus{}, fmt.Errorf("gh pr view %d: %w: %s", n, err, out)
	}
	var r rawPRStatus
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return PRStatus{}, fmt.Errorf("parse gh pr view %d: %w", n, err)
	}
	return PRStatus{
		Number:            r.Number,
		HeadRefName:       r.HeadRefName,
		HeadRefOid:        r.HeadRefOid,
		BaseRefName:       r.BaseRefName,
		Title:             r.Title,
		Author:            r.Author.Login,
		URL:               r.URL,
		State:             r.State,
		IsDraft:           r.IsDraft,
		ReviewDecision:    r.ReviewDecision,
		CIState:           rollupCIState(r.StatusCheckRollup),
		MergeStateStatus:  r.MergeStateStatus,
		ReviewRequests:    r.requestedLogins(),
		Reviewers:         r.reviewerLogins(),
		HasReviewComments: r.hasReviewComments(),
	}, nil
}

// ViewerLogin returns the authenticated gh user's login. repoDir scopes
// the runner like every other call so gh resolves the right auth host
// from the repo's remote. Callers treat an error as "viewer unknown"
// and skip viewer-relative signals (review-requested) rather than
// failing the fetch.
func (c *Client) ViewerLogin(repoDir string) (string, error) {
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "api", "user", "--jq", ".login",
	)
	if err != nil {
		return "", fmt.Errorf("gh api user: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

// rollupCIState reduces gh's heterogeneous statusCheckRollup list to a single
// CIState. Priority: any failure → FAILING; any in-flight check → PENDING;
// otherwise PASSING; empty list → NONE.
func rollupCIState(checks []rawCheck) CIState {
	if len(checks) == 0 {
		return CINone
	}
	pending := false
	for _, c := range latestCheckPerName(checks) {
		// CheckRun: completed checks set Conclusion (SUCCESS, FAILURE,
		// CANCELLED, TIMED_OUT, ACTION_REQUIRED, NEUTRAL, SKIPPED, STALE).
		// In-flight checks have Status in (QUEUED, IN_PROGRESS, WAITING,
		// PENDING, REQUESTED) and an empty Conclusion.
		// StatusContext: uses State (SUCCESS, FAILURE, ERROR, PENDING,
		// EXPECTED).
		switch c.Conclusion {
		case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return CIFailing
		case "SUCCESS", "NEUTRAL", "SKIPPED", "STALE", "":
			// fall through to state check below
		}
		switch c.State {
		case "FAILURE", "ERROR":
			return CIFailing
		case "PENDING", "EXPECTED":
			pending = true
		}
		// CheckRun in flight: empty Conclusion + non-COMPLETED Status.
		if c.Conclusion == "" && c.Status != "" && c.Status != "COMPLETED" {
			pending = true
		}
	}
	if pending {
		return CIPending
	}
	return CIPassing
}

// latestCheckPerName collapses superseded runs out of a statusCheckRollup
// list. When a newer push (or a concurrency group) cancels an in-flight
// workflow run and a re-run completes, GitHub's rollup contains BOTH the
// CANCELLED first run and its replacement under the same check name —
// while the PR page shows only the latest. Without this reduction the
// stale CANCELLED entry marks the PR CI-failing forever. Keyed by CheckRun
// name (or StatusContext context); the entry with the latest
// completedAt/startedAt wins, with later list position breaking ties.
// Unnamed entries are kept as-is.
func latestCheckPerName(checks []rawCheck) []rawCheck {
	at := func(c rawCheck) string {
		// RFC3339 timestamps compare correctly as strings; CompletedAt
		// is empty for in-flight runs, so fall back to StartedAt.
		if c.CompletedAt != "" {
			return c.CompletedAt
		}
		return c.StartedAt
	}
	out := make([]rawCheck, 0, len(checks))
	idxByName := map[string]int{}
	for _, c := range checks {
		name := c.Name
		if name == "" {
			name = c.Context
		}
		if name == "" {
			out = append(out, c)
			continue
		}
		i, ok := idxByName[name]
		if !ok {
			idxByName[name] = len(out)
			out = append(out, c)
			continue
		}
		if at(c) >= at(out[i]) {
			out[i] = c
		}
	}
	return out
}

// ListMergeQueuedHeads returns the set of OPEN PR headRefNames that GitHub
// reports as currently in the repo's merge queue. The signal lives only in
// GraphQL — `gh pr list --json` does not expose `isInMergeQueue` — so we
// resolve owner/name from the local repo and run a small graphql query. An
// empty map (with nil error) means nothing is queued or the repo has no
// merge queue configured.
func (c *Client) ListMergeQueuedHeads(repoDir string) (map[string]bool, error) {
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "repo", "view", "--json", "owner,name",
	)
	if err != nil {
		return nil, fmt.Errorf("gh repo view: %w: %s", err, out)
	}
	var owner struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &owner); err != nil {
		return nil, fmt.Errorf("parse gh repo view: %w", err)
	}
	if owner.Owner.Login == "" || owner.Name == "" {
		return nil, fmt.Errorf("gh repo view: missing owner or name")
	}
	const query = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){pullRequests(states:OPEN,first:100,orderBy:{field:UPDATED_AT,direction:DESC}){nodes{headRefName isInMergeQueue}}}}`
	gOut, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "api", "graphql",
		"-F", "owner="+owner.Owner.Login,
		"-F", "name="+owner.Name,
		"-f", "query="+query,
	)
	if err != nil {
		return nil, fmt.Errorf("gh api graphql: %w: %s", err, gOut)
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []struct {
						HeadRefName    string `json:"headRefName"`
						IsInMergeQueue bool   `json:"isInMergeQueue"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(gOut), &resp); err != nil {
		return nil, fmt.Errorf("parse gh api graphql: %w", err)
	}
	queued := make(map[string]bool)
	for _, n := range resp.Data.Repository.PullRequests.Nodes {
		if n.IsInMergeQueue && n.HeadRefName != "" {
			queued[n.HeadRefName] = true
		}
	}
	return queued, nil
}

func (c *Client) FetchPR(num int) (PRInfo, error) {
	out, err := c.runner.Run(
		context.Background(), "",
		"gh", "pr", "view", strconv.Itoa(num),
		"--json", "number,headRefName,baseRefName,headRefOid,baseRefOid,title,body,url,headRepository,headRepositoryOwner,author,state,isDraft,reviewDecision,statusCheckRollup,mergeStateStatus",
	)
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %d: %w: %s", num, err, out)
	}
	var raw prViewResponse
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PRInfo{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	info := PRInfo{
		Number:           raw.Number,
		HeadRef:          raw.HeadRefName,
		BaseRef:          raw.BaseRefName,
		HeadSHA:          raw.HeadRefOid,
		BaseSHA:          raw.BaseRefOid,
		Title:            raw.Title,
		Author:           raw.Author.Login,
		Body:             raw.Body,
		URL:              raw.URL,
		HeadRepoOwner:    raw.HeadRepositoryOwner.Login,
		HeadRepoName:     raw.HeadRepository.Name,
		HeadRepoURL:      raw.HeadRepository.URL,
		State:            raw.State,
		IsDraft:          raw.IsDraft,
		ReviewDecision:   raw.ReviewDecision,
		CIState:          rollupCIState(raw.StatusCheckRollup),
		MergeStateStatus: raw.MergeStateStatus,
	}
	// gh's default `headRepository` payload doesn't include `url`,
	// so the field above is usually empty. Synthesize it from
	// owner+name so callers (e.g. the review flow fetching a fork
	// branch) always have a working clone URL to feed git fetch.
	if info.HeadRepoURL == "" && info.HeadRepoOwner != "" && info.HeadRepoName != "" {
		info.HeadRepoURL = fmt.Sprintf("https://github.com/%s/%s", info.HeadRepoOwner, info.HeadRepoName)
	}
	return info, nil
}

// PRComment is an existing comment on a PR — either a line-anchored
// inline review comment (Path/Line set), a review summary, or a
// top-level conversation comment.
type PRComment struct {
	Author string
	Kind   string // "inline", "review", or "comment"
	Path   string // "" unless line-anchored
	Line   int    // 0 unless line-anchored
	Body   string
}

// FetchPRComments returns the existing human/bot comments on a PR so a
// reviewer can avoid restating points already raised. It makes two gh
// calls: `gh pr view` for top-level conversation comments and review
// summaries, and `gh api .../comments` for the line-anchored inline
// review comments (which `gh pr view` omits). Both rely on gh resolving
// the repo from the cwd, exactly as FetchPR does.
//
// Empty (or whitespace-only) comment bodies are dropped — review
// submissions with no summary text produce these and carry no signal.
func (c *Client) FetchPRComments(num int) ([]PRComment, error) {
	var out []PRComment

	viewRaw, err := c.runner.Run(
		context.Background(), "",
		"gh", "pr", "view", strconv.Itoa(num),
		"--json", "comments,reviews",
	)
	if err != nil {
		return nil, fmt.Errorf("gh pr view %d comments: %w: %s", num, err, viewRaw)
	}
	var conv struct {
		Comments []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body string `json:"body"`
		} `json:"comments"`
		Reviews []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body string `json:"body"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal([]byte(viewRaw), &conv); err != nil {
		return nil, fmt.Errorf("parse gh pr view comments: %w", err)
	}
	for _, r := range conv.Reviews {
		if b := strings.TrimSpace(r.Body); b != "" {
			out = append(out, PRComment{Author: r.Author.Login, Kind: "review", Body: b})
		}
	}
	for _, cm := range conv.Comments {
		if b := strings.TrimSpace(cm.Body); b != "" {
			out = append(out, PRComment{Author: cm.Author.Login, Kind: "comment", Body: b})
		}
	}

	inlineRaw, err := c.runner.Run(
		context.Background(), "",
		"gh", "api", "--paginate",
		fmt.Sprintf("repos/{owner}/{repo}/pulls/%d/comments", num),
	)
	if err != nil {
		return nil, fmt.Errorf("gh api pulls/%d/comments: %w: %s", num, err, inlineRaw)
	}
	var inline []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Path string `json:"path"`
		Line int    `json:"line"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(inlineRaw), &inline); err != nil {
		return nil, fmt.Errorf("parse inline pr comments: %w", err)
	}
	for _, ic := range inline {
		if b := strings.TrimSpace(ic.Body); b != "" {
			out = append(out, PRComment{Author: ic.User.Login, Kind: "inline", Path: ic.Path, Line: ic.Line, Body: b})
		}
	}
	return out, nil
}
