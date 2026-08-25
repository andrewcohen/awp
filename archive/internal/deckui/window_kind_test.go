package deckui

import "testing"

// TestTheWindowKindIsTheNameNotTheCommand: two of the deck's window args carry
// a tmux command after the name. Keying a pane backend on the whole arg would
// mean the backend's map had to spell the tmux command too — so `W` could never
// be a pane, because its arg is `watch:awp watch` and the pane runs argv.
func TestTheWindowKindIsTheNameNotTheCommand(t *testing.T) {
	for arg, want := range map[string]string{
		PaneKindWatch + ":awp watch": PaneKindWatch,
		ReviewStackArg:               "review",
		PaneKindAgent:                PaneKindAgent,
		"editor":                     "editor",
		"":                           "",
	} {
		if got := WindowKind(arg); got != want {
			t.Errorf("WindowKind(%q) = %q, want %q", arg, got, want)
		}
	}
}

// ciAndWatch is a backend that hosts the two kinds this change adds, and
// nothing else — so a fall-through failure shows up as the wrong kind opening
// rather than as everything opening.
func ciAndWatch() *fakePanes {
	return &fakePanes{handles: map[string]bool{PaneKindCI: true, PaneKindWatch: true}}
}

// TestCIOpensAsAPane: `i` is not an ActionOpenWindow, so without naming it as a
// kind it could never reach a pane host — it would open a tmux window on a deck
// that has no tmux to open it in.
func TestCIOpensAsAPane(t *testing.T) {
	var handlerCalls []Action
	backend := ciAndWatch()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(r ActionRequest) error { handlerCalls = append(handlerCalls, r.Action); return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionCI, "")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("i opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if backend.opened != PaneKindCI {
		t.Errorf("the backend was asked for %q, want %q", backend.opened, PaneKindCI)
	}
	if len(handlerCalls) != 0 {
		t.Errorf("the tmux handler was still called: %v", handlerCalls)
	}
}

// TestWatchOpensAsAPane: the same for `W`, whose arg carries a command the pane
// path has no use for.
func TestWatchOpensAsAPane(t *testing.T) {
	backend := ciAndWatch()
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
		func(ActionRequest) error { return nil }).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionOpenWindow, PaneKindWatch+":awp watch")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("W opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if backend.opened != PaneKindWatch {
		t.Errorf("the backend was asked for %q, want %q", backend.opened, PaneKindWatch)
	}
}

// TestCIAndWatchStillReachTmuxWithoutABackend: awp deck is unchanged, and `i`
// in particular now takes a branch it did not before.
func TestCIAndWatchStillReachTmuxWithoutABackend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action Action
		arg    string
	}{
		{"ci", ActionCI, ""},
		{"watch", ActionOpenWindow, PaneKindWatch + ":awp watch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []ActionRequest
			m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}},
				func(r ActionRequest) error { got = append(got, r); return nil })
			m.width, m.height = 120, 40

			next, cmd := m.trigger(tc.action, tc.arg)
			if next.(Model).active != nil {
				if _, isPane := next.(Model).active.(*panePopover); isPane {
					t.Fatal("a pane opened with no backend wired")
				}
			}
			runCmd(cmd)
			if len(got) != 1 || got[0].Action != tc.action || got[0].Arg != tc.arg {
				t.Errorf("the handler saw %+v, want one %v with arg %q", got, tc.action, tc.arg)
			}
		})
	}
}
