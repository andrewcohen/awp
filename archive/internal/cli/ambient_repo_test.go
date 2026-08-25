package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// jjRootRunner answers `jj root` with a fixed directory and nothing else. (The
// review tests' rootRunner answers every probe with the root, which is the wrong
// shape here: this is about what happens *after* the root comes back.)
type jjRootRunner struct{ root string }

func (r jjRootRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	if name == "jj" && len(args) == 1 && args[0] == "root" {
		return r.root + "\n", nil
	}
	return "", nil
}

// Which project a command run inside a secondary workspace is about.
//
// `jj root` answers with the workspace's own directory, and a project's name is
// its root's basename — so `awp w label` confirmed the label against
// `<workspace>/<workspace>`, a project named after the workspace. Only the echo
// was wrong: workspace.Service resolves the source repo for itself, which is why
// the label landed in the right place while saying the wrong thing.
func TestAmbientRepoRootResolvesAWorkspaceToItsProject(t *testing.T) {
	project := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "some-feature")
	if err := os.MkdirAll(filepath.Join(workspace, ".jj"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// What jj writes in a secondary workspace: a pointer at the source repo's
	// store.
	pointer := filepath.Join(project, ".jj", "repo")
	if err := os.WriteFile(filepath.Join(workspace, ".jj", "repo"), []byte(pointer), 0o644); err != nil {
		t.Fatalf("write pointer: %v", err)
	}

	app := &App{runner: jjRootRunner{root: workspace}, out: &bytes.Buffer{}}
	got, err := app.ambientRepoRoot()
	if err != nil {
		t.Fatalf("ambientRepoRoot: %v", err)
	}
	if got != project {
		t.Errorf("ambientRepoRoot = %q, want the project %q", got, project)
	}
	if name := projectNameFor(got); name == filepath.Base(workspace) {
		t.Errorf("the project is named after the workspace (%q)", name)
	}
}

// A primary repo is its own source, so standing at the top of one is unaffected.
func TestAmbientRepoRootLeavesAPrimaryRepoAlone(t *testing.T) {
	repo := t.TempDir()
	app := &App{runner: jjRootRunner{root: repo}, out: &bytes.Buffer{}}
	got, err := app.ambientRepoRoot()
	if err != nil {
		t.Fatalf("ambientRepoRoot: %v", err)
	}
	if got != repo {
		t.Errorf("ambientRepoRoot = %q, want %q", got, repo)
	}
}
