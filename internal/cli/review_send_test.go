package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/review"
)

func sampleComment() review.Comment {
	return review.Comment{
		// A stored comment always carries an id; the prompt needs it to offer a
		// reply command.
		ID:     "1700000000-human",
		Author: review.AuthorHuman,
		Body:   "this drops the error",
		State:  review.Open,
		Anchor: review.Anchor{
			Path:          "internal/cli/deck.go",
			Side:          review.SideNew,
			LineHint:      42,
			Text:          "\t_ = doThing()",
			ContextBefore: []string{"func run() {"},
			ContextAfter:  []string{"}"},
		},
	}
}

// The prompt must be approval-gated: a review comment is a judgement call, and
// an agent that silently rewrites code in response removes the reviewer from the
// loop the comment was meant to open.
// Sending moves the comment out of the open state so the deck's count stops
// counting it — but not to addressed, which is inferred from the code changing
// rather than taken on the agent's word.
func TestMarkCommentSentSetsSentNotAddressed(t *testing.T) {
	store := review.Store{Root: t.TempDir()}
	r, err := store.Open("/repo/proj", review.Target{Kind: review.TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	c, err := store.AddComment(r, sampleComment())
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := markCommentSent(store, r, c); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	got, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].State != review.Sent {
		t.Fatalf("expected state %q, got %+v", review.Sent, got)
	}
	if review.OpenCount(got) != 0 {
		t.Fatal("a sent comment should no longer count as awaiting triage")
	}
}

// sendPromptToAgent reports progress on every path, so a caller with no progress
// UI passing nil used to panic on the nil interface — taking the deck down
// instead of sending the comment. Absorbed in the function rather than trusting
// every call site to remember.
func TestSendPromptToAgentToleratesANilReporter(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a nil reporter must not panic, got %v", r)
		}
	}()
	// An empty workspace name fails early, which is enough to prove the reporter
	// guard runs before anything dereferences it.
	err := sendPromptToAgent(nil, nil, deckui.Item{}, "a prompt", nil)
	if err == nil {
		t.Fatal("expected an error for a workspace-less item")
	}
}

// And an empty prompt is rejected before any tmux work.
func TestSendPromptToAgentRejectsEmptyPrompt(t *testing.T) {
	if err := sendPromptToAgent(nil, nil, deckui.Item{WorkspaceName: "ws"}, "   ", nil); err == nil {
		t.Fatal("expected an empty prompt to be rejected")
	}
}

// A comment on a blank line is ordinary ("add a test here"), and the prompt has
// to say which line is meant. Indentation alone cannot: with no text to show, the
// anchored line would render as nothing and read as a comment on the line above.
// An anchor with no surrounding context still marks its line.
// Numbering must not run below line 1 when the anchor sits near the top.
// A comment beside a very long line must not paste that whole line into the
// prompt — a one-line remark should not become a multi-kilobyte message.
// Truncation is display-only: the stored anchor keeps its full text, because that
// is what relocation matches on.
// The numbered block already marks the anchored line, so the separate
// "The line reads:" line was duplicated noise.

// The envelope is a pointer, not a transcript: address, remark, and the two rules
// that matter. Everything else was noise the agent does not need.
func TestCommentPromptIsAPointerNotAPaste(t *testing.T) {
	c := sampleComment()
	got := commentPromptFor(c, "abcdef")

	for _, want := range []string{
		"internal/cli/deck.go:42",
		"at abcdef",
		"id ",
		"this drops the error",
		"Read the file yourself",
		"awp review reply --to",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
	// The surrounding code must not be pasted — that is what made a comment beside
	// a long line produce a multi-kilobyte message.
	if strings.Contains(got, "func run() {") {
		t.Fatalf("expected no pasted context:\n%s", got)
	}
	if len(got) > 600 {
		t.Fatalf("expected a compact envelope, got %d bytes:\n%s", len(got), got)
	}
}

// A long anchored line cannot blow up the envelope either.
func TestCommentPromptStaysSmallForALongLine(t *testing.T) {
	c := sampleComment()
	c.Anchor.Text = strings.Repeat("x", 5000)
	if got := commentPromptFor(c, ""); len(got) > 600 {
		t.Fatalf("expected the envelope bounded, got %d bytes", len(got))
	}
}

// Changing code must be gated on approval, and a removed line must say so — that
// is the difference between commenting on code and on its deletion.
//
// Both branches have to be there. The gate is about changing code, not about
// replying: an agent told only "propose and stop" treats a question as something
// to propose an answer to and then waits for a yes nobody knew to give.
func TestCommentPromptKeepsTheApprovalGateAndSide(t *testing.T) {
	got := commentPromptFor(sampleComment(), "")
	for _, want := range []string{
		"Reply before changing anything",
		"--proposal",
		"then stop",
		// The branch that does not wait, in whatever words: an answer is just a reply.
		"needs no approval",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the prompt does not say %q:\n%s", want, got)
		}
	}
	// The old wording told the agent to wait for something it had no way to
	// observe, which is either a burned turn polling or an ignored instruction.
	if strings.Contains(got, "wait for approval") {
		t.Errorf("the prompt still tells the agent to wait:\n%s", got)
	}
	c := sampleComment()
	c.Anchor.Side = review.SideOld
	if got := commentPromptFor(c, ""); !strings.Contains(got, "removed line") {
		t.Fatalf("expected the removed side spelled out:\n%s", got)
	}
}

// With no id there is nothing to thread against, so the prompt must not print a
// reply command that cannot work.
func TestCommentPromptWithoutAnIDOmitsTheReplyCommand(t *testing.T) {
	c := sampleComment()
	c.ID = ""
	got := commentPromptFor(c, "")
	if strings.Contains(got, "awp review reply") {
		t.Fatalf("expected no reply command without an id:\n%s", got)
	}
	if !strings.Contains(got, "Reply before changing anything") {
		t.Fatalf("expected the gate to survive:\n%s", got)
	}
}

// A remark about a block has to say so, or the agent reads a comment about five
// lines as a comment about the first of them.
func TestCommentPromptNamesTheRange(t *testing.T) {
	c := sampleComment()
	c.Anchor.EndLineHint = c.Anchor.LineHint + 4
	got := commentPromptFor(c, "")
	want := fmt.Sprintf("%s:%d-%d", c.Anchor.Path, c.Anchor.LineHint, c.Anchor.EndLineHint)
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q in the prompt, got:\n%s", want, got)
	}
}

// GitHub names a comment by its last line, with a start above it; ours names the
// first, with an end below. The translation has to be the right way round or a
// range publishes upside down.
func TestPublishTranslatesARangeToGitHubsShape(t *testing.T) {
	a := review.Anchor{Path: "a.go", LineHint: 12, EndLineHint: 18}
	if got := commentEndLine(a); got != 18 {
		t.Fatalf("expected GitHub's line to be the range's end, got %d", got)
	}
	if got := rangeStartLine(a); got != 12 {
		t.Fatalf("expected start_line to be the range's first line, got %d", got)
	}
	one := review.Anchor{Path: "a.go", LineHint: 12}
	if got := commentEndLine(one); got != 12 {
		t.Fatalf("expected a single-line comment to send its own line, got %d", got)
	}
	if got := rangeStartLine(one); got != 0 {
		t.Fatalf("expected no start_line for a single-line comment, got %d", got)
	}
}

// An approved proposal renders the approval prompt, not the finding prompt. Same
// funnel, branching on the record: the viewer hands the approved comment to the
// one sink it already has, and what makes the message different is a fact the
// record carries.
func TestAnApprovedProposalGetsTheApprovalPrompt(t *testing.T) {
	c := review.Comment{
		ID: "p1", Author: "agent", Body: "wrap it in m.fail and return early",
		ReplyTo: "f1", Proposal: review.ProposalApproved,
		Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 12},
	}
	got := commentPromptFor(c, "andrew/proposals")

	// The gate is open, and saying so is the whole message.
	if !strings.Contains(got, "Go ahead") {
		t.Errorf("the approval prompt does not open the gate:\n%s", got)
	}
	// It must not repeat the instruction that stopped the agent in the first place.
	if strings.Contains(got, "before changing anything") {
		t.Errorf("the approval prompt still tells the agent to wait:\n%s", got)
	}
	// The proposal is echoed: the agent may be reading this on a fresh turn with
	// none of the context it wrote there.
	if !strings.Contains(got, "wrap it in m.fail") {
		t.Errorf("the approval prompt does not say what was approved:\n%s", got)
	}
	// And it names the finding to answer, so the exchange closes where it opened.
	if !strings.Contains(got, "f1") {
		t.Errorf("the approval prompt does not name the review comment:\n%s", got)
	}
}

// A pending proposal is not an approval. Only the approved state changes the
// message, or an agent that proposed something would read its own offer back as
// permission to act on it.
func TestAPendingProposalDoesNotGetTheApprovalPrompt(t *testing.T) {
	c := review.Comment{
		ID: "p1", Author: "agent", Body: "wrap it", ReplyTo: "f1",
		Proposal: review.ProposalPending,
		Anchor:   review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 12},
	}
	if got := commentPromptFor(c, ""); strings.Contains(got, "Go ahead") {
		t.Errorf("a pending proposal reads as approved:\n%s", got)
	}
}
