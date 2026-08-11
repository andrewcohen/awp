package cli

import (
	"errors"
	"slices"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
	"github.com/andrewcohen/awp/internal/zmx"
)

// unreadHost is a pane host over a service that records what it was told to
// mark read.
func unreadHost(svc *promptSvc, fz *fakeZmx) zmxPanes {
	if fz == nil {
		fz = &fakeZmx{}
	}
	return zmxPanes{
		client: zmx.New(fz.run),
		svcFor: func(string) workspace.Service { return svc },
	}
}

// TestOpeningTheAgentClearsTheUnreadMark. Every tmux window switch marks the
// workspace read, so under tmux reading the agent's output clears the badge. No
// pane path did, so the dot and the attention count survived reading the very
// output they were pointing at — and the next glance at the deck said the same
// workspace still wanted you.
func TestOpeningTheAgentClearsTheUnreadMark(t *testing.T) {
	svc := &promptSvc{}
	if _, _, err := unreadHost(svc, nil).Open(paneItem(), deckui.PaneKindAgent, 80, 24); err != nil {
		t.Fatalf("open the agent pane: %v", err)
	}
	if !slices.Contains(svc.read, paneItem().WorkspaceName) {
		t.Errorf("marked read: %v, want the %q row", svc.read, paneItem().WorkspaceName)
	}
}

// TestOnlyTheAgentPaneClearsTheMark is why this is narrower than the tmux rule.
// The mark means the agent produced output you have not seen, and its own pane is
// the only one that shows it. Under tmux any window switch cleared it because a
// switch puts you in the session with the agent one key away; here the panes are
// separate surfaces, so opening jjui is not evidence you read anything.
func TestOnlyTheAgentPaneClearsTheMark(t *testing.T) {
	for _, kind := range []string{"editor", "vcs", deckui.PaneKindCI, deckui.PaneKindWatch, ""} {
		svc := &promptSvc{}
		if _, _, err := unreadHost(svc, nil).Open(paneItem(), kind, 80, 24); err != nil {
			t.Fatalf("open the %q pane: %v", kind, err)
		}
		if len(svc.read) != 0 {
			t.Errorf("the %q pane marked %v read, but it shows none of the agent's output", kind, svc.read)
		}
	}
}

// TestAnAgentPaneThatWillNotOpenLeavesTheMark: the mark is the reason you are
// about to look at something, so clearing it before the pane exists would lose
// the signal without showing anyone the output. The failure here is a paste into
// a live session that zmx would not take.
func TestAnAgentPaneThatWillNotOpenLeavesTheMark(t *testing.T) {
	svc := &promptSvc{pending: workspace.PendingPrompt{Text: "fix the tests"}}
	fz := &fakeZmx{live: true, sendErr: errors.New("zmx is not answering")}
	if _, _, err := unreadHost(svc, fz).Open(paneItem(), deckui.PaneKindAgent, 80, 24); err == nil {
		t.Fatal("expected Open to report the failure")
	}
	if len(svc.read) != 0 {
		t.Errorf("marked %v read on a pane that never opened", svc.read)
	}
}

// TestAPaneWithNoServiceStillOpens: the mark is bookkeeping. A host wired
// without a service — every zmxPanes in these tests that does not care about
// state — must still open panes rather than panicking on a nil interface.
func TestAPaneWithNoServiceStillOpens(t *testing.T) {
	host := zmxPanes{client: zmx.New((&fakeZmx{}).run)}
	if _, _, err := host.Open(paneItem(), deckui.PaneKindAgent, 80, 24); err != nil {
		t.Fatalf("open the agent pane with no service wired: %v", err)
	}
}
