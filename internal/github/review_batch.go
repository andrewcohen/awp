package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Publishing a review as one review.
//
// The REST endpoint for a review comment (`POST pulls/N/comments`) creates a
// *single-comment review* per call. Eight comments therefore appeared on the PR as
// eight review entries with empty bodies, plus a ninth carrying the verdict — seen on
// alpha #2329 and app-main #54, where seven and eight empty COMMENTED reviews
// are still sitting. GitHub does not allow deleting a submitted review, so that mess
// is permanent; the only fix is to stop making it.
//
// GraphQL's addPullRequestReview takes every thread in one mutation. Two calls:
//
//  1. Create the review with its threads and *no* event, which leaves it PENDING —
//     staged, and visible to nobody but the author.
//  2. Submit it with the verdict and body.
//
// This inverts the argument the REST path was built on. Posting one comment at a time
// was meant to make partial failure recoverable, because a batch that fails halfway
// leaves you unable to tell what landed. But a GraphQL mutation is atomic — a bad line
// fails the whole thing and creates nothing — and a pending review is itself the
// staging area: if step 2 fails, step 1's work is invisible and can be discarded
// (deletePullRequestReview) so a retry starts clean. Nothing is ever half-published
// where somebody else can see it.

// DraftThread is one thread of a batched review: a body attached to a line, or to a
// range when StartLine is set.
type DraftThread struct {
	Path string
	// Line is the thread's last line, GitHub's own convention for a range.
	Line int
	// StartLine is the first line of a range, zero for a single line.
	StartLine int
	// Side is "RIGHT" for the new side of the diff, "LEFT" for the old.
	Side string
	Body string
}

// CreatedReview is a staged review: its id, and the threads it now holds.
type CreatedReview struct {
	ID      string
	Threads []CreatedThread
}

// CreatedThread is one comment GitHub created, so a local record can name what it
// produced rather than pointing every comment at the review as a whole.
type CreatedThread struct {
	ID   string
	Path string
	Line int
}

// ThreadID is the id GitHub gave the thread at path:line, empty when there is none.
// Matched on the location because that is what both sides agree on — the mutation
// takes no client-side identifiers.
func (r CreatedReview) ThreadID(path string, line int) string {
	for _, t := range r.Threads {
		if t.Path == path && t.Line == line {
			return t.ID
		}
	}
	return ""
}

// PRNodeID is a PR's GraphQL node id, which every review mutation is addressed by.
func (c *Client) PRNodeID(num int) (string, error) {
	out, err := c.runner.Run(
		context.Background(), c.dir,
		"gh", "pr", "view", strconv.Itoa(num), "--json", "id",
	)
	if err != nil {
		return "", fmt.Errorf("gh pr view %d --json id: %w: %s", num, err, out)
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parse gh pr view %d id: %w", num, err)
	}
	if strings.TrimSpace(resp.ID) == "" {
		return "", fmt.Errorf("gh pr view %d: no node id", num)
	}
	return resp.ID, nil
}

const createReviewMutation = `mutation($prId:ID!,$oid:GitObjectID,$threads:[DraftPullRequestReviewThread]){
  addPullRequestReview(input:{pullRequestId:$prId,commitOID:$oid,threads:$threads}){
    pullRequestReview{ id comments(first:100){nodes{ id path line originalLine }} }
  }
}`

// CreatePendingReview stages a review holding every thread, without submitting it.
//
// No event, so GitHub leaves it PENDING: the comments exist but only the author can
// see them. That is what makes the second call safe to fail.
func (c *Client) CreatePendingReview(prNodeID, commitOID string, threads []DraftThread) (CreatedReview, error) {
	if strings.TrimSpace(prNodeID) == "" {
		return CreatedReview{}, fmt.Errorf("review: no pull request to review")
	}
	vars := map[string]any{"prId": prNodeID, "threads": draftThreadVars(threads)}
	// Omitted rather than sent empty: GitHub defaults it to the PR's newest commit,
	// and an empty GitObjectID is a type error rather than a default.
	if sha := strings.TrimSpace(commitOID); sha != "" {
		vars["oid"] = sha
	}
	var resp struct {
		Data struct {
			AddPullRequestReview struct {
				PullRequestReview *struct {
					ID       string `json:"id"`
					Comments struct {
						Nodes []struct {
							ID           string `json:"id"`
							Path         string `json:"path"`
							Line         *int   `json:"line"`
							OriginalLine *int   `json:"originalLine"`
						} `json:"nodes"`
					} `json:"comments"`
				} `json:"pullRequestReview"`
			} `json:"addPullRequestReview"`
		} `json:"data"`
	}
	if err := c.graphql(createReviewMutation, vars, &resp); err != nil {
		return CreatedReview{}, fmt.Errorf("staging the review: %w", err)
	}
	pr := resp.Data.AddPullRequestReview.PullRequestReview
	if pr == nil || strings.TrimSpace(pr.ID) == "" {
		return CreatedReview{}, fmt.Errorf("staging the review: GitHub returned no review")
	}
	out := CreatedReview{ID: pr.ID}
	for _, n := range pr.Comments.Nodes {
		// line is null for a comment GitHub already considers outdated; originalLine is
		// where it was written, which is what we asked for and what we can match on.
		line := 0
		switch {
		case n.Line != nil:
			line = *n.Line
		case n.OriginalLine != nil:
			line = *n.OriginalLine
		}
		out.Threads = append(out.Threads, CreatedThread{ID: n.ID, Path: n.Path, Line: line})
	}
	return out, nil
}

// draftThreadVars shapes threads the way DraftPullRequestReviewThread expects.
//
// startSide is sent alongside startLine because GitHub defaults it to the side of the
// pull request rather than to the side already given for the end, so a range on the
// old side would otherwise lose its start.
func draftThreadVars(threads []DraftThread) []map[string]any {
	out := make([]map[string]any, 0, len(threads))
	for _, t := range threads {
		side := t.Side
		if side == "" {
			side = "RIGHT"
		}
		v := map[string]any{
			"path": t.Path,
			"line": t.Line,
			"side": side,
			"body": t.Body,
		}
		if t.StartLine > 0 && t.StartLine < t.Line {
			v["startLine"] = t.StartLine
			v["startSide"] = side
		}
		out = append(out, v)
	}
	return out
}

const submitReviewMutation = `mutation($id:ID!,$event:PullRequestReviewEvent!,$body:String){
  submitPullRequestReview(input:{pullRequestReviewId:$id,event:$event,body:$body}){
    pullRequestReview{ id state }
  }
}`

// SubmitStagedReview submits a pending review with its verdict and body.
func (c *Client) SubmitStagedReview(reviewID, event, body string) error {
	switch event {
	case EventApprove, EventComment, EventRequestChanges:
	default:
		return fmt.Errorf("unknown review event %q", event)
	}
	if EventNeedsBody(event) && strings.TrimSpace(body) == "" {
		return fmt.Errorf("a %s review needs a summary", strings.ToLower(event))
	}
	vars := map[string]any{"id": reviewID, "event": event}
	// An empty body on an approval is the difference between "approved" and
	// "approved, with an empty comment attached".
	if strings.TrimSpace(body) != "" {
		vars["body"] = body
	}
	var resp struct {
		Data struct {
			SubmitPullRequestReview struct {
				PullRequestReview *struct {
					State string `json:"state"`
				} `json:"pullRequestReview"`
			} `json:"submitPullRequestReview"`
		} `json:"data"`
	}
	if err := c.graphql(submitReviewMutation, vars, &resp); err != nil {
		return fmt.Errorf("submitting the review: %w", err)
	}
	return nil
}

const deleteReviewMutation = `mutation($id:ID!){
  deletePullRequestReview(input:{pullRequestReviewId:$id}){ clientMutationId }
}`

// DeleteStagedReview discards a pending review.
//
// Only ever called on one that was never submitted, which is the only kind GitHub
// allows deleting. Used to clean up after a failed submit so the comments are not left
// staged where a retry would add a second copy of every one of them.
func (c *Client) DeleteStagedReview(reviewID string) error {
	if strings.TrimSpace(reviewID) == "" {
		return nil
	}
	var resp struct{}
	if err := c.graphql(deleteReviewMutation, map[string]any{"id": reviewID}, &resp); err != nil {
		return fmt.Errorf("discarding the staged review: %w", err)
	}
	return nil
}

// graphql runs a query with variables and decodes the response.
//
// The request goes through a temp file because variables here are nested (a list of
// thread objects) and gh's -f/-F flags only carry flat values; --input takes the whole
// JSON body. It also keeps comment bodies out of argv, which is where the shell
// escaping problem in review bodies came from in the first place.
func (c *Client) graphql(query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return fmt.Errorf("encoding the request: %w", err)
	}
	f, err := os.CreateTemp("", "awp-gql-*.json")
	if err != nil {
		return fmt.Errorf("writing the request: %w", err)
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the request: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing the request: %w", err)
	}
	raw, runErr := c.runner.Run(context.Background(), c.dir, "gh", "api", "graphql", "--input", filepath.Clean(name))
	// GraphQL reports failures in the body, and gh exits non-zero for them too. Read
	// the errors array first either way: its message is the useful one, where gh's is
	// only that the request failed.
	if msg := graphqlErrors(raw); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	if runErr != nil {
		return fmt.Errorf("%w: %s", runErr, strings.TrimSpace(raw))
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("parsing the response: %w", err)
	}
	return nil
}

// graphqlErrors is the joined messages from a GraphQL errors array, empty when there
// are none (or when the payload is not readable as one).
func graphqlErrors(raw string) string {
	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return ""
	}
	msgs := make([]string, 0, len(resp.Errors))
	for _, e := range resp.Errors {
		if m := strings.TrimSpace(e.Message); m != "" {
			msgs = append(msgs, m)
		}
	}
	return strings.Join(msgs, "; ")
}
