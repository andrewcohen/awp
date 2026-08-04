package cli

import (
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

const loopConfigJSON = `{"dev_loop":{"phases":["explore","implement","verify","commit"],"gates":[
  {"name":"test","phase":"verify","match":"go test"},
  {"name":"build","phase":"verify","match":"go build"}
]}}`

// seedLoopWorkspace registers a workspace whose snapshot carries a given phase
// and task-list state so the track hook has something to compare against.
func seedLoopWorkspace(fs *fakeStore, root, ws, phase string, hasTasks bool) {
	fs.byRepo[root] = map[string]workspace.Entry{
		ws: {
			Name:    ws,
			Path:    filepath.Join(root, ws),
			DevLoop: &workspace.DevLoopSnapshot{Phase: phase, HasTasks: hasTasks},
		},
	}
}

func loopSnap(fs *fakeStore, root, ws string) *workspace.DevLoopSnapshot {
	return fs.byRepo[root][ws].DevLoop
}

// Before a task list exists, all activity — even editing a spec file — reads as
// explore (the pre-loop planning phase).
func TestLoopTrackEditBeforeTaskListStaysExplore(t *testing.T) {
	const root = "/tmp/awp-loop-spec"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "explore", false)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-spec", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"Edit","tool_input":{"file_path":"specs/foo-spec.md"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "explore" {
		t.Errorf("phase = %q, want explore (no task list yet)", got)
	}
}

// Creating the task list crosses out of explore into the implement loop.
func TestLoopTrackTaskCreateLeavesExplore(t *testing.T) {
	const root = "/tmp/awp-loop-create"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "explore", false)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-create", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"TaskCreate","tool_input":{"subject":"wire the meta line"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	snap := loopSnap(fs, root, "feat-x")
	if !snap.HasTasks {
		t.Errorf("HasTasks = false, want true after a TaskCreate")
	}
	if snap.Phase != "implement" {
		t.Errorf("phase = %q, want implement (left explore)", snap.Phase)
	}
}

// With a task list, an edit is implementation.
func TestLoopTrackEditWithTasksSetsImplement(t *testing.T) {
	const root = "/tmp/awp-loop-edit"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "verify", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-edit", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "implement" {
		t.Errorf("phase = %q, want implement", got)
	}
}

// An edit outside the session's tree is not work on the current unit, so it must
// not pull the phase back to implement — repairing a review record under ~/.awp
// while verifying is the case this was found on, and it would have looked like the
// unit had gone back to being written.
func TestLoopTrackIgnoresEditsOutsideTheSessionsTree(t *testing.T) {
	root := t.TempDir()
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "verify", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	withGateRepo(t, root, loopConfigJSON)

	outside := filepath.Join(t.TempDir(), "reviews", "alpha", "comments", "1785.json")
	withStdin(t, `{"cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"`+outside+`"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "verify" {
		t.Errorf("phase = %q, want verify (the edit was not in the tree)", got)
	}

	// The same edit inside the tree still counts, so this is a scope change rather
	// than the tracker having stopped noticing edits.
	withStdin(t, `{"cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"`+filepath.Join(root, "internal", "foo.go")+`"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "implement" {
		t.Errorf("phase = %q, want implement (the edit was in the tree)", got)
	}
}

// A gate command moves to that gate's phase once in the loop.
func TestLoopTrackGateBashSetsVerify(t *testing.T) {
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
	if got := loopSnap(fs, root, "feat-x").Phase; got != "verify" {
		t.Errorf("phase = %q, want verify", got)
	}
}

// A new unit going in_progress restarts the loop at implement (not explore).
func TestLoopTrackInProgressResetsToImplement(t *testing.T) {
	const root = "/tmp/awp-loop-reset"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "verify", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-reset", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"2","status":"in_progress","subject":"next"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "implement" {
		t.Errorf("phase = %q, want implement on a new unit", got)
	}
}

// Completing a unit resets the live phase toward implement for the next one.
func TestLoopTrackCompletedResetsToImplement(t *testing.T) {
	const root = "/tmp/awp-loop-complete"
	fs := newFakeStore()
	seedLoopWorkspace(fs, root, "feat-x", "verify", true)
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-loop-complete", root)
	withGateRepo(t, root, loopConfigJSON)

	withStdin(t, `{"tool_name":"TaskUpdate","tool_input":{"taskId":"1","status":"completed"}}`)
	if err := runLoopTrack(); err != nil {
		t.Fatalf("runLoopTrack: %v", err)
	}
	if got := loopSnap(fs, root, "feat-x").Phase; got != "implement" {
		t.Errorf("phase = %q, want implement after unit completion", got)
	}
}

func TestLoopTrackSkipsWriteWhenUnchanged(t *testing.T) {
	const root = "/tmp/awp-loop-noop"
	fs := newFakeStore()
	// Already implementing with a task list; another edit derives the same.
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
