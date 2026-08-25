package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/andrewcohen/awp/internal/review"
)

// Handing a review comment to the agent.
//
// This is the round trip a standalone review TUI structurally cannot close: it
// has no idea an agent exists, so a finding got from "noticed while reading" to
// "being fixed" via a human copying text between panes.
//
// **By default the agent just makes the change.** A review comment is an
// instruction, and the round trip this file exists to close is "noticed while
// reading" → "fixed"; a gate in the middle of it is a second keypress for the
// reviewer on every remark, which is the copying-between-panes cost in another
// form.
//
// It used to gate on approval, reusing the propose-then-approve shape of the
// PR-repair prompts (the `p r` flow), on the argument that a comment is a
// judgement call and an agent that silently rewrites code in response to one
// removes the reviewer from the loop the comment opened. That argument holds for
// remarks whose answer is genuinely uncertain and not for the ordinary ones, and
// the gate could not tell them apart — so it charged the uncertain case's price on
// all of them. The reviewer is still in the loop either way: the change arrives as
// a diff they are already reading.
//
// AWP_REVIEW_APPROVAL=1 requires the gate back — see requireProposalApproval. The
// machinery is untouched by the default: an agent may still send `--proposal` when
// it wants a yes, and `A` in the viewer still approves one. What the default
// changes is only whether the prompt *demands* one.

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
	reply := ""
	if c.ID != "" {
		reply = fmt.Sprintf("  awp review reply --to %s --body-file <path>\n", c.ID)
	}
	b.WriteString(replyRuleFor(reply))
	return b.String()
}

// replyRuleFor is the closing instruction of a finding prompt: what the agent is
// told to do about the remark, and how to reply.
//
// reply is the indented command line to show, or "" for none — which is what a
// comment with no id gets, since there is nothing to address the reply to and a
// literal `--to <id>` would be an instruction the agent cannot follow. The caller
// supplies it rather than an id because the batched prompt wants the `<id>`
// placeholder (its remarks each carry their own) while a single idless comment
// wants no command at all.
//
// One function because the single-comment and batched prompts say the same rules,
// and they were two copies of a string that has now changed twice.
//
// --body-file rather than --body throughout: a reply is exactly the long markdown
// body #95 was about, and a backtick put through a shell argument arrives mangled
// in a way nobody notices until it is on someone's PR.
func replyRuleFor(reply string) string {
	// The lead-in ends in a colon when a command follows it and a full stop when
	// none does, so an idless prompt does not trail a colon into nothing.
	lead := func(sentence string) string {
		if reply == "" {
			return sentence + ".\n"
		}
		return sentence + ":\n" + reply
	}
	if !requireProposalApproval() {
		// Reply *after*, and say what you did. The reply is still wanted — it is what
		// closes the thread the remark opened, and what the reviewer reads next to the
		// diff — but it is a report rather than a request.
		return lead("Make the change, then reply saying what you did") +
			"If the right answer is unclear, or the remark is a question, reply and ask\n" +
			"instead of guessing. Add --proposal to any reply you want a yes on before\n" +
			"acting, and stop there.\n"
	}
	// Two branches below, named by what the reply *is* rather than by what you did,
	// so an agent reading this literally gets the right answer either way.
	//
	// "Then stop" rather than the old "wait for approval": waiting was not
	// observable from here, so an agent told to wait either burned a turn polling
	// for something with no channel or ignored the instruction. It is told now —
	// approving sends it a prompt of its own — and `awp review list` is where it
	// confirms.
	return lead("Reply before changing anything") + proposalGate
}

// requireProposalApproval reports whether an agent must get a yes before changing
// code in response to a review remark.
//
// Off unless AWP_REVIEW_APPROVAL is set to something truthy. Read per prompt rather
// than cached at startup so flipping it does not need the deck restarted — the deck
// is long-lived and a setting you cannot change without losing your panes is a
// setting nobody tries.
//
// Deliberately an environment variable and not a config field. It is a preference
// about how much rope to give an agent, which is the kind of thing you want to turn
// on for one session while you watch it, and a config field would make that an edit
// and an undo.
func requireProposalApproval() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AWP_REVIEW_APPROVAL"))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
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
	b.WriteString(replyRuleFor("  awp review reply --to <id> --body-file <path>\n"))
	return b.String()
}

// proposalGate is the rule an agent is held to when a finding reaches it *and*
// AWP_REVIEW_APPROVAL is set. The gate is about changing code, not about replying:
// an answer or an explanation is an ordinary reply, and only an offer to change
// something waits for a yes.
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
