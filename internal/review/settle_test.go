package review

import (
	"path/filepath"
	"strings"
	"testing"
)

func settleStore(t *testing.T) (Store, Review) {
	t.Helper()
	root := t.TempDir()
	store := Store{Root: filepath.Join(root, "reviews")}
	r, err := store.Open(filepath.Join(root, "repo"), Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return store, r
}

// TestSettlingAConversationStopsItCounting is the whole point of the state: a
// remark you are done with must stop asking for attention, and the deck's badge
// counts Open roots.
func TestSettlingAConversationStopsItCounting(t *testing.T) {
	store, r := settleStore(t)
	finding, err := store.AddComment(r, Comment{
		Author: AuthorHuman, Body: "this loop is quadratic", State: Open,
		Anchor: Anchor{Path: "a.go", Side: SideNew, LineHint: 12},
	})
	if err != nil {
		t.Fatalf("seed the finding: %v", err)
	}
	reply, err := store.Reply(r, finding.ID, Comment{Author: "claude", Body: "rewriting", State: Open})
	if err != nil {
		t.Fatalf("seed the reply: %v", err)
	}

	// From the reply's id, because the viewer settles whichever message the cursor
	// is on and a conversation is settled as a whole.
	if err := store.Settle(r, reply.ID, true); err != nil {
		t.Fatalf("settle: %v", err)
	}
	after, err := store.Comments(r)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if n := OpenCount(after); n != 0 {
		t.Errorf("a settled conversation still counts %d finding(s)", n)
	}
	for _, c := range after {
		switch c.ID {
		case finding.ID:
			if c.State != Settled {
				t.Errorf("the root is %q, want %q", c.State, Settled)
			}
		case reply.ID:
			// The reply keeps its own state: OpenCount counts roots, so writing over a
			// reply would erase what that reply recorded and buy nothing.
			if c.State == Settled {
				t.Error("the reply was overwritten as settled")
			}
		}
	}

	// And it comes back, because settling is the reviewer's assertion rather than a
	// fact about the code.
	if err := store.Settle(r, finding.ID, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened, err := store.Comments(r)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if n := OpenCount(reopened); n != 1 {
		t.Errorf("a reopened conversation counts %d findings, want 1", n)
	}
}

// TestSettlingRefusesAMirroredThread. Those are GitHub's records, and resolving
// one is a call to GitHub — silently writing Settled onto our copy would make the
// local store disagree with the PR while telling the reviewer it was closed.
func TestSettlingRefusesAMirroredThread(t *testing.T) {
	store, r := settleStore(t)
	err := store.Settle(r, RemoteThreadID("PRRT_1"), true)
	if err == nil {
		t.Fatal("settling a mirrored thread was accepted")
	}
	if !strings.Contains(err.Error(), "PRRT_1") {
		t.Errorf("error %q does not name the thread", err)
	}
}

// TestRootOfWalksToTheOpeningRemark, and does not spin on a record whose ReplyTo
// points into a cycle — a corrupt file must not hang the surface reading it.
func TestRootOfWalksToTheOpeningRemark(t *testing.T) {
	comments := []Comment{
		{ID: "c1", Body: "the finding"},
		{ID: "c2", ReplyTo: "c1"},
		{ID: "c3", ReplyTo: "c2"},
	}
	if got := RootOf(comments, "c3"); got != "c1" {
		t.Errorf("RootOf(c3) = %q, want c1", got)
	}
	if got := RootOf(comments, "c1"); got != "c1" {
		t.Errorf("a root answers with itself, got %q", got)
	}
	// An id nobody holds is its own root: there is nothing to walk to, and the
	// caller's next step reports it missing.
	if got := RootOf(comments, "gone"); got != "gone" {
		t.Errorf("RootOf(gone) = %q", got)
	}
	cyclic := []Comment{{ID: "a", ReplyTo: "b"}, {ID: "b", ReplyTo: "a"}}
	if got := RootOf(cyclic, "a"); got != "a" && got != "b" {
		t.Errorf("a cycle answered %q rather than stopping", got)
	}
}
