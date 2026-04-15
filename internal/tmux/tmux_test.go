package tmux

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	call := []string{name}
	call = append(call, args...)
	f.calls = append(f.calls, call)
	if f.err != nil {
		return "", f.err
	}
	return f.out, nil
}

func TestSendCommandUsesLiteralSendKeysThenEnter(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.SendCommand("qa", "pi 'fix tests'"); err != nil {
		t.Fatalf("SendCommand returned error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 tmux calls, got %#v", runner.calls)
	}
	wantFirst := []string{"tmux", "send-keys", "-t", "qa", "-l", "pi 'fix tests'"}
	for i, want := range wantFirst {
		if runner.calls[0][i] != want {
			t.Fatalf("first call mismatch at %d: got %#v want %#v", i, runner.calls[0], wantFirst)
		}
	}
	wantSecond := []string{"tmux", "send-keys", "-t", "qa", "Enter"}
	for i, want := range wantSecond {
		if runner.calls[1][i] != want {
			t.Fatalf("second call mismatch at %d: got %#v want %#v", i, runner.calls[1], wantSecond)
		}
	}
}

func TestNewSessionIssuesExpectedCommand(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.NewSession("[awp]repo__qa", "/tmp/ws", "agent"); err != nil {
		t.Fatalf("NewSession returned error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 tmux call, got %#v", runner.calls)
	}
	want := []string{"tmux", "new-session", "-d", "-s", "[awp]repo__qa", "-n", "agent", "-c", "/tmp/ws"}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("call mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestSwitchClientIssuesExpectedCommand(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.SwitchClient("[awp]repo__qa"); err != nil {
		t.Fatalf("SwitchClient returned error: %v", err)
	}
	want := []string{"tmux", "switch-client", "-t", "[awp]repo__qa"}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("call mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestSessionExistsMatchesNameExactly(t *testing.T) {
	runner := &fakeRunner{}
	runner.out = "other\n[awp]repo__qa\n"
	client := New(runner)
	ok, err := client.SessionExists("[awp]repo__qa")
	if err != nil || !ok {
		t.Fatalf("expected session to exist: ok=%v err=%v", ok, err)
	}
	ok, err = client.SessionExists("[awp]repo__missing")
	if err != nil || ok {
		t.Fatalf("expected no match: ok=%v err=%v", ok, err)
	}
}

func TestNewWindowInSessionBuildsTarget(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.NewWindowInSession("[awp]repo__qa", "logs", "/tmp/ws"); err != nil {
		t.Fatalf("NewWindowInSession: %v", err)
	}
	want := []string{"tmux", "new-window", "-d", "-t", "[awp]repo__qa:", "-n", "logs", "-c", "/tmp/ws"}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestNewShellWindowInSessionBuildsTargetAndReturnsWindowID(t *testing.T) {
	runner := &fakeRunner{out: "[awp]repo__qa:3\n"}
	client := New(runner)
	target, err := client.NewShellWindowInSession("[awp]repo__qa", "/tmp/ws")
	if err != nil {
		t.Fatalf("NewShellWindowInSession: %v", err)
	}
	if target != "[awp]repo__qa:3" {
		t.Fatalf("unexpected target: %q", target)
	}
	want := []string{"tmux", "new-window", "-d", "-t", "[awp]repo__qa:", "-P", "-F", "#{session_name}:#{window_index}", "-c", "/tmp/ws"}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestSplitPaneInSessionBuildsTarget(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.SplitPaneInSession("[awp]repo__qa", "agent", "/tmp/ws", true); err != nil {
		t.Fatalf("SplitPaneInSession: %v", err)
	}
	want := []string{"tmux", "split-window", "-d", "-t", "[awp]repo__qa:agent", "-h", "-c", "/tmp/ws"}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestListSessionsParsesIdAndName(t *testing.T) {
	runner := &fakeRunner{out: "$1\tmain\n$2\t[awp]repo__qa\n"}
	client := New(runner)
	sessions, err := client.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2, got %#v", sessions)
	}
	if sessions[1].ID != "$2" || sessions[1].Name != "[awp]repo__qa" {
		t.Fatalf("bad parse: %#v", sessions[1])
	}
}

func TestKillSessionIssuesExpectedCommand(t *testing.T) {
	runner := &fakeRunner{}
	client := New(runner)
	if err := client.KillSession("[awp]repo__qa"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	want := []string{"tmux", "kill-session", "-t", "[awp]repo__qa"}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestCurrentSessionNameReturnsTrimmedValue(t *testing.T) {
	runner := &fakeRunner{out: "[awp]repo__qa\n"}
	client := New(runner)
	name, err := client.CurrentSessionName()
	if err != nil {
		t.Fatalf("CurrentSessionName: %v", err)
	}
	if name != "[awp]repo__qa" {
		t.Fatalf("unexpected name: %q", name)
	}
}

func TestPaneCurrentCommandUsesDisplayMessageTargetAndTrims(t *testing.T) {
	runner := &fakeRunner{out: "zsh\n"}
	client := New(runner)
	got, err := client.PaneCurrentCommand("[awp]repo__qa:editor")
	if err != nil {
		t.Fatalf("PaneCurrentCommand: %v", err)
	}
	if got != "zsh" {
		t.Fatalf("unexpected command: %q", got)
	}
	want := []string{"tmux", "display-message", "-p", "-t", "[awp]repo__qa:editor", "#{pane_current_command}"}
	if len(runner.calls) != 1 {
		t.Fatalf("unexpected call count: %#v", runner.calls)
	}
	for i, w := range want {
		if runner.calls[0][i] != w {
			t.Fatalf("mismatch at %d: got %#v want %#v", i, runner.calls[0], want)
		}
	}
}

func TestSendCommandReturnsError(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	client := New(runner)
	if err := client.SendCommand("qa", "pi hi"); err == nil {
		t.Fatal("expected error")
	}
}
