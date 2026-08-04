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
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"`+filepath.Join(root, "foo.go")+`"}}`)
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
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"Write","tool_input":{"file_path":"`+filepath.Join(root, "foo.go")+`"}}`)
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
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"NotebookEdit","tool_input":{"notebook_path":"`+filepath.Join(root, "n.ipynb")+`"}}`)
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

// The gate is about keeping changes to the session's tree attached to a task. A
// file outside it is state rather than the change being made — repairing a review
// record under ~/.awp is the case this was found on, and it is exactly what a
// debugging session has to do.
func TestRequireTaskAllowsEditsOutsideTheSessionsTree(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed"}) // no in_progress
	root := t.TempDir()
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	withGateRepo(t, root, gateConfigJSON)

	outside := filepath.Join(t.TempDir(), "reviews", "alpha", "comments", "1785.json")
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"`+outside+`"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("a file outside the session's tree should not be gated, got %v", err)
	}

	// Same gate, same session, a file inside: still blocked. Otherwise the fix
	// would read as working while having simply turned the gate off.
	inside := filepath.Join(root, "internal", "cli", "thing.go")
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"`+inside+`"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); !errors.Is(err, ErrTaskRequired) {
		t.Fatalf("a file inside the tree must still be gated, got %v", err)
	}
}

// The payload's cwd is the tree, not the hook process's own directory: the hook
// runs wherever it was launched from, and only the payload knows where the agent
// is working.
func TestRequireTaskScopesToThePayloadCWD(t *testing.T) {
	session := seedTasks(t, map[string]string{"1": "completed"})
	root := t.TempDir()
	withWorkspaceEnv(t, "feat-x", filepath.Base(root), root)
	withGateRepo(t, root, gateConfigJSON)

	// A relative target resolves against the payload's cwd, so it is inside.
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"internal/cli/thing.go"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); !errors.Is(err, ErrTaskRequired) {
		t.Fatalf("a relative path is inside the tree by construction, got %v", err)
	}
	// …but only after cleaning.
	withStdin(t, `{"session_id":"`+session+`","cwd":"`+root+`","tool_name":"Edit","tool_input":{"file_path":"../../elsewhere/thing.go"}}`)
	if err := runRequireTask([]string{"--hook"}, io.Discard); err != nil {
		t.Fatalf("a relative path that climbs out is outside the tree, got %v", err)
	}
}

func TestWithinScope(t *testing.T) {
	root := filepath.Join("/repo", "awp")
	cases := []struct {
		name, path, root string
		want             bool
	}{
		{"a file in the tree", filepath.Join(root, "internal", "a.go"), root, true},
		{"the root itself", root, root, true},
		{"a sibling directory", "/repo/other/a.go", root, false},
		{"a prefix that is not a parent", "/repo/awp-notes/a.go", root, false},
		{"home state", "/Users/andrewcohen/.awp/reviews/x/comments/1.json", root, false},
		// "Cannot tell" has to leave the gate in place: this decides whether a gate
		// applies, so an unparsed payload must not hand out an exemption.
		{"no root", "/repo/awp/a.go", "", true},
		{"no path", "", root, true},
	}
	for _, c := range cases {
		if got := withinScope(c.path, c.root); got != c.want {
			t.Errorf("%s: withinScope(%q, %q) = %v, want %v", c.name, c.path, c.root, got, c.want)
		}
	}
}

func TestRequireTaskRejectsUnknownArg(t *testing.T) {
	withStdin(t, "")
	if err := runRequireTask([]string{"--nope"}, io.Discard); err == nil {
		t.Fatal("expected error for unknown argument")
	}
}
