package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// Resolving the workspace from the working directory — the third way to answer
// "which workspace am I in", and the only one nobody has to remember to inject
// (#216).

// recordedWorkspaces is a state file's worth of entries: a repo with its default at
// the root and two workspaces beneath it.
func recordedWorkspaces(root string) map[string]map[string]workspace.Entry {
	return map[string]map[string]workspace.Entry{
		root: {
			"default": {Name: "default", Path: root},
			"qa":      {Name: "qa", Path: filepath.Join(root, "qa")},
			"feat":    {Name: "feat", Path: filepath.Join(root, "feat")},
		},
	}
}

// TestTheWorkingDirectoryNamesTheWorkspace, which is the whole feature: an agent
// running in a workspace's directory can be identified without being told.
func TestTheWorkingDirectoryNamesTheWorkspace(t *testing.T) {
	root := t.TempDir()
	byRepo := recordedWorkspaces(root)

	name, repo, gotRoot, ok := workspaceForDir(filepath.Join(root, "qa"), byRepo)
	if !ok {
		t.Fatal("a workspace's own directory resolved to nothing")
	}
	if name != "qa" {
		t.Errorf("workspace is %q, want qa", name)
	}
	if repo != filepath.Base(root) {
		t.Errorf("repo is %q, want %q", repo, filepath.Base(root))
	}
	// The recorded root, in the spelling the state file uses — not the resolved one.
	// It is a lookup key: everything downstream matches it against the state's own
	// repo keys, so answering with a different spelling of the same directory would
	// find nothing.
	if gotRoot != root {
		t.Errorf("repo root is %q, want the recorded %q", gotRoot, root)
	}
}

// TestASubdirectoryOfAWorkspaceStillResolves. An agent does not stay at the top of
// its working copy — a gate runs where the code is.
func TestASubdirectoryOfAWorkspaceStillResolves(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "qa", "internal", "cli")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	name, _, _, ok := workspaceForDir(deep, recordedWorkspaces(root))
	if !ok || name != "qa" {
		t.Errorf("a subdirectory of qa resolved to %q (ok=%v), want qa", name, ok)
	}
}

// TestTheDeepestWorkspaceWins. The repo root is a workspace record too (`default`),
// and the others sit inside it — so a walk that stopped at the first match from the
// top would report every agent in the repo as `default`.
func TestTheDeepestWorkspaceWins(t *testing.T) {
	root := t.TempDir()
	byRepo := recordedWorkspaces(root)

	if name, _, _, _ := workspaceForDir(filepath.Join(root, "feat"), byRepo); name != "feat" {
		t.Errorf("the feat directory resolved to %q", name)
	}
	// And the root itself is still the default, not one of its children.
	if name, _, _, _ := workspaceForDir(root, byRepo); name != "default" {
		t.Errorf("the repo root resolved to %q, want default", name)
	}
}

// TestADirectoryOutsideEveryWorkspaceResolvesToNothing. Reporting *some* workspace
// for a shell in an unrelated tree would write status against a workspace the user
// is not in, which is worse than the silence this replaces.
func TestADirectoryOutsideEveryWorkspaceResolvesToNothing(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	if _, _, _, ok := workspaceForDir(elsewhere, recordedWorkspaces(root)); ok {
		t.Error("a directory in no workspace resolved to one anyway")
	}
}

// TestASymlinkedPathIsTheSameDirectory. On macOS a working copy under /tmp is
// recorded as /tmp/... and the process's cwd reads back as /private/tmp/... — the
// same directory in two spellings, which a string comparison calls two workspaces.
func TestASymlinkedPathIsTheSameDirectory(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "qa")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "via-a-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	// Recorded by its real path, entered through the link.
	name, _, _, ok := workspaceForDir(link, recordedWorkspaces(root))
	if !ok || name != "qa" {
		t.Errorf("entering qa through a symlink resolved to %q (ok=%v), want qa", name, ok)
	}
}

// TestNoRecordedWorkspacesResolvesToNothing, without walking to / and matching an
// empty path along the way.
func TestNoRecordedWorkspacesResolvesToNothing(t *testing.T) {
	if _, _, _, ok := workspaceForDir(t.TempDir(), nil); ok {
		t.Error("an empty state file resolved a workspace")
	}
	if _, _, _, ok := workspaceForDir("", recordedWorkspaces(t.TempDir())); ok {
		t.Error("an empty directory resolved a workspace")
	}
}

// TestAWorkspaceWithNoRecordedPathIsSkipped rather than matching everything: an
// empty path cleans to ".", which would otherwise be a live entry in the lookup.
func TestAWorkspaceWithNoRecordedPathIsSkipped(t *testing.T) {
	root := t.TempDir()
	byRepo := map[string]map[string]workspace.Entry{
		root: {"pathless": {Name: "pathless"}},
	}
	if _, _, _, ok := workspaceForDir(filepath.Join(root, "somewhere"), byRepo); ok {
		t.Error("an entry with no path matched a directory")
	}
}

// TestTheEnvironmentStillWinsWhenItHasAnAnswer. cwd is the fallback, not the
// authority: a shell cd'd into a workspace is in that directory without being that
// workspace's agent, and a launcher that named the workspace named it deliberately.
func TestTheEnvironmentStillWinsWhenItHasAnAnswer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root) // so the cwd leg reads an empty state file, not the developer's
	t.Setenv("AWP_WORKSPACE", "from-the-env")
	t.Setenv("AWP_REPO", "repo-from-the-env")
	t.Setenv("AWP_REPO_ROOT", root)
	t.Setenv("TMUX", "")

	name, repo, gotRoot := resolveWorkspaceIdent()
	if name != "from-the-env" {
		t.Errorf("workspace is %q, want from-the-env", name)
	}
	if repo != "repo-from-the-env" || gotRoot != root {
		t.Errorf("repo/root are %q/%q, want repo-from-the-env/%q", repo, gotRoot, root)
	}
}

// TestAnEmptyEnvironmentFallsBackToTheDirectory — the failure this closes. A zmx
// session created by an older awp has no AWP_WORKSPACE and never will, so without
// this leg it reports nothing for as long as it lives.
func TestAnEmptyEnvironmentFallsBackToTheDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AWP_WORKSPACE", "")
	t.Setenv("AWP_REPO", "")
	t.Setenv("AWP_REPO_ROOT", "")
	t.Setenv("TMUX", "")

	repoRoot := filepath.Join(home, "project")
	wsPath := filepath.Join(repoRoot, "qa")
	if err := os.MkdirAll(wsPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStateFile(t, home, repoRoot, map[string]workspace.Entry{
		"default": {Name: "default", Path: repoRoot},
		"qa":      {Name: "qa", Path: wsPath},
	})
	chdir(t, wsPath)

	name, repo, gotRoot := resolveWorkspaceIdent()
	if name != "qa" {
		t.Fatalf("workspace is %q, want qa — the cwd leg did not answer", name)
	}
	if repo != "project" {
		t.Errorf("repo is %q, want project", repo)
	}
	if gotRoot != repoRoot {
		t.Errorf("repo root is %q, want the recorded %q", gotRoot, repoRoot)
	}
}

// writeStateFile puts one repo's entries in the global state file under home, which
// is where the cwd leg reads them from.
func writeStateFile(t *testing.T, home, repoRoot string, entries map[string]workspace.Entry) {
	t.Helper()
	dir := filepath.Join(home, ".awp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]map[string]workspace.Entry{repoRoot: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace-state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
