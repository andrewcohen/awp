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
	if c.Approved() {
		return approvalPromptFor(c)
	}
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
		// --body-file rather than --body: a proposal is exactly the long markdown
		// body #95 was about, and a backtick put through a shell argument arrives
		// mangled in a way nobody notices until it is on someone's PR.
		fmt.Fprintf(&b, "Reply before changing anything:\n  awp review reply --to %s --body-file <path>\n", c.ID)
	} else {
		b.WriteString("Reply before changing anything.\n")
	}
	// Two branches, named by what the reply *is* rather than by what you did, so an
	// agent reading this literally gets the right answer either way.
	//
	// "Then stop" rather than the old "wait for approval": waiting was not
	// observable from here, so an agent told to wait either burned a turn polling
	// for something with no channel or ignored the instruction. It is told now —
	// approving sends it a prompt of its own — and `awp review list` is where it
	// confirms.
	b.WriteString(proposalGate)
	return b.String()
}

// unsentPromptFor renders one prompt covering every remark the reviewer has
// written and not sent — what `ctrl+s` hands over from the viewer.
//
// One message rather than one per comment, and that is the whole reason this
// exists next to commentPromptFor. A prompt arrives at an agent as a paste and a
// return, so N sends is N turns, started within milliseconds of each other,
// racing over the same working copy: the second one reads files the first is
// halfway through rewriting. Batched, the agent sees the review the way the
// reviewer wrote it — a set of remarks about one change — and decides an order.
//
// The address, the remark, and the two rules, per comment, exactly as the
// single-comment prompt says them. The rules are stated once at the end rather
// than repeated under each: they are the same rules, and an agent reading them
// five times has been told nothing extra.
func unsentPromptFor(cs []review.Comment, revision string) string {
	if len(cs) == 1 {
		// Not a list of one. The single-comment prompt is the established shape and
		// the one the agent has seen before; wrapping it in batch framing would make
		// the commonest case read as the unusual one.
		return commentPromptFor(cs[0], revision)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d review comments", len(cs))
	if strings.TrimSpace(revision) != "" {
		fmt.Fprintf(&b, " on %s", strings.TrimSpace(revision))
	}
	b.WriteString("\n")

	for i, c := range cs {
		where := c.Anchor.Where()
		if c.Anchor.Side == review.SideOld {
			where += " (removed line, old side)"
		}
		fmt.Fprintf(&b, "\n%d. %s", i+1, where)
		if c.ID != "" {
			fmt.Fprintf(&b, " — id %s", c.ID)
		}
		b.WriteString("\n")
		for _, l := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
			// Indented under its number, so a remark that runs to several lines cannot
			// be read as the start of the next one.
			b.WriteString("   " + l + "\n")
		}
	}

	b.WriteString("\nRead the files yourself; these are pointers, not pastes.\n")
	b.WriteString("Reply on each before changing anything:\n" +
		"  awp review reply --to <id> --body-file <path>\n")
	b.WriteString(proposalGate)
	return b.String()
}

// proposalGate is the rule an agent is held to when a finding reaches it. The
// gate is about changing code, not about replying: an answer or an explanation is
// an ordinary reply, and only an offer to change something waits for a yes.
const proposalGate = "Add --proposal if the reply is a change you mean to make, then stop.\n" +
	"You get a prompt when it is approved. Answering a question or explaining\n" +
	"what is already there needs no approval: reply and carry on.\n"

// approvalPromptFor is what the agent is told when its proposal is approved.
//
// Rendered from the same funnel as every other comment prompt, branching on the
// record rather than on a second sink: the viewer hands the approved proposal to
// SendComment exactly as it hands over any other comment, and what makes this
// message different is a fact the record already carries.
//
// It echoes the proposal back in full. The agent stopped when it made the offer
// and may well be reading this on a fresh turn with none of the context it wrote
// there — and unlike the surrounding-code paste that had to be cut from the
// finding prompt, this is bounded by construction: it is the agent's own words,
// and it wrote them to be read.
func approvalPromptFor(c review.Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Proposal approved — %s\n", c.Anchor.Where())
	if c.ID != "" {
		fmt.Fprintf(&b, "proposal id %s\n", c.ID)
	}
	if c.ReplyTo != "" {
		fmt.Fprintf(&b, "answering review comment %s\n", c.ReplyTo)
	}

	b.WriteString("\nApproved:\n")
	for _, l := range strings.Split(strings.TrimRight(c.Body, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}

	// Named as the thing to do, because that is the whole content of the message:
	// the gate that stopped it is open.
	b.WriteString("\nGo ahead.")
	if c.ReplyTo != "" {
		fmt.Fprintf(&b, " Reply on the review comment when it is done:\n  awp review reply --to %s --body-file <path>", c.ReplyTo)
	}
	b.WriteString("\n")
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
