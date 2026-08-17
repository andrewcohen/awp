package deckui

import (
	"strings"
	"testing"
)

// `a` is free. It opened the agent window until the captain claimed the letter;
// enter is the one way to a workspace's agent now, and this pins that the old
// binding is gone rather than merely unused — a stray ActionOpenWindow "agent"
// left behind would be a second door that only some surfaces know about.
//
// Phase 3 of the captain spec turns these assertions around: `a` will open the
// captain, and enter will still open the agent.
func TestAOpensNothingUntilTheCaptainTakesIt(t *testing.T) {
	var dispatched []ActionRequest
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Path: "/r/ws"}},
		func(r ActionRequest) error { dispatched = append(dispatched, r); return nil })
	m.width, m.height = 120, 40

	m, _ = pressKey(m, "a")

	if m.active != nil {
		t.Errorf("`a` opened %T", m.active)
	}
	for _, r := range dispatched {
		if r.Action == ActionOpenWindow && r.Arg == "agent" {
			t.Error("`a` still opens the agent window")
		}
	}
}

// The keymap and the help overlay agree that it is gone. deckKeyGroups is read by
// the `?` overlay, so a leftover row there advertises a key that does nothing.
func TestNothingStillAdvertisesAAsTheAgentWindow(t *testing.T) {
	for _, g := range deckKeyGroups() {
		for _, k := range g.Keys {
			if k[0] == "a" {
				t.Errorf("the ? overlay still lists a bare `a`: %q → %q (group %q)", k[0], k[1], g.Title)
			}
			if strings.Contains(k[1], "agent window") {
				t.Errorf("%q still describes an agent window: %q", k[0], k[1])
			}
		}
	}
}
