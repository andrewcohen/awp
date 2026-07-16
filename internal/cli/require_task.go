package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrTaskRequired signals that the require-task gate denied an edit because no
// task is in_progress. main maps it to exit code 2 so Claude blocks the tool
// call and feeds the reason (already written to stderr) back to the agent —
// the same block mechanism as ErrGateBlocked.
var ErrTaskRequired = errors.New("task required")

// requireTaskPayload is the subset of a PreToolUse hook payload the
// require-task gate reads. session_id locates the session's task list; the
// tool name + input decide whether this edit is gated.
type requireTaskPayload struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// editPath pulls the target path out of an Edit/Write (file_path) or
// NotebookEdit (notebook_path) tool_input.
func (p requireTaskPayload) editPath() string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	_ = json.Unmarshal(p.ToolInput, &in)
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.NotebookPath
}

// runRequireTask implements `awp internal require-task --hook`: the
// PreToolUse(Edit|Write|NotebookEdit) hook that denies editing a non-markdown
// file unless the session has a task in_progress. It mirrors `gate check
// --hook`: a denial is a stderr reason + ErrTaskRequired (→ exit 2, which
// Claude treats as "block this tool call"); every other path returns nil
// (allow). It FAILS OPEN — an unreadable payload or missing task state allows
// the edit so a hook error can never wedge editing.
func runRequireTask(args []string, errOut io.Writer) error {
	for _, arg := range args {
		switch arg {
		case "--hook":
			// Only mode today; accepted for parity with `gate check --hook`.
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}

	payload, err := readRequireTaskPayload()
	if err != nil {
		return nil // unreadable payload → fail open
	}

	switch payload.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
	default:
		return nil // not an edit tool → allow
	}

	path := payload.editPath()
	if path == "" || isMarkdownPath(path) {
		return nil // markdown / no path → exempt
	}

	if payload.SessionID == "" {
		return nil // can't locate task state → fail open
	}

	if hasInProgressTask(payload.SessionID) {
		return nil // a task is in progress → allow
	}

	_, _ = fmt.Fprintln(errOut, requireTaskDenyReason(path))
	return ErrTaskRequired
}

// isMarkdownPath reports whether p is a markdown document, which is always
// exempt from the task gate (specs, READMEs, notes don't need a task).
func isMarkdownPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}

// hasInProgressTask reports whether any task in the session's task list has
// status in_progress. Task state lives at
// <claude-config>/tasks/<session_id>/<n>.json.
func hasInProgressTask(sessionID string) bool {
	dir := sessionTasksDir(sessionID)
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var t struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &t) == nil && t.Status == "in_progress" {
			return true
		}
	}
	return false
}

// sessionTasksDir returns the task-list directory for a session, honoring
// $CLAUDE_CONFIG_DIR (falling back to ~/.claude). Returns "" when the home
// dir can't be resolved so callers fail open.
func sessionTasksDir(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	base := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".claude")
	}
	return filepath.Join(base, "tasks", sessionID)
}

func requireTaskDenyReason(path string) string {
	return fmt.Sprintf("Edit blocked: no task is in_progress. Before editing %s, create a task (TaskCreate) and mark it in_progress (TaskUpdate), then retry this edit. Markdown files (.md/.markdown/.mdx) are exempt.", filepath.Base(path))
}

// readRequireTaskPayload decodes the hook JSON on stdin. Reuses the
// overridable reportStatusStdin reader so tests can feed a payload.
func readRequireTaskPayload() (requireTaskPayload, error) {
	data, err := io.ReadAll(reportStatusStdin())
	if err != nil || len(data) == 0 {
		return requireTaskPayload{}, err
	}
	var p requireTaskPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return requireTaskPayload{}, err
	}
	return p, nil
}
