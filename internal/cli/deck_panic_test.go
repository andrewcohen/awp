package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/awplog"
)

// A crash has to reach the log, because for an alt-screen program the trace goes
// to a screen the terminal has already swapped away from.
func TestACrashIsRecordedBeforeItTakesTheProcessDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)

	rethrown := func() (r any) {
		defer func() { r = recover() }()
		defer logDeckPanic()
		panicFromADeckFrame()
		return nil
	}()

	// It must still die of it: a crash is a bug, and this only makes it say so
	// somewhere durable on the way.
	if rethrown == nil {
		t.Fatal("logDeckPanic swallowed the panic, so the deck would carry on in whatever state crashed it")
	}

	lines, err := awplog.Tail(50)
	if err != nil {
		t.Fatal(err)
	}
	logged := strings.Join(lines, "\n")
	if !strings.Contains(logged, "the deck fell over") {
		t.Errorf("the log does not mention what panicked:\n%s", logged)
	}
	// The stack is the whole point — the value alone says no more than the status
	// bar would have.
	if !strings.Contains(logged, "panicFromADeckFrame") {
		t.Errorf("the log has no stack trace, so it names the crash without locating it:\n%s", logged)
	}
}

func panicFromADeckFrame() { panic("the deck fell over") }

// A normal return must not be mistaken for a crash, or every clean exit would
// file a panic.
func TestACleanExitLogsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)

	func() { defer logDeckPanic() }()

	// A log that was never created is the strongest form of "wrote nothing", so
	// the read failing that way is the pass.
	lines, err := awplog.Tail(50)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	if got := strings.TrimSpace(strings.Join(lines, "\n")); got != "" {
		t.Errorf("a clean exit wrote to the log:\n%s", got)
	}
}
