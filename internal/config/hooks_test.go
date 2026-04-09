package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPostWorkspaceStartMissingConfigReturnsNoHooks(t *testing.T) {
	repo := t.TempDir()
	provider := NewFileHookProvider()

	hooks, err := provider.PostWorkspaceStart(repo)
	if err != nil {
		t.Fatalf("PostWorkspaceStart returned error: %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("expected no hooks, got %+v", hooks)
	}
}

func TestPostWorkspaceStartParsesHookCommands(t *testing.T) {
	repo := t.TempDir()
	cfgDir := filepath.Join(repo, ".awp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := `{
  "hooks": {
    "bootstrap": [
      "cp <root>/.env .env",
      " ",
      "mise trust"
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	provider := NewFileHookProvider()
	hooks, err := provider.PostWorkspaceStart(repo)
	if err != nil {
		t.Fatalf("PostWorkspaceStart returned error: %v", err)
	}
	want := []string{"cp <root>/.env .env", "mise trust"}
	if !reflect.DeepEqual(hooks, want) {
		t.Fatalf("hooks = %#v, want %#v", hooks, want)
	}
}

func TestPostWorkspaceStartReturnsParseError(t *testing.T) {
	repo := t.TempDir()
	cfgDir := filepath.Join(repo, ".awp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"hooks":`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	provider := NewFileHookProvider()
	_, err := provider.PostWorkspaceStart(repo)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("expected parse config error, got %v", err)
	}
}
