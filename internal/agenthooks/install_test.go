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
	for _, event := range []string{"Stop", "SessionStart", "PreToolUse", "Notification"} {
		cmd := HookCommand(event, "idle")
		if strings.Contains(cmd, "--prompt-stdin") {
			t.Errorf("event %q should not include --prompt-stdin; got %q", event, cmd)
		}
	}
}

func TestHookMarkerVersionBumped(t *testing.T) {
	// Guard: dropping the version below 4 would let stale installs from
	// before the prompt-capture rollout keep their version-3 entries,
	// which lack --prompt-stdin on UserPromptSubmit.
	if HookMarkerVersion < 4 {
		t.Fatalf("HookMarkerVersion = %d, want >= 4 so existing installs re-write to capture prompts", HookMarkerVersion)
	}
}
