package vterm

import (
	"slices"
	"strings"
	"testing"
)

// A handed-over child talks to the same terminal awp does, so it should be told
// the truth about it — xterm-256color would give up capabilities that are
// genuinely there.
func TestHostTermRestoresTheRealTerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")

	got := HostTerm(Env([]string{"TERM=tmux-256color", "PATH=/bin"}))
	if !slices.Contains(got, "TERM=xterm-ghostty") {
		t.Errorf("TERM was not restored: %v", got)
	}
	if slices.Contains(got, "TERM="+TermType) {
		t.Errorf("the emulator's TERM survived: %v", got)
	}
	if n := countPrefix(got, "TERM="); n != 1 {
		t.Errorf("the environment has %d TERM entries, want 1", n)
	}
}

// The one that matters. `zmx attach` reads ZMX_SESSION and switches the calling
// client's session instead of making a new one, so an attach that inherited it
// steals the terminal awp is running in — and this repo is developed from inside
// a zmx session, so that is awp's own agent. Restoring TERM must not restore this.
func TestHostTermLeavesTheMultiplexerMarkersDropped(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")

	base := []string{
		"TERM=xterm-ghostty",
		"ZMX_SESSION=awp.awp.default.agent",
		"TMUX=/tmp/tmux-501/default,123,0",
		"TMUX_PANE=%4",
		"PATH=/bin",
	}
	got := HostTerm(Env(base))
	for _, banned := range []string{"ZMX_SESSION=", "TMUX=", "TMUX_PANE="} {
		if countPrefix(got, banned) != 0 {
			t.Errorf("%s came back: %v", banned, got)
		}
	}
	if !slices.Contains(got, "PATH=/bin") {
		t.Error("an unrelated variable was dropped")
	}
}

// With no TERM to restore there is nothing honest to say, so the environment is
// left as Env made it rather than having TERM deleted outright.
func TestHostTermWithNothingToRestoreChangesNothing(t *testing.T) {
	t.Setenv("TERM", "")

	env := Env([]string{"PATH=/bin"})
	if got := HostTerm(env); !slices.Equal(got, env) {
		t.Errorf("HostTerm changed the environment to %v", got)
	}
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}
