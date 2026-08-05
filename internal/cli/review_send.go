package cli

import (
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/review"
)

// Handing a review comment to the agent.
//
// This is the round trip a standalone review TUI structurally cannot close: it
// has no idea an agent exists, so a finding got from "noticed while reading" to
// "being fixed" via a human copying text between panes.
//
// The prompt deliberately reuses the propose-then-approve shape already
// established for PR-repair prompts (see the `p r` flow): the agent reports the
// problem and its proposed fix for approval *before* changing anything. A review
// comment is a judgement call, and an agent that silently rewrites code in
// response to one removes the reviewer from the loop the comment was meant to
// open.

// commentPromptFor renders the prompt sent to a workspace's agent for one
// comment. revision names the change under review, empty when unresolved.
//
// Deliberately terse, and deliberately a *location* rather than a transcript.
// Two earlier versions were worse: one pasted the surrounding code, which is
// redundant (the agent can read the file, and is sitting in the workspace that
// holds it) and occasionally enormous — a comment beside a long README table row
// turned a one-line remark into a multi-kilobyte message. The other wrapped it in
// explanatory prose the agent does not need. What is left is the address, the
// remark, and the two rules that matter: read it yourself, reply before changing.
func commentPromptFor(c review.Comment, revision string) string {
	var b strings.Builder

	// "a.go:12", "a.go:12-18", "a.go", "the whole change" — one spelling of a
	// location, shared with the compose header, the comment index and the publish
	// log (see review.Anchor.Where). A remark about a block has to say so, or the
	// agent reads a comment about five lines as a comment about the first of them.
	where := c.Anchor.Where()
	if c.Anchor.Side == review.SideOld {
		where += " (removed line, old side)"
	}
	fmt.Fprintf(&b, "Review comment on %s\n", where)
	if strings.TrimSpace(revision) != "" {
		fmt.Fprintf(&b, "at %s\n", strings.TrimSpace(revision))
	}
	if c.ID != "" {
		fmt.Fprintf(&b, "id %s\n", c.ID)
	}

	b.WriteString("\n")
	for _, l := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
		b.WriteString(l + "\n")
	}

	b.WriteString("\nRead the file yourself; this is a pointer, not a paste.\n")
	if c.ID != "" {
		fmt.Fprintf(&b, "Reply before changing anything:\n  awp review reply --to %s --body \"...\"\n", c.ID)
		b.WriteString("Then wait for approval.\n")
	} else {
		b.WriteString("Reply before changing anything, then wait for approval.\n")
	}
	return b.String()
}

// markCommentSent records that a comment has been handed to the agent, so the
// deck's finding count stops counting it as awaiting triage.
//
// It does not mark it addressed: that is inferred later from the anchored code
// changing (phase 2's re-anchoring), rather than taken on the agent's word.
func markCommentSent(store review.Store, r review.Review, c review.Comment) error {
	c.State = review.Sent
	return store.UpdateComment(r, c)
}
