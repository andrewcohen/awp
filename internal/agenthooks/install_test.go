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

// legacyAwpEntry builds a pre-marker awp hook entry: the report-status
// command awp installed before the x-awp marker (and bare-`awp`) existed,
// with no x-awp key. These are what accumulated as duplicates in real
// settings.json files because the marker-only match never recognized them.
func legacyAwpEntry(state string) map[string]any {
	return map[string]any{"hooks": []any{map[string]any{
		"type":    "command",
		"command": `[ -n "$TMUX" ] && awp internal report-status --state ` + state + ` >/dev/null 2>&1 || true`,
	}}}
}

// statusSpec is the matcher-less status-reporting spec for an event, matching
// what desiredHookSpecs builds.
func statusSpec(event, state string) hookSpec {
	return hookSpec{event: event, id: "status", state: state, command: HookCommand(event, state)}
}

func TestSyncEventEntriesDedupsLegacyEntries(t *testing.T) {
	event, state := "Stop", "idle"
	userEntry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo hi"}}}
	// Two legacy unmarked awp entries around a user entry — the shape that
	// accumulated in real installs.
	entries := []any{legacyAwpEntry(state), userEntry, legacyAwpEntry(state)}
	specs := []hookSpec{statusSpec(event, state)}

	out, changed := syncEventEntries(entries, specs)
	if !changed {
		t.Fatal("expected a change when collapsing duplicate awp entries")
	}
	awp, user := 0, 0
	for _, raw := range out {
		if isAwpEntry(raw.(map[string]any)) {
			awp++
		} else {
			user++
		}
	}
	if awp != 1 {
		t.Errorf("expected exactly 1 awp entry after dedup, got %d", awp)
	}
	if user != 1 {
		t.Errorf("expected the user entry to survive, got %d", user)
	}
	if !eventAwpEntriesMatch(out, specs) {
		t.Error("surviving awp entry should be the canonical (marked) one")
	}
}

func TestSyncEventEntriesNoChangeWhenCanonical(t *testing.T) {
	event, state := "Stop", "idle"
	specs := []hookSpec{statusSpec(event, state)}
	if _, changed := syncEventEntries([]any{desiredEntry(specs[0])}, specs); changed {
		t.Error("a single canonical entry should report no change (no needless rewrite)")
	}
}

func TestEventAwpEntriesMatchRejectsDuplicates(t *testing.T) {
	event, state := "Stop", "idle"
	specs := []hookSpec{statusSpec(event, state)}
	// Two canonical entries is still drift — IsClaudeInstalled must report
	// false so the next InstallClaude collapses them.
	entries := []any{desiredEntry(specs[0]), desiredEntry(specs[0])}
	if eventAwpEntriesMatch(entries, specs) {
		t.Error("duplicate awp entries should not count as a canonical install")
	}
}

func TestSyncEventEntriesInstallsGateHooksAlongsideStatus(t *testing.T) {
	// PostToolUse carries both the matcher-less status entry and the
	// Bash-matched gate-record entry; they must coexist.
	specs := specsByEvent()["PostToolUse"]
	if len(specs) != 2 {
		t.Fatalf("PostToolUse should have 2 awp specs (status + gate-record), got %d", len(specs))
	}
	out, changed := syncEventEntries(nil, specs)
	if !changed {
		t.Fatal("expected a change installing into an empty event")
	}
	ids := map[string]bool{}
	var gateEntry map[string]any
	for _, raw := range out {
		e := raw.(map[string]any)
		id := awpEntryID(e)
		ids[id] = true
		if id == "gate-record" {
			gateEntry = e
		}
	}
	if !ids["status"] || !ids["gate-record"] {
		t.Errorf("expected both status and gate-record entries, got ids %v", ids)
	}
	if gateEntry["matcher"] != "Bash" {
		t.Errorf("gate-record entry matcher = %v, want Bash", gateEntry["matcher"])
	}
	if cmd := entryCommand(gateEntry); !strings.Contains(cmd, "gate record") {
		t.Errorf("gate-record command = %q, want it to run `gate record`", cmd)
	}
	if !eventAwpEntriesMatch(out, specs) {
		t.Error("freshly synced entries should match the desired specs")
	}
}

func TestGateCheckSpecMatchesTaskUpdate(t *testing.T) {
	var found bool
	for _, s := range specsByEvent()["PreToolUse"] {
		if s.id == "gate-check" {
			found = true
			if s.matcher != "TaskUpdate" {
				t.Errorf("gate-check matcher = %q, want TaskUpdate", s.matcher)
			}
			if !strings.Contains(s.command, "gate check --hook") {
				t.Errorf("gate-check command = %q, want `gate check --hook`", s.command)
			}
		}
	}
	if !found {
		t.Error("PreToolUse should include a gate-check spec")
	}
}

func TestSyncEventEntriesDropsUpgradedStatusEntry(t *testing.T) {
	// A legacy status entry on PostToolUse should be rewritten to the new
	// id-tagged form, and the gate-record entry added — without duplicating.
	event := "PostToolUse"
	specs := specsByEvent()[event]
	out, _ := syncEventEntries([]any{legacyAwpEntry("working")}, specs)
	if !eventAwpEntriesMatch(out, specs) {
		t.Errorf("upgrading a legacy status entry should converge to canonical; got %v", out)
	}
}

func TestRemoveAwpEntryDropsLegacyUnmarkedEntries(t *testing.T) {
	out, removed := removeAwpEntry([]any{legacyAwpEntry("waiting")})
	if !removed {
		t.Fatal("expected removeAwpEntry to drop a legacy unmarked awp entry")
	}
	if len(out) != 0 {
		t.Errorf("expected no entries left, got %d", len(out))
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
