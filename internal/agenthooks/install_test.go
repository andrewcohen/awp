package agenthooks

import (
	"strings"
	"testing"
)

func TestHookCommandUserPromptSubmitReadsStdin(t *testing.T) {
	cmd := HookCommand("UserPromptSubmit", "working")
	if !strings.Contains(cmd, "--prompt-stdin") {
		t.Errorf("UserPromptSubmit hook should include --prompt-stdin so the prompt text is captured; got %q", cmd)
	}
	if !strings.Contains(cmd, "--state working") {
		t.Errorf("UserPromptSubmit hook should still set --state working; got %q", cmd)
	}
}

func TestHookCommandOtherEventsDoNotReadStdin(t *testing.T) {
	for _, event := range []string{"Stop", "SessionStart", "PreToolUse", "PostToolUse", "Notification"} {
		cmd := HookCommand(event, "idle")
		if strings.Contains(cmd, "--prompt-stdin") {
			t.Errorf("event %q should not include --prompt-stdin; got %q", event, cmd)
		}
	}
}

func TestHookCommandPreToolUseDeclaresBlockingTools(t *testing.T) {
	cmd := HookCommand("PreToolUse", "working")
	if !strings.Contains(cmd, "--waiting-when-tool") {
		t.Errorf("PreToolUse hook should include --waiting-when-tool so AskUserQuestion flips state to waiting; got %q", cmd)
	}
	for _, tool := range BlockingTools {
		if !strings.Contains(cmd, tool) {
			t.Errorf("PreToolUse hook should mention blocking tool %q; got %q", tool, cmd)
		}
	}
}

func TestHookCommandOtherEventsOmitWaitingWhenTool(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "PostToolUse", "Stop", "SessionStart", "Notification"} {
		cmd := HookCommand(event, "working")
		if strings.Contains(cmd, "--waiting-when-tool") {
			t.Errorf("event %q should not include --waiting-when-tool; got %q", event, cmd)
		}
	}
}

func TestDesiredClaudeHooksIncludesPostToolUse(t *testing.T) {
	hooks := DesiredClaudeHooks()
	if state, ok := hooks["PostToolUse"]; !ok || state != "working" {
		t.Errorf("DesiredClaudeHooks PostToolUse = %q (ok=%v), want \"working\" so the row flips back from waiting after a blocking tool returns", state, ok)
	}
}

func TestDesiredClaudeHooksWaitingEvents(t *testing.T) {
	hooks := DesiredClaudeHooks()
	// PermissionRequest and Elicitation are the dedicated "blocked on the
	// user" events; both must flip the row to waiting so the deck shows
	// yellow while a permission dialog / MCP form is up.
	for _, event := range []string{"PermissionRequest", "Elicitation"} {
		if state, ok := hooks[event]; !ok || state != "waiting" {
			t.Errorf("DesiredClaudeHooks[%q] = %q (ok=%v), want \"waiting\"", event, state, ok)
		}
	}
}

func TestDesiredClaudeHooksOmitsNotification(t *testing.T) {
	// Notification fires for Claude's ~60s idle ping, not just permission
	// prompts, so mapping it to waiting flooded the unread summary with
	// false ▲ triangles for already-finished agents. Permission prompts are
	// covered by the dedicated PermissionRequest event instead.
	if state, ok := DesiredClaudeHooks()["Notification"]; ok {
		t.Errorf("DesiredClaudeHooks should not install Notification; got %q", state)
	}
	found := false
	for _, e := range ObsoleteClaudeHooks() {
		if e == "Notification" {
			found = true
		}
	}
	if !found {
		t.Error("ObsoleteClaudeHooks should list Notification so stale installs get cleaned up")
	}
}

func TestRemoveAwpEntryDropsOnlyAwpEntries(t *testing.T) {
	userEntry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo hi"}}}
	awpEntry := map[string]any{"x-awp": map[string]any{"version": float64(HookMarkerVersion), "state": "waiting"}}

	out, removed := removeAwpEntry([]any{userEntry, awpEntry})
	if !removed {
		t.Fatal("expected removeAwpEntry to report a removal")
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 surviving entry, got %d", len(out))
	}
	if _, isAwp := out[0].(map[string]any)["x-awp"]; isAwp {
		t.Error("the surviving entry should be the user's, not awp's")
	}

	if _, removed := removeAwpEntry([]any{userEntry}); removed {
		t.Error("removeAwpEntry should not report a removal when no awp entry is present")
	}
}

func TestHookMarkerVersionBumped(t *testing.T) {
	// Guard: dropping the version below 5 would let stale installs from
	// before the blocking-tool / PostToolUse rollout keep their older
	// entries, which lack the AskUserQuestion → waiting wiring.
	if HookMarkerVersion < 5 {
		t.Fatalf("HookMarkerVersion = %d, want >= 5 so existing installs re-write to pick up --waiting-when-tool", HookMarkerVersion)
	}
}
