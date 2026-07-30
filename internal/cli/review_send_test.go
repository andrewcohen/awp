package cli

import (
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

// Replying must be gated on approval, and a removed line must say so — that is
// the difference between commenting on code and on its deletion.
func TestCommentPromptKeepsTheApprovalGateAndSide(t *testing.T) {
	got := commentPromptFor(sampleComment(), "")
	if !strings.Contains(got, "Reply before changing anything") || !strings.Contains(got, "wait for approval") && !strings.Contains(got, "Then wait for approval") {
		t.Fatalf("expected the approval gate:\n%s", got)
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
