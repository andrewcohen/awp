package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PR review threads, for rendering a PR's existing conversation inline in the
// diff and for resolving it.
//
// FetchPRComments already returns comment *bodies*, which is enough to keep an
// agent from re-raising a point. Rendering threads in a reviewer's diff needs
// more: a thread's node ID (to resolve it), whether it is already resolved, and
// which side of the diff each comment sits on.
//
// That data is GraphQL-only. The REST endpoint FetchPRComments uses exposes
// neither thread grouping nor resolution state, and resolving is a mutation with
// no REST equivalent at all — so this file speaks GraphQL through the same
// `gh api graphql` path the merge-queue queries already use.

// ThreadComment is one comment within a review thread.
type ThreadComment struct {
	// ID is the comment's GraphQL node ID — the same id addPullRequestReview hands
	// back for a comment it creates. That is what lets a mirrored thread be
	// recognised as the echo of a comment published from here, rather than guessed
	// at from its body and line.
	ID     string
	Author string
	Body   string
}

// ReviewThread is a line-anchored conversation on a PR.
type ReviewThread struct {
	// ID is the GraphQL node ID, required to resolve or unresolve.
	ID string
	// Path and Line locate the thread. Line is the thread's current position,
	// which GitHub recomputes as the PR moves; StartLine is set for multi-line
	// threads.
	Path      string
	Line      int
	StartLine int
	// Side is "RIGHT" for the new side, "LEFT" for the old.
	Side string
	// Resolved and Outdated are why a thread might be hidden by default: a
	// resolved thread is settled, an outdated one refers to code that has since
	// changed.
	Resolved bool
	Outdated bool
	Comments []ThreadComment
}

const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      reviewThreads(first:100){
        nodes{
          id isResolved isOutdated path line startLine diffSide
          comments(first:50){ nodes{ id body author{ login } } }
        }
      }
    }
  }
}`

// repoOwnerName resolves which repository the client is acting on, from the
// directory it runs gh in (see Client.dir — anything acting on a review has to set
// it, or the answer comes from wherever the process was started). GraphQL takes
// owner and name explicitly, unlike the REST wrappers where gh infers them.
func (c *Client) repoOwnerName() (string, string, error) {
	out, err := c.runner.Run(context.Background(), c.dir, "gh", "repo", "view", "--json", "owner,name")
	if err != nil {
		return "", "", fmt.Errorf("gh repo view: %w: %s", err, out)
	}
	var repo struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &repo); err != nil {
		return "", "", fmt.Errorf("parse gh repo view: %w", err)
	}
	if repo.Owner.Login == "" || repo.Name == "" {
		return "", "", fmt.Errorf("gh repo view: missing owner or name")
	}
	return repo.Owner.Login, repo.Name, nil
}

// FetchReviewThreads returns a PR's review threads.
func (c *Client) FetchReviewThreads(num int) ([]ReviewThread, error) {
	owner, name, err := c.repoOwnerName()
	if err != nil {
		return nil, err
	}
	raw, err := c.runner.Run(
		context.Background(), c.dir,
		"gh", "api", "graphql",
		"-f", "query="+reviewThreadsQuery,
		"-F", "owner="+owner,
		"-F", "name="+name,
		"-F", "number="+strconv.Itoa(num),
	)
	if err != nil {
		return nil, fmt.Errorf("gh api graphql review threads for %d: %w: %s", num, err, raw)
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							IsOutdated bool   `json:"isOutdated"`
							Path       string `json:"path"`
							Line       int    `json:"line"`
							StartLine  int    `json:"startLine"`
							DiffSide   string `json:"diffSide"`
							Comments   struct {
								Nodes []struct {
									ID     string `json:"id"`
									Body   string `json:"body"`
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("parse review threads for %d: %w", num, err)
	}
	nodes := resp.Data.Repository.PullRequest.ReviewThreads.Nodes
	out := make([]ReviewThread, 0, len(nodes))
	for _, n := range nodes {
		t := ReviewThread{
			ID: n.ID, Path: n.Path, Line: n.Line, StartLine: n.StartLine,
			Side: n.DiffSide, Resolved: n.IsResolved, Outdated: n.IsOutdated,
		}
		for _, cm := range n.Comments.Nodes {
			// Empty bodies carry no signal, same reasoning as FetchPRComments.
			if strings.TrimSpace(cm.Body) == "" {
				continue
			}
			t.Comments = append(t.Comments, ThreadComment{ID: cm.ID, Author: cm.Author.Login, Body: cm.Body})
		}
		if len(t.Comments) == 0 {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

const resolveThreadMutation = `mutation($id:ID!){
  resolveReviewThread(input:{threadId:$id}){ thread{ id isResolved } }
}`

const unresolveThreadMutation = `mutation($id:ID!){
  unresolveReviewThread(input:{threadId:$id}){ thread{ id isResolved } }
}`

// ResolveReviewThread marks a thread resolved. Reversible via
// UnresolveReviewThread, which is why this needs no confirmation step.
func (c *Client) ResolveReviewThread(threadID string) error {
	return c.mutateThread(resolveThreadMutation, threadID)
}

// UnresolveReviewThread reopens a resolved thread.
func (c *Client) UnresolveReviewThread(threadID string) error {
	return c.mutateThread(unresolveThreadMutation, threadID)
}

func (c *Client) mutateThread(mutation, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("review thread id is required")
	}
	raw, err := c.runner.Run(
		context.Background(), c.dir,
		"gh", "api", "graphql",
		"-f", "query="+mutation,
		"-F", "id="+threadID,
	)
	if err != nil {
		return fmt.Errorf("gh api graphql resolve thread: %w: %s", err, raw)
	}
	return nil
}
