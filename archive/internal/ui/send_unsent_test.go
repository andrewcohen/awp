package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ctrlS is the key as the program receives it. The compose box binds the same one,
// which is the point of it — one verb — so both halves of that are pinned here.
func ctrlS() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
}

// TestCtrlSHandsOverEverythingWaiting. The count comes from the store, and the
// viewer's job is to say it: a reviewer who commented their way down a file wants
// one gesture at the end, and needs to be told how much of it went.
func TestCtrlSHandsOverEverythingWaiting(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	asked := 0
	m.SendUnsent = func() (int, error) { asked++; return 3, nil }

	m = pressKey(m, ctrlS())
	if asked != 1 {
		t.Fatalf("the store was asked %d times", asked)
	}
	if !strings.Contains(m.status, "3 remarks") {
		t.Errorf("status %q does not say how many went", m.status)
	}
	if m.statusErr {
		t.Error("a send that worked was reported as a failure")
	}
}

// TestCtrlSWithNothingWaitingIsNotAFailure. Pressing it is a question — is any of
// this still with me? — and zero is the answer. Reported as an error it would put
// the review in the failure log for a key that had nothing to do.
func TestCtrlSWithNothingWaitingIsNotAFailure(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SendUnsent = func() (int, error) { return 0, nil }

	m = pressKey(m, ctrlS())
	if m.statusErr {
		t.Errorf("nothing to send was reported as a failure: %q", m.status)
	}
	if !strings.Contains(m.status, "nothing to send") {
		t.Errorf("status %q does not say the queue was empty", m.status)
	}
}

// TestCtrlSSaysWhenItFailed, rather than leaving the reviewer to guess whether
// their remarks are with the agent. Through fail(), so it also reaches the log.
func TestCtrlSSaysWhenItFailed(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SendUnsent = func() (int, error) { return 0, errors.New("no agent running for qa — press a to start one") }

	m = pressKey(m, ctrlS())
	if !m.statusErr || !strings.Contains(m.status, "no agent running") {
		t.Errorf("status %q (err=%v) does not carry the reason", m.status, m.statusErr)
	}
}

// TestCtrlSIsUnavailableWithNoStore. Every seam here is optional — standalone
// `awp diff` in a directory with no review wires none of them — and the rule for a
// nil one is to say so rather than appear to work.
func TestCtrlSIsUnavailableWithNoStore(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SendUnsent = nil

	m = pressKey(m, ctrlS())
	if !strings.Contains(m.status, "unavailable") {
		t.Errorf("status %q does not say the key has nowhere to send", m.status)
	}
}

// TestTheComposeBoxKeepsCtrlSForItself is what makes one key safe for both
// meanings. With the box open ctrl+s is "save and send this remark"; the batch send
// must not also fire, or a comment still being typed would be handed over twice —
// once as itself and once as part of a set it is not in yet.
func TestTheComposeBoxKeepsCtrlSForItself(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	batch := 0
	m.SendUnsent = func() (int, error) { batch++; return 1, nil }
	m.focus = FocusHunks
	m = press(m, "c")
	if !m.Filtering() {
		t.Fatal("expected the compose box to own the keyboard")
	}

	m = pressKey(m, ctrlS())
	if batch != 0 {
		t.Errorf("the batch send fired %d times from inside the compose box", batch)
	}
}

// TestCtrlSIsInTheHelp. viewerKeyGroups is the canonical binding surface, so a key
// bound without a row there is a key nobody will find.
func TestCtrlSIsInTheHelp(t *testing.T) {
	sends := 0
	for _, g := range viewerKeyGroups(nil) {
		for _, row := range g.Keys {
			if row[0] == "ctrl+s" {
				sends++
			}
		}
	}
	// Two: the compose box's "save and send this", and the review-wide "send what
	// is waiting". Both are real and they are different, so both need saying.
	if sends != 2 {
		t.Errorf("found %d ctrl+s help rows, want 2 (in the compose box, and review-wide)", sends)
	}
}

func pressKey(m Model, k tea.KeyPressMsg) Model {
	updated, _ := m.Update(k)
	return updated.(Model)
}
