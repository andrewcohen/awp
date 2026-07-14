package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// fakeStore is an in-memory reportStatusStore for tests. It satisfies the
// updater interface so writeWorkspaceStatus exercises the Update path.
type fakeStore struct {
	mu      sync.Mutex
	byRepo  map[string]map[string]workspace.Entry
	updates int // count of Update calls, for asserting compare-and-skip
}

func newFakeStore() *fakeStore {
	return &fakeStore{byRepo: map[string]map[string]workspace.Entry{}}
}

func (f *fakeStore) Load(repoRoot string) (map[string]workspace.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := map[string]workspace.Entry{}
	for k, v := range f.byRepo[repoRoot] {
		cp[k] = v
	}
	return cp, nil
}

func (f *fakeStore) LoadAll() (map[string]map[string]workspace.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]workspace.Entry{}
	for repo, entries := range f.byRepo {
		cp := map[string]workspace.Entry{}
		for k, v := range entries {
			cp[k] = v
		}
		out[repo] = cp
	}
	return out, nil
}

func (f *fakeStore) Save(repoRoot string, entries map[string]workspace.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := map[string]workspace.Entry{}
	for k, v := range entries {
		cp[k] = v
	}
	f.byRepo[repoRoot] = cp
	return nil
}

func (f *fakeStore) Update(repoRoot string, fn func(map[string]workspace.Entry) map[string]workspace.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates++
	cur := map[string]workspace.Entry{}
	for k, v := range f.byRepo[repoRoot] {
		cur[k] = v
	}
	updated := fn(cur)
	cp := map[string]workspace.Entry{}
	for k, v := range updated {
		cp[k] = v
	}
	f.byRepo[repoRoot] = cp
	return nil
}

func withFakeStore(t *testing.T, fs *fakeStore) {
	t.Helper()
	prev := stateStore
	stateStore = func() reportStatusStore { return fs }
	t.Cleanup(func() { stateStore = prev })
}

func withStdin(t *testing.T, s string) {
	t.Helper()
	prev := reportStatusStdin
	reportStatusStdin = func() io.Reader { return strings.NewReader(s) }
	t.Cleanup(func() { reportStatusStdin = prev })
}

func withWorkspaceEnv(t *testing.T, name, repo, root string) {
	t.Helper()
	t.Setenv("AWP_WORKSPACE", name)
	t.Setenv("AWP_REPO", repo)
	t.Setenv("AWP_REPO_ROOT", root)
}

func withCWD(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %q: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestReportStatusPersistsPromptFromFlag(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)

	if err := runReportStatus([]string{"--state", "working", "--prompt", "build the thing"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	got := fs.byRepo[root]["feat-x"]
	if got.Status != "working" {
		t.Errorf("Status = %q, want %q", got.Status, "working")
	}
	if got.ActivePrompt != "build the thing" {
		t.Errorf("ActivePrompt = %q, want %q", got.ActivePrompt, "build the thing")
	}
}

func TestReportStatusPersistsPromptFromStdin(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)
	withStdin(t, `{"hook_event_name":"UserPromptSubmit","prompt":"refactor auth"}`)

	if err := runReportStatus([]string{"--state", "working", "--prompt-stdin"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	got := fs.byRepo[root]["feat-x"]
	if got.ActivePrompt != "refactor auth" {
		t.Errorf("ActivePrompt = %q, want %q", got.ActivePrompt, "refactor auth")
	}
}

func TestReportStatusIdleClearsPrompt(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x", ActivePrompt: "prior prompt"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)

	if err := runReportStatus([]string{"--state", "idle"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	got := fs.byRepo[root]["feat-x"]
	if got.Status != "idle" {
		t.Errorf("Status = %q, want %q", got.Status, "idle")
	}
	if got.ActivePrompt != "" {
		t.Errorf("ActivePrompt = %q, want cleared (idle means agent finished the turn)", got.ActivePrompt)
	}
}

func TestReportStatusExitedClearsPromptAndUnread(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x", ActivePrompt: "prior prompt", Unread: true},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)

	if err := runReportStatus([]string{"--state", "exited"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	got := fs.byRepo[root]["feat-x"]
	if got.ActivePrompt != "" {
		t.Errorf("ActivePrompt = %q, want cleared on exited", got.ActivePrompt)
	}
	if got.Unread {
		t.Error("Unread = true, want cleared on exited (agent gone, nothing to act on)")
	}
}

func TestReportStatusWaitingKeepsPrompt(t *testing.T) {
	// A "waiting" report (PermissionRequest / Elicitation / AskUserQuestion)
	// fires while the agent is mid-task asking for the user's attention. The
	// prompt is still active context for the deck.
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x", ActivePrompt: "prior prompt"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)

	if err := runReportStatus([]string{"--state", "waiting"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	if got := fs.byRepo[root]["feat-x"]; got.ActivePrompt != "prior prompt" {
		t.Errorf("ActivePrompt = %q, want preserved on waiting", got.ActivePrompt)
	}
}

func TestReportStatusUnknownArgErrors(t *testing.T) {
	err := runReportStatus([]string{"--state", "working", "--nope"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--nope") {
		t.Fatalf("expected unknown arg error, got %v", err)
	}
}

func TestReadPromptFromStdinMissingField(t *testing.T) {
	if _, ok := readPromptFromStdin(strings.NewReader(`{"hook_event_name":"Stop"}`)); ok {
		t.Fatalf("expected ok=false when prompt field missing")
	}
}

func TestReadPromptFromStdinMalformed(t *testing.T) {
	if _, ok := readPromptFromStdin(strings.NewReader(`not json`)); ok {
		t.Fatalf("expected ok=false for non-JSON input")
	}
}

func TestRunReportStatusValidatesState(t *testing.T) {
	if err := runReportStatus([]string{"--state", "exploded"}, io.Discard); err == nil {
		t.Fatal("expected invalid state error")
	}
	if err := runReportStatus(nil, io.Discard); err == nil {
		t.Fatal("expected missing --state error")
	}
}

func TestReportStatusWaitingWhenToolMatchesOverridesState(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)
	withStdin(t, `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion"}`)

	if err := runReportStatus([]string{"--state", "working", "--waiting-when-tool", "AskUserQuestion"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	got := fs.byRepo[root]["feat-x"]
	if got.Status != "waiting" {
		t.Errorf("Status = %q, want %q (override should kick in)", got.Status, "waiting")
	}
	if !got.Unread {
		t.Errorf("Unread = false, want true (waiting transition should badge the row)")
	}
}

func TestReportStatusWaitingWhenToolNonMatchKeepsState(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)
	withStdin(t, `{"hook_event_name":"PreToolUse","tool_name":"Read"}`)

	if err := runReportStatus([]string{"--state", "working", "--waiting-when-tool", "AskUserQuestion"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	if got := fs.byRepo[root]["feat-x"]; got.Status != "working" {
		t.Errorf("Status = %q, want %q (non-matching tool keeps --state value)", got.Status, "working")
	}
}

func TestReportStatusWaitingWhenToolEmptyStdinKeepsState(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)
	withStdin(t, ``)

	if err := runReportStatus([]string{"--state", "working", "--waiting-when-tool", "AskUserQuestion"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	if got := fs.byRepo[root]["feat-x"]; got.Status != "working" {
		t.Errorf("Status = %q, want %q (empty stdin must not crash or override)", got.Status, "working")
	}
}

func TestReportStatusStaleNameResolvesByCWD(t *testing.T) {
	// Regression: renaming a workspace previously orphaned the agent. The
	// running agent's process env froze AWP_WORKSPACE=<old>, so every hook
	// looked up an entry that no longer exists and the status update was
	// silently dropped. Rename preserves Entry.Path, so the CWD fallback
	// (which Claude's hook subprocesses inherit from Claude's CWD = the
	// workspace root) must route the report to the renamed entry.
	const root = "/tmp/awp-test-repo"
	wsPath := t.TempDir()
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"renamed": {Name: "renamed", Path: wsPath},
	}
	withFakeStore(t, fs)
	// Agent's env is stale: it was launched as "old-name" before the rename.
	withWorkspaceEnv(t, "old-name", "awp-test-repo", root)
	withCWD(t, wsPath)

	if err := runReportStatus([]string{"--state", "working"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	got := fs.byRepo[root]["renamed"]
	if got.Status != "working" {
		t.Errorf("renamed entry Status = %q, want %q (CWD fallback should have routed the stale name)", got.Status, "working")
	}
}

func TestRunReportStatusHelp(t *testing.T) {
	var out bytes.Buffer
	if err := runReportStatus([]string{"--help"}, &out); err != nil {
		t.Fatalf("--help returned err: %v", err)
	}
	if !strings.Contains(out.String(), "--prompt") {
		t.Errorf("usage should mention --prompt; got %q", out.String())
	}
}
