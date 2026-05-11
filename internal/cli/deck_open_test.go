package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

func TestOpenProjectViaTmuxRecordsDefaultWorkspaceEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "my-project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	runner := &deckFakeRunner{outs: map[string]string{}}
	if err := openProjectViaTmux(runner)(deckui.ProjectItem{Path: repo, Name: "my-project"}); err != nil {
		t.Fatalf("opener returned error: %v", err)
	}

	entries, err := state.NewJSONStore().Load(repo)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	got, ok := entries["default"]
	if !ok {
		t.Fatalf("expected default entry, got entries=%+v", entries)
	}
	if got.Name != "default" || got.Path != repo {
		t.Fatalf("unexpected default entry: %+v", got)
	}
}

func TestOpenProjectViaTmuxPreservesExistingEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "my-project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	store := state.NewJSONStore()
	seed := map[string]workspace.Entry{
		"default": {Name: "default", Path: repo, Bookmark: "main"},
	}
	if err := store.Save(repo, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runner := &deckFakeRunner{outs: map[string]string{}}
	if err := openProjectViaTmux(runner)(deckui.ProjectItem{Path: repo, Name: "my-project"}); err != nil {
		t.Fatalf("opener returned error: %v", err)
	}

	entries, err := store.Load(repo)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if entries["default"].Bookmark != "main" {
		t.Fatalf("clobbered existing entry: %+v", entries["default"])
	}
}
