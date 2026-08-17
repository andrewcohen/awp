package vterm

import (
	"slices"
	"strings"
	"testing"
)

// TestAHostedProcessIsNotToldItIsInsideSomethingElse is the whole point of Env:
// every one of these says "you are already inside me", and none of them is true
// of a process talking to this emulator.
//
// ZMX_SESSION is the one that cost something. A pane that inherits it makes
// `zmx attach` switch awp's own client's session instead of creating a new one,
// so opening a pane steals the terminal the deck is running in and the session
// it was pulled off comes back empty.
func TestAHostedProcessIsNotToldItIsInsideSomethingElse(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"TERM=tmux-256color",
		"TMUX=/tmp/tmux-501/default,123,0",
		"TMUX_PANE=%7",
		"ZMX_SESSION=awp.awp.default.agent",
		"AWP_WORKSPACE=default",
	}
	got := Env(base)
	for _, name := range []string{"TMUX", "TMUX_PANE", "ZMX_SESSION"} {
		if kv, ok := lookup(got, name); ok {
			t.Errorf("%s survived as %q — the hosted process is not inside it", name, kv)
		}
	}
	if kv, _ := lookup(got, "TERM"); kv != "TERM="+TermType {
		t.Errorf("TERM is %q, want %q", kv, "TERM="+TermType)
	}
	// Everything that isn't a marker is the caller's business, not ours.
	for _, want := range []string{"PATH=/usr/bin", "AWP_WORKSPACE=default"} {
		if !slices.Contains(got, want) {
			t.Errorf("Env dropped %q", want)
		}
	}
}

// TestAHostedAgentIsNotAChildOfTheSessionThatStartedAwp.
//
// awp is usually developed from inside a Claude Code session, so the deck inherits
// that session's markers and hands them to every agent it starts. The child reads
// CLAUDE_CODE_CHILD_SESSION, concludes it is a sub-agent of a session already
// recording, and turns transcript saving off — then works normally and writes
// nothing down. `awp watch` reads transcripts, so the dev-loop view goes blind for
// that workspace with no error anywhere. The captain is only where it surfaced,
// because Claude Code happens to say so on its start-up line.
func TestAHostedAgentIsNotAChildOfTheSessionThatStartedAwp(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"CLAUDE_CODE_SESSION_ID=abc-123",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/claude/sock",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret",
	}
	got := Env(base)
	for _, name := range []string{
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_MESSAGING_TOKEN",
	} {
		if kv, ok := lookup(got, name); ok {
			t.Errorf("%s survived as %q — the hosted agent is its own session, not a child of awp's", name, kv)
		}
	}
}

// And the other half of the rule: configuration the user chose survives, so a
// hosted agent behaves like the same agent in a terminal. Stripping these would
// make a pane a different place to work, which is the one thing a pane is trying not
// to be.
func TestAHostedAgentKeepsTheUsersOwnClaudeSettings(t *testing.T) {
	base := []string{
		"CLAUDE_CODE_EXECPATH=/opt/homebrew/bin/claude",
		"CLAUDE_CODE_TMPDIR=/tmp/claude-501",
		"CLAUDE_CODE_ENABLE_TODO_TOOLS=1",
	}
	got := Env(base)
	for _, want := range base {
		if !slices.Contains(got, want) {
			t.Errorf("Env dropped %q, which is the user's configuration rather than a session marker", want)
		}
	}
}

// TestEnvStatesTermEvenWhenTheCallerDidNot: the emulator's TERM is not a
// correction of an inherited value, it is the answer regardless.
func TestEnvStatesTermEvenWhenTheCallerDidNot(t *testing.T) {
	got := Env([]string{"PATH=/usr/bin"})
	if kv, ok := lookup(got, "TERM"); !ok || kv != "TERM="+TermType {
		t.Errorf("TERM is %q (present %v), want %q", kv, ok, "TERM="+TermType)
	}
	if n := count(got, "TERM"); n != 1 {
		t.Errorf("TERM appears %d times, want 1", n)
	}
}

func lookup(env []string, name string) (string, bool) {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return kv, true
		}
	}
	return "", false
}

func count(env []string, name string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			n++
		}
	}
	return n
}
