package cli

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// fakeStore is an in-memory reportStatusStore for tests. It satisfies the
// updater interface so writeWorkspaceStatus exercises the Update path.
type fakeStore struct {
	mu     sync.Mutex
	byRepo map[string]map[string]workspace.Entry
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

func TestReportStatusExitedClearsPrompt(t *testing.T) {
	const root = "/tmp/awp-test-repo"
	fs := newFakeStore()
	fs.byRepo[root] = map[string]workspace.Entry{
		"feat-x": {Name: "feat-x", Path: root + "/.jj/awp/feat-x", ActivePrompt: "prior prompt"},
	}
	withFakeStore(t, fs)
	withWorkspaceEnv(t, "feat-x", "awp-test-repo", root)

	if err := runReportStatus([]string{"--state", "exited"}, io.Discard); err != nil {
		t.Fatalf("runReportStatus: %v", err)
	}

	if got := fs.byRepo[root]["feat-x"]; got.ActivePrompt != "" {
		t.Errorf("ActivePrompt = %q, want cleared on exited", got.ActivePrompt)
	}
}

func TestReportStatusWaitingKeepsPrompt(t *testing.T) {
	// Notification ("waiting") fires while the agent is mid-task asking for
	// the user's attention. The prompt is still active context for the deck.
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

func TestRunReportStatusHelp(t *testing.T) {
	var out bytes.Buffer
	if err := runReportStatus([]string{"--help"}, &out); err != nil {
		t.Fatalf("--help returned err: %v", err)
	}
	if !strings.Contains(out.String(), "--prompt") {
		t.Errorf("usage should mention --prompt; got %q", out.String())
	}
}
