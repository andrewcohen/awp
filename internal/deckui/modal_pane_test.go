package deckui

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// fakePanes hosts a harmless process and records what it was asked for.
type fakePanes struct {
	kinds    []string
	handles  map[string]bool
	opened   string
	restored int
	err      error
}

func (f *fakePanes) Describes(kind string) bool { return f.handles[kind] }

func (f *fakePanes) Open(_ Item, kind string, _, _ int) (*exec.Cmd, func(), error) {
	f.kinds = append(f.kinds, kind)
	if f.err != nil {
		return nil, nil, f.err
	}
	f.opened = kind
	return exec.Command("sh", "-c", "echo PANE-UP; sleep 30"), func() { f.restored++ }, nil
}

func paneModel(t *testing.T, backend PaneBackend) Model {
	t.Helper()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}}, func(ActionRequest) error { return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40
	return m
}

func allKinds() *fakePanes {
	return &fakePanes{handles: map[string]bool{"agent": true, "editor": true, "vcs": true, "": true}}
}

// The whole point of the backend: the deck's UI is unchanged and only where
// the process lives differs. With one wired in, a window key hosts a pane
// instead of reaching the tmux handler.
func TestAWindowKeyHostsAPaneWhenABackendIsWired(t *testing.T) {
	var handlerCalls []Action
	backend := allKinds()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { handlerCalls = append(handlerCalls, r.Action); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if cmd == nil {
		t.Error("opening a pane scheduled no work, so it will never repaint")
	}
	if len(handlerCalls) != 0 {
		t.Errorf("the tmux handler was still called: %v", handlerCalls)
	}
	if backend.opened != "agent" {
		t.Errorf("the backend was asked for %q, want agent", backend.opened)
	}
}

// Without a backend the deck is exactly what it was: the key reaches the
// handler and opens a tmux window.
func TestWithoutABackendTheDeckIsUnchanged(t *testing.T) {
	var got []ActionRequest
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { got = append(got, r); return nil })
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionOpenWindow, "agent")
	if next.(Model).active != nil {
		t.Error("a pane opened with no backend wired")
	}
	// The handler runs inside the returned command, not during trigger.
	runCmd(cmd)
	if len(got) != 1 || got[0].Action != ActionOpenWindow || got[0].Arg != "agent" {
		t.Errorf("the handler saw %+v, want one ActionOpenWindow/agent", got)
	}
}

// A backend that declines a kind must fall through to tmux, which is what
// keeps the review and PR-description windows working.
func TestAnUnhandledKindFallsThroughToTmux(t *testing.T) {
	var got []string
	backend := &fakePanes{handles: map[string]bool{"agent": true}}
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { got = append(got, r.Arg); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionOpenWindow, ReviewStackArg)
	if next.(Model).active != nil {
		t.Error("a declined kind opened a pane anyway")
	}
	runCmd(cmd)
	if len(got) != 1 || got[0] != ReviewStackArg {
		t.Errorf("the handler saw %v, want the review window to fall through", got)
	}
}

// Every key belongs to the program except the one that leaves — esc, q and
// ctrl+c all mean something to an agent.
func TestOnlyTheLeaveKeyIsInterceptedInAPane(t *testing.T) {
	m := paneModel(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p := m.active.(*panePopover)
	t.Cleanup(func() { p.close(&m) })

	for _, k := range []tea.KeyPressMsg{
		{Code: tea.KeyEsc}, {Code: 'q', Text: "q"}, {Code: 'c', Mod: tea.ModCtrl}, {Code: '?', Text: "?"},
	} {
		p.update(&m, k)
		if m.active == nil {
			t.Fatalf("%q closed the pane; only %s may", k.String(), paneLeaveKey)
		}
	}

	p.update(&m, tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if m.active != nil {
		t.Errorf("%s did not close the pane", paneLeaveKey)
	}
}

// A pane that has closed can still have a frame in flight; painting it would
// put the previous process's screen inside the current one.
func TestAStalePanesFrameIsIgnored(t *testing.T) {
	m := paneModel(t, allKinds())
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p := m.active.(*panePopover)
	t.Cleanup(func() { p.close(&m) })

	if cmd := p.update(&m, vterm.OutputMsg{Gen: p.term.Gen() - 1}); cmd != nil {
		t.Error("a frame from an older pane was accepted")
	}
	if cmd := p.update(&m, vterm.OutputMsg{Gen: p.term.Gen()}); cmd == nil {
		t.Error("the live pane's frame did not re-arm the wait, so it repaints once and stops")
	}
	p.update(&m, vterm.ExitMsg{Gen: p.term.Gen() - 1})
	if m.active == nil {
		t.Error("an older pane's exit closed the current one")
	}
}

func TestAFailedOpenReportsWhyAndOpensNothing(t *testing.T) {
	backend := allKinds()
	backend.err = errors.New("zmx session is gone")
	m := paneModel(t, backend)

	next, _ := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	if got.active != nil {
		t.Fatal("a pane opened despite the backend refusing")
	}
	if !strings.Contains(got.status, "zmx session is gone") {
		t.Errorf("status is %q, want the backend's reason", got.status)
	}
}

// The popover has to fit the deck at every size it agrees to open at — the
// arithmetic a border change silently broke elsewhere in this repo.
func TestThePanePopoverFitsTheDeck(t *testing.T) {
	for w := 20; w <= 220; w++ {
		for h := 6; h <= 70; h++ {
			if !paneFits(w, h) {
				continue
			}
			tw, th := paneDims(w, h)
			if boxW := tw + 4 + borderCells; boxW > w {
				t.Fatalf("at %dx%d the pane box is %d wide, past the deck's %d", w, h, boxW, w)
			}
			if boxH := th + 2 + borderCells + 4; boxH > h {
				t.Fatalf("at %dx%d the pane box is %d tall, past the deck's %d", w, h, boxH, h)
			}
		}
	}
}

func TestATinyDeckRefusesAPane(t *testing.T) {
	m := paneModel(t, allKinds())
	m.width, m.height = 40, 12
	next, _ := m.trigger(ActionOpenWindow, "agent")
	got := next.(Model)
	if got.active != nil {
		t.Fatal("a 40x12 deck opened a pane it cannot draw")
	}
	if !strings.Contains(got.status, "too small") {
		t.Errorf("status is %q, want it to say the terminal is too small", got.status)
	}
}

// runCmd drains a tea.Cmd, including the batch trigger returns, so the
// handler dispatch inside it actually runs.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runCmd(c)
		}
	}
}

// Summoning has nowhere to hand off to when awp is hosting the panes, and
// `tmux switch-client` from outside tmux exits 0 having done nothing. So enter
// brings the workspace's agent into the deck instead of silently no-opping.
func TestSummonOpensTheAgentPaneWhenABackendIsWired(t *testing.T) {
	backend := allKinds()
	m := paneModel(t, backend)

	next, _ := m.trigger(ActionSummon, "")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("enter opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if backend.opened != "agent" {
		t.Errorf("enter asked the backend for %q, want agent", backend.opened)
	}
}

// And without a backend, enter is the tmux handoff it has always been.
func TestSummonIsUnchangedWithoutABackend(t *testing.T) {
	var got []Action
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { got = append(got, r.Action); return nil })
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionSummon, "")
	if next.(Model).active != nil {
		t.Error("a pane opened with no backend wired")
	}
	runCmd(cmd)
	if len(got) != 1 || got[0] != ActionSummon {
		t.Errorf("the handler saw %v, want one ActionSummon", got)
	}
}
