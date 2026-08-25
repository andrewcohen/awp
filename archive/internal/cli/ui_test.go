package cli

import (
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// Standalone `awp diff` has to resolve the same review an agent filing from that
// directory would, or a finding lands in a review the viewer is not reading — the
// failure that cost a whole PR review on 2026-07-31.
func TestDiffSubjectResolvesTheWorkspaceFromCwd(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{
		{Name: "default", Path: "/repo"},
		{Name: "pr-54", Path: "/ws/pr-54", PRNumber: 54},
	}}
	// Inside the PR workspace: its own review, and the PR it is pinned to, so `P`
	// knows where to publish.
	got := diffSubjectFor(svc, "/repo", "/ws/pr-54/app")
	if got.WorkspaceName != "pr-54" || got.PRNumber != 54 {
		t.Fatalf("expected the pr-54 workspace, got %+v", got)
	}
	// The workspace's root, not the directory you happen to be standing in: the
	// review and the send-to-agent path both key off the workspace.
	if got.Path != "/ws/pr-54" {
		t.Fatalf("expected the workspace root as the path, got %q", got.Path)
	}
	// The project name keys the tmux session, so it has to be the repo's own name.
	if got.ProjectName != "repo" {
		t.Fatalf("expected the repo's name as the project, got %q", got.ProjectName)
	}
	// In the source repo it is the default workspace's review — which is exactly
	// what `awp review add` resolves there, so the two agree.
	if got := diffSubjectFor(svc, "/repo", "/repo/internal"); got.WorkspaceName != "default" {
		t.Fatalf("expected the default workspace in the source repo, got %+v", got)
	}
	// An untracked directory still yields a subject, so `awp diff` in a plain repo
	// is a review surface rather than one that refuses to comment at all.
	if got := diffSubjectFor(svc, "/elsewhere", "/elsewhere"); got.WorkspaceName != "" || got.RepoRoot != "/elsewhere" {
		t.Fatalf("expected a bare subject outside any workspace, got %+v", got)
	}
}
