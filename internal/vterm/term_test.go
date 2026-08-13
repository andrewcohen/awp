//go:build ghosttyvt

package vterm

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// What a hosted terminal has to do, asked of whichever one a pane gets: render a
// process's output, answer its queries without wedging, carry typing to it, resize
// the pty and the screen together, report an exit, and refuse work once closed.
//
// These were written against the x/vt implementation and are unchanged by its
// deletion, because none of them was ever about x/vt — the harness (start,
// awaitScreen) is what names the emulator, and it names the only one there is.

func TestAHostedProcessRendersItsOutput(t *testing.T) {
	term := start(t, "sh", "-c", "echo HELLO-FROM-PTY; sleep 30")
	awaitScreen(t, term, "HELLO-FROM-PTY")

	if got := strings.Count(term.View(), "\n"); got != 9 {
		t.Errorf("the screen is %d newlines for a 10-row terminal, want 9", got)
	}
}

// The regression guard for the deadlock that this package exists to get right.
//
// A terminal application asks the terminal questions. tmux sends CSI c
// (Primary Device Attributes) as soon as it attaches. The emulator answers on
// its own read side, and if nothing drains that side the write blocks inside
// the parser while holding the emulator's lock — so the next View never
// returns. Without the reply-drain goroutine in Start, this test hangs.
func TestATerminalQueryDoesNotWedgeTheEmulator(t *testing.T) {
	// Ask for device attributes, then keep printing so a wedged emulator is
	// distinguishable from one that simply has nothing more to say.
	term := start(t, "sh", "-c", `printf '\033[c'; sleep 0.3; echo ALIVE-AFTER-QUERY; sleep 30`)
	awaitScreen(t, term, "ALIVE-AFTER-QUERY")
}

func TestTypingReachesTheProcess(t *testing.T) {
	term := start(t, "sh", "-c", "read line; echo GOT:$line; sleep 30")
	if err := term.Send([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	awaitScreen(t, term, "GOT:ping")
}

func TestResizeMovesThePtyAndTheEmulatorTogether(t *testing.T) {
	// The process reports the size it sees, on its own, every 100ms — so the
	// assertion is about what the far side of the PTY believes, not about
	// anything this test told it.
	term := start(t, "sh", "-c", "while :; do stty size; sleep 0.1; done")
	awaitScreen(t, term, "10 40")

	if err := term.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	if w, h := term.Size(); w != 80 || h != 24 {
		t.Errorf("Size reports %dx%d after resizing to 80x24", w, h)
	}
	if got := strings.Count(term.View(), "\n"); got != 23 {
		t.Errorf("the screen is %d newlines after resizing to 24 rows, want 23", got)
	}
	// The process must agree, or its output wraps at the old width while the
	// emulator lays it out at the new one.
	awaitScreen(t, term, "24 80")
}

func TestAFinishedProcessIsReported(t *testing.T) {
	term := start(t, "sh", "-c", "exit 3")
	msg, ok := term.AwaitExit()().(ExitMsg)
	if !ok {
		t.Fatal("AwaitExit did not produce an ExitMsg")
	}
	if msg.Err == nil {
		t.Error("a process that exited 3 reported no error")
	}
	if msg.Gen != 1 {
		t.Errorf("ExitMsg carries generation %d, want the 1 it was started with", msg.Gen)
	}
}

// A closed Term must refuse work rather than write to a dead descriptor, and
// closing twice must not panic — the deck closes on both esc and process exit.
func TestClosingIsSafeAndFinal(t *testing.T) {
	term := start(t, "sh", "-c", "sleep 30")
	if err := term.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := term.Send([]byte("x")); err == nil {
		t.Error("a closed terminal accepted input")
	}
	if err := term.Resize(20, 5); err == nil {
		t.Error("a closed terminal accepted a resize")
	}
}

func TestOpenRejectsNonsense(t *testing.T) {
	if _, err := Open(1, 0, 10, exec.Command("true"), HostColors{}); err == nil {
		t.Error("a zero width was accepted")
	}
	if _, err := Open(1, 40, 10, nil, HostColors{}); err == nil {
		t.Error("a nil command was accepted")
	}
}

// SendKey must reach the process, and must do so through the emulator's own
// encoder rather than a table of escape sequences kept here. The proof it is
// wired to the right channel is that a plain character and a control key both
// arrive: they take different paths through the encoder.
func TestSendKeyReachesTheProcess(t *testing.T) {
	term := start(t, "sh", "-c", "read line; echo GOT:$line; sleep 30")
	for _, r := range "hi" {
		term.SendKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	term.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	awaitScreen(t, term, "GOT:hi")
}

// ctrl+c through SendKey must interrupt, which is the difference between a
// portal you can get out of and a pane that has captured your keyboard.
func TestAControlKeyIsEncodedAsAControlKey(t *testing.T) {
	term := start(t, "sh", "-c", `trap 'echo CAUGHT-INT; exit 0' INT; while :; do sleep 0.1; done`)
	// Let the trap install before interrupting it.
	time.Sleep(300 * time.Millisecond)
	term.SendKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	awaitScreen(t, term, "CAUGHT-INT")
}
