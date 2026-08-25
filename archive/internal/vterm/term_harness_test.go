//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The harness the tests that drive a real terminal share.
//
// Tagged, because there is one emulator and it is cgo against an archive built by
// Zig (see ghostty.go). What that costs is written down in AGENTS.md: a plain `go
// test ./...` does not run any of the tests in this file's dependents, so a change
// to the emulator's own behaviour is not checked until someone runs the tagged
// suite. It is the trade for having deleted the pure-Go emulator that used to
// answer these questions differently.

// render returns the screen with ANSI stripped, for matching on text.
func render(t Hosted) string {
	var b strings.Builder
	s := t.View()
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && !strings.ContainsRune("mHKJhlr", rune(s[i])) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// awaitScreen polls the rendered screen for want. It uses a deadline rather
// than a fixed sleep so a genuine deadlock fails loudly instead of hanging the
// test binary until the package timeout.
func awaitScreen(t *testing.T, term Hosted, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(render(term), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited 5s for %q on screen; got:\n%s", want, render(term))
}

// start runs args on a 40x10 terminal.
func start(t *testing.T, args ...string) Hosted {
	t.Helper()
	return startHost(t, HostColors{}, args...)
}

// startHost is start with something said about the outer terminal's colours.
func startHost(t *testing.T, host HostColors, args ...string) Hosted {
	t.Helper()
	term, err := Open(1, 40, 10, exec.Command(args[0], args[1:]...), host) //nolint:gosec // fixed test commands
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = term.Close() })
	return term
}

// awaitExited waits for the hosted process to end, through the same command the
// deck waits on rather than through the terminal's internals — which is what these
// tests used to reach into, and what tied them to one implementation's fields.
func awaitExited(t *testing.T, term Hosted, what string) {
	t.Helper()
	done := make(chan tea.Msg, 1)
	exit := term.AwaitExit()
	go func() { done <- exit() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}
