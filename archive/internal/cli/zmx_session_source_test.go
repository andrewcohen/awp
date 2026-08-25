package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/zmx"
)

// lsRunner answers `zmx ls` with fixed lines and counts what it was asked.
type lsRunner struct {
	lines []string
	err   error
	calls [][]string
}

func (r *lsRunner) run(_ context.Context, _ string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.err != nil {
		return "", r.err
	}
	return strings.Join(r.lines, "\n") + "\n", nil
}

func zmxSource(lines ...string) (zmxSessions, *lsRunner) {
	r := &lsRunner{lines: lines}
	return zmxSessions{client: zmx.New(r.run)}, r
}

// TestALiveAgentSessionReadsAsRunning is the ordinary case: the agent is the
// session's own process, so "is it running" is the session being live.
func TestALiveAgentSessionReadsAsRunning(t *testing.T) {
	src, _ := zmxSource("name=awp.repo.qa.agent\tpid=42\tclients=1\tcreated=1786124270")
	snap := src.sessions(false)
	if !snap.known {
		t.Fatal("a successful read left the snapshot unknown")
	}
	if !snap.agentRunning("repo", "qa") {
		t.Errorf("a live agent session did not read as running: %+v", snap.facts("repo", "qa"))
	}
	if got := snap.facts("repo", "qa").name; got != "awp.repo.qa.agent" {
		t.Errorf("session name = %q, want the zmx session's own name", got)
	}
}

// TestAnEndedAgentSessionReadsAsExited: zmx keeps a session listed after its
// command exits so the output can still be read, so listed and running are
// different questions. The deck's "exited" row is present && agentGone.
func TestAnEndedAgentSessionReadsAsExited(t *testing.T) {
	src, _ := zmxSource("name=awp.repo.qa.agent\tpid=42\tended=1\texit_code=0")
	snap := src.sessions(false)
	f := snap.facts("repo", "qa")
	if !f.present || !f.agentGone {
		t.Errorf("an ended agent session read as %+v, want present and gone", f)
	}
	if snap.agentRunning("repo", "qa") {
		t.Error("an ended agent session read as running")
	}
}

// TestAWorkspaceThatNeverRanAnAgentDoesNotReadAsExited: an editor session means
// the workspace has a session, but says nothing about an agent. Reporting
// agentGone here would badge the row "exited" for an agent that never started —
// which the tmux path avoids too, by only sniffing sessions that have an agent
// window at all.
func TestAWorkspaceThatNeverRanAnAgentDoesNotReadAsExited(t *testing.T) {
	src, _ := zmxSource("name=awp.repo.qa.editor\tpid=42\tclients=1")
	f := src.sessions(false).facts("repo", "qa")
	if !f.present {
		t.Error("an editor session is a session; the workspace should read as present")
	}
	if f.agentGone {
		t.Error("a workspace with no agent session read as having lost one")
	}
}

// TestAnAgentSessionWinsTheNameFromAnEditorOne, whichever order zmx lists them
// in — the name is what the deck shows and what the session keys off.
func TestAnAgentSessionWinsTheNameFromAnEditorOne(t *testing.T) {
	for _, order := range [][]string{
		{"name=awp.repo.qa.editor", "name=awp.repo.qa.agent"},
		{"name=awp.repo.qa.agent", "name=awp.repo.qa.editor"},
	} {
		src, _ := zmxSource(order...)
		if got := src.sessions(false).facts("repo", "qa").name; got != "awp.repo.qa.agent" {
			t.Errorf("listed as %v, the name came out %q, want the agent's", order, got)
		}
	}
}

// TestSessionsAwpDidNotMakeAreNotTheDecksBusiness. `zmx ls` lists every session
// on the machine.
func TestSessionsAwpDidNotMakeAreNotTheDecksBusiness(t *testing.T) {
	src, _ := zmxSource("name=my-scratch-shell\tpid=9", "name=awp.repo.qa.agent\tpid=42")
	snap := src.sessions(false)
	if len(snap.byWorkspace) != 1 {
		t.Errorf("read %d workspaces from a list holding one awp session: %+v", len(snap.byWorkspace), snap.byWorkspace)
	}
}

// TestALeftoverTmuxSessionIsNotASessionHere is the reason this is a separate
// source rather than a merge. A workspace whose agent is in a tmux session
// started earlier by `awp deck` reads as having nothing: zdeck cannot show it,
// `a` will not reach it, and calling it active is the stale read the seam exists
// to remove. Named after the tmux scheme so the intent survives a refactor.
func TestALeftoverTmuxSessionIsNotASessionHere(t *testing.T) {
	src, _ := zmxSource("name=[awp]repo__qa\tpid=42\tclients=1")
	snap := src.sessions(false)
	if snap.facts("repo", "qa").present {
		t.Error("a tmux session name read as a zmx session for the same workspace")
	}
	if !snap.known {
		t.Error("the read succeeded, so the snapshot is known — the workspace simply has nothing here")
	}
}

// TestTheFirstPaintCostsNoSubprocess. Which workspace the user is looking at is
// in the environment, so unlike the tmux source's display-message there is
// nothing to shell out to.
func TestTheFirstPaintCostsNoSubprocess(t *testing.T) {
	t.Setenv("ZMX_SESSION", "awp.repo.qa.agent")
	src, r := zmxSource("name=awp.repo.qa.agent")
	snap := src.sessions(true)
	if len(r.calls) != 0 {
		t.Errorf("the fast path ran %v", r.calls)
	}
	if snap.known {
		t.Error("the fast path claimed to know the substrate")
	}
	if !snap.isCurrent("repo", "qa") {
		t.Error("the deck's own session did not read as current, so the first cursor lands elsewhere")
	}
}

// TestAWorkspaceOutsideAnyZmxSessionHasNoCurrentRow: running zdeck from a plain
// terminal is the normal case, and must not invent a current workspace.
func TestAWorkspaceOutsideAnyZmxSessionHasNoCurrentRow(t *testing.T) {
	t.Setenv("ZMX_SESSION", "")
	src, _ := zmxSource("name=awp.repo.qa.agent")
	if snap := src.sessions(false); snap.hasCurrent {
		t.Errorf("current = %+v with no ZMX_SESSION set", snap.current)
	}
}

// TestADaemonThatIsNotAnsweringIsNotAnEmptySubstrate. known=false is how the
// deck says "unread", which suppresses the caution glyphs and stale
// decorations. Returning a known-empty snapshot would render every workspace as
// dead because one subprocess failed.
func TestADaemonThatIsNotAnsweringIsNotAnEmptySubstrate(t *testing.T) {
	r := &lsRunner{err: errors.New("zmx is not answering")}
	snap := zmxSessions{client: zmx.New(r.run)}.sessions(false)
	if snap.known {
		t.Error("a failed read claimed to know the substrate")
	}
}

// TestZdeckReadsTheSubstrateItHostsOn: the host names its own source, so the
// two cannot disagree. A deck that hosted panes on zmx while reading tmux would
// decorate every row from sessions its keys never touch, and nothing about that
// fails — the rows just describe someone else's terminal.
func TestZdeckReadsTheSubstrateItHostsOn(t *testing.T) {
	host := zmxPanes{client: zmx.New((&lsRunner{}).run)}
	if _, ok := host.sessionSource().(zmxSessions); !ok {
		t.Errorf("zdeck's session source is %T, want zmxSessions", host.sessionSource())
	}
}

// The dev-URL discoverer's PIDs, which is #342. `d` reported no dev server for
// every row under a pane host, because the enumeration asked tmux — and a deck
// hosting its own panes has no tmux server to ask. The PIDs now come from the
// session source, so each substrate answers for itself.

// TestEverySessionOffersItsProcess. A zmx session is the process, so there are
// no panes under it to enumerate and the session's own PID is the root a dev
// server will be a descendant of.
func TestEverySessionOffersItsProcess(t *testing.T) {
	src, _ := zmxSource(
		"name=awp.repo.qa.agent\tpid=42\tclients=1",
		"name=awp.repo.qa.dev\tpid=43\tclients=1",
		"name=awp.repo.other.agent\tpid=44\tclients=1",
	)
	roots := src.roots()
	if got := roots[workspaceRef{project: "repo", workspace: "other"}]; len(got) != 1 || got[0] != 44 {
		t.Errorf("the other workspace's roots are %v, want [44]", got)
	}
	qa := roots[workspaceRef{project: "repo", workspace: "qa"}]
	if len(qa) != 2 {
		t.Fatalf("qa has %v roots, want both of its sessions", qa)
	}
	// Both sessions of a workspace, because the dev server is not obliged to be in
	// the agent's: the point of a `dev` pane kind is that it is somewhere else.
	for _, want := range []int{42, 43} {
		if !slicesContainsInt(qa, want) {
			t.Errorf("qa's roots %v are missing pid %d", qa, want)
		}
	}
}

// TestAnEndedSessionOffersNoProcess. zmx keeps a session listed after its
// command exits, and the kernel is free to have given that PID to someone else
// — so a dead session's pid would hand the discoverer a stranger's sockets and
// report them as this workspace's dev server.
func TestAnEndedSessionOffersNoProcess(t *testing.T) {
	src, _ := zmxSource("name=awp.repo.qa.agent\tpid=42\tended=1\texit_code=0")
	if roots := src.roots(); len(roots) != 0 {
		t.Errorf("an ended session offered %v", roots)
	}
}

// TestADaemonThatWillNotAnswerOffersNothing, rather than a partial answer. The
// discoverer treats an empty map as "nothing found", which is the honest report
// when the substrate could not be read.
func TestADaemonThatWillNotAnswerOffersNothing(t *testing.T) {
	r := &lsRunner{err: errors.New("no daemon")}
	if roots := (zmxSessions{client: zmx.New(r.run)}).roots(); len(roots) != 0 {
		t.Errorf("a failed read offered %v", roots)
	}
}

func slicesContainsInt(haystack []int, want int) bool {
	for _, n := range haystack {
		if n == want {
			return true
		}
	}
	return false
}
