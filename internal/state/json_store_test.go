package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

func TestJSONStoreRoundTripGlobalPerRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoA := filepath.Join(t.TempDir(), "repo-a")
	repoB := filepath.Join(t.TempDir(), "repo-b")
	store := NewJSONStore()

	entriesA := map[string]workspace.Entry{"foo": {Name: "foo", Path: "/tmp/foo"}}
	entriesB := map[string]workspace.Entry{"bar": {Name: "bar", Path: "/tmp/bar"}}

	if err := store.Save(repoA, entriesA); err != nil {
		t.Fatalf("Save repoA returned error: %v", err)
	}
	if err := store.Save(repoB, entriesB); err != nil {
		t.Fatalf("Save repoB returned error: %v", err)
	}

	gotA, err := store.Load(repoA)
	if err != nil {
		t.Fatalf("Load repoA returned error: %v", err)
	}
	if gotA["foo"].Path != "/tmp/foo" {
		t.Fatalf("unexpected repoA entry: %+v", gotA)
	}

	gotB, err := store.Load(repoB)
	if err != nil {
		t.Fatalf("Load repoB returned error: %v", err)
	}
	if gotB["bar"].Path != "/tmp/bar" {
		t.Fatalf("unexpected repoB entry: %+v", gotB)
	}

	globalPath := filepath.Join(home, ".awp", "workspace-state.json")
	data, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("expected global state file: %v", err)
	}
	var state map[string]map[string]workspace.Entry
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse global state failed: %v", err)
	}
	if len(state) != 2 {
		t.Fatalf("expected 2 repos in global state, got %d", len(state))
	}
}

func TestJSONStoreLoadsLegacyLocalRepoState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := t.TempDir()
	legacyPath := filepath.Join(repo, ".awp", "workspace-state.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	legacy := map[string]workspace.Entry{"qa": {Name: "qa", Path: "/tmp/qa"}}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatalf("write legacy file failed: %v", err)
	}

	store := NewJSONStore()
	got, err := store.Load(repo)
	if err != nil {
		t.Fatalf("Load legacy returned error: %v", err)
	}
	if got["qa"].Path != "/tmp/qa" {
		t.Fatalf("unexpected legacy load result: %+v", got)
	}
}
