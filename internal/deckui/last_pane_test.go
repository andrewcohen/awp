package deckui

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// recordingPanes is a backend that remembers which row's pane it was asked for,
// which is what a test about alternating between two of them has to see.
type recordingPanes struct {
	fakePanes
	opened []string // "workspace/kind", in the order they were opened
}

func (r *recordingPanes) Open(item Item, kind string, w, h int) (*exec.Cmd, func(), error) {
	cmd, restore, err := r.fakePanes.Open(item, kind, w, h)
	if err == nil {
		r.opened = append(r.opened, item.WorkspaceName+"/"+kind)
	}
	return cmd, restore, err
}

// twoRowPanes is a deck with two workspaces and a backend that hosts every kind,
// so a test can open a pane on one row and leave the cursor on another.
func twoRowPanes(t *testing.T, backend PaneBackend) Model {
	t.Helper()
	m := New([]Item{
		{ProjectName: "proj", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/tmp"},
		{ProjectName: "proj", WorkspaceName: "two", Path: "/tmp", RepoRoot: "/tmp"},
	}, func(ActionRequest) error { return nil }).WithPaneBackend(backend)
	m.width, m.height = 120, 40
	return m
}

// pressPaneKey drives a key through Update, so the binding is exercised rather
// than the helper it dispatches to, and reports whether a pane came up.
func pressPaneKey(m Model, msg tea.KeyPressMsg) (Model, bool) {
	updated, _ := m.Update(msg)
	got := updated.(Model)
	p, ok := got.active.(*panePopover)
	if ok {
		defer p.close(&got)
	}
	return got, ok
}

// resumeKey is ctrl+\, which leaves a pane and — from the row list — goes back
// into one.
func resumeKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl} }

func pressResume(m Model) (Model, bool) { return pressPaneKey(m, resumeKey()) }
func pressL(m Model) (Model, bool)      { return pressPaneKey(m, runeKey("L")) }

// leave closes an open pane the way ctrl+\ does, so the deck is back on the row
// list with the pane remembered behind it.
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

// openOn opens the given kind on the named row and leaves it again, which is the
// unit of history both keys read.
func openOn(t *testing.T, m Model, workspace, kind string) Model {
	t.Helper()
	for i, it := range m.items() {
		if it.WorkspaceName == workspace {
			m.cursor = i
			next, _ := m.trigger(ActionOpenWindow, kind)
			return leave(t, next.(Model))
		}
	}
	t.Fatalf("no row called %q", workspace)
	return m
}

// TestResumeGoesBackIntoThePaneYouLeft. ctrl+\ leaves a pane, so from the row
// list it is the way back in — out to check something, back to carry on, one key.
func TestResumeGoesBackIntoThePaneYouLeft(t *testing.T) {
	backend := &recordingPanes{fakePanes: *allKinds()}
	var tmux []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { tmux = append(tmux, r.Action); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = leave(t, next.(Model))

	backend.opened = nil
	m, opened := pressResume(m)
	if !opened {
		t.Fatalf("ctrl+\\ opened no pane; status %q", m.status)
	}
	if len(backend.opened) != 1 || backend.opened[0] != "ws/agent" {
		t.Errorf("the backend was asked for %v, want one ws/agent pane", backend.opened)
	}
	if len(tmux) != 0 {
		t.Errorf("ctrl+\\ reached the tmux handler: %v", tmux)
	}
}

// TestResumeGoesBackToTheRowThePaneWasOn, not to the row the cursor is on. The
// two differ exactly when the key is worth having.
func TestResumeGoesBackToTheRowThePaneWasOn(t *testing.T) {
	backend := &recordingPanes{fakePanes: *allKinds()}
	m := twoRowPanes(t, backend)
	m = openOn(t, m, "two", "editor")

	m.cursor = 0
	backend.opened = nil
	m, opened := pressResume(m)
	if !opened {
		t.Fatalf("ctrl+\\ opened no pane; status %q", m.status)
	}
	if len(backend.opened) != 1 || backend.opened[0] != "two/editor" {
		t.Errorf("the backend was asked for %v, want the editor pane on two", backend.opened)
	}
	// The cursor comes with it: leaving the pane again has to land on the row the
	// pane was, or the keys and ctrl+\ disagree about where you are.
	if m.cursor != 1 {
		t.Errorf("cursor is on row %d, want the row the pane was on (1)", m.cursor)
	}
}

// TestLAlternatesBetweenThePanesYouveBeenIn. This is what `tmux switch-client -l`
// is for: the two most recent things you were in are one keypress apart, and
// pressing it twice puts you back. A single slot could only ever offer the pane
// you just had, which is ctrl+\'s job.
func TestLAlternatesBetweenThePanesYouveBeenIn(t *testing.T) {
	backend := &recordingPanes{fakePanes: *allKinds()}
	m := twoRowPanes(t, backend)
	m = openOn(t, m, "one", "agent")
	m = openOn(t, m, "two", "agent")

	backend.opened = nil
	m, opened := pressL(m)
	if !opened {
		t.Fatalf("L opened no pane; status %q", m.status)
	}
	m = leave(t, m)
	if _, ok := pressL(m); !ok {
		t.Fatalf("the second L opened no pane; status %q", m.status)
	}
	if want := []string{"one/agent", "two/agent"}; strings.Join(backend.opened, ",") != strings.Join(want, ",") {
		t.Errorf("L opened %v, want it to alternate %v", backend.opened, want)
	}
}

// TestResumingDoesNotEraseTheAlternate. Re-entering the pane you are already on
// must not push it into the previous slot, or ctrl+\ would erase what L exists to
// reach — and holding one pane open would erase the memory of every other.
func TestResumingDoesNotEraseTheAlternate(t *testing.T) {
	backend := &recordingPanes{fakePanes: *allKinds()}
	m := twoRowPanes(t, backend)
	m = openOn(t, m, "one", "agent")
	m = openOn(t, m, "two", "agent")

	m, ok := pressResume(m)
	if !ok {
		t.Fatalf("ctrl+\\ opened no pane; status %q", m.status)
	}
	m = leave(t, m)

	backend.opened = nil
	m, ok = pressL(m)
	if !ok {
		t.Fatalf("L opened no pane; status %q", m.status)
	}
	if len(backend.opened) != 1 || backend.opened[0] != "one/agent" {
		t.Errorf("L opened %v after a resume, want the alternate (one/agent) intact", backend.opened)
	}
}

// TestLSaysThereIsOnlyOnePaneSoFar. One pane deep there is nothing to alternate
// with, and the useful thing to say is which key does have somewhere to go.
func TestLSaysThereIsOnlyOnePaneSoFar(t *testing.T) {
	m := twoRowPanes(t, &recordingPanes{fakePanes: *allKinds()})
	m = openOn(t, m, "one", "agent")

	m, opened := pressL(m)
	if opened {
		t.Fatal("L opened a pane with nothing to alternate to")
	}
	if !strings.Contains(m.status, "only one pane") || !strings.Contains(m.status, PaneLeaveKey) {
		t.Errorf("status %q, want it to name the key that can go back", m.status)
	}
}

// TestNeitherKeyOpensAnythingBeforeYouHaveBeenAnywhere, and both say so: a key
// that does nothing reads as broken.
func TestNeitherKeyOpensAnythingBeforeYouHaveBeenAnywhere(t *testing.T) {
	for _, tc := range []struct {
		what  string
		press func(Model) (Model, bool)
		says  string
	}{
		{"ctrl+\\", pressResume, "no pane to go back to"},
		{"L", pressL, "no pane to switch to"},
	} {
		var tmux []Action
		m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
			func(r ActionRequest) error { tmux = append(tmux, r.Action); return nil }).
			WithPaneBackend(allKinds())
		m.width, m.height = 120, 40

		m, opened := tc.press(m)
		if opened {
			t.Errorf("%s opened a pane before one had ever been opened", tc.what)
		}
		if !strings.Contains(m.status, tc.says) {
			t.Errorf("%s: status %q, want it to contain %q", tc.what, m.status, tc.says)
		}
		if len(tmux) != 0 {
			t.Errorf("%s fell through to tmux: %v", tc.what, tmux)
		}
	}
}

// TestAFailedOpenIsNotSomewhereToGoBackTo. The pane never came up, so there is
// no "back" to it — recording the attempt would make the key reopen a program the
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

	m, opened := pressResume(m)
	if opened {
		t.Fatal("ctrl+\\ reopened a pane that failed to open")
	}
	if !strings.Contains(m.status, "no pane to go back to") {
		t.Errorf("status %q, want it to say there is nowhere to go back to", m.status)
	}
}

// TestResumeRefusesAWorkspaceThatIsGone. The pane's row is resolved when the key
// is pressed rather than captured at open time, so a workspace deleted in between
// is a refusal — where a stored Item would open a program in a directory that is
// no longer there.
func TestResumeRefusesAWorkspaceThatIsGone(t *testing.T) {
	backend := &recordingPanes{fakePanes: *allKinds()}
	m := twoRowPanes(t, backend)
	m = openOn(t, m, "two", "agent")

	// The refresh that lands after the delete.
	m.itemsAll = []Item{{ProjectName: "proj", WorkspaceName: "one", Path: "/tmp", RepoRoot: "/tmp"}}
	m.cursor = 0

	backend.opened = nil
	m, opened := pressResume(m)
	if opened {
		t.Fatal("ctrl+\\ opened a pane for a workspace that is no longer on the deck")
	}
	if len(backend.opened) != 0 {
		t.Errorf("the backend was asked for %v for a workspace that is gone", backend.opened)
	}
	if !strings.Contains(m.status, "not on the deck any more") {
		t.Errorf("status %q, want it to name the missing workspace", m.status)
	}
}

// TestResumeReachesAPaneWhoseRowTheScopeHasDropped. An agent that exits leaves
// the attention scope, and the pane is still the one you were in — refusing to go
// back to it because of the scope would make the key depend on which list you
// happen to be looking at.
func TestResumeReachesAPaneWhoseRowTheScopeHasDropped(t *testing.T) {
	backend := &recordingPanes{fakePanes: *allKinds()}
	m := twoRowPanes(t, backend)
	m = openOn(t, m, "two", "agent")

	// A filter is the cheapest way to say "the row is not in the list you are
	// looking at"; the scope filters do the same thing to the same lookup.
	m.filter = "one"
	m.cursor = 0
	backend.opened = nil
	m, opened := pressResume(m)
	if !opened {
		t.Fatalf("ctrl+\\ would not reach a row the list is not showing; status %q", m.status)
	}
	if len(backend.opened) != 1 || backend.opened[0] != "two/agent" {
		t.Errorf("the backend was asked for %v, want the agent pane on two", backend.opened)
	}
}

// TestWithNoBackendLIsStillTmuxs. awp deck has no pane host, and L there means
// `tmux switch-client -l` — the same alternation, one substrate over — so the
// deck must not swallow the key on its way.
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

// TestWithNoBackendCtrlBackslashIsLeftAlone. There is no pane to resume on the
// tmux deck, and ctrl+\ belongs to tmux there — claiming it to say nothing would
// be worse than not claiming it.
func TestWithNoBackendCtrlBackslashIsLeftAlone(t *testing.T) {
	var tmux []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { tmux = append(tmux, r.Action); return nil })
	m.width, m.height = 120, 40

	updated, cmd := m.Update(resumeKey())
	got := updated.(Model)
	if cmd != nil {
		t.Error("ctrl+\\ scheduled work on a deck with no panes")
	}
	if got.status != "" {
		t.Errorf("ctrl+\\ reported %q on a deck with no panes", got.status)
	}
	if len(tmux) != 0 {
		t.Errorf("ctrl+\\ reached the tmux handler: %v", tmux)
	}
}
