package cli

import (
	"errors"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// With no positional argument, resolveWatchTarget falls back to the workspace
// named by AWP_WORKSPACE (the session env) rather than prompting — so `awp
// watch` inside a workspace session (e.g. the deck's `W` window) picks it up.
func TestResolveWatchTargetUsesEnvWhenNoPositional(t *testing.T) {
	t.Setenv("AWP_WORKSPACE", "feat-x")
	a := &App{picker: func(string, []string) (string, error) {
		t.Fatal("picker should not be called when AWP_WORKSPACE resolves a workspace")
		return "", errors.New("unreachable")
	}}
	entries := []workspace.CrossRepoEntry{
		{ProjectName: "p", Name: "other", Status: "idle"},
		{ProjectName: "p", Name: "feat-x", Status: "working"},
	}
	got, err := a.resolveWatchTarget(nil, entries)
	if err != nil {
		t.Fatalf("resolveWatchTarget: %v", err)
	}
	if got.Name != "feat-x" {
		t.Errorf("resolved %q, want feat-x (from AWP_WORKSPACE)", got.Name)
	}
}
