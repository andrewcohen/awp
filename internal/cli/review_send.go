package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/andrewcohen/awp/internal/review"
)

// Handing a review comment to the agent.
//
// This is the round trip tuicr structurally cannot close: it has no idea an
// agent exists, so a finding got from "noticed while reading" to "being fixed"
// via a human copying text between panes.
//
// The prompt deliberately reuses the propose-then-approve shape already
// established for PR-repair prompts (see the `p r` flow): the agent reports the
// problem and its proposed fix for approval *before* changing anything. A review
// comment is a judgement call, and an agent that silently rewrites code in
// response to one removes the reviewer from the loop the comment was meant to
// open.

// commentPromptFor renders the prompt sent to a workspace's agent for one
// comment.
func commentPromptFor(c review.Comment) string {
	var b strings.Builder
	b.WriteString("A reviewer left a comment on your change.\n\n")
	fmt.Fprintf(&b, "File: %s\n", c.Anchor.Path)
	if c.Anchor.LineHint > 0 {
		side := "new"
		if c.Anchor.Side == review.SideOld {
			side = "old (removed line)"
		}
		fmt.Fprintf(&b, "Line: %d (%s side)\n", c.Anchor.LineHint, side)
	}
	if strings.TrimSpace(c.Anchor.Text) != "" {
		fmt.Fprintf(&b, "The line reads: %s\n", strings.TrimSpace(c.Anchor.Text))
	}
	b.WriteString("\nSurrounding context:\n")
	b.WriteString(renderAnchorContext(c.Anchor))
	b.WriteString("\nThe comment:\n")
	for _, l := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}

	// How to reply is part of the prompt, not left to the agent's judgement —
	// otherwise the response scrolls past in the agent pane and the reviewer
	// never sees it.
	b.WriteString(`
Before changing anything: read the code around that line, decide whether you
agree, and reply with (a) your understanding of the problem and (b) the fix you
propose — or why you think no change is warranted. Wait for approval.

Once approved, make the change, then record your reply on the comment so the
reviewer sees it in the diff:

    awp review add --file `)
	b.WriteString(c.Anchor.Path)
	if c.Anchor.LineHint > 0 {
		fmt.Fprintf(&b, " --line %d", c.Anchor.LineHint)
	}
	b.WriteString(" --author agent --body \"<your reply>\"\n")
	return b.String()
}

// renderAnchorContext renders the anchored line with its neighbours, numbered,
// and marks the anchored line with ">".
//
// Numbering rather than indentation alone, because indentation cannot say *which*
// line is meant when the anchored line is blank — and a comment on a blank line
// ("add a test here") is perfectly ordinary. The marker is drawn whether or not
// the line has text, so the position is always explicit.
func renderAnchorContext(a review.Anchor) string {
	first := a.LineHint - len(a.ContextBefore)
	if first < 1 {
		first = 1
	}
	// Width the largest number needs, so the gutter lines up.
	last := first + len(a.ContextBefore) + len(a.ContextAfter)
	width := len(strconv.Itoa(last))

	var b strings.Builder
	n := first
	write := func(marker, text string) {
		fmt.Fprintf(&b, "  %*d %s %s\n", width, n, marker, text)
		n++
	}
	for _, l := range a.ContextBefore {
		write("|", l)
	}
	write(">", a.Anchor())
	for _, l := range a.ContextAfter {
		write("|", l)
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
