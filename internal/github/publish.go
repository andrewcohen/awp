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
	Line int
	// Side is "RIGHT" for the new side of the diff, "LEFT" for the old.
	Side string
	Body string
	// CommitID is the head SHA the comment is anchored against. GitHub requires
	// it and rejects comments anchored to a commit that is no longer the head.
	CommitID string
	// InReplyTo posts into an existing thread instead of starting one.
	InReplyTo string
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
		side := nc.Side
		if side == "" {
			side = "RIGHT"
		}
		args = append(args,
			"-f", "path="+nc.Path,
			"-F", "line="+strconv.Itoa(nc.Line),
			"-f", "side="+side,
		)
		if nc.CommitID != "" {
			args = append(args, "-f", "commit_id="+nc.CommitID)
		}
	}
	raw, err := c.runner.Run(context.Background(), "", "gh", args...)
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
		context.Background(), "",
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
