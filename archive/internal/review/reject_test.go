package review

import (
	"testing"
	"time"
)

// A publish that refuses over a bad anchor used to leave nothing behind. The only
// way to clear the block was to delete the finding, which is what someone did on a
// real review — the body went with it and nobody noticed for two days.

func TestRejectedCarriesTheReason(t *testing.T) {
	var c Comment
	if _, ok := c.Rejected(); ok {
		t.Fatal("expected a fresh comment not to be refused")
	}
	c.Reject = &RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(0, 0)}
	reason, ok := c.Rejected()
	if !ok || reason != "line 688 is not in the diff" {
		t.Fatalf("expected the reason back, got %q / %v", reason, ok)
	}
}

// The reason survives a round trip, which is the whole point: it is read on a
// later run, by a reader who was not there when the publish refused.
func TestRejectionSurvivesTheStore(t *testing.T) {
	s := testStore(t)
	r, err := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	saved, err := s.AddComment(r, Comment{
		Author: AuthorHuman, Body: "this leaks",
		Anchor: Anchor{Path: "a.go", Side: SideNew, LineHint: 688, Text: "leak()"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	saved.Reject = &RejectRecord{Reason: "line 688 is not in the diff", At: time.Unix(1700000000, 0)}
	if err := s.UpdateComment(r, saved); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if len(back) != 1 {
		t.Fatalf("expected the finding kept, got %d", len(back))
	}
	reason, ok := back[0].Rejected()
	if !ok || reason != "line 688 is not in the diff" {
		t.Fatalf("expected the reason to survive, got %q / %v", reason, ok)
	}
	if back[0].Body != "this leaks" {
		t.Fatalf("expected the body kept, got %q", back[0].Body)
	}
}

// A refusal is not a position in a comment's life. A finding handed to the agent
// and then refused by a publish is still sent, and still needs triage — so State
// is untouched and the badge goes on counting it.
func TestRejectionLeavesTheLifecycleAlone(t *testing.T) {
	sent := Comment{ID: "1", State: Sent, Reject: &RejectRecord{Reason: "not in the diff"}}
	if sent.State != Sent {
		t.Fatalf("expected the state kept, got %q", sent.State)
	}
	open := Comment{ID: "2", State: Open, Reject: &RejectRecord{Reason: "not in the diff"}}
	if got := OpenCount([]Comment{open}); got != 1 {
		t.Fatalf("expected a refused finding still to want triage, got %d", got)
	}
	// And it is still ours and still unpublished, which is what makes a later run
	// retry it rather than skip it.
	if got := open.Origin(); got != OriginLocal {
		t.Fatalf("expected a refused finding to stay local, got %v", got)
	}
	if open.OnGitHub() {
		t.Fatal("a refused finding is the one thing that is definitely not on GitHub")
	}
}
