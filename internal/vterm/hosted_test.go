package vterm

import (
	"os/exec"
	"strings"
	"testing"
)

// TestAnUnsetChoiceIsTheDefaultEmulator. Nothing about a pane changes until
// someone asks for a change, so the variable being absent has to be exactly the
// path that was there before it existed.
func TestAnUnsetChoiceIsTheDefaultEmulator(t *testing.T) {
	t.Setenv(VTEnv, "")
	term, err := Open(1, 20, 4, exec.Command("true"), HostColors{})
	if err != nil {
		t.Fatalf("open with %s unset: %v", VTEnv, err)
	}
	t.Cleanup(func() { _ = term.Close() })
	if _, ok := term.(*Term); !ok {
		t.Errorf("an unset %s opened a %T, want the x/vt *Term", VTEnv, term)
	}
}

// TestAnUnknownEmulatorIsRefusedByName rather than quietly treated as the
// default. A typo that silently ran x/vt would report the two emulators agreeing
// when only one of them ever ran.
func TestAnUnknownEmulatorIsRefusedByName(t *testing.T) {
	t.Setenv(VTEnv, "gostty")
	term, err := Open(1, 20, 4, exec.Command("true"), HostColors{})
	if err == nil {
		_ = term.Close()
		t.Fatal("a misspelled emulator was accepted")
	}
	for _, want := range []string{"gostty", EmulatorXVT, EmulatorGhostty} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — the reader cannot tell what to type instead", err, want)
		}
	}
}

// TestAskingForGhosttyWithoutItSaysSo. libghostty-vt is cgo against a Zig-built
// archive, so most builds do not have it. The one thing that must not happen is
// running the default and calling it ghostty.
func TestAskingForGhosttyWithoutItSaysSo(t *testing.T) {
	t.Setenv(VTEnv, EmulatorGhostty)
	term, err := Open(1, 20, 4, exec.Command("true"), HostColors{})
	if err == nil {
		// Built WITH the tag: then it really is ghostty, and must not be a *Term.
		t.Cleanup(func() { _ = term.Close() })
		if _, isXVT := term.(*Term); isXVT {
			t.Fatal("asking for ghostty returned the x/vt implementation")
		}
		return
	}
	if !strings.Contains(err.Error(), "ghosttyvt") {
		t.Errorf("error %q does not name the build tag that would provide it", err)
	}
}
