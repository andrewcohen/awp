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

const replyToThreadMutation = `mutation($threadId:ID!,$body:String!){
  addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$threadId,body:$body}){
    comment{ id }
  }
}`

// ReplyToReviewThread posts a message into an existing thread, returning the new
// comment's node id.
//
// Its own mutation because a reply is not a new thread. addPullRequestReview
// creates threads only, so sending a reply that way would put it on the PR as a
// fresh top-level remark divorced from the question it answers.
//
// Posted on its own rather than staged into a pending review, which the input's
// optional pullRequestReviewId would allow. Two reasons, both about what a reply
// is: it needs no verdict — answering a question is not submitting a review, and
// requiring one would make "reply" the most ceremonious thing in the viewer — and
// it is what GitHub's own Reply button does, so the conversation reads the same
// way from either end.
//
// The id comes back because it is how a local record recognises its own echo:
// the mirror will report this same id when it next reads the thread, and without
// it the reply would be drawn twice — once as our record, once as GitHub's.
func (c *Client) ReplyToReviewThread(threadID, body string) (string, error) {
	if strings.TrimSpace(threadID) == "" {
		return "", fmt.Errorf("review thread id is required")
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("reply body is empty")
	}
	// Through the JSON request path, not gh's -f flags: a reply is prose the
	// reviewer typed, and putting it in argv is where the backtick-escaping problem
	// in review bodies came from (see Client.graphql).
	var resp struct {
		Data struct {
			AddPullRequestReviewThreadReply struct {
				Comment *struct {
					ID string `json:"id"`
				} `json:"comment"`
			} `json:"addPullRequestReviewThreadReply"`
		} `json:"data"`
	}
	vars := map[string]any{"threadId": threadID, "body": body}
	if err := c.graphql(replyToThreadMutation, vars, &resp); err != nil {
		return "", fmt.Errorf("replying to the thread: %w", err)
	}
	comment := resp.Data.AddPullRequestReviewThreadReply.Comment
	if comment == nil {
		// GraphQL reported no error and no comment. Treated as a failure rather than
		// as a success with an unknown id: unlike the REST posts, where an
		// unreadable id means the comment definitely landed, here there is nothing
		// to say it did — and reporting success would mark the draft published and
		// lose it.
		return "", fmt.Errorf("replying to the thread: GitHub returned no comment")
	}
	return comment.ID, nil
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
	return c.setThreadResolved(resolveThreadMutation, threadID, true)
}

// UnresolveReviewThread reopens a resolved thread.
func (c *Client) UnresolveReviewThread(threadID string) error {
	return c.setThreadResolved(unresolveThreadMutation, threadID, false)
}

// setThreadResolved runs one of the resolution mutations and confirms GitHub agrees
// about the state afterwards.
//
// Both halves of that are the fix for one bug with an unusually quiet failure. This
// used to run the mutation and look at nothing: not the GraphQL errors array — which
// is where GraphQL reports refusals, and which `gh api graphql` does not always exit
// non-zero for — and not the state it asked for. A refused resolve was therefore
// reported as success, the caller wrote `resolved: true` into the local mirror, and
// the diff hides resolved threads by default. So the conversation disappeared, the
// mirror said GitHub had resolved it, and GitHub said it had not.
//
// Through Client.graphql so there is one path that reads a GraphQL response, rather
// than a second one that skips the part where GitHub says no. The returned state is
// then checked against what was asked: "GitHub accepted the call" and "the thread is
// now resolved" are different claims, and only the second is safe to mirror.
func (c *Client) setThreadResolved(mutation, threadID string, want bool) error {
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("review thread id is required")
	}
	// One shape for both mutations: whichever ran, the other field is absent.
	type threadResult struct {
		Thread *struct {
			ID         string `json:"id"`
			IsResolved bool   `json:"isResolved"`
		} `json:"thread"`
	}
	var resp struct {
		Data struct {
			Resolve   *threadResult `json:"resolveReviewThread"`
			Unresolve *threadResult `json:"unresolveReviewThread"`
		} `json:"data"`
	}
	verb := "resolving"
	if !want {
		verb = "reopening"
	}
	if err := c.graphql(mutation, map[string]any{"id": threadID}, &resp); err != nil {
		return fmt.Errorf("%s the thread: %w", verb, err)
	}
	result := resp.Data.Resolve
	if result == nil {
		result = resp.Data.Unresolve
	}
	if result == nil || result.Thread == nil {
		return fmt.Errorf("%s the thread: GitHub returned no thread", verb)
	}
	if result.Thread.IsResolved != want {
		// Accepted and did not do it. Reported rather than mirrored: a mirror that
		// claims a thread is resolved when GitHub disagrees hides the conversation.
		return fmt.Errorf("%s the thread: GitHub still reports it as %s", verb,
			map[bool]string{true: "resolved", false: "unresolved"}[result.Thread.IsResolved])
	}
	return nil
}
