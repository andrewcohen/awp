package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/review"
)

// settleModel is a viewer holding one local conversation — a finding of yours and
// the agent's reply to it — with the cursor parked on the finding.
func settleModel(t *testing.T, calls *[][2]any) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	finding := commentOn("a.go", 1, "alpha", "this loop is quadratic")
	reply := review.Comment{
		ID: "c2", Author: "claude", Body: "agreed, rewriting", State: review.Open,
		ReplyTo: finding.ID, Anchor: finding.Anchor,
	}
	m.SetComments([]review.Comment{finding, reply})
	m.SettleThread = func(id string, settled bool) error {
		*calls = append(*calls, [2]any{id, settled})
		return nil
	}
	m.focus = FocusHunks
	m.seekToComment(0)
	return m
}

// TestRSettlesALocalConversation is the gesture that was missing: the one kind of
// thread awp fully owns was the one you could not close from the keyboard, because
// R only knew how to resolve GitHub's.
func TestRSettlesALocalConversation(t *testing.T) {
	var calls [][2]any
	m := settleModel(t, &calls)

	m = press(m, "R")
	if len(calls) != 1 {
		t.Fatalf("the store was asked %d times: %v", len(calls), calls)
	}
	if got := calls[0]; got[0] != "c1" || got[1] != true {
		t.Errorf("settled %v, want the conversation's root c1 → true", got)
	}
	if !strings.Contains(m.status, "settled") {
		t.Errorf("status %q does not say what happened", m.status)
	}
	// And it reads as settled on this frame rather than after the next refresh tick:
	// a keystroke whose effect arrives seconds later reads as one that did nothing.
	for _, c := range m.Comments() {
		if c.ID == "c1" && c.State != review.Settled {
			t.Errorf("the root is %q locally, want %q", c.State, review.Settled)
		}
	}
}

// TestASettledConversationFoldsOutOfTheWay, the way a resolved GitHub thread
// does. Settling means the same thing about a conversation — dealt with, no longer
// what you are reading the diff for — so it earns the same amount of screen: one
// marker line per message instead of a full card.
func TestASettledConversationFoldsOutOfTheWay(t *testing.T) {
	var calls [][2]any
	m := settleModel(t, &calls)
	before := localRowsFor(m, "c1", "c2")
	if before < 4 {
		t.Fatalf("the fixture's conversation is only %d rows, so folding it proves nothing", before)
	}

	m = press(m, "R")
	// One row for the whole conversation, replies included — the same shape a folded
	// mirrored thread takes, rather than one marker per message.
	if got := localRowsFor(m, "c1", "c2"); got != 1 {
		t.Fatalf("a settled conversation takes %d rows, want 1 (was %d open)", got, before)
	}

	// And enter opens it again, or R would hide a conversation for good.
	m.seekToComment(0)
	m = pressKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := localRowsFor(m, "c1", "c2"); got != before {
		t.Errorf("enter left it at %d rows, want the %d it had open", got, before)
	}
}

// localRowsFor counts the stream rows our own comments occupy — commentRowsFor is
// for mirrored threads and prefixes the id GitHub's copies carry.
func localRowsFor(m Model, ids ...string) int {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	n := 0
	for _, r := range m.stream.rows {
		if !isCommentRow(r.kind) || r.comment < 0 || r.comment >= len(m.stream.comments) {
			continue
		}
		if want[m.stream.comments[r.comment].ID] {
			n++
		}
	}
	return n
}

// TestRAgainReopensIt. Settling is the reviewer's assertion, so it has to be
// retractable — you settle a conversation, then notice the fix is wrong.
func TestRAgainReopensIt(t *testing.T) {
	var calls [][2]any
	m := settleModel(t, &calls)

	m = press(m, "R")
	m = press(m, "R")
	if len(calls) != 2 {
		t.Fatalf("the store was asked %d times: %v", len(calls), calls)
	}
	if calls[1][1] != false {
		t.Errorf("the second R sent %v, want a reopen", calls[1])
	}
	if !strings.Contains(m.status, "reopened") {
		t.Errorf("status %q does not say it reopened", m.status)
	}
}

// TestSettlingFromAReplyActsOnTheConversation. Resolving a mirrored thread works
// from any of its messages, and a local conversation is settled by its root's
// state — so the cursor sitting on a reply must not settle the reply.
func TestSettlingFromAReplyActsOnTheConversation(t *testing.T) {
	var calls [][2]any
	m := settleModel(t, &calls)
	m.seekToComment(1) // the agent's reply

	m = press(m, "R")
	if len(calls) != 1 || calls[0][0] != "c1" {
		t.Fatalf("settled %v from a reply, want the root c1", calls)
	}
}

// TestSettlingSaysWhenItFailed rather than leaving a conversation reading as
// closed when the store refused to write it.
func TestSettlingSaysWhenItFailed(t *testing.T) {
	var calls [][2]any
	m := settleModel(t, &calls)
	m.SettleThread = func(string, bool) error { return errors.New("review.json is read-only") }

	m = press(m, "R")
	if !m.statusErr || !strings.Contains(m.status, "read-only") {
		t.Errorf("status %q (err=%v) does not carry the reason", m.status, m.statusErr)
	}
	for _, c := range m.Comments() {
		if c.ID == "c1" && c.State == review.Settled {
			t.Error("a refused settle was recorded locally anyway")
		}
	}
}

// TestSettlingIsUnavailableWithNoStore. Every seam is optional, and the rule for a
// nil one is to say so rather than appear to work.
func TestSettlingIsUnavailableWithNoStore(t *testing.T) {
	var calls [][2]any
	m := settleModel(t, &calls)
	m.SettleThread = nil

	m = press(m, "R")
	if !strings.Contains(m.status, "unavailable") {
		t.Errorf("status %q does not say the key has nowhere to write", m.status)
	}
}
