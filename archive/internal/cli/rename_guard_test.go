package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
)

// TestRenameRefusesWhileTheAgentIsLiveInZmx.
//
// The rename guard asked tmux, and under a pane host tmux always answers
// "no session" — so the guard never fired and every step below it was skipped.
// The workspace was renamed while its zmx session kept the old name, and the
// next `a` computed the new name, found nothing, and started a SECOND agent
// beside a first still running with AWP_WORKSPACE frozen at a workspace that no
// longer existed.
//
// Refusing rather than renaming the session, because zmx has no rename: the
// alternative is killing the agent, which loses the context that is the point of
// the thing being renamed. Stopping it is the user's call.
func TestRenameRefusesWhileTheAgentIsLiveInZmx(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{names: []string{"awp.repo.qa.agent"}}
	svc := &deckFakeService{}
	err := handleDeckAction(tmux.New(r), svc, r, deckui.ActionRequest{
		Item:   deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo"},
		Action: deckui.ActionRename,
		Arg:    "qb",
	}, nil)
	if err == nil {
		t.Fatal("rename went ahead with a live zmx agent")
	}
	for _, want := range []string{"live agent", "awp.repo.qa.agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if svc.renameOld != "" || svc.renameNew != "" {
		t.Errorf("the workspace was renamed anyway: %q → %q", svc.renameOld, svc.renameNew)
	}
}

// TestRenameProceedsWhenTheZmxAgentHasExited: zmx keeps a session listed after
// its command exits, so "listed" is not "running". An ended session is not a
// live agent and must not block a rename forever.
func TestRenameProceedsWhenTheZmxAgentHasExited(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{ended: true, names: []string{"awp.repo.qa.agent"}}
	svc := &deckFakeService{}
	if err := handleDeckAction(tmux.New(r), svc, r, deckui.ActionRequest{
		Item:   deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo"},
		Action: deckui.ActionRename,
		Arg:    "qb",
	}, nil); err != nil {
		t.Fatalf("rename was blocked by an agent that had already exited: %v", err)
	}
	if svc.renameOld != "qa" || svc.renameNew != "qb" {
		t.Errorf("rename args %q → %q, want qa → qb", svc.renameOld, svc.renameNew)
	}
}

// TestRenameIgnoresAnotherWorkspacesAgent. The lookup is by the exact session
// name, so a sibling workspace's live agent is not this workspace's problem.
func TestRenameIgnoresAnotherWorkspacesAgent(t *testing.T) {
	requireZmx(t)
	r := &zmxLsRunner{names: []string{"awp.repo.other.agent", "awp.other.qa.agent"}}
	svc := &deckFakeService{}
	if err := handleDeckAction(tmux.New(r), svc, r, deckui.ActionRequest{
		Item:   deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo"},
		Action: deckui.ActionRename,
		Arg:    "qb",
	}, nil); err != nil {
		t.Fatalf("rename was blocked by someone else's agent: %v", err)
	}
	if svc.renameNew != "qb" {
		t.Errorf("rename did not happen; new=%q", svc.renameNew)
	}
}
