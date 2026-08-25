package cli

import (
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
)

// knownSessions builds a snapshot in the state a full refresh produces, for
// tests that care about what the rows do with it rather than how it was read.
type knownSessions struct{ snap deckSessionSnapshot }

func newKnownSessions() *knownSessions {
	return &knownSessions{snap: deckSessionSnapshot{
		known:       true,
		byWorkspace: map[workspaceRef]sessionFacts{},
	}}
}

// running: a session exists and the agent in it is alive.
func (k *knownSessions) running(project, workspace string) *knownSessions {
	return k.add(project, workspace, false)
}

// exited: a session exists but its agent does not — the state both substrates
// can be in, and the one that makes a stored "working" a lie.
func (k *knownSessions) exited(project, workspace string) *knownSessions {
	return k.add(project, workspace, true)
}

func (k *knownSessions) add(project, workspace string, agentGone bool) *knownSessions {
	k.snap.byWorkspace[workspaceRef{project: project, workspace: workspace}] = sessionFacts{
		name:      DeckSessionName(project, workspace),
		present:   true,
		agentGone: agentGone,
	}
	return k
}

// TestASessionNameRoundTripsThroughAWorkspaceRef: the snapshot is keyed by
// workspace so that either substrate can fill it, which means the tmux source
// has to get back out of a session name exactly what DeckSessionName put in. A
// mismatch would not error — rows would silently read as having no session.
func TestASessionNameRoundTripsThroughAWorkspaceRef(t *testing.T) {
	for _, tc := range []struct{ project, workspace string }{
		{"awp", "default"},
		{"awp", "andrew-feature"},
		{"my.repo", "ws.with.dots"},
		{"repo", "ws-with-dashes"},
	} {
		name := DeckSessionName(tc.project, tc.workspace)
		got, ok := tmuxWorkspaceRef(name)
		if !ok {
			t.Errorf("%q did not parse back as an awp session", name)
			continue
		}
		want := workspaceRef{project: tc.project, workspace: tc.workspace}
		if got != want {
			t.Errorf("%q round-tripped to %+v, want %+v", name, got, want)
		}
	}
}

// TestOnlyAwpSessionsBecomeRefs: `zmx ls` and `tmux list-sessions` both list
// everything on the machine. The deck has nothing to say about the rest of the
// user's sessions, and a row keyed off one would be nonsense.
func TestOnlyAwpSessionsBecomeRefs(t *testing.T) {
	for _, name := range []string{"", "work", "[awp]no-separator", "awp.repo.ws.agent"} {
		if ref, ok := tmuxWorkspaceRef(name); ok {
			t.Errorf("%q was read as an awp session, giving ref %+v", name, ref)
		}
	}
}

// TestTheSnapshotSeparatesNoSessionFromDeadAgent is the distinction the two
// fields exist for. Collapsing them would make a workspace whose agent quit
// indistinguishable from one that was never started — and the row for those two
// is different ("exited" vs idle with no session).
func TestTheSnapshotSeparatesNoSessionFromDeadAgent(t *testing.T) {
	snap := newKnownSessions().running("repo", "alive").exited("repo", "dead").snap

	if f := snap.facts("repo", "alive"); !f.present || f.agentGone {
		t.Errorf("alive: present=%v agentGone=%v, want present with a live agent", f.present, f.agentGone)
	}
	if f := snap.facts("repo", "dead"); !f.present || !f.agentGone {
		t.Errorf("dead: present=%v agentGone=%v, want a session whose agent is gone", f.present, f.agentGone)
	}
	if f := snap.facts("repo", "never-started"); f.present || f.agentGone {
		t.Errorf("never-started: present=%v agentGone=%v, want the zero value", f.present, f.agentGone)
	}

	if !snap.agentRunning("repo", "alive") {
		t.Error("agentRunning said no for a live agent")
	}
	if snap.agentRunning("repo", "dead") {
		t.Error("agentRunning said yes for a session whose agent has exited")
	}
	if snap.agentRunning("repo", "never-started") {
		t.Error("agentRunning said yes for a workspace with no session")
	}
}

// TestNoCurrentWorkspaceWithoutOne: isCurrent compares a struct, so a snapshot
// that never learned a current workspace must not match the zero-value row.
// Comparing bare strings, "" == "" would have made every unnamed row current.
func TestNoCurrentWorkspaceWithoutOne(t *testing.T) {
	snap := newKnownSessions().running("repo", "ws").snap
	if snap.isCurrent("", "") {
		t.Error("a snapshot with no current workspace claimed the empty one")
	}
	if snap.isCurrent("repo", "ws") {
		t.Error("a snapshot with no current workspace claimed a real one")
	}

	snap.current, snap.hasCurrent = workspaceRef{project: "repo", workspace: "ws"}, true
	if !snap.isCurrent("repo", "ws") {
		t.Error("the current workspace did not report as current")
	}
	if snap.isCurrent("repo", "other") {
		t.Error("a different workspace reported as current")
	}
}

// TestNoTmuxClientReadsAsNothing: the source is constructed before we know
// whether tmux is there. Returning a usable empty snapshot rather than
// panicking is what lets the fast first paint run with no probes at all.
func TestNoTmuxClientReadsAsNothing(t *testing.T) {
	snap := tmuxSessions{}.sessions(false)
	if snap.known {
		t.Error("a snapshot with no tmux client claimed to know the substrate")
	}
	if len(snap.byWorkspace) != 0 {
		t.Errorf("got %d sessions from no client", len(snap.byWorkspace))
	}
	if snap.hasCurrent {
		t.Error("no client, but a current workspace")
	}
}

// TestNoTmuxClientOffersNoProcesses, the same reason: the discoverer's tick can
// land before anything has been probed.
func TestNoTmuxClientOffersNoProcesses(t *testing.T) {
	if roots := (tmuxSessions{}).roots(); len(roots) != 0 {
		t.Errorf("got %v processes from no client", roots)
	}
}

// TestTheDiscoverersKeysAreTheKeysTheRowsCarry. The deck looks a dev URL up by
// Item.SessionName, and both session sources answer keyed by workspace — so what
// the discoverer is handed has to be re-keyed to the name the row carries. Under
// zmx the substrate's own name is a third spelling (awp.repo.qa.agent, one per
// pane kind), so keying on it would find every dev server and attach none of
// them to a row.
func TestTheDiscoverersKeysAreTheKeysTheRowsCarry(t *testing.T) {
	src, _ := zmxSource("name=awp.repo.qa.agent\tpid=42\tclients=1")
	byRow := rootsByRow(src.roots())
	want := DeckSessionName("repo", "qa")
	if got := byRow[want]; len(got) != 1 || got[0] != 42 {
		t.Errorf("keyed by %q the roots are %v, want [42]; whole map: %v", want, got, byRow)
	}
	// The row the deck builds carries that same name, which is the other end of
	// the lookup.
	item := deckui.Item{SessionName: DeckSessionName("repo", "qa")}
	if _, ok := byRow[item.SessionName]; !ok {
		t.Errorf("the row's session name %q is not a key of %v", item.SessionName, byRow)
	}
}

// TestNothingToReKeyStaysNothing: an empty answer must not become a map of one
// row with no processes, which the discoverer would read as "found nothing here"
// rather than "nothing was read".
func TestNothingToReKeyStaysNothing(t *testing.T) {
	if got := rootsByRow(nil); got != nil {
		t.Errorf("re-keying nothing produced %v", got)
	}
}
