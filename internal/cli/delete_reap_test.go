package cli

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/cmderr"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
)

// zmxLsRunner answers `zmx ls` with fixed session names and records the kills.
type zmxLsRunner struct {
	names   []string
	killErr error
	killed  []string
	tmux    [][]string
	// tmuxErr replaces the no-server answer, for the case where tmux fails in a way
	// that is a real error rather than the absence of a server.
	tmuxErr error
	// ended marks every listed session as exited. zmx keeps a session listed
	// after its command dies, so "listed" and "running" are different
	// questions, and the guards that ask them have to disagree.
	ended bool
}

func (r *zmxLsRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	switch name {
	case "tmux":
		r.tmux = append(r.tmux, args)
		if r.tmuxErr != nil {
			return "", r.tmuxErr
		}
		// No server, which is what a machine hosting its own panes looks like — and
		// spelled the way the real runner spells it, wrapping the exit rather than
		// describing it. Written as "exit status 1" here for a long time, which is
		// exec's phrasing and not one any caller sees: the message the runner
		// substitutes is what reached tmux's guards, and the fake saying otherwise
		// is why this suite stayed green while a real delete reported failure.
		return "", cmderr.Exited(
			`"tmux" exited 1:`+"\nno server running on /private/tmp/tmux-502/default",
			exitStatusOne(),
		)
	case "zmx":
		if len(args) == 0 {
			return "", nil
		}
		switch args[0] {
		case "ls":
			var b strings.Builder
			for _, n := range r.names {
				b.WriteString("name=" + n + "\tpid=1")
				if r.ended {
					b.WriteString("\tended=1\texit_code=0")
				}
				b.WriteString("\n")
			}
			return b.String(), nil
		case "kill":
			if r.killErr != nil {
				return "", r.killErr
			}
			r.killed = append(r.killed, args[1])
		}
	}
	return "", nil
}

// exitStatusOne is a real *exec.ExitError, so what the fake returns has the same
// structure production does and cmderr.RanAndFailed answers the same way.
func exitStatusOne() error {
	return exec.Command("sh", "-c", "exit 1").Run()
}

func requireZmx(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("zmx"); err != nil {
		t.Skip("zmx is not on PATH, and killWorkspaceSessions deliberately does nothing without it")
	}
}

// TestDeletingAWorkspaceKillsItsAgentSession is the bug Andrew hit. The jj
// workspace and the state entry went; the agent kept running in a directory
// that no longer existed, and then came back as a permanent "unmanaged" row
// because the session source now reads zmx.
//
// runDeleteJob calls handleDeckAction directly — the delete runs in a detached
// subprocess with no terminal and therefore no pane host to ask — so the reap
// has to be part of the delete itself, not something a host is consulted about.
func TestDeletingAWorkspaceKillsItsAgentSession(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{names: []string{
		"awp.repo.qa.agent",
		"awp.repo.qa.editor",
	}}
	if err := killWorkspaceSessions(r, "repo", "qa", nil); err != nil {
		t.Fatalf("killWorkspaceSessions: %v", err)
	}
	for _, want := range []string{"awp.repo.qa.agent", "awp.repo.qa.editor"} {
		if !slices.Contains(r.killed, want) {
			t.Errorf("%s survived the delete; killed %v", want, r.killed)
		}
	}
}

// TestDeletingAWorkspaceLeavesEveryOtherSessionAlone. The reap matches on the
// parsed (project, workspace), so a workspace whose name is a prefix of
// another's — or another project's workspace of the same name — is not in
// range. This is the test that would have caught a `strings.HasPrefix` shortcut.
func TestDeletingAWorkspaceLeavesEveryOtherSessionAlone(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{names: []string{
		"awp.repo.qa.agent",
		"awp.repo.qa-2.agent",    // longer name sharing the prefix
		"awp.other.qa.agent",     // same workspace name, different project
		"my-own-scratch-session", // not awp's at all
		"awp.repo.default.agent", // sibling workspace
	}}
	if err := killWorkspaceSessions(r, "repo", "qa", nil); err != nil {
		t.Fatalf("killWorkspaceSessions: %v", err)
	}
	if len(r.killed) != 1 || r.killed[0] != "awp.repo.qa.agent" {
		t.Errorf("killed %v, want only awp.repo.qa.agent", r.killed)
	}
}

// TestASurvivingSessionIsReportedWithTheCommandToFinishIt. The workspace is
// already gone by then, so this cannot undo anything — but a live agent in a
// deleted tree is the whole bug, and silence is how it came back last time.
func TestASurvivingSessionIsReportedWithTheCommandToFinishIt(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{
		names:   []string{"awp.repo.qa.agent"},
		killErr: errors.New("zmx is not answering"),
	}
	err := killWorkspaceSessions(r, "repo", "qa", nil)
	if err == nil {
		t.Fatal("a session that refused to die was not reported")
	}
	for _, want := range []string{"awp.repo.qa.agent", "zmx kill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestTheDeleteActionReapsBothSubstrates walks the real action, so the wiring is
// covered and not just the helper. Unconditional rather than gated on a pane
// host: the delete spec does not carry that answer, and killing sessions that do
// not exist is a no-op, so `awp deck` is unaffected.
func TestTheDeleteActionReapsBothSubstrates(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{names: []string{"awp.repo.qa.agent"}}
	svc := &fakeService{}
	err := handleDeckAction(tmux.New(r), svc, r, deckui.ActionRequest{
		Item:   deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo", Path: "/repo/qa"},
		Action: deckui.ActionDelete,
	}, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !slices.Contains(r.killed, "awp.repo.qa.agent") {
		t.Errorf("the delete action left the agent session running; killed %v", r.killed)
	}
}

// TestATmuxFailureDoesNotCostTheZmxReapItsTurn.
//
// The two substrates are torn down in order, and the tmux half used to return its
// error — which meant any tmux trouble at all ended the delete before the zmx reap
// it sits in front of. The workspace is already deleted by then, so the session is
// left running an agent in a directory that is gone, with nothing in awp still
// pointing at it.
//
// A tmux that cannot be run at all rather than an absent server: an absent server
// is now no sessions and no error, so it no longer exercises this path. The error
// still has to be reported — a tmux session nobody killed is worth saying out loud
// — but reporting it is not a reason to skip the rest of the delete.
func TestATmuxFailureDoesNotCostTheZmxReapItsTurn(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{
		names:   []string{"awp.repo.qa.agent"},
		tmuxErr: errors.New(`"tmux" is not on $PATH for this process.`),
	}
	svc := &fakeService{}
	err := handleDeckAction(tmux.New(r), svc, r, deckui.ActionRequest{
		Item:   deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo", Path: "/repo/qa"},
		Action: deckui.ActionDelete,
	}, nil)
	if !slices.Contains(r.killed, "awp.repo.qa.agent") {
		t.Errorf("a tmux failure left the agent session running; killed %v", r.killed)
	}
	if err == nil {
		t.Error("the tmux failure was swallowed; a session nobody killed has to be reported")
	}
}
