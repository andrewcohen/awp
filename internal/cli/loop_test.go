package cli

import (
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// loopConfigJSON defines a loop with explicit phases so PhaseForTool's phase
// guard (hasPhase) resolves.
const loopConfigJSON = `{"dev_loop":{"phases":["explore","implement","test"],"gates":[
  {"name":"test","phase":"test","match":"go test"},
  {"name":"build","phase":"implement","match":"go build"}
]}}`

// seedLoopWorkspace registers a workspace whose snapshot carries a given
// phase/started so the track hook has something to compare against.
func seedLoopWorkspace(fs *fakeStore, root, ws, phase string, started bool) {
	fs.byRepo[root] = map[string]workspace.Entry{
		ws: {
			Name:    ws,
			Path:    filepath.Join(root, ws),
			DevLoop: &workspace.DevLoopSnapshot{Phase: phase, Started: started},
		},
	}
}

func loopSnap(fs *fakeStore, root, ws string) *workspace.DevLoopSnapshot {
	return fs.byRepo[root][ws].DevLoop
}

func TestLoopTrackEditSetsImplementPhase(t *testing.T) {
	const root = "/tmp/awp-loop-edit"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "", false)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-edit", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	snap := loopSnap(fs, root, "feat-x")
	if snap.Phase != "implement" {
		t.Errorf("phase = %q, want implement", snap.Phase)
	}
	if !snap.Started {
		t.Errorf("started = false, want true after an edit")
	}
}

func TestLoopTrackReadBeforeStartedSetsExplore(t *testing.T) {
	const root = "/tmp/awp-loop-read"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "", false)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-read", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"Read","tool_input":{"file_path":"internal/foo.go"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "explore" {
		t.Errorf("phase = %q, want explore", got)
	}
}

func TestLoopTrackGateBashSetsGatePhase(t *testing.T) {
	const root = "/tmp/awp-loop-gate"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "implement", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-gate", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "test" {
		t.Errorf("phase = %q, want test", got)
	}
}

func TestLoopTrackInProgressResetsPhase(t *testing.T) {
	const root = "/tmp/awp-loop-reset"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "implement", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-reset", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"2","status":"in_progress","subject":"next"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	snap := loopSnap(fs, root, "feat-x")
	if snap.Phase != "" {
		t.Errorf("phase = %q, want cleared on a new unit", snap.Phase)
	}
	if snap.Started {
		t.Errorf("started = true, want false on a new unit")
	}
}

func TestLoopTrackSkipsWriteWhenUnchanged(t *testing.T) {
	const root = "/tmp/awp-loop-noop"
	fs := newFakeStore()
	// Already implementing; another edit derives the same (implement, started).
	seedLoopWorkspace(fs, root, "feat-x", "implement", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-noop", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if fs.updates != 0 {
		t.Errorf("an unchanged phase should not write state; updates=%d", fs.updates)
	}
}
