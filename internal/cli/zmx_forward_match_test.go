package cli

import (
	"testing"

	"github.com/andrewcohen/awp/internal/zmx"
)

// rowsAre wires a session source to a fixed set of deck rows, the way the deck
// wires it to the store.
func rowsAre(refs ...workspaceRef) func([]string) zmxSessions {
	return func(lines []string) zmxSessions {
		src, _ := zmxSource(lines...)
		src.rows = func() []workspaceRef { return refs }
		return src
	}
}

// TestAShortenedNameStillFindsItsRow is the whole reason the match runs
// forward. A name too long for zmx has to be shortened to exist, and a
// shortened segment is not the workspace's name — so reading the name back
// would fail to match exactly the workspaces the shortening is for, and their
// agents would read as having no session at all.
//
// The name here is what a shortened one looks like: generated from the row, so
// whatever the shortening does to it, this test is asking the question the deck
// asks.
func TestAShortenedNameStillFindsItsRow(t *testing.T) {
	// A workspace named after a PR's head branch — the case that overflows.
	ref := workspaceRef{project: "alpha", workspace: "pr-2336-dev-mlwzqyrmxslo"}
	name := zmx.SessionName(ref.project, ref.workspace, "agent")
	src := rowsAre(ref)([]string{"name=" + name + "\tpid=42\tclients=1"})

	f := src.sessions(false).facts(ref.project, ref.workspace)
	if !f.present {
		t.Fatalf("the row did not find its own session %q", name)
	}
	if f.name != name {
		t.Errorf("matched session %q, want %q", f.name, name)
	}
}

// TestALabelledSessionFindsAWorkspaceWithNoRow. A session outlives the row that
// made it — a workspace deleted or renamed while its session ran — and the deck
// has to keep seeing it, or nothing will ever offer to reap it. No stem matches,
// so the labels answer instead, and they hold the real workspace name even when
// the name was shortened.
func TestALabelledSessionFindsAWorkspaceWithNoRow(t *testing.T) {
	src := rowsAre(workspaceRef{project: "alpha", workspace: "still-here"})([]string{
		"name=awp.alpha.gone-4f2a.agent\tpid=42\tclients=0\t" +
			"awp_project=alpha\tawp_workspace=deleted-workspace\tawp_kind=agent",
	})
	f := src.sessions(false).facts("alpha", "deleted-workspace")
	if !f.present {
		t.Error("a session whose row is gone was not seen at all, so nothing can reap it")
	}
}

// TestAnUnlabelledSessionWithNoRowFallsBackToItsName, which is every session
// that existed before the labels did. Lossy for a shortened name, but a leftover
// filed under a shortened workspace is better than one the deck cannot see.
func TestAnUnlabelledSessionWithNoRowFallsBackToItsName(t *testing.T) {
	src := rowsAre(workspaceRef{project: "alpha", workspace: "still-here"})([]string{
		"name=awp.alpha.older-workspace.agent\tpid=42\tclients=0",
	})
	if f := src.sessions(false).facts("alpha", "older-workspace"); !f.present {
		t.Error("an unlabelled session with no row was dropped")
	}
}

// TestTheStemWinsOverTheLabels. A label is written by whoever created the
// session and can be stale — a workspace renamed since keeps the old label,
// because the labels live with the session and nothing rewrites them. The stem
// is generated from the row on this pass, so when both answer, the row wins.
func TestTheStemWinsOverTheLabels(t *testing.T) {
	ref := workspaceRef{project: "alpha", workspace: "renamed-to-this"}
	name := zmx.SessionName(ref.project, ref.workspace, "agent")
	src := rowsAre(ref)([]string{
		"name=" + name + "\tpid=42\tclients=1\t" +
			"awp_project=alpha\tawp_workspace=named-this-before\tawp_kind=agent",
	})
	snap := src.sessions(false)
	if !snap.facts(ref.project, ref.workspace).present {
		t.Error("the row's own session was filed under the stale label instead")
	}
	if snap.facts("alpha", "named-this-before").present {
		t.Error("a stale label claimed a session the row already accounted for")
	}
}

// TestWithNoRowsWiredEverySessionIsStillRead: the fallback is not a degraded
// mode to be avoided, it is what every caller that has no row list gets. A
// source with none must behave exactly as it did before rows existed.
func TestWithNoRowsWiredEverySessionIsStillRead(t *testing.T) {
	src, _ := zmxSource("name=awp.repo.qa.agent\tpid=42\tclients=1")
	if src.rows != nil {
		t.Fatal("this test is meaningless if the source has rows")
	}
	if !src.sessions(false).agentRunning("repo", "qa") {
		t.Error("a source with no rows stopped seeing its sessions")
	}
}

// TestSomeoneElsesSessionIsStillIgnored. `zmx ls` lists every session on the
// machine. Matching forward must not turn a hand-started shell into a row.
func TestSomeoneElsesSessionIsStillIgnored(t *testing.T) {
	src := rowsAre(workspaceRef{project: "alpha", workspace: "qa"})([]string{
		"name=dev\tpid=42\tclients=1",
		"name=notes\tpid=43\tclients=0",
	})
	snap := src.sessions(false)
	if len(snap.byWorkspace) != 0 {
		t.Errorf("claimed %d session(s) awp did not create: %+v", len(snap.byWorkspace), snap.byWorkspace)
	}
}
