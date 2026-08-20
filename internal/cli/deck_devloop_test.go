package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/watch"
	"github.com/andrewcohen/awp/internal/workspace"
)

func TestDevLoopSnapshotEqual(t *testing.T) {
	a := &workspace.DevLoopSnapshot{Done: 3, Total: 7, Phase: "implement", Task: "x"}
	same := &workspace.DevLoopSnapshot{Done: 3, Total: 7, Phase: "implement", Task: "x"}
	diff := &workspace.DevLoopSnapshot{Done: 4, Total: 7, Phase: "implement", Task: "x"}

	cases := []struct {
		name string
		a, b *workspace.DevLoopSnapshot
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs value", nil, a, false},
		{"value vs nil", a, nil, false},
		{"equal values", a, same, true},
		{"different values", a, diff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := devLoopSnapshotEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("devLoopSnapshotEqual = %v, want %v", got, tc.want)
			}
		})
	}
}

// slugifyPath mirrors watch's transcript-dir naming so the fixture lands
// where watch.Locate looks for it.
func slugifyPath(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
}

// writeTranscript stands up a Claude Code transcript for workspacePath under
// the (temp) HOME and returns the workspace path to hand to the loader.
func writeTranscript(t *testing.T, home, workspacePath string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", slugifyPath(workspacePath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	p := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestBuildDevLoopSummary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := filepath.Join(t.TempDir(), "proj", "ws")

	// A completed unit, an in-progress unit, and a `go test` gate that moves
	// the loop into the verify phase.
	writeTranscript(t, home, ws,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":[{"content":"scaffold","status":"completed"},{"content":"wire meta line","status":"in_progress"}]}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b1","is_error":false}]}}`,
	)

	got := buildDevLoopSummary(watch.DefaultLoop(), ws, "working")
	if got == nil {
		t.Fatal("buildDevLoopSummary = nil, want a summary")
	}
	if got.Done != 1 || got.Total != 2 {
		t.Errorf("progress = %d/%d, want 1/2", got.Done, got.Total)
	}
	if got.Phase != "verify" {
		t.Errorf("phase = %q, want %q", got.Phase, "verify")
	}
	if got.Task != "wire meta line" {
		t.Errorf("task = %q, want %q", got.Task, "wire meta line")
	}
	// The passing `go test` gate is reconciled from the transcript into the
	// gate map so the event-driven snapshot self-heals.
	if got.Gates["test"] != "pass" {
		t.Errorf("reconciled gates = %v, want test=pass", got.Gates)
	}
	if _, ok := got.Gates["build"]; ok {
		t.Errorf("gates should omit unrun gates; got %v", got.Gates)
	}
}

func TestBuildDevLoopSummaryReconcilesFailingGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := filepath.Join(t.TempDir(), "proj", "ws-fail")
	writeTranscript(t, home, ws,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":[{"content":"wire meta line","status":"in_progress"}]}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go build ./..."}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b1","is_error":true}]}}`,
	)
	got := buildDevLoopSummary(watch.DefaultLoop(), ws, "working")
	if got == nil {
		t.Fatal("buildDevLoopSummary = nil, want a summary")
	}
	if got.Gates["build"] != "fail" {
		t.Errorf("reconciled gates = %v, want build=fail", got.Gates)
	}
}

func TestDevLoopSnapshotEqualGates(t *testing.T) {
	base := &workspace.DevLoopSnapshot{Done: 1, Gates: map[string]string{"test": "pass"}}
	same := &workspace.DevLoopSnapshot{Done: 1, Gates: map[string]string{"test": "pass"}}
	diffVal := &workspace.DevLoopSnapshot{Done: 1, Gates: map[string]string{"test": "fail"}}
	diffLen := &workspace.DevLoopSnapshot{Done: 1, Gates: map[string]string{"test": "pass", "build": "pass"}}
	diffKey := &workspace.DevLoopSnapshot{Done: 1, UnitKey: "2", Gates: map[string]string{"test": "pass"}}

	if !devLoopSnapshotEqual(base, same) {
		t.Error("equal gate maps should compare equal")
	}
	if devLoopSnapshotEqual(base, diffVal) {
		t.Error("different gate value should compare unequal")
	}
	if devLoopSnapshotEqual(base, diffLen) {
		t.Error("different gate count should compare unequal")
	}
	if devLoopSnapshotEqual(base, diffKey) {
		t.Error("different UnitKey should compare unequal")
	}
}

func TestBuildDevLoopSummaryNoTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := filepath.Join(t.TempDir(), "no", "transcript")
	if got := buildDevLoopSummary(watch.DefaultLoop(), ws, "working"); got != nil {
		t.Errorf("buildDevLoopSummary with no transcript = %+v, want nil", got)
	}
}

func TestBuildDevLoopSummaryEmptyStateIsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := filepath.Join(t.TempDir(), "empty", "ws")
	// A transcript with only a read (explore is not yet started, no todos,
	// no gates) yields nothing worth showing on a row.
	writeTranscript(t, home, ws,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"just some prose, no tools"}]}}`,
	)
	if got := buildDevLoopSummary(watch.DefaultLoop(), ws, "working"); got != nil {
		t.Errorf("buildDevLoopSummary with empty state = %+v, want nil", got)
	}
}

// TestSlowFoldDoesNotQueueTheNextRefresh is the bound on how many scans of one
// transcript can be outstanding at once.
//
// The scan fan-out is best-effort and bounded by deckEnrichTimeout, so a
// transcript that folds slower than the timeout leaves its goroutine running
// after the refresh has given up on it. The next refresh arrives
// refreshInterval later and asks for the same transcript. It used to block on
// the fold's mutex — a wait no timeout applies to — so every refresh for the
// duration of the slow fold added another waiter, one per workspace, unbounded.
//
// Now it declines. The measurement is the second caller's latency: queueing
// shows up as it taking as long as the fold it is waiting on.
func TestSlowFoldDoesNotQueueTheNextRefresh(t *testing.T) {
	e := &devLoopFoldEntry{}

	// Stand in for a fold in progress: hold the entry's lock the way the
	// goroutine doing the reading would.
	e.mu.Lock()
	released := make(chan struct{})
	go func() {
		<-released
		e.mu.Unlock()
	}()
	defer close(released)

	start := time.Now()
	_, err := e.state(watch.DefaultLoop(), "working")
	elapsed := time.Since(start)

	if !errors.Is(err, errFoldBusy) {
		t.Fatalf("state while a fold was running returned err=%v, want errFoldBusy — a second scan of one transcript should be declined, not queued", err)
	}
	// Generous, because the assertion is "did not wait for the fold" and the fold
	// here never finishes at all. Blocking would hang until the deferred close.
	if elapsed > time.Second {
		t.Fatalf("state took %s to decline — it queued behind the running fold instead of skipping it", elapsed)
	}
}
