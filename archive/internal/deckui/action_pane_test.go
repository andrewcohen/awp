package deckui

import (
	"errors"
	"strings"
	"testing"
)

// errPaneOpen stands in for whatever the backend could not do — most likely the
// action not being in the workspace's config at all.
var errPaneOpen = errors.New("no user action by that name in /repo")

func devAction() []UserAction {
	return []UserAction{{Name: "dev", Command: "pnpm dev", Alias: "d"}}
}

func actionItem() []Item {
	return []Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/tmp", RepoRoot: "/tmp"}}
}

// TestAUserActionOpensAsAPane: `x` used to be the one window key a pane host
// could never claim. It reached the tmux path, which opens with "no session for
// this workspace? make one" — so on a deck that hosts the agent it started a
// second one, invisible, and then switch-client no-opped and nothing appeared to
// happen. Refusing it was the stopgap; hosting it is the fix.
func TestAUserActionOpensAsAPane(t *testing.T) {
	var handlerCalls []Action
	backend := &fakePanes{handles: map[string]bool{PaneKindForAction("dev"): true}}
	m := New(actionItem(), func(r ActionRequest) error { handlerCalls = append(handlerCalls, r.Action); return nil }).
		WithUserActions(devAction()).WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionCustom, "dev")
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("x opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if backend.opened != PaneKindForAction("dev") {
		t.Errorf("the backend was asked for %q, want %q", backend.opened, PaneKindForAction("dev"))
	}
	if len(handlerCalls) != 0 {
		t.Errorf("the tmux handler was still called: %v", handlerCalls)
	}
}

// TestTheActionMenusAliasOpensThePane is the same thing through the keys the
// user actually presses, since the arg the pane kind is built from comes out of
// the menu rather than from trigger's caller.
func TestTheActionMenusAliasOpensThePane(t *testing.T) {
	backend := &fakePanes{handles: map[string]bool{PaneKindForAction("dev"): true}}
	m := New(actionItem(), func(ActionRequest) error { return nil }).
		WithUserActions(devAction()).WithPaneBackend(backend)
	m.width, m.height = 120, 40

	opened, _ := m.Update(runeKey("x"))
	next, _ := opened.(Model).Update(runeKey("d"))
	got := next.(Model)
	p, ok := got.active.(*panePopover)
	if !ok {
		t.Fatalf("x d opened no pane; status %q", got.status)
	}
	t.Cleanup(func() { p.close(&got) })
	if backend.opened != PaneKindForAction("dev") {
		t.Errorf("the backend was asked for %q, want %q", backend.opened, PaneKindForAction("dev"))
	}
}

// TestABackgroundActionIsNotAPane: marking an action background is a request for
// it to run *without* one, and the job substrate is where those live. The pane
// branch runs before the one that dispatches jobs, so claiming every custom
// action there would have quietly turned every background action into a pane.
func TestABackgroundActionIsNotAPane(t *testing.T) {
	backend := &fakePanes{handles: map[string]bool{PaneKindForAction("seed"): true}}
	m := New(actionItem(), func(ActionRequest) error { return nil }).
		WithUserActions([]UserAction{{Name: "seed", Command: "pnpm seed", Alias: "s", Background: true}}).
		WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionCustom, "seed")
	if p, ok := next.(Model).active.(*panePopover); ok {
		got := next.(Model)
		p.close(&got)
		t.Fatal("a background action opened a pane, which is the one thing marking it background asks for it not to do")
	}
	if len(backend.kinds) != 0 {
		t.Errorf("the backend was asked for %v; a background action is not a window", backend.kinds)
	}
}

// TestAUserActionStillReachesTmuxWithoutABackend: awp deck is unchanged. The
// pane branch is new on this action, so this is the half that says the old path
// still runs when there is nothing hosting panes.
func TestAUserActionStillReachesTmuxWithoutABackend(t *testing.T) {
	var got []ActionRequest
	m := New(actionItem(), func(r ActionRequest) error { got = append(got, r); return nil }).
		WithUserActions(devAction())
	m.width, m.height = 120, 40

	next, cmd := m.trigger(ActionCustom, "dev")
	if p, ok := next.(Model).active.(*panePopover); ok {
		n := next.(Model)
		p.close(&n)
		t.Fatal("a pane opened with no backend wired")
	}
	execCmd(t, cmd)
	if len(got) != 1 || got[0].Action != ActionCustom || got[0].Arg != "dev" {
		t.Errorf("the handler saw %+v, want one ActionCustom with arg %q", got, "dev")
	}
}

// TestAnActionsKindCannotCollideWithAFixedOne. A user action's name comes from
// whoever wrote the config, so without the namespace an action called "agent"
// would address the workspace's agent session — running its command where the
// coding agent belongs, under the name the deck reads agent state from.
func TestAnActionsKindCannotCollideWithAFixedOne(t *testing.T) {
	for _, fixed := range []string{PaneKindAgent, PaneKindCI, PaneKindWatch, "editor", "vcs", ""} {
		if got := PaneKindForAction(fixed); got == fixed {
			t.Errorf("an action called %q takes the kind %q, which is the fixed one", fixed, got)
		}
	}
}

// TestAnActionKindRoundTrips: the kind is written into a session name and read
// back out of one — the sessions overlay reopens a pane from the kind it parsed
// — so the two directions have to agree, and nothing else may answer to them.
func TestAnActionKindRoundTrips(t *testing.T) {
	for _, name := range []string{"dev", "agent", "action_dev", "d"} {
		got, ok := ActionFromPaneKind(PaneKindForAction(name))
		if !ok || got != name {
			t.Errorf("ActionFromPaneKind(PaneKindForAction(%q)) = %q, %v; want %q, true", name, got, ok, name)
		}
	}
	for _, notAnAction := range []string{PaneKindAgent, PaneKindCI, PaneKindWatch, "editor", "vcs", "", PaneKindActionPrefix} {
		if got, ok := ActionFromPaneKind(notAnAction); ok {
			t.Errorf("kind %q reads as the user action %q", notAnAction, got)
		}
	}
}

// TestAPaneWearsTheNameTheUserGaveIt: the namespace is there so two kinds cannot
// collide, and it is not something the user typed — so the pane's chrome and its
// errors say "dev", not "action_dev".
func TestAPaneWearsTheNameTheUserGaveIt(t *testing.T) {
	if got := PaneLabel(PaneKindForAction("dev")); got != "dev" {
		t.Errorf("PaneLabel(%q) = %q, want dev", PaneKindForAction("dev"), got)
	}
	if got := PaneLabel(PaneKindAgent); got != PaneKindAgent {
		t.Errorf("PaneLabel(%q) = %q, want it unchanged", PaneKindAgent, got)
	}
	if got := PaneLabel(""); got != "shell" {
		t.Errorf("PaneLabel(\"\") = %q, want shell", got)
	}
}

// TestTheActionPaneErrorNamesTheAction is about the one thing a failed open has
// to do: say which action, so the fix is in the message rather than in a guess.
func TestTheActionPaneErrorNamesTheAction(t *testing.T) {
	backend := &fakePanes{handles: map[string]bool{PaneKindForAction("dev"): true}, err: errPaneOpen}
	m := New(actionItem(), func(ActionRequest) error { return nil }).
		WithUserActions(devAction()).WithPaneBackend(backend)
	m.width, m.height = 120, 40

	next, _ := m.trigger(ActionCustom, "dev")
	status := next.(Model).status
	if !strings.Contains(status, "dev") || !strings.Contains(status, errPaneOpen.Error()) {
		t.Errorf("status %q does not name both the action and what went wrong", status)
	}
}
