package deckui

import (
	"strings"
	"testing"
)

// TestNothingActsOnARowThatIsStillBeingCreated is the invariant blockIfSettingUp
// exists for, checked against every action rather than the ones someone
// remembered.
//
// The bug it pins: the pane interception landed in trigger *ahead* of the guard,
// so a pane backend got to act on a row whose workspace did not exist yet. That
// is not the harmless no-op the tmux path would have been. The session name is
// derived from the project and workspace, which the optimistic row does have —
// so the pane opens a program under the name the real workspace will use, in
// whatever directory is left when Path is empty. Nothing ever reaps it, and the
// agent is in the wrong tree permanently.
//
// It surfaced on the review flow because `r` parks the reviewer's brief at the
// end of its job: an agent started before then also comes up with no brief, and
// the parked one is never delivered because an agent already exists.
func TestNothingActsOnARowThatIsStillBeingCreated(t *testing.T) {
	for _, a := range AllActions() {
		var handled []Action
		backend := allKinds()
		// Optimistic rows carry a name and a repo but no working copy — that is
		// what "the create job hasn't finished" looks like on screen.
		m := New([]Item{{
			ProjectName:   "proj",
			WorkspaceName: "pr-1234-branch",
			RepoRoot:      "/tmp/repo",
			Status:        "starting",
			Optimistic:    true,
		}}, func(r ActionRequest) error { handled = append(handled, r.Action); return nil }).
			WithPaneBackend(backend)
		m.width, m.height = 120, 40

		next, _ := m.trigger(a, "agent")
		got := next.(Model)

		if _, open := got.active.(*panePopover); open {
			t.Errorf("action %v opened a pane on a row that is still being created", a)
		}
		if backend.opened != "" {
			t.Errorf("action %v asked the backend for a %q pane", a, backend.opened)
		}
		if len(handled) > 0 {
			t.Errorf("action %v reached the tmux handler as %v", a, handled)
		}
		if !strings.Contains(got.status, "still being created") {
			t.Errorf("action %v left status %q, want it to say the workspace is still being created", a, got.status)
		}
	}
}

// TestTheGuardDoesNotBlockARealRow is the control. A guard that blocked
// everything would pass the test above and break the deck.
func TestTheGuardDoesNotBlockARealRow(t *testing.T) {
	backend := allKinds()
	m := paneModel(t, backend)
	next, _ := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("a settled row opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
}
