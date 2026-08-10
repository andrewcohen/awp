package deckui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// With the handover on, nothing is emulated: no popover goes on m.active, because
// the deck is suspended rather than drawing a frame with a pane in it.
func TestTheHandoverOpensNoPopover(t *testing.T) {
	t.Setenv(PaneExecEnv, "1")

	m := paneModel(t, allKinds())
	next, cmd := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)

	if got.active != nil {
		t.Errorf("a %T went on m.active; a handed-over pane has no modal to draw", got.active)
	}
	if cmd == nil {
		t.Fatal("no command was returned, so nothing ever runs")
	}
}

// The emulator stays reachable, because the whole point of the flag is comparing
// the two on the same session.
func TestWithoutTheFlagThePaneIsStillEmulated(t *testing.T) {
	m := paneModel(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)

	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("m.active is %T, want the emulated popover; status %q", got.active, got.status)
	}
	t.Cleanup(func() { p.close(&got) })
}

// A handed-over pane is the whole terminal, so the size that refuses an emulated
// one is no obstacle. The emulated path still has its own chrome to fit.
func TestATinyDeckStillHandsTheTerminalOver(t *testing.T) {
	m := paneModel(t, allKinds())
	m.width, m.height = 10, 4
	if paneFits(m.width, m.height) {
		t.Fatal("this size was supposed to be too small to emulate")
	}

	next, _ := m.trigger(ActionOpenWindow, "agent")
	if got := next.(Model); !strings.Contains(got.status, "too small") {
		t.Errorf("the emulated path did not refuse; status %q", got.status)
	}

	t.Setenv(PaneExecEnv, "1")
	next, cmd := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	if strings.Contains(got.status, "too small") {
		t.Errorf("the handover refused a size it does not care about; status %q", got.status)
	}
	if cmd == nil {
		t.Error("no command was returned")
	}
}

// Coming back from a handed-over pane has to catch the deck up: it was suspended
// the whole time the agent was working, so its rows are as old as the moment you
// left. Same reason closing an emulated pane asks for a fresh read.
func TestReturningFromAHandoverAsksForAFreshRead(t *testing.T) {
	m := paneModel(t, allKinds())
	reads := 0
	m.refresher = func() tea.Cmd { reads++; return func() tea.Msg { return nil } }

	next, _ := m.Update(paneExecDoneMsg{label: "agent"})
	if reads != 1 {
		t.Errorf("returning asked for %d fresh reads, want 1", reads)
	}
	if status := next.(Model).status; status != "" {
		t.Errorf("a clean return set status %q", status)
	}
}

func TestAHandoverThatFailedSaysSo(t *testing.T) {
	m := paneModel(t, allKinds())
	m.refresher = func() tea.Cmd { return nil }

	next, _ := m.Update(paneExecDoneMsg{label: "agent", err: errors.New("zmx: no such session")})
	if status := next.(Model).status; !strings.Contains(status, "no such session") {
		t.Errorf("status is %q, want the reason the handover failed", status)
	}
}
