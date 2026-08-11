package deckui

import (
	"errors"
	"strings"
	"testing"
)

// twoRowPanes is a deck with two workspaces and a backend that hosts every
// kind, so a test can open a pane on one row and leave the cursor on another.
func twoRowPanes(t *testing.T, backend *fakePanes) Model {
	t.Helper()
	m := New([]Item{
		{ProjectName: "proj", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/tmp"},
		{ProjectName: "proj", WorkspaceName: "two", Path: "/tmp", RepoRoot: "/tmp"},
	}, func(ActionRequest) error { return nil }).WithPaneBackend(backend)
	m.width, m.height = 120, 40
	return m
}

// pressL is the key under test, through Update so the binding is exercised
// rather than the helper it dispatches to.
func pressL(m Model) (Model, bool) {
	updated, _ := m.Update(runeKey("L"))
	got := updated.(Model)
	p, ok := got.active.(*panePopover)
	if ok {
		defer p.close(&got)
	}
	return got, ok
}

// leave closes an open pane the way ctrl+\ does, so the deck is back on the row
// list with the pane recorded behind it.
func leave(t *testing.T, m Model) Model {
	t.Helper()
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane open to leave; status %q", m.status)
	}
	p.close(&m)
	m.active = nil
	return m
}

// TestLGoesBackToThePaneYouLeft. This is the whole point of the key: you left
// the agent to look something up on the deck, and L is the way back without
// finding the row again.
func TestLGoesBackToThePaneYouLeft(t *testing.T) {
	backend := allKinds()
	var tmux []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { tmux = append(tmux, r.Action); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = leave(t, next.(Model))

	backend.kinds = nil
	m, opened := pressL(m)
	if !opened {
		t.Fatalf("L opened no pane; status %q", m.status)
	}
	if len(backend.kinds) != 1 || backend.kinds[0] != "agent" {
		t.Errorf("the backend was asked for %v, want one agent pane", backend.kinds)
	}
	if len(tmux) != 0 {
		// `tmux switch-client -l` from a deck that hosts its own panes exits 0
		// having done nothing, so reaching tmux is a silent no-op.
		t.Errorf("L reached the tmux handler: %v", tmux)
	}
}

// TestLGoesBackToTheRowThePaneWasOn, not to the row the cursor is on. The two
// differ exactly when the key is worth having.
func TestLGoesBackToTheRowThePaneWasOn(t *testing.T) {
	backend := allKinds()
	m := twoRowPanes(t, backend)
	m.cursor = 1
	next, _ := m.trigger(ActionOpenWindow, "editor")
	m = leave(t, next.(Model))

	m.cursor = 0
	backend.kinds = nil
	m, opened := pressL(m)
	if !opened {
		t.Fatalf("L opened no pane; status %q", m.status)
	}
	if len(backend.kinds) != 1 || backend.kinds[0] != "editor" {
		t.Errorf("the backend was asked for %v, want the editor pane back", backend.kinds)
	}
	// And the cursor comes with it: leaving the pane again has to land on the row
	// the pane was, or L and ctrl+\ disagree about where you are.
	if m.cursor != 1 {
		t.Errorf("cursor is on row %d, want the row the pane was on (1)", m.cursor)
	}
}

// TestLSaysThereIsNoPaneYet rather than opening the selected row's shell. It is
// a key about where you have been, and before you have been anywhere it has no
// answer — one it has to give out loud, since a key that does nothing reads as
// broken.
func TestLSaysThereIsNoPaneYet(t *testing.T) {
	var tmux []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { tmux = append(tmux, r.Action); return nil }).
		WithPaneBackend(allKinds())
	m.width, m.height = 120, 40

	m, opened := pressL(m)
	if opened {
		t.Fatal("L opened a pane before one had ever been opened")
	}
	if !strings.Contains(m.status, "no pane to go back to") {
		t.Errorf("status %q, want it to say there is nowhere to go back to", m.status)
	}
	if len(tmux) != 0 {
		t.Errorf("L fell through to tmux: %v", tmux)
	}
}

// TestAFailedOpenIsNotSomewhereToGoBackTo. The pane never came up, so there is
// no "back" to it — recording the attempt would make L reopen a program the
// user never saw.
func TestAFailedOpenIsNotSomewhereToGoBackTo(t *testing.T) {
	backend := allKinds()
	backend.err = errors.New("no such session")
	m := twoRowPanes(t, backend)

	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	if _, open := m.active.(*panePopover); open {
		t.Fatal("a failed open still put a pane on screen")
	}

	m, opened := pressL(m)
	if opened {
		t.Fatal("L reopened a pane that failed to open")
	}
	if !strings.Contains(m.status, "no pane to go back to") {
		t.Errorf("status %q, want it to say there is nowhere to go back to", m.status)
	}
}

// TestLRefusesAWorkspaceThatIsGone. The pane's row is resolved when the key is
// pressed rather than captured at open time, so a workspace deleted in between
// is a refusal — where a stored Item would open a program in a directory that is
// no longer there.
func TestLRefusesAWorkspaceThatIsGone(t *testing.T) {
	backend := allKinds()
	m := twoRowPanes(t, backend)
	m.cursor = 1
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = leave(t, next.(Model))

	// The refresh that lands after the delete.
	m.itemsAll = []Item{{ProjectName: "proj", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/tmp"}}
	m.cursor = 0

	backend.kinds = nil
	m, opened := pressL(m)
	if opened {
		t.Fatal("L opened a pane for a workspace that is no longer on the deck")
	}
	if len(backend.kinds) != 0 {
		t.Errorf("the backend was asked for %v for a workspace that is gone", backend.kinds)
	}
	if !strings.Contains(m.status, "not on the deck any more") {
		t.Errorf("status %q, want it to name the missing workspace", m.status)
	}
}

// TestLGoesBackToAPaneWhoseRowTheScopeHasDropped. An agent that exits leaves the
// attention scope, and the pane is still the one you were in — refusing to go
// back to it because of the scope would make L depend on which list you happen
// to be looking at.
func TestLGoesBackToAPaneWhoseRowTheScopeHasDropped(t *testing.T) {
	backend := allKinds()
	m := twoRowPanes(t, backend)
	m.cursor = 1
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = leave(t, next.(Model))

	// A filter is the cheapest way to say "the row is not in the list you are
	// looking at"; the scope filters do the same thing to the same lookup.
	m.filter = "one"
	m.cursor = 0
	backend.kinds = nil
	m, opened := pressL(m)
	if !opened {
		t.Fatalf("L would not go back to a row the list is not showing; status %q", m.status)
	}
	if len(backend.kinds) != 1 || backend.kinds[0] != "agent" {
		t.Errorf("the backend was asked for %v, want the agent pane back", backend.kinds)
	}
}

// TestWithNoBackendLIsStillTmuxs. awp deck has no pane host, and L there means
// `tmux switch-client -l` — the deck must not swallow the key on its way.
func TestWithNoBackendLIsStillTmuxs(t *testing.T) {
	var tmux []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { tmux = append(tmux, r.Action); return nil })
	m.width, m.height = 120, 40

	updated, cmd := m.Update(runeKey("L"))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("L scheduled nothing on a tmux deck; status %q", got.status)
	}
	execCmd(t, cmd)
	if len(tmux) != 1 || tmux[0] != ActionLastSession {
		t.Errorf("the tmux handler saw %v, want one ActionLastSession", tmux)
	}
}
