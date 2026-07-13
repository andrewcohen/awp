package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/config"
)

// --- transcript builders ----------------------------------------------------

const ts = "2026-07-13T10:00:00Z"

func line(typ string, content ...any) string {
	m := map[string]any{
		"type":      typ,
		"timestamp": ts,
		"message":   map[string]any{"content": content},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func tu(name, id string, input map[string]any) any {
	return map[string]any{"type": "tool_use", "name": name, "id": id, "input": input}
}

func tr(id string, isErr bool) any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isErr}
}

func txt(s string) any { return map[string]any{"type": "text", "text": s} }

func transcript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func build(t *testing.T, lines ...string) State {
	t.Helper()
	st, err := BuildState(DefaultLoop(), transcript(t, lines...), "working", time.Now())
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	return st
}

func gate(st State, name string) GateState {
	for _, g := range st.Gates {
		if g.Name == name {
			return g
		}
	}
	return GateState{}
}

func loopGate(t *testing.T, name string) Gate {
	t.Helper()
	for _, g := range DefaultLoop().Gates {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("no gate %q in default loop", name)
	return Gate{}
}

// --- tests ------------------------------------------------------------------

func TestResolveAndIsConfigured(t *testing.T) {
	if IsConfigured(config.Config{}) {
		t.Fatal("empty config should not be configured")
	}
	if got := Resolve(config.Config{}); len(got.Phases) != 5 {
		t.Fatalf("default loop should have 5 phases, got %d", len(got.Phases))
	}

	var cfg config.Config
	cfg.DevLoop.Gates = []config.DevLoopGate{{Name: "check", Phase: "gates", Match: "make check"}}
	if !IsConfigured(cfg) {
		t.Fatal("config with a gate should be configured")
	}
	got := Resolve(cfg)
	if len(got.Gates) != 1 || got.Gates[0].Name != "check" {
		t.Fatalf("expected the configured gate, got %+v", got.Gates)
	}
	if len(got.Phases) == 0 {
		t.Fatal("gates-only config should still get default phases")
	}
}

func TestCommitGateExcludesWip(t *testing.T) {
	commit := loopGate(t, "commit")
	if !commit.Matches(`jj commit -m "feat: add thing"`) {
		t.Fatal("commit gate should match a real commit")
	}
	if commit.Matches(`jj describe -m "wip: scratch"`) {
		t.Fatal("commit gate should NOT match a wip commit")
	}
	if !commit.Marker {
		t.Fatal("commit gate should be a marker (no gate light)")
	}
}

func TestBuildStateGatePassFail(t *testing.T) {
	st := build(t,
		line("assistant", tu("Bash", "b1", map[string]any{"command": "gofmt -w ."})),
		line("user", tr("b1", false)),
		line("assistant", tu("Bash", "b2", map[string]any{"command": "go test ./..."})),
		line("user", tr("b2", true)),
	)
	if g := gate(st, "fmt"); g.Result != "pass" {
		t.Fatalf("fmt gate: want pass, got %q", g.Result)
	}
	if g := gate(st, "test"); g.Result != "fail" || g.RedCount != 1 {
		t.Fatalf("test gate: want fail/1, got %q/%d", g.Result, g.RedCount)
	}
	if st.CurrentPhase != "test" {
		t.Fatalf("phase: want test, got %q", st.CurrentPhase)
	}
}

func TestPerUnitGateReset(t *testing.T) {
	st := build(t,
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "Unit A"})),
		line("assistant", tu("TaskCreate", "c2", map[string]any{"subject": "Unit B"})),
		line("assistant", tu("TaskUpdate", "u1", map[string]any{"taskId": "1", "status": "in_progress"})),
		line("assistant", tu("Bash", "b1", map[string]any{"command": "go build ./..."})),
		line("user", tr("b1", false)),
		line("assistant", tu("TaskUpdate", "u2", map[string]any{"taskId": "1", "status": "completed"})),
		line("assistant", tu("TaskUpdate", "u3", map[string]any{"taskId": "2", "status": "in_progress"})),
	)
	// Unit B is current; Unit A's build gate must NOT carry over.
	if g := gate(st, "build"); g.Result != "" {
		t.Fatalf("build gate should reset for new unit, got %q", g.Result)
	}
	if len(st.Todos) != 2 {
		t.Fatalf("want 2 todos, got %d", len(st.Todos))
	}
	if st.Todos[0].Status != "completed" || st.Todos[1].Status != "in_progress" {
		t.Fatalf("todo statuses wrong: %+v", st.Todos)
	}
}

func TestTaskListReconstruction(t *testing.T) {
	st := build(t,
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "first"})),
		line("assistant", tu("TaskCreate", "c2", map[string]any{"subject": "second"})),
		line("assistant", tu("TaskUpdate", "u1", map[string]any{"taskId": "1", "status": "completed"})),
		line("assistant", tu("TaskUpdate", "u2", map[string]any{"taskId": "2", "subject": "second (renamed)"})),
	)
	if len(st.Todos) != 2 {
		t.Fatalf("want 2 todos, got %d: %+v", len(st.Todos), st.Todos)
	}
	if st.Todos[0].Content != "first" || st.Todos[0].Status != "completed" {
		t.Fatalf("todo 0 wrong: %+v", st.Todos[0])
	}
	if st.Todos[1].Content != "second (renamed)" {
		t.Fatalf("todo 1 rename not applied: %+v", st.Todos[1])
	}
}

func TestChecklistFallback(t *testing.T) {
	st := build(t,
		line("assistant", txt("Plan:\n- [x] wire it up\n- [ ] add tests\n- [~] docs")),
	)
	if len(st.Todos) != 3 {
		t.Fatalf("want 3 todos from checklist, got %d: %+v", len(st.Todos), st.Todos)
	}
	if st.Todos[0].Status != "completed" || st.Todos[1].Status != "pending" || st.Todos[2].Status != "in_progress" {
		t.Fatalf("checklist statuses wrong: %+v", st.Todos)
	}
}

func TestUnitAnnounceFallback(t *testing.T) {
	st := build(t,
		line("assistant", txt("Unit 1: scaffolding")),
		line("assistant", txt("Unit 2: wiring")),
	)
	if len(st.Todos) != 2 {
		t.Fatalf("want 2 units, got %d: %+v", len(st.Todos), st.Todos)
	}
	if st.Todos[0].Status != "completed" || st.Todos[1].Status != "in_progress" {
		t.Fatalf("unit statuses wrong: %+v", st.Todos)
	}
}

func TestTaskToolBeatsProseUnits(t *testing.T) {
	// When the task tool is used, prose "Unit N:" mentions are ignored.
	st := build(t,
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "real task"})),
		line("assistant", txt("Unit 9: this is just commentary")),
	)
	if len(st.Todos) != 1 || st.Todos[0].Content != "real task" {
		t.Fatalf("task tool should win over prose units: %+v", st.Todos)
	}
}

func TestStickyChoice(t *testing.T) {
	now := time.Date(2026, 7, 13, 14, 0, 0, 0, time.UTC)
	// Current session still active → stay, even though a different file is newest.
	if got := stickyChoice("a.jsonl", "b.jsonl", now.Add(-1*time.Second), now); got != "a.jsonl" {
		t.Fatalf("active current should be kept, got %q", got)
	}
	// Current session idle past the window → follow the newer file.
	if got := stickyChoice("a.jsonl", "b.jsonl", now.Add(-30*time.Second), now); got != "b.jsonl" {
		t.Fatalf("stale current should hand off, got %q", got)
	}
	// Newest is already current → no change.
	if got := stickyChoice("a.jsonl", "a.jsonl", now, now); got != "a.jsonl" {
		t.Fatalf("same file should stay, got %q", got)
	}
	// No current yet → take the newest.
	if got := stickyChoice("", "b.jsonl", time.Time{}, now); got != "b.jsonl" {
		t.Fatalf("empty current should take newest, got %q", got)
	}
}

func TestGeneratePreamble(t *testing.T) {
	p := GeneratePreamble(DefaultLoop())
	for _, want := range []string{"TaskCreate", "TaskUpdate", "gofmt", "go test", "wip:"} {
		if !strings.Contains(p, want) {
			t.Fatalf("preamble missing %q:\n%s", want, p)
		}
	}
	// Wrong/absent tool name and harness plumbing must not appear.
	for _, bad := range []string{"TodoWrite", "ToolSearch"} {
		if strings.Contains(p, bad) {
			t.Fatalf("preamble should not reference %q:\n%s", bad, p)
		}
	}
	if strings.Contains(p, "Unit N") {
		t.Fatalf("preamble should no longer instruct the Unit N: prose form:\n%s", p)
	}
}

func TestGeneratePreambleUsesCommandOverMatch(t *testing.T) {
	var cfg config.Config
	cfg.DevLoop.Gates = []config.DevLoopGate{
		{Name: "lint", Phase: "gates", Match: `pnpm lint\b`, Command: "pnpm lint <files you changed>"},
	}
	p := GeneratePreamble(Resolve(cfg))
	if !strings.Contains(p, "pnpm lint <files you changed>") {
		t.Fatalf("preamble should show the command, not the regex:\n%s", p)
	}
	if strings.Contains(p, `pnpm lint\b`) {
		t.Fatalf("preamble should not show the raw match regex:\n%s", p)
	}
}
