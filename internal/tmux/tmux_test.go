package tmux

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	call := []string{name}
	call = append(call, args...)
	f.calls = append(f.calls, call)
	if f.err != nil {
		return "", f.err
	}
	return "", nil
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

func TestSendCommandReturnsError(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	client := New(runner)
	if err := client.SendCommand("qa", "pi hi"); err == nil {
		t.Fatal("expected error")
	}
}
