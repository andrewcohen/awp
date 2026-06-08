package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
)

type fakeDoctor struct {
	runs int
	err  error
}

func (d *fakeDoctor) Run() error {
	d.runs++
	return d.err
}

func (d *fakeDoctor) RunGlobal(bool) error {
	d.runs++
	return d.err
}

func (d *fakeDoctor) RunRepo(bool) error {
	d.runs++
	return d.err
}

type fakeService struct {
	openName          string
	openBookmark      string
	openPrompt        string
	openYes           bool
	prepareName       string
	prepareBookmark   string
	prepareRunHooks   bool
	recordedWorkspace string
	recordedSessionID string
	recordedSession   string
	renameOld         string
	renameNew         string
	deleteName        string
	deleteForce       bool
	listEntries       []workspace.ListEntry
	infoEntry         workspace.InfoEntry
	listErr           error
	infoErr           error
	openErr           error
	prepareErr        error
	renameErr         error
	deleteErr         error
}

func (f *fakeService) List() ([]workspace.ListEntry, error) { return f.listEntries, f.listErr }
func (f *fakeService) Info(string) (workspace.InfoEntry, error) {
	return f.infoEntry, f.infoErr
}
func (f *fakeService) Open(name string, bookmark string, prompt string, yes bool) error {
	f.openName = name
	f.openBookmark = bookmark
	f.openPrompt = prompt
	f.openYes = yes
	return f.openErr
}
func (f *fakeService) Rename(oldName, newName string) error {
	f.renameOld, f.renameNew = oldName, newName
	return f.renameErr
}
func (f *fakeService) Delete(name string, force bool) error {
	f.deleteName, f.deleteForce = name, force
	return f.deleteErr
}
func (f *fakeService) DeleteWithOptions(name string, opts workspace.DeleteOptions) error {
	return f.Delete(name, opts.Force)
}
func (f *fakeService) RecordSession(workspaceName, sessionID, sessionName string) error {
	f.recordedWorkspace = workspaceName
	f.recordedSessionID = sessionID
	f.recordedSession = sessionName
	return nil
}
func (f *fakeService) RecordBookmark(string, string) error   { return nil }
func (f *fakeService) RecordPROverride(string, int) error    { return nil }
func (f *fakeService) ListAll() ([]workspace.CrossRepoEntry, error) { return nil, nil }
func (f *fakeService) UpdatePrompt(string, string) error            { return nil }
func (f *fakeService) UpdateStatus(string, string) error            { return nil }
func (f *fakeService) ClearSession(string) error                    { return nil }
func (f *fakeService) PruneOrphans(bool) ([]string, error)          { return nil, nil }
func (f *fakeService) MarkRead(string) error                        { return nil }
func (f *fakeService) PrepareWorkspace(name, bookmark string, runHooks bool) (string, string, error) {
	f.prepareName = name
	f.prepareBookmark = bookmark
	f.prepareRunHooks = runHooks
	if f.prepareErr != nil {
		return "", "", f.prepareErr
	}
	normalized := name
	if normalized == "" {
		normalized = "from-bookmark"
	}
	return normalized, "/tmp/" + normalized, nil
}
func (f *fakeService) Bootstrap(string) error { return nil }
func (f *fakeService) BootstrapAll() error    { return nil }

type openDeckRunner struct {
	sessions map[string]struct{}
}

func newOpenDeckRunner() *openDeckRunner {
	return &openDeckRunner{sessions: map[string]struct{}{}}
}

func (r *openDeckRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	if name == "jj" && len(args) == 1 && args[0] == "root" {
		return "/tmp/repo\n", nil
	}
	if name != "tmux" {
		return "", nil
	}
	if len(args) >= 2 && args[0] == "new-session" {
		for i := 1; i < len(args)-1; i++ {
			if args[i] == "-s" {
				r.sessions[args[i+1]] = struct{}{}
				break
			}
		}
		return "", nil
	}
	if len(args) >= 2 && args[0] == "list-sessions" {
		if len(r.sessions) == 0 {
			return "", nil
		}
		var b strings.Builder
		i := 1
		for session := range r.sessions {
			b.WriteString("$")
			b.WriteString(string(rune('0' + i)))
			b.WriteString("\t")
			b.WriteString(session)
			b.WriteString("\n")
			i++
		}
		return b.String(), nil
	}
	return "", nil
}

func TestRunDoctor(t *testing.T) {
	svc := &fakeService{}
	doc := &fakeDoctor{}
	app := NewApp(svc, &bytes.Buffer{})
	app.SetDoctor(doc)
	if err := app.Run([]string{"doctor"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if doc.runs != 1 {
		t.Fatalf("expected doctor to run once, got %d", doc.runs)
	}
}

func TestRunWorkspaceAlias(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "foo"}}}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"w", "list"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDeleteParsesForceBeforeOrAfterName(t *testing.T) {
	tests := [][]string{{"workspace", "delete", "--force", "foo"}, {"workspace", "delete", "foo", "--force"}, {"workspace", "rm", "foo", "--force"}}
	for _, args := range tests {
		svc := &fakeService{}
		app := NewApp(svc, &bytes.Buffer{})
		if err := app.Run(args); err != nil {
			t.Fatalf("Run(%v) returned error: %v", args, err)
		}
		if !svc.deleteForce || svc.deleteName != "foo" {
			t.Fatalf("unexpected delete args for %v: force=%t name=%q", args, svc.deleteForce, svc.deleteName)
		}
	}
}

func TestRunListOutputsNamesOnly(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "foo", Active: true}, {Name: "bar", Active: false}}}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	if err := app.Run([]string{"workspace", "list"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := out.String()
	if got != "foo\nbar\n" {
		t.Fatalf("unexpected list output: %q", got)
	}
}

func TestRunOpenHelp(t *testing.T) {
	svc := &fakeService{}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	if err := app.Run([]string{"w", "open", "help"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Usage: awp w open") {
		t.Fatalf("expected open usage, got %q", out.String())
	}
}

func TestRunOpenParsesBookmarkWithoutName(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	app.isInteractive = func(io.Reader) bool { return false }
	if err := app.Run([]string{"w", "open", "-b", "team/example-branch"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.prepareName != "" || svc.prepareBookmark != "team/example-branch" || !svc.prepareRunHooks {
		t.Fatalf("unexpected prepare call: name=%q bookmark=%q runHooks=%t", svc.prepareName, svc.prepareBookmark, svc.prepareRunHooks)
	}
}

func TestRunOpenParsesYesFlag(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	if err := app.Run([]string{"w", "open", "-y", "qa"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.prepareName != "qa" || svc.prepareBookmark != "" {
		t.Fatalf("unexpected prepare call: name=%q bookmark=%q", svc.prepareName, svc.prepareBookmark)
	}
}

func TestRunOpenUsesPickerWhenNoArg(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "qa"}, {Name: "default"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	app.in = bytes.NewBuffer(nil)
	app.picker = func(_ string, options []string) (string, error) {
		if len(options) != 2 {
			t.Fatalf("unexpected picker options: %#v", options)
		}
		return "qa", nil
	}
	if err := app.Run([]string{"w", "open"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.prepareName != "qa" {
		t.Fatalf("expected prepare qa, got %q", svc.prepareName)
	}
}

func TestRunOpenParsesPromptFlag(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	if err := app.Run([]string{"w", "open", "qa", "--prompt", "fix tests"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.prepareName != "qa" {
		t.Fatalf("unexpected prepare name: %q", svc.prepareName)
	}
}

func TestRunOpenInteractivePrefillsFlags(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "qa"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	app.in = bytes.NewBuffer(nil)
	app.isPiped = func(io.Reader) bool { return false }
	app.isInteractive = func(io.Reader) bool { return true }
	app.openForm = func(initial openRequest, _ Runner, _ io.Reader, _ io.Writer) (openRequest, error) {
		if initial.Bookmark != "team/feature" || initial.Prompt != "fix tests" || !initial.Yes {
			t.Fatalf("unexpected initial request: %+v", initial)
		}
		initial.Name = "qa"
		return initial, nil
	}
	if err := app.Run([]string{"w", "open", "--bookmark", "team/feature", "--prompt", "fix tests", "--yes"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.prepareName != "qa" || svc.prepareBookmark != "team/feature" {
		t.Fatalf("unexpected prepare call: name=%q bookmark=%q", svc.prepareName, svc.prepareBookmark)
	}
}

func TestRunOpenInteractiveSubmitImpliesYes(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "qa"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	app.in = bytes.NewBuffer(nil)
	app.isPiped = func(io.Reader) bool { return false }
	app.isInteractive = func(io.Reader) bool { return true }
	app.openForm = func(initial openRequest, _ Runner, _ io.Reader, _ io.Writer) (openRequest, error) {
		initial.Name = "new-workspace"
		initial.Yes = false
		return initial, nil
	}
	if err := app.Run([]string{"w", "open"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.prepareName != "new-workspace" {
		t.Fatalf("expected prepare new-workspace, got %q", svc.prepareName)
	}
}

func TestRunDeleteUsesPickerWhenNoArg(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "default"}, {Name: "qa"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.in = bytes.NewBuffer(nil)
	app.picker = func(_ string, options []string) (string, error) {
		if len(options) != 1 || options[0] != "qa" {
			t.Fatalf("unexpected picker options: %#v", options)
		}
		return "qa", nil
	}
	if err := app.Run([]string{"w", "rm", "--force"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.deleteName != "qa" || !svc.deleteForce {
		t.Fatalf("unexpected delete call: name=%q force=%t", svc.deleteName, svc.deleteForce)
	}
}

func TestRunDeletePickerErrorsWhenOnlyDefaultExists(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "default"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.in = bytes.NewBuffer(nil)
	if err := app.Run([]string{"w", "rm", "--force"}); err == nil || !strings.Contains(err.Error(), "no removable workspaces") {
		t.Fatalf("expected no removable workspaces error, got %v", err)
	}
}

func TestRunInfoOutputsDetails(t *testing.T) {
	svc := &fakeService{infoEntry: workspace.InfoEntry{Name: "qa", Path: "/tmp/qa", Managed: true, JJExists: true, TmuxWindow: "qa", TmuxExists: true, ActiveWindow: true}}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	if err := app.Run([]string{"workspace", "info", "qa"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("FIELD")) || !bytes.Contains([]byte(got), []byte("path")) {
		t.Fatalf("unexpected info output: %q", got)
	}
}

func TestRunDiffRejectsArgs(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"diff", "extra"}); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Fatalf("expected diff arg error, got %v", err)
	}
}

func TestRunDiffCallsWorkflow(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	called := false
	app.diff = func(runner Runner, in io.Reader, out io.Writer) error {
		called = true
		return nil
	}
	if err := app.Run([]string{"diff"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !called {
		t.Fatal("expected diff workflow to be called")
	}
}

func TestRunDeckRejectsArgs(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"deck", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected deck arg error, got %v", err)
	}
	if err := app.Run([]string{"deck", "--scope=bogus"}); err == nil || !strings.Contains(err.Error(), "invalid --scope") {
		t.Fatalf("expected invalid --scope error, got %v", err)
	}
}

func TestRunDeckPassesScopeFlag(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	var gotScope deckui.Scope
	app.deck = func(runner Runner, _ workspace.Service, _ io.Reader, _ io.Writer, scope deckui.Scope) error {
		gotScope = scope
		return nil
	}
	if err := app.Run([]string{"deck", "--scope=attention"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gotScope != deckui.ScopeAttention {
		t.Fatalf("expected ScopeAttention, got %v", gotScope)
	}
	if err := app.Run([]string{"deck", "--scope=open-pr"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if gotScope != deckui.ScopeInbox {
		t.Fatalf("expected ScopeInbox, got %v", gotScope)
	}
}

func TestRunDeckCallsWorkflow(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	called := false
	app.deck = func(runner Runner, gotSvc workspace.Service, in io.Reader, out io.Writer, scope deckui.Scope) error {
		called = true
		if gotSvc != svc {
			t.Fatal("expected service to be passed to deck workflow")
		}
		if scope != deckui.ScopeAll {
			t.Fatalf("expected default scope ScopeAll, got %v", scope)
		}
		return nil
	}
	if err := app.Run([]string{"deck"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !called {
		t.Fatal("expected deck workflow to be called")
	}
}

func TestRunPropagatesServiceError(t *testing.T) {
	svc := &fakeService{prepareErr: errors.New("boom")}
	app := NewApp(svc, &bytes.Buffer{})
	app.runner = newOpenDeckRunner()
	if err := app.Run([]string{"workspace", "open", "foo"}); err == nil {
		t.Fatal("expected error")
	}
}

// recordingRunner captures every command issued and returns canned outputs
// for known queries. It's enough to drive openWorkspaceWithReporter end to
// end without touching a real shell.
type recordingRunner struct {
	calls [][]string
}

func (r *recordingRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if name == "jj" && len(args) >= 1 && args[0] == "root" {
		return "/tmp/repo\n", nil
	}
	if name == "tmux" && len(args) >= 1 && args[0] == "list-sessions" {
		return "", nil
	}
	if name == "tmux" && len(args) >= 1 && args[0] == "show-environment" {
		return "", errors.New("exit status 1")
	}
	if name == "tmux" && len(args) >= 1 && args[0] == "display-message" {
		return "zsh\n", nil
	}
	return "", nil
}

// TestOpenWorkspaceNoSwitchInjectsEnvBeforeAgentInvocation pins the critical
// dispatch-without-switching path: AWP_REPO_ROOT (and friends) MUST land on
// the new tmux session via `-e` *before* we send the agent prompt — without
// this, the agent process inherits a shell env that has no AWP_* and the
// hooks fail to identify the workspace.
func TestOpenWorkspaceNoSwitchInjectsEnvBeforeAgentInvocation(t *testing.T) {
	svc := &fakeService{}
	runner := &recordingRunner{}
	err := openWorkspaceWithReporter(runner, svc, openRequest{
		Name:     "qa",
		Prompt:   "fix tests",
		Yes:      true,
		NoSwitch: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}

	newSessionIdx := -1
	sendKeysIdx := -1
	switchClientIdx := -1
	var newSessionCall []string
	for i, call := range runner.calls {
		if len(call) < 2 || call[0] != "tmux" {
			continue
		}
		switch call[1] {
		case "new-session":
			newSessionIdx = i
			newSessionCall = call
		case "send-keys":
			if sendKeysIdx < 0 {
				sendKeysIdx = i
			}
		case "switch-client":
			switchClientIdx = i
		}
	}
	if newSessionIdx < 0 {
		t.Fatalf("expected tmux new-session call; got %#v", runner.calls)
	}
	if sendKeysIdx < 0 {
		t.Fatalf("expected tmux send-keys call (agent prompt); got %#v", runner.calls)
	}
	if newSessionIdx > sendKeysIdx {
		t.Fatalf("new-session must come before send-keys; got new-session@%d send-keys@%d", newSessionIdx, sendKeysIdx)
	}
	if switchClientIdx >= 0 {
		t.Fatalf("NoSwitch:true must not call switch-client; calls=%#v", runner.calls)
	}

	wantPairs := map[string]string{
		"AWP_WORKSPACE": "qa",
		"AWP_REPO":      "repo",
		"AWP_REPO_ROOT": "/tmp/repo",
	}
	for k, v := range wantPairs {
		want := k + "=" + v
		found := false
		for i := 0; i < len(newSessionCall)-1; i++ {
			if newSessionCall[i] == "-e" && newSessionCall[i+1] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("new-session missing -e %s; got %#v", want, newSessionCall)
		}
	}
}

// existingSessionRunner is a recordingRunner variant that reports the
// target session as already existing — so openWorkspaceWithReporter takes
// the "reuse" path and the agent is presumed already running.
type existingSessionRunner struct {
	recordingRunner
}

func (r *existingSessionRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if name == "tmux" && len(args) >= 1 && args[0] == "list-sessions" {
		_, _ = r.recordingRunner.Run(ctx, dir, name, args...)
		return "$1\t[awp]repo__qa\n", nil
	}
	return r.recordingRunner.Run(ctx, dir, name, args...)
}

// TestOpenWorkspaceExistingSessionPastesPromptInsteadOfReTypingInvocation
// locks in the bug fix: when the session is already up (deck summoned it
// before this run, so the agent is running), we must NOT send "<agent>
// '<prompt>'" — that types literal shell syntax into the running agent's
// input box. Paste the bare prompt instead so the agent sees one user
// message.
func TestOpenWorkspaceExistingSessionPastesPromptInsteadOfReTypingInvocation(t *testing.T) {
	svc := &fakeService{}
	runner := &existingSessionRunner{}
	err := openWorkspaceWithReporter(runner, svc, openRequest{
		Name:     "qa",
		Prompt:   "fix tests",
		Yes:      true,
		NoSwitch: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}

	for _, call := range runner.calls {
		if len(call) >= 2 && call[0] == "tmux" && call[1] == "new-session" {
			t.Fatalf("must not call tmux new-session when session already exists; got %#v", runner.calls)
		}
	}

	sawPasteBuffer := false
	sawSetBuffer := false
	for _, call := range runner.calls {
		if len(call) >= 2 && call[0] == "tmux" {
			switch call[1] {
			case "set-buffer":
				for _, a := range call {
					if a == "fix tests" {
						sawSetBuffer = true
					}
				}
			case "paste-buffer":
				for i := 0; i < len(call)-1; i++ {
					if call[i] == "-t" && call[i+1] == "[awp]repo__qa:agent" {
						sawPasteBuffer = true
					}
				}
			}
		}
	}
	if !sawSetBuffer {
		t.Errorf("expected tmux set-buffer to carry the prompt text; got %#v", runner.calls)
	}
	if !sawPasteBuffer {
		t.Errorf("expected tmux paste-buffer -t :agent; got %#v", runner.calls)
	}

	for _, call := range runner.calls {
		if len(call) < 6 || call[0] != "tmux" || call[1] != "send-keys" {
			continue
		}
		if call[4] != "-l" {
			continue
		}
		body := call[5]
		if strings.Contains(body, "fix tests") && (strings.Contains(body, " '") || strings.Contains(body, "pi ") || strings.Contains(body, "claude ")) {
			t.Errorf("send-keys must not deliver \"<invocation> '<prompt>'\" to an existing agent pane; got %q", body)
		}
	}
}

// indexOfJJFetch returns the index of the first `jj git fetch` call in
// the recorded runner calls, or -1 if it never ran.
func indexOfJJFetch(calls [][]string) int {
	for i, call := range calls {
		if len(call) >= 3 && call[0] == "jj" && call[1] == "git" && call[2] == "fetch" {
			return i
		}
	}
	return -1
}

// TestOpenWorkspaceFetchesBeforeAnchoringBookmark pins the create flow's
// `jj git fetch`: when the workspace anchors on an existing bookmark we
// must fetch first so the working copy lands on the current origin tip
// (and so an origin-only branch is present locally to track). The fetch
// has to precede the tmux session setup — by then PrepareWorkspace has
// already resolved the bookmark revision.
func TestOpenWorkspaceFetchesBeforeAnchoringBookmark(t *testing.T) {
	svc := &fakeService{}
	runner := &recordingRunner{}
	err := openWorkspaceWithReporter(runner, svc, openRequest{
		Bookmark: "andrew/feature",
		Yes:      true,
		NoSwitch: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}

	fetchIdx := indexOfJJFetch(runner.calls)
	if fetchIdx < 0 {
		t.Fatalf("expected a `jj git fetch` call when anchoring on a bookmark; got %#v", runner.calls)
	}
	newSessionIdx := -1
	for i, call := range runner.calls {
		if len(call) >= 2 && call[0] == "tmux" && call[1] == "new-session" {
			newSessionIdx = i
			break
		}
	}
	if newSessionIdx >= 0 && fetchIdx > newSessionIdx {
		t.Errorf("jj git fetch must come before tmux new-session; fetch@%d new-session@%d", fetchIdx, newSessionIdx)
	}
}

// TestOpenWorkspaceSkipsFetchWithoutBookmark guards the gate: a fresh
// workspace with no bookmark to anchor on starts from the local working
// copy, so a fetch wouldn't change where it lands — don't pay the
// network round-trip (and don't block offline creation).
func TestOpenWorkspaceSkipsFetchWithoutBookmark(t *testing.T) {
	svc := &fakeService{}
	runner := &recordingRunner{}
	err := openWorkspaceWithReporter(runner, svc, openRequest{
		Name:     "scratch",
		Yes:      true,
		NoSwitch: true,
	}, nil)
	if err != nil {
		t.Fatalf("openWorkspaceWithReporter: %v", err)
	}
	if idx := indexOfJJFetch(runner.calls); idx >= 0 {
		t.Errorf("did not expect a `jj git fetch` without a bookmark; found at %d in %#v", idx, runner.calls)
	}
}
