package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type PRInfo struct {
	Number   int    `json:"number"`
	HeadRef  string `json:"headRefName"`
	BaseRef  string `json:"baseRefName"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
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
type PRStatus struct {
	Number           int
	HeadRefName      string
	URL              string
	State            PRState
	IsDraft          bool
	IsInMergeQueue   bool
	ReviewDecision   ReviewDecision
	CIState          CIState
	MergeStateStatus MergeStateStatus
}

// rawCheck is the (partial) shape of an entry in statusCheckRollup. gh returns
// a heterogeneous list of CheckRun and StatusContext rows. Both expose a
// Conclusion (CheckRun) or State (StatusContext) field plus a Status field; we
// reduce them to a single CIState via rollupCIState.
type rawCheck struct {
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	State      string `json:"state"`
}

type rawPRStatus struct {
	Number            int              `json:"number"`
	HeadRefName       string           `json:"headRefName"`
	URL               string           `json:"url"`
	State             PRState          `json:"state"`
	IsDraft           bool             `json:"isDraft"`
	ReviewDecision    ReviewDecision   `json:"reviewDecision"`
	StatusCheckRollup []rawCheck       `json:"statusCheckRollup"`
	MergeStateStatus  MergeStateStatus `json:"mergeStateStatus"`
}

// ListPRStatus fetches PRs (any state) for the repo at repoDir via gh and
// returns a normalized status projection per PR. repoDir scopes the runner's
// working directory; gh derives the owner/name from that repo's remote.
func (c *Client) ListPRStatus(repoDir string) ([]PRStatus, error) {
	out, err := c.runner.Run(
		context.Background(), repoDir,
		"gh", "pr", "list",
		"--state", "all",
		"--limit", "100",
		"--json", "number,headRefName,url,state,isDraft,reviewDecision,statusCheckRollup,mergeStateStatus",
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
			Number:           r.Number,
			HeadRefName:      r.HeadRefName,
			URL:              r.URL,
			State:            r.State,
			IsDraft:          r.IsDraft,
			ReviewDecision:   r.ReviewDecision,
			CIState:          rollupCIState(r.StatusCheckRollup),
			MergeStateStatus: r.MergeStateStatus,
		}
	}
	return statuses, nil
}

// rollupCIState reduces gh's heterogeneous statusCheckRollup list to a single
// CIState. Priority: any failure → FAILING; any in-flight check → PENDING;
// otherwise PASSING; empty list → NONE.
func rollupCIState(checks []rawCheck) CIState {
	if len(checks) == 0 {
		return CINone
	}
	pending := false
	for _, c := range checks {
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
		"--json", "number,headRefName,baseRefName,title,body,url",
	)
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %d: %w: %s", num, err, out)
	}
	var info PRInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return PRInfo{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return info, nil
}
