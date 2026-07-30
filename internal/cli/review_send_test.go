package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/review"
)

func sampleComment() review.Comment {
	return review.Comment{
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

func TestCommentPromptCarriesLocationAndBody(t *testing.T) {
	got := commentPromptFor(sampleComment())
	for _, want := range []string{
		"internal/cli/deck.go",
		"Line: 42",
		"new side",
		"this drops the error",
		"_ = doThing()",
		"func run() {",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

// The prompt must be approval-gated: a review comment is a judgement call, and
// an agent that silently rewrites code in response removes the reviewer from the
// loop the comment was meant to open.
func TestCommentPromptIsApprovalGated(t *testing.T) {
	got := commentPromptFor(sampleComment())
	for _, want := range []string{"Before changing anything", "Wait for approval"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt is not approval-gated, missing %q:\n%s", want, got)
		}
	}
	// It must also say how to reply, or the response scrolls past in the agent
	// pane and the reviewer never sees it.
	if !strings.Contains(got, "awp review add") {
		t.Fatalf("prompt does not tell the agent how to reply:\n%s", got)
	}
	if !strings.Contains(got, "--author agent") {
		t.Fatalf("reply instruction should attribute the reply to the agent:\n%s", got)
	}
}

func TestCommentPromptMarksRemovedLineSide(t *testing.T) {
	c := sampleComment()
	c.Anchor.Side = review.SideOld
	got := commentPromptFor(c)
	if !strings.Contains(got, "old (removed line)") {
		t.Fatalf("expected the old side to be spelled out:\n%s", got)
	}
}

func TestCommentPromptSurvivesAThinAnchor(t *testing.T) {
	c := review.Comment{Body: "why?", Anchor: review.Anchor{Path: "a.go"}}
	got := commentPromptFor(c)
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "why?") {
		t.Fatalf("expected path and body with no line or context:\n%s", got)
	}
	if strings.Contains(got, "Line: 0") {
		t.Fatalf("a missing line should be omitted, not printed as zero:\n%s", got)
	}
}

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
