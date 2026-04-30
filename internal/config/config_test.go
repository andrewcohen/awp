package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".awp")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func isolateGlobalConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestLoadActionsFromProjectConfig(t *testing.T) {
	isolateGlobalConfig(t)
	repo := t.TempDir()
	writeConfig(t, repo, `{
		"actions": {
			"dev": {"command": "pnpm dev", "alias": "d"},
			"lint": {"command": "pnpm lint", "alias": "l"}
		}
	}`)
	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	dev := cfg.Actions["dev"]
	if dev.Command != "pnpm dev" || dev.Alias != "d" {
		t.Fatalf("unexpected dev action: %+v", dev)
	}
}

func TestLoadMissingConfigReturnsEmpty(t *testing.T) {
	isolateGlobalConfig(t)
	repo := t.TempDir()
	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Actions) != 0 {
		t.Fatalf("expected no actions, got %d", len(cfg.Actions))
	}
}

func TestMergeProjectWinsOverGlobal(t *testing.T) {
	global := Config{
		Actions: map[string]UserAction{
			"dev":  {Command: "npm dev", Alias: "d"},
			"test": {Command: "npm test", Alias: "t"},
		},
	}
	project := Config{
		Actions: map[string]UserAction{
			"dev": {Command: "pnpm dev", Alias: "d"},
		},
	}
	merged := merge(global, project)
	if merged.Actions["dev"].Command != "pnpm dev" {
		t.Fatalf("project should win, got %q", merged.Actions["dev"].Command)
	}
	if merged.Actions["test"].Command != "npm test" {
		t.Fatal("global test action should be included")
	}
}
