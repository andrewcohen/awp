package cli

import (
	"bytes"
	"encoding/json"
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

func TestGateRecordWritesPass(t *testing.T) {
	const root = "/tmp/awp-gate-pass"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-pass", root)
	withGateRepo(t, root, gateConfigJSON)

	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord(nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}

	got := fs.byRepo[root]["feat-x"].DevLoop.Gates
	if got["test"] != "pass" {
		t.Errorf("gate test = %q, want pass (all gates: %v)", got["test"], got)
	}
}

func TestGateRecordWritesFailOnNonzeroExit(t *testing.T) {
	const root = "/tmp/awp-gate-fail"
	fs := newFakeStore()
	seedGateWorkspace(fs, root, "feat-x", nil)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-fail", root)
	withGateRepo(t, root, gateConfigJSON)

	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go build ./..."},"tool_response":{"is_error":true}}`)
	if err := runGateRecord(nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates["build"]; got != "fail" {
		t.Errorf("gate build = %q, want fail", got)
	}
}

func TestGateRecordNoUnitInProgressRecordsNothing(t *testing.T) {
	const root = "/tmp/awp-gate-nounit"
	fs := newFakeStore()
	// UnitKey empty → exploration, not a unit.
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: filepath.Join(root, "feat-x"), DevLoop: &workspace.DevLoopSnapshot{}},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-gate-nounit", root)
	withGateRepo(t, root, gateConfigJSON)

	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord(nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if got := fs.byRepo[root]["feat-x"].DevLoop.Gates; len(got) != 0 {
		t.Errorf("gates = %v, want empty (no unit in progress)", got)
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
	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"gofmt -l . && go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord(nil, &bytes.Buffer{}); err != nil {
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
	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"ls -la"},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord([]string{"--json"}, &out); err != nil {
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

	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord(nil, &bytes.Buffer{}); err != nil {
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
	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord([]string{"--json"}, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	var rep struct {
		Workspace       string            `json:"workspace"`
		Unit            string            `json:"unit"`
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
	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord(nil, &out); err != nil {
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
	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go build ./..."},"tool_response":{"exit_code":1}}`)
	if err := runGateRecord(nil, &out); err != nil {
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
	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."},"tool_response":{"exit_code":0}}`)
	if err := runGateRecord(nil, &out); err != nil {
		t.Fatalf("runGateRecord: %v", err)
	}
	if out.String() != "" {
		t.Errorf("nudge=off still printed: %q", out.String())
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
