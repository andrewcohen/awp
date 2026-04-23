package cli

import (
	"bytes"
	"context"
	"errors"
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
	info        workspace.InfoEntry
	deleteName  string
	deleteForce bool
}

func (s *deckFakeService) List() ([]workspace.ListEntry, error)         { return nil, nil }
func (s *deckFakeService) ListAll() ([]workspace.CrossRepoEntry, error) { return nil, nil }
func (s *deckFakeService) Info(string) (workspace.InfoEntry, error)     { return s.info, nil }
func (s *deckFakeService) Open(string, string, string, bool) error      { return nil }
func (s *deckFakeService) PrepareWorkspace(string, string, bool) (string, string, error) {
	return "", "", nil
}
func (s *deckFakeService) Bootstrap(string) error      { return nil }
func (s *deckFakeService) Rename(string, string) error { return nil }
func (s *deckFakeService) Delete(name string, force bool) error {
	s.deleteName = name
	s.deleteForce = force
	return nil
}
func (s *deckFakeService) RecordSession(string, string, string) error { return nil }
func (s *deckFakeService) UpdatePrompt(string, string) error          { return nil }
func (s *deckFakeService) UpdateStatus(string, string) error          { return nil }
func (s *deckFakeService) ClearSession(string) error                  { return nil }

func TestOpenNamedWindowCreatesShellWindowAndSwitchesToIt(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}":                                  "$1\t[awp]repo__qa\n",
		"tmux new-window -d -t [awp]repo__qa: -P -F #{session_name}:#{window_index} -c /tmp/ws": "[awp]repo__qa:3\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{info: workspace.InfoEntry{Path: "/tmp/ws"}}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa", Path: "/tmp/ws"}

	if err := openNamedWindow(client, svc, item, ""); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	want := [][]string{
		{"tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}"},
		{"tmux", "new-window", "-d", "-t", "[awp]repo__qa:", "-P", "-F", "#{session_name}:#{window_index}", "-c", "/tmp/ws"},
		{"tmux", "select-window", "-t", "[awp]repo__qa:3"},
		{"tmux", "switch-client", "-t", "[awp]repo__qa"},
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("unexpected calls: %#v", runner.calls)
	}
	for i := range want {
		if strings.Join(runner.calls[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("call %d mismatch: got %#v want %#v", i, runner.calls[i], want[i])
		}
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

	if err := openNamedWindow(client, svc, item, "editor"); err != nil {
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

	if err := openNamedWindow(client, svc, item, "editor"); err != nil {
		t.Fatalf("openNamedWindow: %v", err)
	}

	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call, " "), "send-keys") {
			t.Fatalf("unexpected send-keys call: %#v", runner.calls)
		}
	}
}

func TestHandleDeckActionDeleteUsesForceAndKillsSession(t *testing.T) {
	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}": "$1\t[awp]repo__qa\n",
	}}
	client := tmux.New(runner)
	svc := &deckFakeService{}
	item := deckui.Item{ProjectName: "repo", WorkspaceName: "qa"}

	if err := handleDeckAction(client, svc, nil, deckui.ActionRequest{Item: item, Action: deckui.ActionDelete}); err != nil {
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
	cases := map[string]string{
		"editor": "$EDITOR",
		"review": "tuicr -r @",
		"vcs":    "jjui",
		"agent":  "",
	}
	for name, want := range cases {
		if got := defaultWindowCommand(name); got != want {
			t.Fatalf("defaultWindowCommand(%q) = %q want %q", name, got, want)
		}
	}
}
