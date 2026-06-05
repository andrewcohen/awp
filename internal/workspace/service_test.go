package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntryUnmarshalReadsLegacyPROverride(t *testing.T) {
	// Old state file written before the rename uses "PROverride";
	// new code should still read it into PRNumber.
	var legacy Entry
	if err := json.Unmarshal([]byte(`{"Name":"ws","PROverride":17}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.PRNumber != 17 {
		t.Errorf("expected PRNumber=17 from legacy PROverride, got %d", legacy.PRNumber)
	}

	// New file uses PRNumber.
	var modern Entry
	if err := json.Unmarshal([]byte(`{"Name":"ws","PRNumber":42}`), &modern); err != nil {
		t.Fatalf("unmarshal modern: %v", err)
	}
	if modern.PRNumber != 42 {
		t.Errorf("expected PRNumber=42, got %d", modern.PRNumber)
	}

	// Both keys present: explicit PRNumber wins.
	var both Entry
	if err := json.Unmarshal([]byte(`{"Name":"ws","PRNumber":1,"PROverride":2}`), &both); err != nil {
		t.Fatalf("unmarshal both: %v", err)
	}
	if both.PRNumber != 1 {
		t.Errorf("expected PRNumber=1 to win over PROverride, got %d", both.PRNumber)
	}
}

func TestEntryMarshalEmitsPRNumberNotPROverride(t *testing.T) {
	e := Entry{Name: "ws", PRNumber: 7}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"PRNumber":7`) {
		t.Errorf("expected PRNumber in JSON, got %s", out)
	}
	if strings.Contains(string(out), `PROverride`) {
		t.Errorf("did not expect legacy PROverride in JSON, got %s", out)
	}
}

type fakeJJ struct {
	repoRoot             string
	repoRootErr          error
	existing             map[string]bool
	added                []Entry
	addRevision          string
	addErr               error
	addSetsExistingOnErr bool
	trackedBookmark      string
	renamedPath          string
	renamedTo            string
	forgotten            []string
	forgetErr            error
	renameErr            error
	workspaceErr         error
	trunk                string
	listNames            []string
	workspaceRevs        map[string]string
	bookmarksByRev       map[string][]string
	forgottenBookmarks   []string
	forgetBookmarkErrs   map[string][]error
	updateStaleErr       error
	updateStaleCalls     int
	emptyRevisions       map[string]bool
	abandoned            []string
	abandonErr           error
	revisionLookupErr    error
	newRevCalls          []newRevCall
	newRevErr            error
}

type newRevCall struct {
	Path     string
	Revision string
}

func (f *fakeJJ) RepoRoot() (string, error)       { return f.repoRoot, f.repoRootErr }
func (f *fakeJJ) SourceRepoRoot() (string, error) { return f.repoRoot, f.repoRootErr }
func (f *fakeJJ) WorkspaceExists(name string) (bool, error) {
	if f.workspaceErr != nil {
		return false, f.workspaceErr
	}
	return f.existing[name], nil
}
func (f *fakeJJ) ListWorkspaceNames() ([]string, error) {
	if f.listNames != nil {
		return f.listNames, nil
	}
	names := make([]string, 0, len(f.existing))
	for name := range f.existing {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakeJJ) AddWorkspace(name, path, revision string) error {
	f.added = append(f.added, Entry{Name: name, Path: path})
	f.addRevision = revision
	if f.addErr != nil {
		if f.addSetsExistingOnErr {
			f.existing[name] = true
		}
		return f.addErr
	}
	f.existing[name] = true
	return nil
}

func (f *fakeJJ) TrackBookmark(bookmarkName string) error {
	f.trackedBookmark = bookmarkName
	return nil
}

func (f *fakeJJ) NewOnRevision(path, revision string) error {
	if f.newRevErr != nil {
		return f.newRevErr
	}
	f.newRevCalls = append(f.newRevCalls, newRevCall{Path: path, Revision: revision})
	return nil
}
func (f *fakeJJ) RenameWorkspace(path, newName string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	f.renamedPath = path
	f.renamedTo = newName
	return nil
}
func (f *fakeJJ) ForgetWorkspace(name string) error {
	if f.forgetErr != nil {
		return f.forgetErr
	}
	f.forgotten = append(f.forgotten, name)
	return nil
}

func (f *fakeJJ) WorkspaceRevision(name string) (string, error) {
	if f.revisionLookupErr != nil {
		return "", f.revisionLookupErr
	}
	if rev, ok := f.workspaceRevs[name]; ok {
		return rev, nil
	}
	return "", nil
}

func (f *fakeJJ) BookmarksAtRevision(revision string) ([]string, error) {
	if f.bookmarksByRev == nil {
		return nil, nil
	}
	return append([]string(nil), f.bookmarksByRev[revision]...), nil
}

func (f *fakeJJ) Trunk() (string, error) {
	return f.trunk, nil
}

func (f *fakeJJ) ForgetBookmark(name string) error {
	if queue, ok := f.forgetBookmarkErrs[name]; ok && len(queue) > 0 {
		err := queue[0]
		f.forgetBookmarkErrs[name] = queue[1:]
		if err != nil {
			return err
		}
	}
	f.forgottenBookmarks = append(f.forgottenBookmarks, name)
	return nil
}

func (f *fakeJJ) UpdateStale() error {
	f.updateStaleCalls++
	return f.updateStaleErr
}

func (f *fakeJJ) IsRevisionEmpty(revision string) (bool, error) {
	if f.emptyRevisions == nil {
		return false, nil
	}
	return f.emptyRevisions[revision], nil
}

func (f *fakeJJ) AbandonRevision(revision string) error {
	if f.abandonErr != nil {
		return f.abandonErr
	}
	f.abandoned = append(f.abandoned, revision)
	return nil
}

type fakeTmux struct {
	windows     map[string]bool
	created     []Entry
	switched    []string
	sentWindow  string
	sentCommand string
	sendErr     error
	renamedOld  string
	renamedNew  string
	killed      []string
	current     string
}

func (f *fakeTmux) WindowExists(name string) (bool, error) { return f.windows[name], nil }
func (f *fakeTmux) NewWindow(name, dir string) error {
	f.windows[name] = true
	f.created = append(f.created, Entry{Name: name, Path: dir})
	return nil
}
func (f *fakeTmux) SendCommand(name, command string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sentWindow = name
	f.sentCommand = command
	return nil
}
func (f *fakeTmux) SwitchToWindow(name string) error {
	f.switched = append(f.switched, name)
	return nil
}
func (f *fakeTmux) RenameWindow(oldName, newName string) error {
	f.renamedOld, f.renamedNew = oldName, newName
	delete(f.windows, oldName)
	f.windows[newName] = true
	return nil
}
func (f *fakeTmux) KillWindow(name string) error {
	f.killed = append(f.killed, name)
	delete(f.windows, name)
	return nil
}
func (f *fakeTmux) CurrentWindow() (string, error) { return f.current, nil }

type fakeStore struct {
	entries map[string]Entry
}

type fakeHooks struct {
	commands []string
	err      error
	calls    int
	repoRoot string
}

func (f *fakeHooks) PostWorkspaceStart(repoRoot string) ([]string, error) {
	f.calls++
	f.repoRoot = repoRoot
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.commands...), nil
}

type runCall struct {
	dir  string
	name string
	args []string
}

type fakeRunner struct {
	calls    []runCall
	failCall int
	failOut  string
	failErr  error
}

func (f *fakeRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	f.calls = append(f.calls, runCall{dir: dir, name: name, args: append([]string(nil), args...)})
	if f.failCall > 0 && len(f.calls) == f.failCall {
		return f.failOut, f.failErr
	}
	return "", nil
}

func (f *fakeStore) Load(string) (map[string]Entry, error) {
	cp := map[string]Entry{}
	for k, v := range f.entries {
		cp[k] = v
	}
	return cp, nil
}
func (f *fakeStore) Save(_ string, entries map[string]Entry) error {
	f.entries = map[string]Entry{}
	for k, v := range entries {
		f.entries[k] = v
	}
	return nil
}

func TestStartCreatesWorkspaceAndWindow(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("Add Auth", "", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if len(jj.added) != 1 {
		t.Fatalf("expected 1 workspace add, got %d", len(jj.added))
	}
	if jj.added[0].Name != "add-auth" {
		t.Fatalf("workspace name = %q, want add-auth", jj.added[0].Name)
	}
	if jj.addRevision != "@" {
		t.Fatalf("expected default revision @, got %q", jj.addRevision)
	}
	if len(tmux.created) != 1 || tmux.created[0].Name != "add-auth" {
		t.Fatalf("expected tmux window add-auth, got %+v", tmux.created)
	}
	if len(tmux.switched) != 1 || tmux.switched[0] != "add-auth" {
		t.Fatalf("expected switch to add-auth, got %+v", tmux.switched)
	}
	entry, ok := store.entries["add-auth"]
	if !ok {
		t.Fatalf("store missing add-auth entry")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		expectedPrefix := filepath.Join(home, ".awp", "workspaces") + string(filepath.Separator)
		if !strings.HasPrefix(entry.Path, expectedPrefix) {
			t.Fatalf("expected workspace path under %q, got %q", expectedPrefix, entry.Path)
		}
	}
}

func TestStartWithBookmarkTracksBookmarkAndUsesBookmarkRevision(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("qa", "feature/qa", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if jj.addRevision != "feature/qa" {
		t.Fatalf("expected add revision feature/qa, got %q", jj.addRevision)
	}
	if jj.trackedBookmark != "feature/qa" {
		t.Fatalf("unexpected tracked bookmark: %q", jj.trackedBookmark)
	}
}

func TestStartUsesBookmarkAsDefaultNameWhenNameMissing(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	out := &bytes.Buffer{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: out})
	if err := svc.createWorkspace("", "feature/qa", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if strings.Contains(out.String(), "Name: ") {
		t.Fatalf("expected no name prompt output, got %q", out.String())
	}
	if len(jj.added) != 1 || jj.added[0].Name != "feature-qa" {
		t.Fatalf("expected workspace name feature-qa, got %+v", jj.added)
	}
	if jj.addRevision != "feature/qa" {
		t.Fatalf("expected add revision feature/qa, got %q", jj.addRevision)
	}
	if jj.trackedBookmark != "feature/qa" {
		t.Fatalf("unexpected tracked bookmark: %q", jj.trackedBookmark)
	}
}

func TestStartRemovesStaleManagedWorkspaceDir(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	home := t.TempDir()
	t.Setenv("HOME", home)
	repoName := filepath.Base(repoRoot)
	stalePath := filepath.Join(home, ".awp", "workspaces", repoName, "qa")
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatalf("mkdir stale path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stalePath, "junk.txt"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("qa", "", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if _, err := os.Stat(stalePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale path to be removed, stat err=%v", err)
	}
}

func TestStartPromptsForNameWhenMissing(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	in := bytes.NewBufferString("My Feature\n")
	out := &bytes.Buffer{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: in, Out: out})
	if err := svc.createWorkspace("", "", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Name: ") {
		t.Fatalf("expected prompt output to contain %q, got %q", "Name: ", out.String())
	}
	if _, ok := store.entries["my-feature"]; !ok {
		t.Fatal("expected normalized prompted name in store")
	}
}

func TestStartOpensExistingWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"foo": true}}
	tmux := &fakeTmux{windows: map[string]bool{"foo": true}}
	store := &fakeStore{entries: map[string]Entry{"foo": {Name: "foo", Path: filepath.Join(repoRoot, ".awp", "workspaces", "foo")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("foo", "", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if len(jj.added) != 0 {
		t.Fatalf("expected no new workspace create")
	}
	if len(tmux.switched) != 1 || tmux.switched[0] != "foo" {
		t.Fatalf("expected switch to foo, got %+v", tmux.switched)
	}
}

func TestStartExistingWorkspaceSetsBookmarkWhenProvided(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"foo": true}}
	tmux := &fakeTmux{windows: map[string]bool{"foo": true}}
	store := &fakeStore{entries: map[string]Entry{"foo": {Name: "foo", Path: filepath.Join(repoRoot, ".awp", "workspaces", "foo")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("foo", "feature/foo", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if jj.trackedBookmark != "feature/foo" {
		t.Fatalf("unexpected tracked bookmark: %q", jj.trackedBookmark)
	}
}

func TestCreateWorkspaceOpensWhenAddReportsAlreadyExists(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}, addErr: errors.New("Workspace named 'qa' already exists"), addSetsExistingOnErr: true}
	tmux := &fakeTmux{windows: map[string]bool{"qa": true}}
	store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: filepath.Join(repoRoot, ".awp", "workspaces", "qa")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("qa", "", "", true); err != nil {
		t.Fatalf("createWorkspace returned error: %v", err)
	}
	if len(tmux.switched) != 1 || tmux.switched[0] != "qa" {
		t.Fatalf("expected switch to qa, got %+v", tmux.switched)
	}
}

func TestStartRunsPostWorkspaceStartHooksForNewWorkspace(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	repoRoot := t.TempDir()
	invocationDir := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	hooks := &fakeHooks{commands: []string{"cp <root>/.env .env", "mise trust"}}
	runner := &fakeRunner{}

	svc := NewService(Dependencies{
		JJ:            jj,
		Tmux:          tmux,
		Store:         store,
		Hooks:         hooks,
		Runner:        runner,
		InvocationDir: invocationDir,
		Input:         bytes.NewBuffer(nil),
		Out:           io.Discard,
	})
	if err := svc.createWorkspace("qa", "", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if hooks.calls != 1 || hooks.repoRoot != repoRoot {
		t.Fatalf("unexpected hooks calls: calls=%d repoRoot=%q", hooks.calls, hooks.repoRoot)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 hook command runs, got %+v", runner.calls)
	}
	if runner.calls[0].name != "/bin/sh" || len(runner.calls[0].args) != 2 || runner.calls[0].args[0] != "-lc" {
		t.Fatalf("unexpected first runner call: %+v", runner.calls[0])
	}
	if got := runner.calls[0].args[1]; !strings.Contains(got, "cp "+invocationDir+"/.env .env") {
		t.Fatalf("unexpected expanded command %q", got)
	}
	if len(tmux.created) != 1 || tmux.created[0].Name != "qa" {
		t.Fatalf("expected tmux window qa, got %+v", tmux.created)
	}
}

func TestStartDoesNotRunHooksForExistingWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"foo": true}}
	tmux := &fakeTmux{windows: map[string]bool{"foo": true}}
	store := &fakeStore{entries: map[string]Entry{"foo": {Name: "foo", Path: filepath.Join(repoRoot, ".awp", "workspaces", "foo")}}}
	hooks := &fakeHooks{commands: []string{"echo hi"}}
	runner := &fakeRunner{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Hooks: hooks, Runner: runner, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("foo", "", "", true); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if hooks.calls != 0 {
		t.Fatalf("expected no hook lookup for existing workspace, got %d", hooks.calls)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected no command runs, got %+v", runner.calls)
	}
}

func TestOpenRunsHooksWhenCreatingWorkspace(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	repoRoot := t.TempDir()
	invocationDir := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	hooks := &fakeHooks{commands: []string{"cp <root>/.env .env", "echo done"}}
	runner := &fakeRunner{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Hooks: hooks, Runner: runner, InvocationDir: invocationDir, Input: bytes.NewBufferString("y\n"), Out: io.Discard})
	if err := svc.Open("", "feature/qa", "", false); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if hooks.calls != 1 {
		t.Fatalf("expected hook lookup on open-created workspace, got %d", hooks.calls)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 hook command runs on open-created workspace, got %+v", runner.calls)
	}
	if runner.calls[0].name != "/bin/sh" || len(runner.calls[0].args) != 2 || runner.calls[0].args[0] != "-lc" {
		t.Fatalf("unexpected first runner call: %+v", runner.calls[0])
	}
	if got := runner.calls[0].args[1]; !strings.Contains(got, "cp "+invocationDir+"/.env .env") {
		t.Fatalf("unexpected expanded command %q", got)
	}
}

func TestStartRollsBackWhenHookFails(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	repoRoot := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}, workspaceRevs: map[string]string{"qa": "rev-qa"}, emptyRevisions: map[string]bool{"rev-qa": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	hooks := &fakeHooks{commands: []string{"pnpm i"}}
	runner := &fakeRunner{failCall: 1, failOut: "install failed", failErr: errors.New("exit status 1")}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Hooks: hooks, Runner: runner, Input: bytes.NewBuffer(nil), Out: io.Discard})
	err := svc.createWorkspace("qa", "", "", true)
	if err == nil {
		t.Fatal("expected start error")
	}
	if !strings.Contains(err.Error(), "bootstrap hook failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jj.forgotten) != 1 || jj.forgotten[0] != "qa" {
		t.Fatalf("expected rollback forget qa, got %+v", jj.forgotten)
	}
	if len(tmux.killed) != 0 {
		t.Fatalf("expected no rollback window kill because tmux window was not created yet, got %+v", tmux.killed)
	}
	if len(tmux.created) != 0 {
		t.Fatalf("expected no tmux window to be created before hook success, got %+v", tmux.created)
	}
	if _, ok := store.entries["qa"]; ok {
		t.Fatalf("expected qa entry removed on rollback, got %+v", store.entries)
	}
	if len(jj.abandoned) != 1 || jj.abandoned[0] != "rev-qa" {
		t.Fatalf("expected rollback to abandon rev-qa, got %+v", jj.abandoned)
	}
	workspacePath := filepath.Join(home, ".awp", "workspaces", "qa")
	if _, statErr := os.Stat(workspacePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected workspace path removed, stat err=%v", statErr)
	}
}

func TestRenameRenamesJJAndTmux(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"old": true}}
	tmux := &fakeTmux{windows: map[string]bool{"old": true}}
	store := &fakeStore{entries: map[string]Entry{"old": {Name: "old", Path: filepath.Join(repoRoot, ".awp", "workspaces", "old")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Rename("old", "new"); err != nil {
		t.Fatalf("Rename returned error: %v", err)
	}
	if jj.renamedTo != "new" {
		t.Fatalf("jj rename target = %q, want new", jj.renamedTo)
	}
	if tmux.renamedOld != "old" || tmux.renamedNew != "new" {
		t.Fatalf("unexpected tmux rename args: %q -> %q", tmux.renamedOld, tmux.renamedNew)
	}
	if _, ok := store.entries["new"]; !ok {
		t.Fatal("store missing new entry")
	}
}

func TestDeleteBlocksDefaultWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{"default": true}}
	store := &fakeStore{entries: map[string]Entry{"default": {Name: "default", Path: filepath.Join(repoRoot, ".awp", "workspaces", "default")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	err := svc.Delete("default", true)
	if err == nil || !strings.Contains(err.Error(), "cannot be removed") {
		t.Fatalf("expected protected workspace error, got %v", err)
	}
	if len(jj.forgotten) != 0 {
		t.Fatalf("expected no jj forget for protected workspace, got %+v", jj.forgotten)
	}
}

func TestDeleteRequiresConfirmationUnlessForced(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"foo": true}}
	tmux := &fakeTmux{windows: map[string]bool{"foo": true}}
	store := &fakeStore{entries: map[string]Entry{"foo": {Name: "foo", Path: filepath.Join(repoRoot, ".awp", "workspaces", "foo")}}}
	in := bytes.NewBufferString("n\n")

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: in, Out: io.Discard})
	if err := svc.Delete("foo", false); err == nil {
		t.Fatal("expected cancellation error")
	}
	if len(jj.forgotten) != 0 {
		t.Fatal("expected no delete when not confirmed")
	}

	if err := svc.Delete("foo", true); err != nil {
		t.Fatalf("forced delete returned error: %v", err)
	}
	if len(jj.forgotten) != 1 || jj.forgotten[0] != "foo" {
		t.Fatalf("unexpected forgotten list: %+v", jj.forgotten)
	}
}

func TestDeleteAbandonsEmptyWorkspaceRevision(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{
		repoRoot:       repoRoot,
		existing:       map[string]bool{"foo": true},
		workspaceRevs:  map[string]string{"foo": "abc123"},
		emptyRevisions: map[string]bool{"abc123": true},
	}
	tmux := &fakeTmux{windows: map[string]bool{"foo": true}}
	store := &fakeStore{entries: map[string]Entry{"foo": {Name: "foo", Path: filepath.Join(repoRoot, ".awp", "workspaces", "foo")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Delete("foo", true); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(jj.abandoned) != 1 || jj.abandoned[0] != "abc123" {
		t.Fatalf("expected abandoned abc123, got %+v", jj.abandoned)
	}
}

func TestDeleteForgetsMatchingBookmarkIncludingRemotes(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{
		repoRoot:      repoRoot,
		existing:      map[string]bool{"team-example-branch": true},
		workspaceRevs: map[string]string{"team-example-branch": "abc123"},
		bookmarksByRev: map[string][]string{
			"abc123": {"team/example-branch"},
		},
	}
	tmux := &fakeTmux{windows: map[string]bool{"team-example-branch": true}}
	store := &fakeStore{entries: map[string]Entry{"team-example-branch": {Name: "team-example-branch", Path: filepath.Join(repoRoot, ".awp", "workspaces", "team-example-branch")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Delete("team-example-branch", true); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(jj.forgottenBookmarks) != 1 || jj.forgottenBookmarks[0] != "team/example-branch" {
		t.Fatalf("expected bookmark forget for tracked bookmark, got %+v", jj.forgottenBookmarks)
	}
}

func TestDeleteProtectsTrunkBookmark(t *testing.T) {
	// Regression: workspace delete must never forget the trunk bookmark,
	// regardless of how it ended up referenced — Entry.Bookmark set to
	// "main" (by a buggy link path, a hand-edited state file, …) or
	// "main" sitting at the workspace's @ revision (workspace anchored on
	// main with no further commits).
	for _, name := range []string{"main", "master", "trunk", "custom-trunk"} {
		t.Run("stored="+name, func(t *testing.T) {
			repoRoot := t.TempDir()
			jj := &fakeJJ{
				repoRoot:      repoRoot,
				existing:      map[string]bool{"feat-x": true},
				workspaceRevs: map[string]string{"feat-x": "abc123"},
				bookmarksByRev: map[string][]string{
					"abc123": {name},
				},
				trunk: name,
			}
			tmux := &fakeTmux{windows: map[string]bool{"feat-x": true}}
			store := &fakeStore{entries: map[string]Entry{
				"feat-x": {Name: "feat-x", Bookmark: name, Path: filepath.Join(repoRoot, ".awp", "workspaces", "feat-x")},
			}}

			svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
			if err := svc.Delete("feat-x", true); err != nil {
				t.Fatalf("Delete returned error: %v", err)
			}
			for _, b := range jj.forgottenBookmarks {
				if b == name {
					t.Fatalf("trunk bookmark %q was forgotten on workspace delete (forgotten: %+v)", name, jj.forgottenBookmarks)
				}
			}
		})
	}
}

func TestDeleteForgetsStoredBookmarkForReviewWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	workspaceName := "pr-1976-rli-trqqqlpytpuo"
	bookmark := "rli/trq-qq-qlpytpuo"
	jj := &fakeJJ{
		repoRoot:       repoRoot,
		existing:       map[string]bool{workspaceName: true},
		workspaceRevs:  map[string]string{workspaceName: "abc123"},
		bookmarksByRev: map[string][]string{"abc123": {bookmark}},
	}
	tmux := &fakeTmux{windows: map[string]bool{workspaceName: true}}
	store := &fakeStore{entries: map[string]Entry{
		workspaceName: {Name: workspaceName, Bookmark: bookmark, Path: filepath.Join(repoRoot, ".awp", "workspaces", workspaceName)},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Delete(workspaceName, true); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(jj.forgottenBookmarks) != 1 || jj.forgottenBookmarks[0] != bookmark {
		t.Fatalf("expected bookmark forget for stored bookmark %q, got %+v", bookmark, jj.forgottenBookmarks)
	}
}

func TestDeleteForgetsStoredBookmarkNotAtWorkspaceRevision(t *testing.T) {
	// Regression: jj bookmarks don't auto-advance with @. If the user
	// committed past the original branch point, the stored bookmark
	// lives on an ancestor commit, not the workspace's working-copy
	// commit. Cleanup must still forget it.
	repoRoot := t.TempDir()
	workspaceName := "feat-x"
	bookmark := "andrew/feat-x"
	jj := &fakeJJ{
		repoRoot:      repoRoot,
		existing:      map[string]bool{workspaceName: true},
		workspaceRevs: map[string]string{workspaceName: "tip999"},
		// bookmarksByRev intentionally has no entry for "tip999" — the
		// bookmark exists in the repo but on an ancestor commit.
		bookmarksByRev: map[string][]string{},
	}
	tmux := &fakeTmux{windows: map[string]bool{workspaceName: true}}
	store := &fakeStore{entries: map[string]Entry{
		workspaceName: {Name: workspaceName, Bookmark: bookmark, Path: filepath.Join(repoRoot, ".awp", "workspaces", workspaceName)},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Delete(workspaceName, true); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(jj.forgottenBookmarks) != 1 || jj.forgottenBookmarks[0] != bookmark {
		t.Fatalf("expected stored bookmark %q to be forgotten, got %+v", bookmark, jj.forgottenBookmarks)
	}
}

func TestDeleteRecoversFromStaleWorkingCopyDuringBookmarkForget(t *testing.T) {
	// Regression: the first jj bookmark forget can fail with "working copy
	// is stale" (typically when another workspace's @ has drifted). The
	// delete flow must call jj workspace update-stale and retry once
	// rather than leaving the workspace half-deleted.
	repoRoot := t.TempDir()
	workspaceName := "main-preview"
	bookmark := "andrew/preview-deploys-from-main"
	staleErr := errors.New("Error: The working copy is stale (not updated since operation 10b9b74564e2).\nHint: Run `jj workspace update-stale` to update it.")
	jj := &fakeJJ{
		repoRoot:      repoRoot,
		existing:      map[string]bool{workspaceName: true},
		workspaceRevs: map[string]string{workspaceName: "abc123"},
		forgetBookmarkErrs: map[string][]error{
			bookmark: {staleErr},
		},
	}
	tmux := &fakeTmux{windows: map[string]bool{workspaceName: true}}
	store := &fakeStore{entries: map[string]Entry{
		workspaceName: {Name: workspaceName, Bookmark: bookmark, Path: filepath.Join(repoRoot, ".awp", "workspaces", workspaceName)},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Delete(workspaceName, true); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if jj.updateStaleCalls != 1 {
		t.Fatalf("expected UpdateStale to be called once, got %d", jj.updateStaleCalls)
	}
	if len(jj.forgottenBookmarks) != 1 || jj.forgottenBookmarks[0] != bookmark {
		t.Fatalf("expected stored bookmark %q to be forgotten after retry, got %+v", bookmark, jj.forgottenBookmarks)
	}
}

func TestPrepareWorkspaceRecordsBookmark(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	name, _, err := svc.PrepareWorkspace("pr-42-feat", "feat/branch", false)
	if err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	entry, ok := store.entries[name]
	if !ok {
		t.Fatalf("expected entry for %q in store: %+v", name, store.entries)
	}
	if entry.Bookmark != "feat/branch" {
		t.Fatalf("expected stored bookmark %q, got %q", "feat/branch", entry.Bookmark)
	}
}

func TestPrepareWorkspaceBackfillsBookmarkOnExisting(t *testing.T) {
	repoRoot := t.TempDir()
	name := "pr-7-foo"
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{name: true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{
		name: {Name: name, Path: filepath.Join(repoRoot, ".awp", "workspaces", name)},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if _, _, err := svc.PrepareWorkspace(name, "foo/bar", false); err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	if got := store.entries[name].Bookmark; got != "foo/bar" {
		t.Fatalf("expected backfilled bookmark %q, got %q", "foo/bar", got)
	}
}

func TestPrepareWorkspaceAnchorsExistingWorkspaceOnBookmark(t *testing.T) {
	// Regression: when PrepareWorkspace was called for a workspace
	// that already existed (e.g. a half-finished review run had left
	// it behind), the old `if exists { return }` branch skipped the
	// step that anchors the working copy on the requested bookmark.
	// Reviewing the same PR a second time then "succeeded" but the
	// code wasn't actually checked out at the PR branch. The reconcile
	// shape always calls jj.NewOnRevision when a bookmark is given.
	repoRoot := t.TempDir()
	name := "pr-1-saltor-default-new-workspace-to-main"
	bookmark := "saltor/default-new-workspace-to-main"
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{name: true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{
		name: {Name: name, Path: filepath.Join(repoRoot, ".awp", "workspaces", name)},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if _, _, err := svc.PrepareWorkspace(name, bookmark, false); err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	if len(jj.newRevCalls) != 1 {
		t.Fatalf("expected one NewOnRevision call to anchor @ on %q, got %d: %+v", bookmark, len(jj.newRevCalls), jj.newRevCalls)
	}
	if jj.newRevCalls[0].Revision != bookmark {
		t.Errorf("NewOnRevision revision = %q, want %q", jj.newRevCalls[0].Revision, bookmark)
	}
}

func TestPrepareWorkspaceAnchorsFreshWorkspaceOnBookmark(t *testing.T) {
	// A newly-created workspace already lands on the bookmark via
	// `jj workspace add -r <bookmark>`, but the reconcile shape still
	// calls NewOnRevision unconditionally so the invariant ("after
	// PrepareWorkspace, @ is a fresh child of the bookmark") doesn't
	// depend on AddWorkspace's revision arg succeeding silently. The
	// redundant call is cheap and makes failure modes easier to reason
	// about. Switching from `jj edit` to `jj new` also lets immutable
	// bookmarks (someone else's PR head) flow through unchanged.
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if _, _, err := svc.PrepareWorkspace("pr-9-feat", "feat/x", false); err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	if len(jj.newRevCalls) != 1 || jj.newRevCalls[0].Revision != "feat/x" {
		t.Errorf("expected NewOnRevision(_, %q), got %+v", "feat/x", jj.newRevCalls)
	}
}

func TestPrepareWorkspaceSkipsAnchorWithNoBookmark(t *testing.T) {
	// No bookmark means no anchoring work — the workspace lands on @
	// from jj's perspective and we leave it alone. Without this guard
	// the reconcile would try to `jj new ""` and bomb on a missing
	// revision arg.
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if _, _, err := svc.PrepareWorkspace("scratch", "", false); err != nil {
		t.Fatalf("PrepareWorkspace returned error: %v", err)
	}
	if len(jj.newRevCalls) != 0 {
		t.Errorf("expected no NewOnRevision calls when bookmark is empty; got %+v", jj.newRevCalls)
	}
}

func TestStartRequiresRepo(t *testing.T) {
	jj := &fakeJJ{repoRootErr: errors.New("no repo"), existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.createWorkspace("foo", "", "", true); err == nil {
		t.Fatal("expected repo error")
	}
}

func TestOpenPromptsToStartWhenMissingAndCancelled(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	in := bytes.NewBufferString("n\n")
	out := &bytes.Buffer{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: in, Out: out})
	err := svc.Open("qa", "", "", false)
	if err == nil || err.Error() != "open cancelled" {
		t.Fatalf("expected open cancelled error, got %v", err)
	}
	if len(jj.added) != 0 {
		t.Fatalf("expected no workspace add, got %+v", jj.added)
	}
	if _, ok := store.entries["qa"]; ok {
		t.Fatalf("did not expect qa entry in store: %+v", store.entries)
	}
	if !strings.Contains(out.String(), "Create it?") {
		t.Fatalf("expected prompt in output, got %q", out.String())
	}
}

func TestOpenPromptsToStartWhenMissingAndConfirmed(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	in := bytes.NewBufferString("y\n")

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: in, Out: io.Discard})
	if err := svc.Open("qa", "", "", false); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if len(jj.added) != 1 || jj.added[0].Name != "qa" {
		t.Fatalf("expected qa workspace to be added, got %+v", jj.added)
	}
	if len(tmux.switched) != 1 || tmux.switched[0] != "qa" {
		t.Fatalf("expected switch to qa, got %+v", tmux.switched)
	}
}

func TestOpenWithYesSkipsPromptWhenMissing(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	out := &bytes.Buffer{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: out})
	if err := svc.Open("qa", "", "", true); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if strings.Contains(out.String(), "Create it?") {
		t.Fatalf("expected no prompt output, got %q", out.String())
	}
	if len(jj.added) != 1 || jj.added[0].Name != "qa" {
		t.Fatalf("expected qa workspace to be added, got %+v", jj.added)
	}
}

func TestOpenWithPromptStartsAgentCommandForNewWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Open("qa", "", "fix failing tests", true); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if tmux.sentWindow != "qa" {
		t.Fatalf("expected prompt sent to qa, got %q", tmux.sentWindow)
	}
	if tmux.sentCommand != "pi 'fix failing tests'" {
		t.Fatalf("unexpected prompt command: %q", tmux.sentCommand)
	}
}

func TestOpenWithPromptDoesNotStartAgentCommandForExistingWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"qa": true}}
	tmux := &fakeTmux{windows: map[string]bool{"qa": true}}
	store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: filepath.Join(repoRoot, ".awp", "workspaces", "qa")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Open("qa", "", "fix failing tests", true); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if tmux.sentCommand != "" {
		t.Fatalf("expected no prompt command for existing workspace, got %q", tmux.sentCommand)
	}
}

func TestOpenWithBookmarkPromptsBeforeStarting(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	out := &bytes.Buffer{}
	in := bytes.NewBufferString("y\n")

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: in, Out: out})
	if err := svc.Open("", "team/example-branch", "", false); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Create it?") {
		t.Fatalf("expected prompt output, got %q", out.String())
	}
	if len(jj.added) != 1 || jj.added[0].Name != "team-example-branch" {
		t.Fatalf("unexpected added workspace: %+v", jj.added)
	}
	if jj.addRevision != "team/example-branch" {
		t.Fatalf("expected add revision team/example-branch, got %q", jj.addRevision)
	}
	if jj.trackedBookmark != "team/example-branch" {
		t.Fatalf("unexpected tracked bookmark: %q", jj.trackedBookmark)
	}
}

func TestInfoReturnsDetails(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"qa": true}}
	tmux := &fakeTmux{windows: map[string]bool{"qa": true}, current: "qa"}
	store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: filepath.Join(repoRoot, ".awp", "workspaces", "qa")}}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	info, err := svc.Info("qa")
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if info.Name != "qa" || !info.Managed || !info.JJExists || !info.TmuxExists || !info.ActiveWindow {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestListCanonicalizesLegacyStateAndIncludesJJWorkspaces(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{
		repoRoot:  repoRoot,
		existing:  map[string]bool{"qa": true, "default": true},
		listNames: []string{"default", "qa"},
	}
	tmux := &fakeTmux{windows: map[string]bool{"qa": true}, current: "qa"}
	store := &fakeStore{entries: map[string]Entry{
		filepath.Join(repoRoot, ".awp", "workspaces", "qa"): {Path: filepath.Join(repoRoot, ".awp", "workspaces", "qa")},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	rows, err := svc.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d (%+v)", len(rows), rows)
	}
	if rows[1].Name != "qa" || rows[1].TmuxWindow != "qa" || !rows[1].Active {
		t.Fatalf("unexpected qa row: %+v", rows[1])
	}
	if _, ok := store.entries["qa"]; !ok {
		t.Fatalf("expected canonicalized key 'qa' in store: %+v", store.entries)
	}
}

func TestListPrunesStaleStateEntriesNotInJJ(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{
		repoRoot:  repoRoot,
		existing:  map[string]bool{"default": true},
		listNames: []string{"default"},
	}
	tmux := &fakeTmux{windows: map[string]bool{"qa": true}, current: "qa"}
	store := &fakeStore{entries: map[string]Entry{
		"default": {Name: "default", Path: filepath.Join(repoRoot, "default")},
		"qa":      {Name: "qa", Path: filepath.Join(repoRoot, "qa")},
	}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	rows, err := svc.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "default" {
		t.Fatalf("expected only default workspace, got %+v", rows)
	}
	if _, ok := store.entries["qa"]; ok {
		t.Fatalf("expected stale qa entry to be pruned, got %+v", store.entries)
	}
}

func TestWantsAttention(t *testing.T) {
	for _, s := range []string{"waiting", "idle", " Idle "} {
		if !WantsAttention(s) {
			t.Errorf("WantsAttention(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"working", "exited", "", "done"} {
		if WantsAttention(s) {
			t.Errorf("WantsAttention(%q) = true, want false", s)
		}
	}
}

func TestUpdateStatusUnreadLifecycle(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"qa": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: repoRoot + "/qa"}}}
	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})

	if err := svc.UpdateStatus("qa", "waiting"); err != nil {
		t.Fatalf("UpdateStatus(waiting): %v", err)
	}
	if !store.entries["qa"].Unread {
		t.Error("waiting should set Unread")
	}

	// Exited clears the stale badge — the agent is gone, so there is
	// nothing for the user to act on.
	if err := svc.UpdateStatus("qa", "exited"); err != nil {
		t.Fatalf("UpdateStatus(exited): %v", err)
	}
	if got := store.entries["qa"]; got.Status != "exited" || got.Unread {
		t.Errorf("after exited: Status=%q Unread=%v, want exited/false", got.Status, got.Unread)
	}

	// Working leaves an existing unread flag alone (deck refresh clears it).
	if err := svc.UpdateStatus("qa", "idle"); err != nil {
		t.Fatalf("UpdateStatus(idle): %v", err)
	}
	if err := svc.UpdateStatus("qa", "working"); err != nil {
		t.Fatalf("UpdateStatus(working): %v", err)
	}
	if got := store.entries["qa"]; !got.Unread {
		t.Errorf("working should not clear Unread, got %+v", got)
	}
}
