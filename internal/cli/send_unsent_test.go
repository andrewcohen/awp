package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/review"
)

// seedReview writes comments into a fresh review and returns the item that
// addresses it.
//
// tempRoot, not t.TempDir: the store lives under ~/.awp/reviews/<basename of the
// repo root>/, and a temp dir's basename is a counter — so two tests both get
// "001" and share one review. Written against the developer's own home, at that.
// tempRoot points HOME at a temp dir, which makes the isolation real rather than
// dependent on the two tests picking different numbers.
func seedReview(t *testing.T, cs ...review.Comment) (deckui.Item, review.Store, review.Review) {
	t.Helper()
	root := tempRoot(t)
	store := review.Store{}
	r, err := store.Open(root, review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open the review: %v", err)
	}
	for _, c := range cs {
		if _, err := store.AddComment(r, c); err != nil {
			t.Fatalf("seed %q: %v", c.Body, err)
		}
	}
	return deckui.Item{RepoRoot: root, WorkspaceName: "ws", Path: root}, store, r
}

func humanRemark(body string) review.Comment {
	return review.Comment{
		Author: review.AuthorHuman, Body: body, State: review.Open,
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 12},
	}
}

// TestSendingUnsentHandsOverOneMessageForTheWholeSet.
//
// One message, because a prompt reaches an agent as a paste and a return: three
// sends is three turns started milliseconds apart, each reading files the others
// are rewriting. The reviewer wrote a set of remarks about one change and that is
// what the agent should be given.
func TestSendingUnsentHandsOverOneMessageForTheWholeSet(t *testing.T) {
	item, store, r := seedReview(t,
		humanRemark("this loop is quadratic"),
		humanRemark("name says list, returns a map"),
	)

	var sent []string
	n, err := sendUnsentToAgentFor(func(_ deckui.Item, text string, _ deckui.Reporter) error {
		sent = append(sent, text)
		return nil
	})(item)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if n != 2 {
		t.Errorf("reported %d remarks sent, want 2", n)
	}
	if len(sent) != 1 {
		t.Fatalf("the agent got %d messages, want 1 for the set", len(sent))
	}
	for _, want := range []string{"this loop is quadratic", "name says list, returns a map"} {
		if !strings.Contains(sent[0], want) {
			t.Errorf("the message does not carry %q:\n%s", want, sent[0])
		}
	}

	// And they stop reading as waiting, or the deck goes on counting remarks the
	// agent is already working on.
	after, err := store.Comments(r)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	for _, c := range after {
		if c.State != review.Sent {
			t.Errorf("%s is %q after the send, want %q", c.ID, c.State, review.Sent)
		}
	}
}

// TestSendingUnsentLeavesTheAgentsOwnFindingsAlone. They are Open and Local like
// yours — Open means awaiting triage, and it is awaiting *yours*. Sending them back
// would answer a question with itself, and would look like the key working.
func TestSendingUnsentLeavesTheAgentsOwnFindingsAlone(t *testing.T) {
	agentFinding := humanRemark("the base is resolved twice")
	agentFinding.Author = "claude"
	item, store, r := seedReview(t, humanRemark("this loop is quadratic"), agentFinding)

	var sent []string
	n, err := sendUnsentToAgentFor(func(_ deckui.Item, text string, _ deckui.Reporter) error {
		sent = append(sent, text)
		return nil
	})(item)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if n != 1 {
		t.Errorf("reported %d remarks sent, want 1 — the agent's own finding is not one of them", n)
	}
	if len(sent) != 1 || strings.Contains(sent[0], "the base is resolved twice") {
		t.Errorf("the agent was sent its own finding back:\n%s", strings.Join(sent, "\n"))
	}

	after, err := store.Comments(r)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	for _, c := range after {
		if c.ByRobot() && c.State != review.Open {
			t.Errorf("the agent's finding moved to %q; it is still awaiting your triage", c.State)
		}
	}
}

// TestSendingNothingSendsNothing. Pressing the key with nothing waiting is a
// question, and zero is the answer: no message, no error, and nothing said to an
// agent that would then have to work out what was being asked of it.
func TestSendingNothingSendsNothing(t *testing.T) {
	already := humanRemark("said this already")
	already.State = review.Sent
	item, _, _ := seedReview(t, already)

	calls := 0
	n, err := sendUnsentToAgentFor(func(deckui.Item, string, deckui.Reporter) error {
		calls++
		return nil
	})(item)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if n != 0 || calls != 0 {
		t.Errorf("sent %d remarks in %d messages with nothing waiting", n, calls)
	}
}

// TestOneUnsentRemarkGetsTheOrdinaryPrompt. The single-comment prompt is the shape
// the agent has seen since this flow existed; wrapping the commonest case in batch
// framing would make it read as the unusual one.
func TestOneUnsentRemarkGetsTheOrdinaryPrompt(t *testing.T) {
	c := humanRemark("this loop is quadratic")
	c.ID = "c1"
	if got, want := unsentPromptFor([]review.Comment{c}, "wip: thing"), commentPromptFor(c, "wip: thing"); got != want {
		t.Errorf("a set of one rendered differently from the comment itself:\n%s\n---\n%s", got, want)
	}
}

// TestTheBatchPromptSaysWhereEachRemarkIs. A remark the agent cannot locate is a
// remark it will guess at, and the id is what lets it reply on the right thread
// rather than filing a second comment beside it.
func TestTheBatchPromptSaysWhereEachRemarkIs(t *testing.T) {
	first := humanRemark("this loop is quadratic")
	first.ID = "c1"
	second := humanRemark("name says list, returns a map")
	second.ID = "c2"
	second.Anchor = review.Anchor{Path: "b.go", Side: review.SideNew, LineHint: 40}

	got := unsentPromptFor([]review.Comment{first, second}, "wip: thing")
	for _, want := range []string{
		"2 review comments", "wip: thing",
		"a.go:12", "c1", "b.go:40", "c2",
		"awp review reply --to", "--proposal",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the batch prompt is missing %q:\n%s", want, got)
		}
	}
}
