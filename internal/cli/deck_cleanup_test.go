package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

func TestKillDesyncedAwpSessionsKillsOnlyUnknownAwpSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed state with repo "alpha" / workspace "default" → expected
	// session "[awp]alpha__default".
	repoAlpha := filepath.Join(t.TempDir(), "alpha")
	if err := state.NewJSONStore().Save(repoAlpha, map[string]workspace.Entry{
		"default": {Name: "default", Path: repoAlpha},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}": strings.Join([]string{
			"$1\t[awp]alpha__default",   // expected → keep
			"$2\t[awp]ghost__default",   // not in state → kill
			"$3\tmain",                  // non-awp session → keep
			"$4\t[awp]current__default", // not in state but is current → keep
		}, "\n") + "\n",
	}}
	tc := tmux.New(runner)

	killDesyncedAwpSessions(tc, "$4")

	var killed []string
	for _, call := range runner.calls {
		if len(call) >= 3 && call[0] == "tmux" && call[1] == "kill-session" {
			killed = append(killed, call[len(call)-1])
		}
	}
	if len(killed) != 1 || killed[0] != "[awp]ghost__default" {
		t.Fatalf("expected only [awp]ghost__default to be killed, got %v", killed)
	}
}

func TestKillDesyncedAwpSessionsNoStateNoKills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	runner := &deckFakeRunner{outs: map[string]string{
		"tmux list-sessions -F #{session_id}\t#{session_name}": "$1\t[awp]alpha__default\n",
	}}
	tc := tmux.New(runner)

	killDesyncedAwpSessions(tc, "$2")

	for _, call := range runner.calls {
		if len(call) >= 2 && call[0] == "tmux" && call[1] == "kill-session" {
			t.Fatalf("did not expect a kill-session call when state is missing: %v", call)
		}
	}
}
