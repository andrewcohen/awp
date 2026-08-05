package review

import (
	"strings"
	"testing"
)

// proposalFixture is a finding that has been handed to the agent, plus the
// agent's reply offering to make a change — the state the approval gate exists
// for.
func proposalFixture(t *testing.T) (Store, Review, Comment, Comment) {
	t.Helper()
	s := testStore(t)
	r, err := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws-1"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	finding, err := s.AddComment(r, Comment{
		Author: AuthorHuman, Body: "this drops the error on the floor", State: Sent,
		Anchor: Anchor{Path: "a.go", LineHint: 12, Text: "resolve(id)"},
	})
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	proposal, err := s.Reply(r, finding.ID, Comment{
		Author: "agent", Body: "wrap it in m.fail and return early", Proposal: ProposalPending,
	})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	return s, r, finding, proposal
}

// The empty value is not a proposal. Every record written before this field
// existed has it, so the zero value has to read as "an ordinary remark" rather
// than as an offer nobody made.
func TestAnOrdinaryCommentIsNotAProposal(t *testing.T) {
	var c Comment
	if c.IsProposal() || c.Approved() || c.AwaitingApproval() {
		t.Errorf("the zero comment reads as a proposal: %+v", c)
	}
}

// A proposal survives the round trip through the store — it is a fact the agent
// reads back, so it has to be on disk rather than only in the sender's memory.
func TestAProposalRoundTripsThroughTheStore(t *testing.T) {
	s, r, _, proposal := proposalFixture(t)

	if !proposal.AwaitingApproval() {
		t.Fatalf("the filed proposal is not pending: %q", proposal.Proposal)
	}
	reloaded, err := s.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	var got Comment
	for _, c := range reloaded {
		if c.ID == proposal.ID {
			got = c
		}
	}
	if !got.IsProposal() || !got.AwaitingApproval() {
		t.Errorf("the proposal came back as %q, want pending", got.Proposal)
	}
}

// Approving flips the proposal and hands the exchange back to the agent: the
// finding it answers moves to Sent, the mirror of Reply reopening the parent when
// the agent speaks. Open means it is waiting on you; Sent means it is waiting on
// the agent, and the deck's finding count reads exactly that.
func TestApprovingMovesTheFindingBackToTheAgent(t *testing.T) {
	s, r, finding, proposal := proposalFixture(t)

	approved, err := s.Approve(r, proposal.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !approved.Approved() {
		t.Errorf("approve returned %q, want approved", approved.Proposal)
	}

	reloaded, err := s.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	for _, c := range reloaded {
		switch c.ID {
		case proposal.ID:
			if !c.Approved() {
				t.Errorf("the stored proposal is %q, want approved", c.Proposal)
			}
		case finding.ID:
			if c.State != Sent {
				t.Errorf("the finding is %q, want sent — the exchange is the agent's again", c.State)
			}
		}
	}
	// And the badge stops asking you to triage a question you have answered.
	if n := OpenCount(reloaded); n != 0 {
		t.Errorf("OpenCount is %d after approving, want 0", n)
	}
}

// Replying puts it back on you, approving hands it back to the agent. The two
// have to be the same axis or the count says one thing while the diff shows
// another.
func TestAProposalReopensTheFindingUntilItIsApproved(t *testing.T) {
	s, r, _, proposal := proposalFixture(t)

	reloaded, err := s.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if n := OpenCount(reloaded); n != 1 {
		t.Fatalf("OpenCount is %d with a proposal awaiting an answer, want 1", n)
	}
	if _, err := s.Approve(r, proposal.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

// Approving an ordinary remark is refused rather than written. Filling the field
// in would invent a proposal nobody made, and the record would then say an agent
// offered to do something it never offered.
func TestApprovingSomethingThatIsNotAProposalIsRefused(t *testing.T) {
	s, r, finding, _ := proposalFixture(t)

	if _, err := s.Approve(r, finding.ID); err == nil {
		t.Fatal("approving a plain finding was accepted")
	} else if !strings.Contains(err.Error(), "not a proposal") {
		t.Errorf("the refusal says %q, which does not say why", err)
	}

	reloaded, err := s.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	for _, c := range reloaded {
		if c.ID == finding.ID && c.IsProposal() {
			t.Errorf("the refused approval still wrote %q onto the finding", c.Proposal)
		}
	}
}

// A missing id is an error, not a silent no-op: the caller asked to approve
// something, and nothing having happened is not an answer.
func TestApprovingAnUnknownCommentIsAnError(t *testing.T) {
	s, r, _, _ := proposalFixture(t)

	if _, err := s.Approve(r, "no-such-id"); err == nil {
		t.Fatal("approving an unknown id was accepted")
	}
	if _, err := s.Approve(r, "  "); err == nil {
		t.Fatal("approving a blank id was accepted")
	}
}

// Pressing the key twice means "get on with it", not an error and not a rewrite.
// The caller can send another nudge; the record has nothing new to say.
func TestApprovingTwiceIsNotAnError(t *testing.T) {
	s, r, _, proposal := proposalFixture(t)

	first, err := s.Approve(r, proposal.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	second, err := s.Approve(r, proposal.ID)
	if err != nil {
		t.Fatalf("approve again: %v", err)
	}
	if !second.Approved() {
		t.Errorf("the second approve returned %q", second.Proposal)
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("the second approve rewrote the record: %v then %v", first.UpdatedAt, second.UpdatedAt)
	}
}
