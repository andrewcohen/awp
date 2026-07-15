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
	if got := Resolve(config.Config{}); len(got.Phases) != 4 {
		t.Fatalf("default loop should have 4 phases, got %d", len(got.Phases))
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
		// A task list moves past explore into the per-unit loop.
		line("assistant", tu("TaskCreate", "t1", map[string]any{"subject": "do the thing"})),
		line("assistant", tu("TaskUpdate", "t2", map[string]any{"taskId": "1", "status": "in_progress"})),
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
	if st.CurrentPhase != "verify" {
		t.Fatalf("phase: want verify, got %q", st.CurrentPhase)
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

func TestTaskCreateSkipsSubjectlessCreate(t *testing.T) {
	// An agent's first TaskCreate uses the batch {"tasks":[…]} form, which
	// has no top-level `subject` and fails validation (creates nothing). The
	// two real single-subject creates follow. The failed one must not mint a
	// phantom task or consume id 1 — otherwise TaskUpdate(taskId "1") would
	// target the phantom instead of the first real task.
	st := build(t,
		line("assistant", tu("TaskCreate", "c0", map[string]any{"tasks": `[{"content":"x","status":"pending"}]`})),
		line("user", tr("c0", true)),
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "first real"})),
		line("assistant", tu("TaskCreate", "c2", map[string]any{"subject": "second real"})),
		line("assistant", tu("TaskUpdate", "u1", map[string]any{"taskId": "1", "status": "in_progress"})),
	)
	if len(st.Todos) != 2 {
		t.Fatalf("want 2 todos (phantom skipped), got %d: %+v", len(st.Todos), st.Todos)
	}
	if st.Todos[0].Content != "first real" || st.Todos[0].Status != "in_progress" {
		t.Fatalf("taskId 1 should be the first real task, in_progress: %+v", st.Todos[0])
	}
	if st.Todos[1].Content != "second real" {
		t.Fatalf("todo 1 wrong: %+v", st.Todos[1])
	}
}

func TestImpliesCurrentUnitWhenWorkStarted(t *testing.T) {
	// Agent created one task but never marked it in_progress, then started
	// working (ran a gate). The task should be implied in_progress so the
	// loop/gates render under it.
	st := build(t,
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "Add brand Sentry tag"})),
		line("assistant", tu("Bash", "b1", map[string]any{"command": "go test ./..."})),
		line("user", tr("b1", false)),
	)
	if len(st.Todos) != 1 {
		t.Fatalf("want 1 todo, got %d", len(st.Todos))
	}
	if st.CurrentUnit() != 0 {
		t.Fatalf("want the single started task implied in_progress, got CurrentUnit=%d (%+v)", st.CurrentUnit(), st.Todos)
	}
}

func TestNoImpliedCurrentUnitBeforeWorkStarts(t *testing.T) {
	// Only a read (exploration) — nothing started — so a freshly created
	// task stays pending, not implied in_progress.
	st := build(t,
		line("assistant", tu("TaskCreate", "c1", map[string]any{"subject": "planned work"})),
		line("assistant", tu("Read", "r1", map[string]any{"file_path": "/x/y.go"})),
	)
	if st.CurrentUnit() != -1 {
		t.Fatalf("want no implied current unit during exploration, got %d (%+v)", st.CurrentUnit(), st.Todos)
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
	for _, want := range []string{"TaskCreate", "TaskUpdate", "gofmt", "go test"} {
		if !strings.Contains(p, want) {
			t.Fatalf("preamble missing %q:\n%s", want, p)
		}
	}
	// The standalone commit sentence was cut as redundant with the opening
	// "independently committable unit" line.
	if strings.Contains(p, "Commit each finished") {
		t.Fatalf("preamble should no longer carry the redundant commit sentence:\n%s", p)
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
