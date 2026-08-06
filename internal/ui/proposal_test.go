package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// proposalModel is a finding with an agent's proposal under it, plus recorders
// for the two halves approving does: writing the yes, and telling the agent.
type approvals struct {
	approved []string
	sent     []review.Comment
	failWith error
}

func proposalModel(t *testing.T, state review.Proposal) (Model, *approvals) {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	rec := &approvals{}
	finding := commentOn("a.go", 1, "alpha", "this drops the error")
	finding.State = review.Sent
	proposal := review.Comment{
		ID: "p1", Author: "agent", Body: "wrap it in m.fail and return early",
		State: review.Open, ReplyTo: finding.ID, Proposal: state,
		Anchor: finding.Anchor,
	}
	m.SetComments([]review.Comment{finding, proposal})
	m.ApproveProposal = func(id string) (review.Comment, error) {
		rec.approved = append(rec.approved, id)
		if rec.failWith != nil {
			return review.Comment{}, rec.failWith
		}
		out := proposal
		out.Proposal = review.ProposalApproved
		return out, nil
	}
	m.SendComment = func(c review.Comment) error {
		rec.sent = append(rec.sent, c)
		return nil
	}
	return m, rec
}

// A pending proposal says so where you read it. The agent has stopped, so this is
// the one message in the change asking the reviewer for something, and a reader
// who cannot see that has no reason to press the key.
func TestAPendingProposalSaysItIsWaitingOnYou(t *testing.T) {
	m, _ := proposalModel(t, review.ProposalPending)

	view := stripANSI(m.renderStreamPanel(90, 20))
	if !strings.Contains(view, chipAwaitingYou) {
		t.Errorf("the stream does not say the proposal is waiting:\n%s", view)
	}
}

// And an approved one is told apart from it, or a proposal you already answered
// goes on reading as live.
func TestAnApprovedProposalIsNotStillWaiting(t *testing.T) {
	m, _ := proposalModel(t, review.ProposalApproved)

	view := stripANSI(m.renderStreamPanel(90, 20))
	if strings.Contains(view, chipAwaitingYou) {
		t.Errorf("an approved proposal still reads as waiting:\n%s", view)
	}
	if !strings.Contains(view, chipApproved) {
		t.Errorf("the stream does not say the proposal was approved:\n%s", view)
	}
}

// The index carries it too, on the conversation's row. A proposal is always a
// reply and the index folds replies into a count, so a state left on the message
// that holds it would never be listed at all — and the index is the surface you
// scan to find what is waiting on you.
func TestTheIndexSaysAConversationIsWaitingOnYou(t *testing.T) {
	m, _ := proposalModel(t, review.ProposalPending)

	if len(m.commentIndex) != 1 {
		t.Fatalf("expected one conversation listed, got %d", len(m.commentIndex))
	}
	if got := m.commentIndex[0].proposal; got != review.ProposalPending {
		t.Errorf("the index row carries %q, want pending", got)
	}
	if row := entryLocation(m.commentIndex[0]); !strings.Contains(row, chipAwaitingYou) {
		t.Errorf("the index row does not say it is waiting: %q", row)
	}
}

// A review-level conversation is listed by a different branch of the same
// function, and a chip added to one branch and not the other is a chip that
// silently disappears for one kind of remark.
func TestAReviewLevelProposalIsAlsoListedAsWaiting(t *testing.T) {
	e := commentEntry{changeWide: true, proposal: review.ProposalPending}
	if row := entryLocation(e); !strings.Contains(row, chipAwaitingYou) {
		t.Errorf("a review-level row drops the chip: %q", row)
	}
}

// A settled proposal beside a live one does not settle the exchange, so the row
// reports the one still waiting.
func TestPendingWinsOverApprovedOnOneRow(t *testing.T) {
	for _, tc := range []struct {
		a, b, want review.Proposal
	}{
		{review.ProposalApproved, review.ProposalPending, review.ProposalPending},
		{review.ProposalPending, review.ProposalApproved, review.ProposalPending},
		{"", review.ProposalApproved, review.ProposalApproved},
		{"", "", ""},
	} {
		if got := strongerProposal(tc.a, tc.b); got != tc.want {
			t.Errorf("strongerProposal(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

// A records the yes and tells the agent. Both halves, in that order: the record
// is the durable one, and a send that fails afterwards is a message the reviewer
// can repeat rather than an approval that never happened.
func TestAApprovesAndTellsTheAgent(t *testing.T) {
	m, rec := proposalModel(t, review.ProposalPending)
	m = cursorToComment(t, m)

	m = press(m, "A")

	if len(rec.approved) != 1 || rec.approved[0] != "p1" {
		t.Fatalf("approved %v, want [p1] (status %q)", rec.approved, m.status)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("the agent was told %d times, want 1 (status %q)", len(rec.sent), m.status)
	}
	if !rec.sent[0].Approved() {
		t.Errorf("the agent was handed a record reading %q, want approved", rec.sent[0].Proposal)
	}
	// Both halves in the line the reviewer is left with. A status naming only the
	// half that worked is how a send that never happened went unnoticed before.
	if !strings.Contains(m.status, "approved") || !strings.Contains(m.status, "agent") {
		t.Errorf("the status does not report both halves: %q", m.status)
	}
}

// The key works from anywhere in the conversation. A proposal is a reply, and
// making the reviewer find which row of a two-message exchange the key belongs to
// is not a distinction worth teaching.
func TestAWorksFromTheFindingAsWellAsTheProposal(t *testing.T) {
	m, rec := proposalModel(t, review.ProposalPending)
	// The first comment row is the finding, not the proposal beneath it.
	m = cursorToComment(t, m)
	if c, ok := m.localCommentAtCursor(); !ok || c.ID == "p1" {
		t.Fatalf("precondition: expected the cursor on the finding, got %+v", c)
	}

	m = press(m, "A")

	if len(rec.approved) != 1 || rec.approved[0] != "p1" {
		t.Errorf("A from the finding approved %v, want [p1] (status %q)", rec.approved, m.status)
	}
}

// The change is visible immediately rather than on the next refresh tick. A key
// whose effect appears two seconds later reads as a key that did nothing, which
// is how you end up pressing it twice.
func TestApprovingUpdatesTheViewAtOnce(t *testing.T) {
	m, _ := proposalModel(t, review.ProposalPending)
	m = cursorToComment(t, m)

	m = press(m, "A")

	view := stripANSI(m.renderStreamPanel(90, 20))
	if strings.Contains(view, chipAwaitingYou) {
		t.Errorf("the stream still says the proposal is waiting:\n%s", view)
	}
	if !strings.Contains(view, chipApproved) {
		t.Errorf("the stream does not show the approval:\n%s", view)
	}
}

// Nothing to approve says so. Silence would be indistinguishable from a key that
// is not wired up.
func TestAOnAConversationWithNoProposalSaysSo(t *testing.T) {
	m, rec := proposalModel(t, "")
	m = cursorToComment(t, m)

	m = press(m, "A")

	if len(rec.approved) != 0 {
		t.Errorf("A approved something with no proposal: %v", rec.approved)
	}
	if m.status == "" {
		t.Error("A did nothing and said nothing")
	}
}

// A missing sender is not a failed approval — the yes is on disk, and the agent
// finds it in `awp review list`. It has to say the difference, though, or the
// reviewer walks away thinking the agent was told.
func TestApprovingWithNoAgentToTellStillApproves(t *testing.T) {
	m, rec := proposalModel(t, review.ProposalPending)
	m.SendComment = nil
	m = cursorToComment(t, m)

	m = press(m, "A")

	if len(rec.approved) != 1 {
		t.Fatalf("the approval was skipped for want of an agent: %v", rec.approved)
	}
	if !strings.Contains(m.status, "approved") {
		t.Errorf("the status does not say it was approved: %q", m.status)
	}
	if !strings.Contains(m.status, "agent") {
		t.Errorf("the status does not say the agent was not told: %q", m.status)
	}
}

// A refused approval must not tell the agent to go ahead. The store refuses when
// the record is not a pending proposal, and a nudge sent anyway would be an
// approval the reviewer never gave and the record does not hold.
func TestAFailedApprovalDoesNotTellTheAgent(t *testing.T) {
	m, rec := proposalModel(t, review.ProposalPending)
	rec.failWith = errors.New("no such comment")
	m = cursorToComment(t, m)

	m = press(m, "A")

	if len(rec.sent) != 0 {
		t.Errorf("the agent was told about an approval that failed: %v", rec.sent)
	}
	if !strings.Contains(m.status, "no such comment") {
		t.Errorf("the status does not carry the reason: %q", m.status)
	}
}

// The index is where a pending proposal announces itself, so it has to be
// answerable from there — the same reasoning as R and D.
func TestAApprovesFromTheCommentIndex(t *testing.T) {
	m, rec := proposalModel(t, review.ProposalPending)
	m.focus = FocusComments
	m.seekToComment(0)

	m = press(m, "A")

	if len(rec.approved) != 1 || rec.approved[0] != "p1" {
		t.Errorf("A from the index approved %v, want [p1] (status %q)", rec.approved, m.status)
	}
}

// The help lists it: ? is the only place the viewer's bindings are written down.
func TestTheHelpNamesTheApproveKey(t *testing.T) {
	for _, g := range viewerKeyGroups(nil) {
		for _, k := range g.Keys {
			if k[0] == "A" {
				if !strings.Contains(k[1], "proposal") {
					t.Errorf("A is listed as %q, which does not say what it approves", k[1])
				}
				return
			}
		}
	}
	t.Error("? does not list A")
}
