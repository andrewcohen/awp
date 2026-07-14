package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// withGateRepo writes a project dev_loop config at root/.awp/config.json and
// isolates the global config lookup to an empty temp dir so tests don't pick
// up the developer's real ~/.config/awp/config.json.
func withGateRepo(t *testing.T, root, configJSON string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".awp"), 0o755); err != nil {
		t.Fatalf("mkdir .awp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".awp", "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

const gateConfigJSON = `{"dev_loop":{"gates":[
  {"name":"fmt","match":"gofmt|go fmt"},
  {"name":"test","match":"go test"},
  {"name":"build","match":"go build"},
  {"name":"commit","match":"jj commit","marker":true}
]}}`

// seedGateWorkspace registers a workspace with an in-progress unit (UnitKey
// set) and optional pre-recorded gate results.
func seedGateWorkspace(fs *fakeStore, root, ws string, gates map[string]string) {
	fs.byRepo[root] = map[string]workspace.Entry{
		ws: {
			Name: ws,
			Path: filepath.Join(root, ws),
			DevLoop: &workspace.DevLoopSnapshot{
				Task:    "prompt plumbing",
				UnitKey: "1",
				Gates:   gates,
			},
		},
	}
}

func TestGateVerdict(t *testing.T) {
	cases := []struct{ flag, event, want string }{
		{"pass", "", "pass"},
		{"fail", "", "fail"},
		{"", "PostToolUse", "pass"},
		{"", "PostToolUseFailure", "fail"},
		{"", "", "pass"},                // default
		{"fail", "PostToolUse", "fail"}, // explicit flag wins
		{"pass", "PostToolUseFailure", "pass"},
	}
	for _, c := range cases {
		if got := gateVerdict(c.flag, c.event); got != c.want {
			t.Errorf("gateVerdict(%q,%q) = %q, want %q", c.flag, c.event, got, c.want)
		}
	}
}

func TestGateRecordWritesPass(t *testing.T) {
	const root = "/tmp/awp-gate-pass"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-pass", root)
	withGateRepo(t, root, gateConfigJSON)

	// PostToolUse fires on success → default verdict pass.
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["test"]; got != "pass" {
		t.Errorf("gate test = %q, want pass", got)
	}
}

func TestGateRecordSkipsWriteWhenUnchanged(t *testing.T) {
	const root = "/tmp/awp-gate-noop"
	fs := newFakeStore()
	// test already recorded pass for the current unit.
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"test": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-noop", root)
	withGateRepo(t, root, gateConfigJSON)

	// Re-running the passing test gate records the same value — no write.
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if fs.updates != 0 {
		t.Errorf("re-recording an unchanged gate result should not write state; updates=%d", fs.updates)
	}

	// A changed result (fail) does write.
	withStdin(t, `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "fail"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if fs.updates != 1 {
		t.Errorf("a changed gate result should write exactly once; updates=%d", fs.updates)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["test"]; got != "fail" {
		t.Errorf("gate test = %q, want fail", got)
	}
}

func TestGateRecordWritesFailViaResultFlag(t *testing.T) {
	const root = "/tmp/awp-gate-fail-flag"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-fail-flag", root)
	withGateRepo(t, root, gateConfigJSON)

	// The PostToolUseFailure hook passes --result fail.
	withStdin(t, `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"go build ./..."}}`)
	if err := runGateRecord([]string{"--result", "fail"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["build"]; got != "fail" {
		t.Errorf("gate build = %q, want fail", got)
	}
}

func TestGateRecordFailViaEventNameFallback(t *testing.T) {
	const root = "/tmp/awp-gate-fail-event"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-fail-event", root)
	withGateRepo(t, root, gateConfigJSON)

	// No --result flag: verdict falls back to hook_event_name.
	withStdin(t, `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"go build ./..."}}`)
	if err := runGateRecord(nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["build"]; got != "fail" {
		t.Errorf("gate build = %q, want fail (from hook_event_name)", got)
	}
}

func TestGateRecordWithoutInProgressStillRecords(t *testing.T) {
	const root = "/tmp/awp-gate-nounit"
	fs := newFakeStore()
	// No UnitKey: the agent never marked a task in_progress — a common lapse.
	// Recording must still happen, otherwise the completion check later blocks
	// on gates that actually passed.
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: filepath.Join(root, "feat-x"), DevLoop: &workspace.DevLoopSnapshot{}},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nounit", root)
	withGateRepo(t, root, gateConfigJSON)

	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["test"]; got != "pass" {
		t.Errorf("gate test = %q, want pass (recorded even without in_progress)", got)
	}
}

func TestGateRecordCreatesSnapshotWhenAbsent(t *testing.T) {
	const root = "/tmp/awp-gate-nosnap"
	fs := newFakeStore()
	// Entry exists but has no DevLoop snapshot yet.
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: filepath.Join(root, "feat-x")},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nosnap", root)
	withGateRepo(t, root, gateConfigJSON)

	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	dl := fs.byRepo[root]["feat-x"].DevLoop
	if dl == nil || dl.Gates["test"] != "pass" {
		t.Errorf("expected a snapshot with test=pass, got %+v", dl)
	}
}

func TestGateRecordCompoundCommandMatchesFirstGate(t *testing.T) {
	const root = "/tmp/awp-gate-compound"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-compound", root)
	withGateRepo(t, root, gateConfigJSON)

	// gofmt matches "fmt"; go test matches "test". First gate in loop order
	// (fmt) wins; only it is recorded.
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gofmt -l . && go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	got := fs.byRepo[root]["feat-x"].DevLoop.Gates
	if got["fmt"] != "pass" {
		t.Errorf("gate fmt = %q, want pass", got["fmt"])
	}
	if _, ok := got["test"]; ok {
		t.Errorf("gate test should not be recorded from a compound command; gates=%v", got)
	}
}

func TestGateRecordNonGateCommandRecordsNothing(t *testing.T) {
	const root = "/tmp/awp-gate-nongate"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nongate", root)
	withGateRepo(t, root, gateConfigJSON)

	var out bytes.Buffer
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if err := runGateRecord([]string{"--json", "--result", "pass"}, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if len(fs.byRepo[root]["feat-x"].DevLoop.Gates) != 0 {
		t.Errorf("non-gate command recorded a gate: %v", fs.byRepo[root]["feat-x"].DevLoop.Gates)
	}
	var rep struct {
		MatchedGate *string `json:"matched_gate"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse --json: %v (%q)", err, out.String())
	}
	if rep.MatchedGate != nil {
		t.Errorf("matched_gate = %v, want null", *rep.MatchedGate)
	}
}

func TestGateRecordNoDevLoopNoOp(t *testing.T) {
	const root = "/tmp/awp-gate-unconfigured"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-unconfigured", root)
	// No dev_loop block → hooks no-op.
	withGateRepo(t, root, `{}`)

	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if len(fs.byRepo[root]["feat-x"].DevLoop.Gates) != 0 {
		t.Errorf("unconfigured repo recorded a gate: %v", fs.byRepo[root]["feat-x"].DevLoop.Gates)
	}
}

func TestGateRecordJSONReport(t *testing.T) {
	const root = "/tmp/awp-gate-json"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-json", root)
	withGateRepo(t, root, gateConfigJSON)

	var out bytes.Buffer
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--json", "--result", "pass"}, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	var rep struct {
		MatchedGate     *string           `json:"matched_gate"`
		Result          string            `json:"result"`
		UnitGates       map[string]string `json:"unit_gates"`
		ReadyToComplete bool              `json:"ready_to_complete"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse --json: %v (%q)", err, out.String())
	}
	if rep.MatchedGate == nil || *rep.MatchedGate != "test" {
		t.Errorf("matched_gate = %v, want test", rep.MatchedGate)
	}
	if rep.Result != "pass" {
		t.Errorf("result = %q, want pass", rep.Result)
	}
	if !rep.ReadyToComplete {
		t.Errorf("ready_to_complete = false, want true (fmt+build+test all pass); unit_gates=%v", rep.UnitGates)
	}
	if rep.UnitGates["test"] != "pass" {
		t.Errorf("unit_gates[test] = %q, want pass", rep.UnitGates["test"])
	}
}

func TestGateRecordNudgeAllGreen(t *testing.T) {
	const root = "/tmp/awp-gate-nudge-green"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nudge-green", root)
	withGateRepo(t, root, gateConfigJSON)

	var out bytes.Buffer
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	ctx := parseAdditionalContext(t, out.String())
	if ctx == "" || !contains(ctx, "all gates green") {
		t.Errorf("nudge = %q, want an all-green reminder", ctx)
	}
}

func TestGateRecordNudgeRedTransition(t *testing.T) {
	const root = "/tmp/awp-gate-nudge-red"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nudge-red", root)
	withGateRepo(t, root, gateConfigJSON)

	var out bytes.Buffer
	withStdin(t, `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"go build ./..."}}`)
	if err := runGateRecord([]string{"--result", "fail"}, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	ctx := parseAdditionalContext(t, out.String())
	if ctx == "" || !contains(ctx, "red") {
		t.Errorf("nudge = %q, want a red-gate reminder", ctx)
	}
}

func TestGateRecordNudgeOffSuppresses(t *testing.T) {
	const root = "/tmp/awp-gate-nudge-off"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nudge-off", root)
	withGateRepo(t, root, `{"dev_loop":{"nudge":"off","gates":[
	  {"name":"fmt","match":"gofmt|go fmt"},
	  {"name":"test","match":"go test"},
	  {"name":"build","match":"go build"}
	]}}`)

	var out bytes.Buffer
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if out.String() != "" {
		t.Errorf("nudge=off still printed: %q", out.String())
	}
}

func TestGateCheckHookInProgressResetsGates(t *testing.T) {
	const root = "/tmp/awp-gate-reset"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "test": "fail"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-reset", root)
	withGateRepo(t, root, gateConfigJSON)

	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"2","status":"in_progress","subject":"next unit"}}`)
	if err := runGateCheck([]string{"--hook"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateCheck: %v", err)
	}
	snap := fs.byRepo[root]["feat-x"].DevLoop
	if len(snap.Gates) != 0 {
		t.Errorf("gates after reset = %v, want empty", snap.Gates)
	}
	if snap.UnitKey != "2" {
		t.Errorf("UnitKey = %q, want 2", snap.UnitKey)
	}
	if snap.Task != "next unit" {
		t.Errorf("Task = %q, want %q", snap.Task, "next unit")
	}
}

func TestGateCheckHookSameUnitKeepsGates(t *testing.T) {
	const root = "/tmp/awp-gate-same-unit"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-same-unit", root)
	withGateRepo(t, root, gateConfigJSON)

	// UnitKey seeded as "1"; re-marking task 1 in_progress must be idempotent.
	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"in_progress"}}`)
	if err := runGateCheck([]string{"--hook"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateCheck: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["fmt"]; got != "pass" {
		t.Errorf("gate fmt = %q after re-marking same unit, want pass (kept)", got)
	}
}

// TestGateCompletionSealsAndNextGateResets covers the unit-boundary reset when
// the agent never marks the next unit in_progress: a green completion seals the
// gates (kept for idempotent re-complete), and the next recorded gate clears
// them so the new unit is gated on its own results only.
func TestGateCompletionSealsAndNextGateResets(t *testing.T) {
	const root = "/tmp/awp-gate-seal"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "test": "pass", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-seal", root)
	withGateRepo(t, root, gateConfigJSON)

	// Complete the unit — all gates green → allowed, and the unit is sealed.
	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"}}`)
	if err := runGateCheck([]string{"--hook"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("completed check: %v", err)
	}
	snap := fs.byRepo[root]["feat-x"].DevLoop
	if !snap.GatesSealed {
		t.Fatalf("expected GatesSealed after a green completion")
	}
	if len(snap.Gates) != 3 {
		t.Errorf("a sealed unit keeps its gates for idempotent re-complete; got %v", snap.Gates)
	}

	// Re-marking the same unit completed stays allowed (gates still present).
	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"}}`)
	if err := runGateCheck([]string{"--hook"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("idempotent re-complete should stay allowed: %v", err)
	}

	// The next unit runs a gate without ever marking in_progress: the sealed
	// results are cleared and only the new gate is recorded.
	withStdin(t, `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gofmt -l ."}}`)
	if err := runGateRecord([]string{"--result", "pass"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	snap = fs.byRepo[root]["feat-x"].DevLoop
	if snap.GatesSealed {
		t.Errorf("recording a gate should clear the seal")
	}
	if len(snap.Gates) != 1 || snap.Gates["fmt"] != "pass" {
		t.Errorf("next unit should start with only its own gate; got %v", snap.Gates)
	}
}

func TestGateCheckHookCompletedBlocksWhenRed(t *testing.T) {
	const root = "/tmp/awp-gate-deny"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "test": "fail", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-deny", root)
	withGateRepo(t, root, gateConfigJSON)

	var stderr bytes.Buffer
	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"}}`)
	err := runGateCheck([]string{"--hook"}, &bytes.Buffer{}, &stderr)
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("expected ErrGateBlocked, got %v", err)
	}
	reason := stderr.String()
	if !contains(reason, "test") || !contains(reason, "red") {
		t.Errorf("reason = %q, want it to name the red 'test' gate", reason)
	}
	if !contains(reason, "prompt plumbing") {
		t.Errorf("reason = %q, want it to name the unit", reason)
	}
}

func TestGateCheckHookCompletedBlocksWhenPending(t *testing.T) {
	const root = "/tmp/awp-gate-deny-pending"
	fs := newFakeStore()
	// fmt+test pass, build never ran (pending).
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "test": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-deny-pending", root)
	withGateRepo(t, root, gateConfigJSON)

	var stderr bytes.Buffer
	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"}}`)
	err := runGateCheck([]string{"--hook"}, &bytes.Buffer{}, &stderr)
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("expected ErrGateBlocked, got %v", err)
	}
	if !contains(stderr.String(), "build") || !contains(stderr.String(), "hasn't run") {
		t.Errorf("reason = %q, want it to flag the pending 'build' gate", stderr.String())
	}
}

func TestGateCheckHookCompletedAllowsWhenGreen(t *testing.T) {
	const root = "/tmp/awp-gate-allow"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "test": "pass", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-allow", root)
	withGateRepo(t, root, gateConfigJSON)

	var stdout, stderr bytes.Buffer
	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"}}`)
	if err := runGateCheck([]string{"--hook"}, &stdout, &stderr); err != nil {
		t.Fatalf("all green should allow (nil err), got %v", err)
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Errorf("all green should be silent; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestGateCheckSelfExitCodes(t *testing.T) {
	const root = "/tmp/awp-gate-self"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", map[string]string{"fmt": "pass", "test": "fail", "build": "pass"})
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-self", root)
	withGateRepo(t, root, gateConfigJSON)

	// Not ready → non-nil error (non-zero exit).
	withStdin(t, ``)
	if err := runGateCheck(nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("self-check with a red gate should return an error")
	}

	// All green → nil.
	fs.byRepo[root]["feat-x"].DevLoop.Gates["test"] = "pass"
	var out bytes.Buffer
	if err := runGateCheck(nil, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("self-check all-green should succeed: %v", err)
	}
	if !contains(out.String(), "ready to complete") {
		t.Errorf("self-check output = %q, want a ready confirmation", out.String())
	}
}

func parseAdditionalContext(t *testing.T, s string) string {
	t.Helper()
	if s == "" {
		return ""
	}
	var payload struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(s), &payload); err != nil {
		t.Fatalf("parse nudge JSON: %v (%q)", err, s)
	}
	return payload.HookSpecificOutput.AdditionalContext
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
