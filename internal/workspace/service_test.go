package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeJJ struct {
	repoRoot     string
	repoRootErr  error
	existing     map[string]bool
	added        []Entry
	addRevision  string
	bookmarkName string
	bookmarkWS   string
	renamedPath  string
	renamedTo    string
	forgotten    []string
	forgetErr    error
	renameErr    error
	workspaceErr error
	listNames    []string
}

func (f *fakeJJ) RepoRoot() (string, error) { return f.repoRoot, f.repoRootErr }
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
	f.existing[name] = true
	return nil
}

func (f *fakeJJ) SetBookmark(bookmarkName string, workspaceName string) error {
	f.bookmarkName = bookmarkName
	f.bookmarkWS = workspaceName
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

type fakeTmux struct {
	windows    map[string]bool
	created    []Entry
	switched   []string
	renamedOld string
	renamedNew string
	killed     []string
	current    string
}

func (f *fakeTmux) WindowExists(name string) (bool, error) { return f.windows[name], nil }
func (f *fakeTmux) NewWindow(name, dir string) error {
	f.windows[name] = true
	f.created = append(f.created, Entry{Name: name, Path: dir})
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
	if err := svc.Start("Add Auth", ""); err != nil {
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

func TestStartWithBookmarkUsesBookmarkRevisionAndSetsBookmark(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Start("qa", "feature/qa"); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if jj.addRevision != "feature/qa" {
		t.Fatalf("expected add revision feature/qa, got %q", jj.addRevision)
	}
	if jj.bookmarkName != "feature/qa" || jj.bookmarkWS != "qa" {
		t.Fatalf("unexpected bookmark set args: name=%q workspace=%q", jj.bookmarkName, jj.bookmarkWS)
	}
}

func TestStartUsesBookmarkAsDefaultNameWhenNameMissing(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	out := &bytes.Buffer{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: out})
	if err := svc.Start("", "feature/qa"); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected no prompt output, got %q", got)
	}
	if len(jj.added) != 1 || jj.added[0].Name != "feature-qa" {
		t.Fatalf("expected workspace name feature-qa, got %+v", jj.added)
	}
	if jj.addRevision != "feature/qa" {
		t.Fatalf("expected add revision feature/qa, got %q", jj.addRevision)
	}
	if jj.bookmarkName != "feature/qa" || jj.bookmarkWS != "feature-qa" {
		t.Fatalf("unexpected bookmark set args: name=%q workspace=%q", jj.bookmarkName, jj.bookmarkWS)
	}
}

func TestStartRemovesStaleManagedWorkspaceDir(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	home := t.TempDir()
	t.Setenv("HOME", home)
	stalePath := filepath.Join(home, ".awp", "workspaces", "qa")
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatalf("mkdir stale path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stalePath, "junk.txt"), []byte("junk"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Start("qa", ""); err != nil {
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
	if err := svc.Start("", ""); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if got := out.String(); got != "Name: " {
		t.Fatalf("expected prompt output %q, got %q", "Name: ", got)
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
	if err := svc.Start("foo", ""); err != nil {
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
	if err := svc.Start("foo", "feature/foo"); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if jj.bookmarkName != "feature/foo" || jj.bookmarkWS != "foo" {
		t.Fatalf("unexpected bookmark set args: name=%q workspace=%q", jj.bookmarkName, jj.bookmarkWS)
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

func TestStartRequiresRepo(t *testing.T) {
	jj := &fakeJJ{repoRootErr: errors.New("no repo"), existing: map[string]bool{}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard})
	if err := svc.Start("foo", ""); err == nil {
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
	err := svc.Open("qa", "")
	if err == nil || err.Error() != "open cancelled" {
		t.Fatalf("expected open cancelled error, got %v", err)
	}
	if len(jj.added) != 0 {
		t.Fatalf("expected no workspace add, got %+v", jj.added)
	}
	if _, ok := store.entries["qa"]; ok {
		t.Fatalf("did not expect qa entry in store: %+v", store.entries)
	}
	if !strings.Contains(out.String(), "Start it?") {
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
	if err := svc.Open("qa", ""); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if len(jj.added) != 1 || jj.added[0].Name != "qa" {
		t.Fatalf("expected qa workspace to be added, got %+v", jj.added)
	}
	if len(tmux.switched) != 1 || tmux.switched[0] != "qa" {
		t.Fatalf("expected switch to qa, got %+v", tmux.switched)
	}
}

func TestOpenWithBookmarkStartsWithoutPrompt(t *testing.T) {
	repoRoot := t.TempDir()
	jj := &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"default": true}}
	tmux := &fakeTmux{windows: map[string]bool{}}
	store := &fakeStore{entries: map[string]Entry{}}
	out := &bytes.Buffer{}

	svc := NewService(Dependencies{JJ: jj, Tmux: tmux, Store: store, Input: bytes.NewBuffer(nil), Out: out})
	if err := svc.Open("", "saltor/no-default-standard-delivery-preference"); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected no prompt output, got %q", got)
	}
	if len(jj.added) != 1 || jj.added[0].Name != "saltor-no-default-standard-delivery-preference" {
		t.Fatalf("unexpected added workspace: %+v", jj.added)
	}
	if jj.addRevision != "saltor/no-default-standard-delivery-preference" {
		t.Fatalf("expected add revision to be bookmark, got %q", jj.addRevision)
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
