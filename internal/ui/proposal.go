package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/review"
)

// Approving an agent's proposal.
//
// The prompt an agent gets with a finding tells it to reply before changing
// anything and then stop. `A` is the other end of that: it records the yes and
// tells the agent to go ahead, so the gate the prompt sets up is answered in the
// place the reviewer is already standing rather than in the agent's tmux window.
//
// Two halves, and both are reported. The record is the durable one — an agent
// that missed the nudge finds the approval in `awp review list` — so it is
// written first, and a send that fails afterwards is a message the reviewer can
// repeat, not a lost approval.

// proposalAtCursor is the pending proposal the cursor is in, if any.
//
// The whole conversation answers, not only the row holding the proposal: `A` on
// the finding, on the proposal, or on any reply beneath means the same thing, the
// way `c` and `R` already act on the conversation under the cursor. A reviewer
// reading a two-message exchange should not have to find which of its rows the
// key belongs to.
func (m Model) proposalAtCursor() (review.Comment, bool) {
	c, ok := m.localCommentAtCursor()
	if !ok {
		return review.Comment{}, false
	}
	// The conversation's own id: a proposal is a reply, so from the parent's row we
	// have to look down, and from a reply's row the sibling proposal is reached
	// through the parent.
	top := c.ID
	if c.ReplyTo != "" {
		top = c.ReplyTo
	}
	if c.AwaitingApproval() {
		return c, true
	}
	for _, own := range m.comments {
		if own.AwaitingApproval() && (own.ID == top || own.ReplyTo == top) {
			return own, true
		}
	}
	return review.Comment{}, false
}

// approveAtCursor says yes to the proposal under the cursor and tells the agent.
func (m Model) approveAtCursor() (tea.Model, tea.Cmd) {
	if m.ApproveProposal == nil {
		m.status = "approving unavailable here"
		return m, nil
	}
	c, ok := m.proposalAtCursor()
	if !ok {
		// Worded as what is missing rather than as what you did wrong: the common
		// way to land here is a conversation whose proposal you already approved.
		m.status = "nothing awaiting approval here"
		return m, nil
	}
	approved, err := m.ApproveProposal(c.ID)
	if err != nil {
		m.fail("approve: %v", err)
		return m, nil
	}
	// Reflect it locally rather than waiting for the refresh tick to bring it back.
	// The tick is a couple of seconds, and a key whose effect appears later reads as
	// a key that did nothing — which is how you end up pressing it twice.
	for i := range m.comments {
		if m.comments[i].ID == approved.ID {
			m.comments[i] = approved
		}
		// The store moved the finding to sent when it took the approval; mirroring
		// that here keeps this copy from disagreeing with disk until the next load.
		if m.comments[i].ID == approved.ReplyTo && m.comments[i].State != review.Published {
			m.comments[i].State = review.Sent
		}
	}
	m.rebuildStream()

	// Both halves get said. The approval is on disk either way, and a send that did
	// not go is the reviewer's cue to go and poke the agent — the failure mode #133
	// was about is a status line that reports the half that worked and stays silent
	// about the half that did not.
	//
	// The wording is the compose box's, verbatim past the first word: "comment
	// saved" has these same three variants (see saveComment), and inventing a
	// parallel set for approving made the same event read as two different ones
	// depending on which key produced it.
	if m.SendComment == nil {
		m.status = "approved (sending unavailable here)"
		return m, nil
	}
	if err := m.SendComment(approved); err != nil {
		m.fail("approved, send failed: %v", err)
		return m, nil
	}
	m.status = "approved and sent to the agent"
	return m, nil
}
