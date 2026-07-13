package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepoConfig(t *testing.T, json string) string {
	t.Helper()
	dir := t.TempDir()
	// Isolate global config + the ~/.awp preamble file from the host.
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	if err := os.MkdirAll(filepath.Join(dir, ".awp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".awp", "config.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodingAgentInvocationInjectsForClaude(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "claude",
		"dev_loop": {"phases": ["implement"], "gates": [{"name": "test", "phase": "implement", "match": "go test"}]}
	}`)
	got := codingAgentInvocation(dir)
	if !strings.Contains(got, "--append-system-prompt") {
		t.Fatalf("claude + configured dev_loop should inject the loop, got %q", got)
	}
	if !strings.Contains(got, "--append-system-prompt-file ") {
		t.Fatalf("preamble should be passed by file path, got %q", got)
	}
}

func TestCodingAgentInvocationSkipsNonClaude(t *testing.T) {
	dir := writeRepoConfig(t, `{
		"agent": "pi",
		"dev_loop": {"gates": [{"name": "test", "phase": "x", "match": "go test"}]}
	}`)
	if strings.Contains(codingAgentInvocation(dir), "--append-system-prompt") {
		t.Fatal("non-claude agent must not get --append-system-prompt")
	}
}

func TestCodingAgentInvocationSkipsUnconfigured(t *testing.T) {
	dir := writeRepoConfig(t, `{"agent": "claude"}`)
	if strings.Contains(codingAgentInvocation(dir), "--append-system-prompt") {
		t.Fatal("no dev_loop should mean no injection")
	}
}
