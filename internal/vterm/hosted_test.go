package vterm

import (
	"os/exec"
	"strings"
	"testing"
)

// Which emulator a pane gets is a property of the build, not of the environment.
// These pin the two answers Open can give — the terminal, or a refusal that says
// how to get one — and that neither of them depends on anything a caller can set.

// TestOpenIsTheGhosttyEmulator, in a build that has it.
func TestOpenIsTheGhosttyEmulator(t *testing.T) {
	term, err := Open(1, 20, 4, exec.Command("true"), HostColors{})
	if err != nil {
		// Built without the tag. That is a legitimate build — it is the one a
		// contributor with no Zig gets — and the other test is the one about it.
		t.Skipf("this build has no %s emulator: %v", EmulatorGhostty, err)
	}
	t.Cleanup(func() { _ = term.Close() })
	if got := term.Emulator(); got != EmulatorGhostty {
		t.Errorf("Open started the %q emulator, want %q", got, EmulatorGhostty)
	}
}

// TestABuildWithoutTheEmulatorSaysHowToGetOne. It used to fall back to x/vt, which
// was the point of having two; with one there is nothing to fall back to, so the
// refusal is all the reader gets and it has to be enough to act on.
func TestABuildWithoutTheEmulatorSaysHowToGetOne(t *testing.T) {
	term, err := Open(1, 20, 4, exec.Command("true"), HostColors{})
	if err == nil {
		_ = term.Close()
		t.Skip("this build has the emulator, so there is no refusal to read")
	}
	for _, want := range []string{EmulatorGhostty, "ghosttyvt", "libghostty-vt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q — the reader cannot tell what to do about it", err, want)
		}
	}
}

// TestNothingInTheEnvironmentPicksAnEmulator. AWP_PANE_VT was retired with the
// second emulator; a leftover value in someone's shell profile has to be inert
// rather than an error about a variable awp no longer reads.
func TestNothingInTheEnvironmentPicksAnEmulator(t *testing.T) {
	t.Setenv("AWP_PANE_VT", "x-vt")
	first, err := Open(1, 20, 4, exec.Command("true"), HostColors{})
	if err != nil {
		if strings.Contains(err.Error(), "AWP_PANE_VT") {
			t.Fatalf("Open still reads AWP_PANE_VT: %v", err)
		}
		t.Skipf("this build has no %s emulator: %v", EmulatorGhostty, err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if got := first.Emulator(); got != EmulatorGhostty {
		t.Errorf("a stale AWP_PANE_VT changed the emulator to %q", got)
	}
}
