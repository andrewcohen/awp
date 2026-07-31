package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Posting review comments to a PR.
//
// Comments are posted one at a time rather than batched into a single review
// submission. A batched review is one API call and looks tidier, but a partial
// failure inside it is unrecoverable: you cannot tell which comments landed, so a
// retry either duplicates everything or drops everything. Posting individually
// means each comment's outcome is known and recorded, and a retry can skip
// exactly what already succeeded.

// NewComment is a comment to post on a PR.
type NewComment struct {
	Path string
	// Line is the comment's last line, which is GitHub's own convention: a
	// multi-line comment is `line` plus a `start_line` above it, not a start plus a
	// length.
	Line int
	// StartLine is the first line of a multi-line comment, zero for a single-line
	// one. GitHub requires it to be in the same diff hunk as Line and rejects the
	// comment otherwise.
	StartLine int
	// Side is "RIGHT" for the new side of the diff, "LEFT" for the old.
	Side string
	Body string
	// CommitID is the head SHA the comment is anchored against. GitHub requires
	// it and rejects comments anchored to a commit that is no longer the head.
	CommitID string
	// InReplyTo posts into an existing thread instead of starting one.
	InReplyTo string
}

// PRHeadSHA is the commit a PR's head currently points at.
//
// Its own call rather than FetchPR, which pulls the title, the body, the status
// rollup and the labels to get at one field. Needed because every new review
// comment has to name the commit it is against: GitHub rejects one without a
// commit_id, and the answer changes whenever the author pushes.
func (c *Client) PRHeadSHA(num int) (string, error) {
	out, err := c.runner.Run(
		context.Background(), c.dir,
		"gh", "pr", "view", strconv.Itoa(num), "--json", "headRefOid",
	)
	if err != nil {
		return "", fmt.Errorf("gh pr view %d: %w: %s", num, err, out)
	}
	var resp struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parse gh pr view %d: %w", num, err)
	}
	if strings.TrimSpace(resp.HeadRefOid) == "" {
		return "", fmt.Errorf("gh pr view %d: no head commit", num)
	}
	return resp.HeadRefOid, nil
}

// PostReviewComment posts a single inline comment and returns the thread ID it
// created or replied to, so a re-publish can recognise it as already done.
func (c *Client) PostReviewComment(num int, nc NewComment) (string, error) {
	owner, name, err := c.repoOwnerName()
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, name, num)
	args := []string{"api", "--method", "POST", endpoint, "-f", "body=" + nc.Body}
	if nc.InReplyTo != "" {
		args = append(args, "-F", "in_reply_to="+nc.InReplyTo)
	} else {
		if strings.TrimSpace(nc.Path) == "" || nc.Line <= 0 {
			return "", fmt.Errorf("review comment needs a path and line")
		}
		// GitHub requires it, and refuses the whole request without it — with an
		// error listing every alternative shape that also did not match, which is
		// not a readable way to learn you forgot one field. Caught here so a run
		// says so once instead of once per comment.
		if strings.TrimSpace(nc.CommitID) == "" {
			return "", fmt.Errorf("review comment needs the commit it is against")
		}
		side := nc.Side
		if side == "" {
			side = "RIGHT"
		}
		args = append(args,
			"-f", "path="+nc.Path,
			"-F", "line="+strconv.Itoa(nc.Line),
			"-f", "side="+side,
		)
		// A range: start_line marks the top of it. start_side is sent alongside
		// because GitHub defaults it to the side of the *pull request*, not to the
		// side already given for the end — a range on the old side loses its start
		// without it.
		if nc.StartLine > 0 && nc.StartLine < nc.Line {
			args = append(args,
				"-F", "start_line="+strconv.Itoa(nc.StartLine),
				"-f", "start_side="+side,
			)
		}
		// Unconditional: it is required, and the guard above has already refused an
		// empty one.
		args = append(args, "-f", "commit_id="+nc.CommitID)
	}
	raw, err := c.runner.Run(context.Background(), c.dir, "gh", args...)
	if err != nil {
		return "", fmt.Errorf("gh api post review comment on %d: %w: %s", num, err, raw)
	}
	var resp struct {
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		// The comment posted; only the identifier is unreadable. Reporting an
		// error here would invite a retry that duplicates it, so this is
		// deliberately treated as success with an unknown id.
		return "", nil
	}
	if resp.NodeID != "" {
		return resp.NodeID, nil
	}
	if resp.ID != 0 {
		return strconv.FormatInt(resp.ID, 10), nil
	}
	return "", nil
}

// Review verdicts, in GitHub's vocabulary for the `event` field of a review
// submission.
const (
	// EventApprove approves the PR. The only verdict whose body may be empty.
	EventApprove = "APPROVE"
	// EventComment leaves a review without a verdict either way.
	EventComment = "COMMENT"
	// EventRequestChanges asks for changes before the PR can merge.
	EventRequestChanges = "REQUEST_CHANGES"
)

// EventNeedsBody reports whether GitHub requires a summary for this verdict. It
// rejects COMMENT and REQUEST_CHANGES without one — the same rule its own UI
// applies, since a verdict that asks for something has to say what.
func EventNeedsBody(event string) bool {
	return event == EventComment || event == EventRequestChanges
}

// SubmitReview submits a review on a PR with a verdict, returning the review's
// id so a re-publish can recognise it as already done.
//
// A separate call from the inline comments rather than one batched submission
// carrying them. A batch is tidier and one round trip, but a partial failure
// inside it is unrecoverable: you cannot tell which comments landed, so a retry
// either duplicates everything or drops everything. Posting the comments
// individually and then submitting the verdict keeps each comment's outcome
// known, at the cost of the verdict arriving as its own event.
func (c *Client) SubmitReview(num int, event, body string) (string, error) {
	switch event {
	case EventApprove, EventComment, EventRequestChanges:
	default:
		return "", fmt.Errorf("unknown review event %q", event)
	}
	if EventNeedsBody(event) && strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("a %s review needs a summary", strings.ToLower(event))
	}
	owner, name, err := c.repoOwnerName()
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, num)
	args := []string{"api", "--method", "POST", endpoint, "-f", "event=" + event}
	// Sent only when there is one: an empty body on an approval is the difference
	// between "approved" and "approved, with an empty comment attached".
	if strings.TrimSpace(body) != "" {
		args = append(args, "-f", "body="+body)
	}
	raw, err := c.runner.Run(context.Background(), c.dir, "gh", args...)
	if err != nil {
		return "", fmt.Errorf("gh api submit review on %d: %w: %s", num, err, raw)
	}
	var resp struct {
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		// Submitted; only the identifier is unreadable. Same call the comment posts
		// make — an error here would invite a retry that submits a second review.
		return "", nil
	}
	if resp.NodeID != "" {
		return resp.NodeID, nil
	}
	if resp.ID != 0 {
		return strconv.FormatInt(resp.ID, 10), nil
	}
	return "", nil
}

// PostPRComment adds a comment on the PR itself rather than on a line of it —
// where a remark about the change as a whole belongs. Returns the comment's id,
// for the same reason PostReviewComment does: a re-publish has to be able to
// recognise what already landed.
//
// The REST endpoint rather than `gh pr comment`, which prints a URL and would
// leave nothing to record. A PR-level comment is an issue comment as far as
// GitHub's API is concerned, which is why the path says issues.
func (c *Client) PostPRComment(num int, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("pr comment is empty")
	}
	owner, name, err := c.repoOwnerName()
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/comments", owner, name, num)
	raw, err := c.runner.Run(
		context.Background(), c.dir,
		"gh", "api", "--method", "POST", endpoint, "-f", "body="+body,
	)
	if err != nil {
		return "", fmt.Errorf("gh api post pr comment on %d: %w: %s", num, err, raw)
	}
	var resp struct {
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		// Posted; only the identifier is unreadable. Same call as
		// PostReviewComment makes — reporting an error would invite a retry that
		// duplicates the comment.
		return "", nil
	}
	if resp.NodeID != "" {
		return resp.NodeID, nil
	}
	if resp.ID != 0 {
		return strconv.FormatInt(resp.ID, 10), nil
	}
	return "", nil
}
