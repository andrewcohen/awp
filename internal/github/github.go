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

// PRStatus is the per-PR projection the deck consumes to render a glyph.
type PRStatus struct {
	Number         int
	HeadRefName    string
	State          PRState
	IsDraft        bool
	ReviewDecision ReviewDecision
	CIState        CIState
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
	Number            int            `json:"number"`
	HeadRefName       string         `json:"headRefName"`
	State             PRState        `json:"state"`
	IsDraft           bool           `json:"isDraft"`
	ReviewDecision    ReviewDecision `json:"reviewDecision"`
	StatusCheckRollup []rawCheck     `json:"statusCheckRollup"`
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
		"--json", "number,headRefName,state,isDraft,reviewDecision,statusCheckRollup",
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
			Number:         r.Number,
			HeadRefName:    r.HeadRefName,
			State:          r.State,
			IsDraft:        r.IsDraft,
			ReviewDecision: r.ReviewDecision,
			CIState:        rollupCIState(r.StatusCheckRollup),
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
