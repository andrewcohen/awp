package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedTasks writes task JSON files for a session under a temp CLAUDE_CONFIG_DIR
// and returns the session id. statuses maps a task id to its status.
func seedTasks(t *testing.T, statuses map[string]string) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", base)
	session := "sess-test"
	dir := filepath.Join(base, "tasks", session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	for id, status := range statuses {
		body := `{"id":"` + id + `","subject":"t` + id + `","status":"` + status + `"}`
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write task: %v", err)
		}
	}
	return session
}

func TestRequireTaskDeniesEditWithoutInProgress(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed", "2": "pending"})
	root := t.TempDir()
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	withGateRepo(t, root, gateConfigJSON)
	withStdin(t, `{"session_id":"`+session+`","tool_name":"Edit","tool_input":{"file_path":"/x/foo.go"}}`)
	var errBuf strings.Builder
	err := runRequireTask([]string{"--hook"}, &errBuf)
	if !errors.Is(err, ErrTaskRequired) {
		t.Fatalf("expected ErrTaskRequired, got %v", err)
	}
	if !strings.Contains(errBuf.String(), "no task is in_progress") {
		t.Errorf("deny reason missing explanation: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "foo.go") {
		t.Errorf("deny reason should name the file: %q", errBuf.String())
	}
}

func TestRequireTaskAllowsEditWithInProgress(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed", "2": "in_progress"})
	root := t.TempDir()
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	withGateRepo(t, root, gateConfigJSON)
	withStdin(t, `{"session_id":"`+session+`","tool_name":"Write","tool_input":{"file_path":"/x/foo.go"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("expected allow (nil), got %v", err)
	}
}

// A repo with no dev_loop configured must not block edits, even without an
// in_progress task — the task gate only enforces on repos that opted in, the
// same predicate (watch.IsConfigured) the gate hooks use.
func TestRequireTaskAllowsWithoutDevLoop(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed"}) // no in_progress
	root := t.TempDir()
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate global config; no project dev_loop
	withStdin(t, `{"session_id":"`+session+`","tool_name":"Edit","tool_input":{"file_path":"/x/foo.go"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("no dev_loop configured should allow the edit, got %v", err)
	}
}

func TestRequireTaskExemptsMarkdown(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed"}) // no in_progress
	for _, path := range []string{"/x/README.md", "/x/notes.markdown", "/x/doc.mdx"} {
		withStdin(t, `{"session_id":"`+session+`","tool_name":"Edit","tool_input":{"file_path":"`+path+`"}}`)
		if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
			t.Errorf("markdown %s should be exempt, got %v", path, err)
		}
	}
}

func TestRequireTaskIgnoresNonEditTools(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed"}) // no in_progress
	withStdin(t, `{"session_id":"`+session+`","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("non-edit tool should allow, got %v", err)
	}
}

func TestRequireTaskDeniesNotebookEdit(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed"})
	root := t.TempDir()
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	withGateRepo(t, root, gateConfigJSON)
	withStdin(t, `{"session_id":"`+session+`","tool_name":"NotebookEdit","tool_input":{"notebook_path":"/x/n.ipynb"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); !errors.Is(err, ErrTaskRequired) {
		t.Fatalf("expected ErrTaskRequired for notebook edit, got %v", err)
	}
}

func TestRequireTaskFailsOpenOnEmptyPayload(t *testing.T) {
	withStdin(t, "")
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("empty payload should fail open (allow), got %v", err)
	}
}

func TestRequireTaskFailsOpenWithoutSession(t *testing.T) {
	// No session_id → can't locate task state → allow rather than block.
	withStdin(t, `{"tool_name":"Edit","tool_input":{"file_path":"/x/foo.go"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("missing session_id should fail open, got %v", err)
	}
}

func TestRequireTaskRejectsUnknownArg(t *testing.T) {
	withStdin(t, "")
	if err := runRequireTask([]string{"--nope"}, io.Discard); err == nil {
		t.Fatal("expected error for unknown argument")
	}
}
