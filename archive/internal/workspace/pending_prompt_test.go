package workspace

import (
	"bytes"
	"io"
	"testing"
)

func pendingPromptService(t *testing.T) (*service, *fakeStore) {
	t.Helper()
	repoRoot := t.TempDir()
	store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: repoRoot + "/qa"}}}
	return NewService(Dependencies{
		JJ:    &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"qa": true}},
		Tmux:  &fakeTmux{windows: map[string]bool{}},
		Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard,
	}), store
}

// A parked prompt is delivered once. Two things can race to start the agent —
// the pane you opened and a retry behind it — and an agent that receives the
// same instruction twice acts on it twice.
func TestAParkedPromptIsTakenOnlyOnce(t *testing.T) {
	svc, _ := pendingPromptService(t)
	if err := svc.RecordPendingPrompt("qa", PendingPrompt{Text: "fix the tests"}); err != nil {
		t.Fatalf("RecordPendingPrompt: %v", err)
	}
	got, err := svc.TakePendingPrompt("qa")
	if err != nil {
		t.Fatalf("TakePendingPrompt: %v", err)
	}
	if got.Text != "fix the tests" {
		t.Fatalf("TakePendingPrompt = %q, want %q", got.Text, "fix the tests")
	}
	again, err := svc.TakePendingPrompt("qa")
	if err != nil {
		t.Fatalf("TakePendingPrompt (second): %v", err)
	}
	if !again.Empty() {
		t.Fatalf("the prompt was delivered twice: %q", again.Text)
	}
}

// The review flag survives the round trip. It is the whole difference between
// a reviewer and a coding agent, and it is not recoverable from the prompt
// text — losing it means the agent that reads someone else's PR also gets told
// to work in units, run gates and commit.
func TestAParkedReviewPromptStaysAReviewPrompt(t *testing.T) {
	svc, _ := pendingPromptService(t)
	if err := svc.RecordPendingPrompt("qa", PendingPrompt{Text: "review PR 12", Review: true}); err != nil {
		t.Fatalf("RecordPendingPrompt: %v", err)
	}
	got, err := svc.TakePendingPrompt("qa")
	if err != nil {
		t.Fatalf("TakePendingPrompt: %v", err)
	}
	if !got.Review {
		t.Fatal("the review flag did not survive parking")
	}
}

// Clearing a prompt clears its flavor too. A stale review flag would hand the
// next parked prompt a role it never asked for.
func TestClearingAPromptClearsItsFlavor(t *testing.T) {
	svc, store := pendingPromptService(t)
	if err := svc.RecordPendingPrompt("qa", PendingPrompt{Text: "review PR 12", Review: true}); err != nil {
		t.Fatalf("RecordPendingPrompt: %v", err)
	}
	if err := svc.RecordPendingPrompt("qa", PendingPrompt{}); err != nil {
		t.Fatalf("RecordPendingPrompt (clear): %v", err)
	}
	if store.entries["qa"].PendingPromptIsReview {
		t.Error("clearing the prompt left the review flag set")
	}
}

// Taking nothing must not write. Every agent pane open asks this question and
// the answer is almost always "no prompt waiting"; rewriting the state file to
// store a field it did not change is a write per keystroke-ish action for
// nothing.
func TestTakingNoPromptWritesNothing(t *testing.T) {
	svc, store := pendingPromptService(t)
	before := store.saves
	if got, err := svc.TakePendingPrompt("qa"); err != nil || !got.Empty() {
		t.Fatalf("TakePendingPrompt = %q, %v; want empty and no error", got.Text, err)
	}
	if store.saves != before {
		t.Fatalf("reading an empty prompt wrote to the store (%d → %d saves)", before, store.saves)
	}
}

// A prompt waiting for an agent is not a prompt an agent is working on. They
// were nearly the same field, and merging them would have made a parked
// prompt show up on the row as though something were already acting on it.
func TestAParkedPromptIsNotTheActiveOne(t *testing.T) {
	svc, store := pendingPromptService(t)
	if err := svc.RecordPendingPrompt("qa", PendingPrompt{Text: "fix the tests"}); err != nil {
		t.Fatalf("RecordPendingPrompt: %v", err)
	}
	if got := store.entries["qa"].ActivePrompt; got != "" {
		t.Fatalf("parking a prompt set ActivePrompt to %q", got)
	}
	if err := svc.UpdatePrompt("qa", "something else"); err != nil {
		t.Fatalf("UpdatePrompt: %v", err)
	}
	if got := store.entries["qa"].PendingPrompt; got != "fix the tests" {
		t.Fatalf("the active prompt overwrote the parked one; PendingPrompt = %q", got)
	}
}
