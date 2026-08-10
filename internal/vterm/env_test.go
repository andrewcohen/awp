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
