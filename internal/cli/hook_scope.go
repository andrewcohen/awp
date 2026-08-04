package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Which files a hook is entitled to an opinion about.
//
// Two hooks fire on every Edit / Write: the task gate, which refuses an edit with
// no task in_progress, and the dev-loop tracker, which reads one as progress on
// the current unit. Both used to take the tool's target as repo code whatever it
// pointed at — and it often isn't. Repairing a review record under ~/.awp is an
// edit to *state*, not to the change under review, and it is exactly what a
// debugging session has to do; being told to open a task for it first is the gate
// misfiring, and the tracker counting it as implementation work is the same
// mistake from the other side.
//
// So the session's working tree is the boundary. Inside it, an edit is the change
// being made and both hooks mean what they say. Outside it, neither hook has any
// standing: the file is not what the task is about and not what the loop is
// tracking.

// isEditTool reports whether a tool's payload names a file it is about to write.
// The two hooks agree on the list so they cannot end up scoping different sets.
func isEditTool(name string) bool {
	switch name {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

// editTargetPath pulls the target path out of an Edit / Write / MultiEdit
// (file_path) or NotebookEdit (notebook_path) tool_input.
func editTargetPath(raw json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(raw, &in)
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.NotebookPath
}

// hookScopeRoot is the working tree the payload is about.
//
// Claude reports the session's cwd in every hook payload, which is the tree the
// agent is working in — and the right answer even when the hook process itself was
// started somewhere else. The process cwd is the fallback for a payload that
// predates the field or omits it.
func hookScopeRoot(payloadCWD string) string {
	if dir := strings.TrimSpace(payloadCWD); dir != "" {
		return dir
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// withinScope reports whether path belongs to the tree rooted at root.
//
// Unresolvable inputs answer *yes*, which is the conservative direction here: this
// governs whether a gate applies, so "cannot tell" has to leave the gate in place
// rather than hand out an exemption for every payload the hook failed to parse.
func withinScope(path, root string) bool {
	path, root = strings.TrimSpace(path), strings.TrimSpace(root)
	if path == "" || root == "" {
		return true
	}
	if !filepath.IsAbs(path) {
		// A relative target is relative to the session's cwd, so it is inside by
		// construction — but only after cleaning, since `../../elsewhere` is not.
		path = filepath.Join(root, path)
	}
	if pathUnder(path, root) {
		return true
	}
	// Compared again through symlinks, because either side may be reached by one —
	// macOS /tmp is /private/tmp, and a workspace can be linked into place. Only as
	// a second opinion: EvalSymlinks fails on a path that does not exist yet, which
	// a Write's target legitimately does not.
	realPath, errPath := filepath.EvalSymlinks(path)
	realRoot, errRoot := filepath.EvalSymlinks(root)
	if errPath != nil || errRoot != nil {
		return false
	}
	return pathUnder(realPath, realRoot)
}

// pathUnder reports whether path is root or sits beneath it.
func pathUnder(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
