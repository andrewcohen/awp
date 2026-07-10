package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

func TestDeckSessionNameFormat(t *testing.T) {
	got := DeckSessionName("agent-deck", "qa")
	if got != "[awp]agent-deck__qa" {
		t.Fatalf("got %q", got)
	}
}

func TestParseAwpSession(t *testing.T) {
	cases := []struct {
		in       string
		repo, ws string
		ok       bool
	}{
		{"[awp]agent-deck__qa", "agent-deck", "qa", true},
		{"[awp]repo__my__workspace", "repo", "my__workspace", true},
		{"main", "", "", false},
		{"[awp]noSeparator", "", "", false},
	}
	for _, tc := range cases {
		r, w, ok := parseAwpSession(tc.in)
		if ok != tc.ok || r != tc.repo || w != tc.ws {
			t.Fatalf("parseAwpSession(%q) = (%q,%q,%v) want (%q,%q,%v)", tc.in, r, w, ok, tc.repo, tc.ws, tc.ok)
		}
	}
}

func TestMaybeUpdateStaleWorkingCopyNonInteractiveReturnsOriginalError(t *testing.T) {
	client := jj.New(NewExecRunner())
	cause := errors.New("stale")
	updated, err := maybeUpdateStaleWorkingCopy(client, strings.NewReader(""), &bytes.Buffer{}, cause)
	if updated {
		t.Fatal("expected no update")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected original error, got %v", err)
	}
}

type deckFakeRunner struct {
	calls [][]string
	outs  map[string]string
}

func (r *deckFakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	key := strings.Join(call, " ")
	return r.outs[key], nil
}

type deckFakeService struct {
	info         workspace.InfoEntry
	deleteName   string
	deleteForce  bool
	renameOld    string
	renameNew    string
	renameErr    error
	recordedName string
	recordedID   string
	recordedSess string
}

func (s *deckFakeService) List() ([]workspace.ListEntry, error)         { return nil, nil }
func (s *deckFakeService) ListAll() ([]workspace.CrossRepoEntry, error) { return nil, nil }
func (s *deckFakeService) Info(string) (workspace.InfoEntry, error)     { return s.info, nil }
func (s *deckFakeService) Open(string, string, string, bool) error      { return nil }
func (s *deckFakeService) PrepareWorkspace(string, string, bool) (string, string, error) {
	return "", "", nil
}
func (s *deckFakeService) Bootstrap(string) error { return nil }
func (s *deckFakeService) BootstrapAll() error    { return nil }
func (s *deckFakeService) Rename(old, new string) error {
	s.renameOld = old
	s.renameNew = new
	return s.renameErr
}
func (s *deckFakeService) Delete(name string, force bool) error {
	s.deleteName = name
	s.deleteForce = force
	return nil
}
func (s *deckFakeService) DeleteWithOptions(name string, opts workspace.DeleteOptions) error {
	if opts.DeferTmuxKill != nil {
		opts.DeferTmuxKill(name)
	}
	return s.Delete(name, opts.Force)
}
func (s *deckFakeService) RecordSession(name, id, sess string) error {
	s.recordedName = name
	s.recordedID = id
	s.recordedSess = sess
	return nil
}
func (s *deckFakeService) RecordBookmark(string, string) error { return nil }
func (s *deckFakeService) RecordPROverride(string, int) error  { return nil }
func (s *deckFakeService) UpdatePrompt(string, string) error   { return nil }
func (s *deckFakeService) UpdateStatus(string, string) error   { return nil }
func (s *deckFakeService) ClearSession(string) error           { return nil }
func (s *deckFakeService) PruneOrphans(bool) ([]string, error) { return nil, nil }
func (s *deckFakeService) MarkRead(string) error               { return nil }

func TestOpenNamedWindowCreatesShellWindowAndSwitchesToIt(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                                                                       "$1\t[awp]repo__qa\n",
		"tmux new-window -d -t [awp]repo__qa: -P -F #{session_name}:#{window_index} -c /tmp/ws -e AWP_WORKSPACE=qa -e AWP_REPO=repo": "[awp]repo__qa:3\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws"}

	if err := openNamedWindow(client, svc, item, "", noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	// The env-injection step issues some show-environment / set-environment
	// calls and a pane_current_command probe before the window work. Verify
	// the window-related calls happen in order, and that env injection
	// preceded them.
	want := [][]string{
		{"tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}"},
		{"tmux", "new-window", "-d", "-t", "[awp]repo__qa:", "-P", "-F", "#{session_name}:#{window_index}", "-c", "/tmp/ws", "-e", "AWP_WORKSPACE=qa", "-e", "AWP_REPO=repo"},
		{"tmux", "select-window", "-t", "[awp]repo__qa:3"},
		{"tmux", "switch-client", "-t", "[awp]repo__qa"},
	}
	idx := 0
	for _, call := range runner.calls {
		if idx >= len(want) {
			break
		}
		if strings.Join(call, "|") == strings.Join(want[idx], "|") {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("missing expected call at index %d (%v); got %#v", idx, want[idx], runner.calls)
	}
	sawSetEnv := false
	for _, call := range runner.calls {
		if len(call) >= 4 && call[1] == "set-environment" {
			if call[4] == "AWP_WORKSPACE" {
				sawSetEnv = true
			}
		}
	}
	if !sawSetEnv {
		t.Fatalf("expected AWP_WORKSPACE to be injected; got %#v", runner.calls)
	}
}

func TestOpenNamedWindowReusesExistingNamedWindowAtShellAndResendsCommand(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                    "$1\t[awp]repo__qa\n",
		"tmux list-windows -t [awp]repo__qa -F #{window_id}\t#{window_name}":      "@1\tagent\n@2\teditor\n",
		"tmux display-message -p -t [awp]repo__qa:editor #{pane_current_command}": "zsh\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws"}

	if err := openNamedWindow(client, svc, item, "editor", noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	found := false
	for _, call := range runner.calls {
		if strings.Join(call, " ") == "tmux send-keys -t [awp]repo__qa:editor -l $EDITOR" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected resend to existing shell pane, calls: %#v", runner.calls)
	}
}

func TestOpenNamedWindowReusesExistingNamedWindowInTUIAndDoesNotSendCommand(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                    "$1\t[awp]repo__qa\n",
		"tmux list-windows -t [awp]repo__qa -F #{window_id}\t#{window_name}":      "@1\tagent\n@2\teditor\n",
		"tmux display-message -p -t [awp]repo__qa:editor #{pane_current_command}": "vim\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws"}

	if err := openNamedWindow(client, svc, item, "editor", noopReporter{}); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call, " "), "send-keys") {
			t.Fatalf("unexpected send-keys call: %#v", runner.calls)
		}
	}
}

// TestSummonWorkspaceSessionLaunchesAgentOnNewSession locks in the fix for
// the async-create regression where pressing enter on a freshly-created
// workspace row before the create-workspace job finished produced a tmux
// session whose "agent" window was a bare shell. Whenever a deck handler
// creates the session itself, it must send the configured AgentInvocation
// to :agent so the user never lands in a non-agent shell. Without this
// the async create-workspace job's `sessionWasNew` check in app.go silently
// no-ops the agent launch on its end.
func TestSummonWorkspaceSessionLaunchesAgentOnNewSession(t *testing.T) {
	// Isolate from the developer's ~/.config/awp/config.json so
	// AgentInvocation resolves to the default "pi" instead of whatever the
	// host has configured.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	runner := &deckFakeRunner{outs: map[string]string{
		// list-sessions returns empty: no [awp]repo__qa session exists yet.
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws", RepoRoot: "/tmp/repo"}

	if err := summonWorkspaceSession(client, svc, item, noopReporter{}); err != nil {
		t.Fatalf("summonWorkspaceSession: %v", err)
	}

	newSessionIdx := -1
	agentSendKeysIdx := -1
	switchClientIdx := -1
	for i, call := range runner.calls {
		if len(call) < 2 || call[0] != "tmux" {
			continue
		}
		joined := strings.Join(call, " ")
		switch {
		case call[1] == "new-session":
			newSessionIdx = i
		case call[1] == "send-keys" && len(call) >= 6 && call[3] == "[awp]repo__qa:agent" && call[4] == "-l" && call[5] == "pi":
			if agentSendKeysIdx < 0 {
				agentSendKeysIdx = i
			}
		case call[1] == "switch-client":
			switchClientIdx = i
		}
		_ = joined
	}
	if newSessionIdx < 0 {
		t.Fatalf("expected tmux new-session; got %#v", runner.calls)
	}
	if agentSendKeysIdx < 0 {
		t.Fatalf("expected tmux send-keys -l pi to [awp]repo__qa:agent; got %#v", runner.calls)
	}
	if agentSendKeysIdx <= newSessionIdx {
		t.Fatalf("agent send-keys must come after new-session; new-session@%d send-keys@%d", newSessionIdx, agentSendKeysIdx)
	}
	if switchClientIdx < 0 {
		t.Fatalf("expected tmux switch-client; got %#v", runner.calls)
	}
	if switchClientIdx < agentSendKeysIdx {
		t.Fatalf("switch-client must come after agent send-keys; switch@%d send-keys@%d", switchClientIdx, agentSendKeysIdx)
	}
}

func TestSummonWorkspaceSessionUnreadLandsOnAgentWindow(t *testing.T) {
	// When a row has unread agent output, summoning should land the
	// user on the :agent window (not whatever tmux last focused) so
	// the badge → focus gesture is completed in one step.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	runner := &deckFakeRunner{outs: map[string]string{
		// Session already exists — exercise the path where summon
		// doesn't create the session itself but should still land on
		// :agent because Unread is set.
		"tmux list-sessions -F #{session_id}\t#{session_name}": "$1\t[awp]repo__qa\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws", RepoRoot: "/tmp/repo", Unread: true}

	if err := summonWorkspaceSession(client, svc, item, noopReporter{}); err != nil {
		t.Fatalf("summonWorkspaceSession: %v", err)
	}

	selectAgentIdx := -1
	switchClientIdx := -1
	for i, call := range runner.calls {
		if len(call) < 2 || call[0] != "tmux" {
			continue
		}
		switch {
		case call[1] == "select-window" && len(call) >= 4 && call[3] == "[awp]repo__qa:agent":
			if selectAgentIdx < 0 {
				selectAgentIdx = i
			}
		case call[1] == "switch-client":
			switchClientIdx = i
		}
	}
	if selectAgentIdx < 0 {
		t.Fatalf("expected select-window -t [awp]repo__qa:agent; got %#v", runner.calls)
	}
	if switchClientIdx < 0 {
		t.Fatalf("expected switch-client; got %#v", runner.calls)
	}
	if selectAgentIdx >= switchClientIdx {
		t.Fatalf("select-window must come before switch-client; select@%d switch@%d", selectAgentIdx, switchClientIdx)
	}
}

func TestSummonWorkspaceSessionNoUnreadSkipsAgentSelect(t *testing.T) {
	// Without unread, summon should not force focus to :agent — tmux's
	// natural last-focused window behavior should win.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}": "$1\t[awp]repo__qa\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws", RepoRoot: "/tmp/repo"}

	if err := summonWorkspaceSession(client, svc, item, noopReporter{}); err != nil {
		t.Fatalf("summonWorkspaceSession: %v", err)
	}

	for _, call := range runner.calls {
		if len(call) >= 4 && call[0] == "tmux" && call[1] == "select-window" && call[3] == "[awp]repo__qa:agent" {
			t.Fatalf("did not expect select-window :agent when Unread=false; got %#v", runner.calls)
		}
	}
}

func TestHandleDeckActionRenameRefusesWhileAgentRuns(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                   "$1\t[awp]repo__qa\n",
		"tmux display-message -p -t [awp]repo__qa:agent #{pane_current_command}": "claude\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa"}

	err := handleDeckAction(client, svc, nil, deckui.ActionRequest{Item: item, Action: deckui.ActionRename, Arg: "qb"}, noopReporter{})
	if err == nil {
		t.Fatal("expected rename to be refused while agent runs")
	}
	if !strings.Contains(err.Error(), "live agent") {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.renameOld != "" || svc.renameNew != "" {
		t.Fatalf("svc.Rename should not have been called, got old=%q new=%q", svc.renameOld, svc.renameNew)
	}
}

func TestHandleDeckActionRenameRenamesSessionWhenAgentIsShell(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                   "$1\t[awp]repo__qa\n",
		"tmux display-message -p -t [awp]repo__qa:agent #{pane_current_command}": "zsh\n",
		"tmux show-environment -t [awp]repo__qb AWP_WORKSPACE":                   "AWP_WORKSPACE=qa\n",
		"tmux show-environment -t [awp]repo__qb AWP_REPO":                        "AWP_REPO=repo\n",
		"tmux show-environment -t [awp]repo__qb AWP_REPO_ROOT":                   "AWP_REPO_ROOT=/repo\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", RepoRoot: "/repo"}

	if err := handleDeckAction(client, svc, nil, deckui.ActionRequest{Item: item, Action: deckui.ActionRename, Arg: "qb"}, noopReporter{}); err != nil {
		t.Fatalf("handleDeckAction: %v", err)
	}
	if svc.renameOld != "qa" || svc.renameNew != "qb" {
		t.Fatalf("unexpected rename args: old=%q new=%q", svc.renameOld, svc.renameNew)
	}
	if svc.recordedName != "qb" || svc.recordedSess != "[awp]repo__qb" || svc.recordedID != "$1" {
		t.Fatalf("RecordSession not invoked with new name: name=%q sess=%q id=%q", svc.recordedName, svc.recordedSess, svc.recordedID)
	}
	sawSessionRename := false
	sawEnvUpdate := false
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		if joined == "tmux rename-session -t [awp]repo__qa [awp]repo__qb" {
			sawSessionRename = true
		}
		if joined == "tmux set-environment -t [awp]repo__qb AWP_WORKSPACE qb" {
			sawEnvUpdate = true
		}
	}
	if !sawSessionRename {
		t.Fatalf("expected tmux rename-session call, calls=%#v", runner.calls)
	}
	if !sawEnvUpdate {
		t.Fatalf("expected AWP_WORKSPACE env update on new session, calls=%#v", runner.calls)
	}
}

func TestHandleDeckActionDeleteUsesForceAndKillsSession(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}": "$1\t[awp]repo__qa\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa"}

	if err := handleDeckAction(client, svc, nil, deckui.ActionRequest{Item: item, Action: deckui.ActionDelete}, noopReporter{}); err != nil {
		t.Fatalf("handleDeckAction: %v", err)
	}
	if svc.deleteName != "qa" || !svc.deleteForce {
		t.Fatalf("unexpected delete args: name=%q force=%v", svc.deleteName, svc.deleteForce)
	}
	wantLast := "tmux kill-session -t [awp]repo__qa"
	if got := strings.Join(runner.calls[len(runner.calls)-1], " "); got != wantLast {
		t.Fatalf("unexpected final call: %q want %q", got, wantLast)
	}
}

func TestDefaultWindowCommand(t *testing.T) {
	// Isolate from the developer's real ~/.config/awp/config.json so the
	// agent default is deterministic regardless of host config.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cases := map[string]string{
		"editor": "$EDITOR",
		"review": "tuicr -r @",
		"vcs":    "jjui",
		"agent":  "pi",
	}
	for name, want := range cases {
		if got := defaultWindowCommand(name); got != want {
			t.Fatalf("defaultWindowCommand(%q) = %q want %q", name, got, want)
		}
	}
}

func TestDefaultWindowCommandAgentIncludesOptions(t *testing.T) {
	// Project config wins over global; verify the agent window picks up
	// both the command and agent_options so flags like `--model …` or
	// `--dangerously-skip-permissions` aren't dropped on the floor.
	repo := t.TempDir()
	cfgDir := repo + "/.awp"
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"agent":"claude","agent_options":"--dangerously-skip-permissions"}`
	if err := os.WriteFile(cfgDir+"/config.json", []byte(cfg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got := defaultWindowCommandWithRepo("agent", repo)
	want := "claude --dangerously-skip-permissions"
	if got != want {
		t.Fatalf("defaultWindowCommandWithRepo agent = %q want %q", got, want)
	}
}
