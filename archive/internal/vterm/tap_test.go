//go:build ghosttyvt

package vterm

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	xterm "github.com/charmbracelet/x/term"
)

// The recorder has to capture both directions, or a capture proves nothing:
// the open question is whether a stray byte was one we wrote or one the
// session produced, and only having both halves answers it.
func TestThePaneLogRecordsBothDirections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pane.log")
	t.Setenv(PaneLogEnv, path)

	term, err := Open(1, 40, 10, exec.Command("sh", "-c", "cat"), HostColors{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })

	if err := term.Send([]byte("ping\n")); err != nil {
		t.Fatalf("send: %v", err)
	}
	awaitScreen(t, term, "ping")
	_ = term.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	log := string(raw)
	if !strings.Contains(log, ` in  "ping\n"`) {
		t.Errorf("what we sent is not in the log:\n%s", log)
	}
	if !strings.Contains(log, " out ") {
		t.Errorf("nothing from the process is in the log:\n%s", log)
	}
	// Escapes are quoted, not raw: a log full of real escape sequences would
	// reprogram whatever terminal reads it.
	if strings.ContainsRune(log, 0x1b) {
		t.Error("the log contains raw escape bytes; catting it would reprogram the reader's terminal")
	}
}

// Unset means off, and the pane must start regardless.
func TestNoPaneLogByDefault(t *testing.T) {
	t.Setenv(PaneLogEnv, "")
	term, err := Open(1, 40, 10, exec.Command("sh", "-c", "echo hi; sleep 5"), HostColors{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	if openLog(PaneLogEnv) != nil {
		t.Error("a pane opened a log with no path configured")
	}
	awaitScreen(t, term, "hi")
}

// A path that cannot be opened must not stop the pane: a debugging aid is
// never a reason a pane will not start.
func TestABadPaneLogPathStillStartsThePane(t *testing.T) {
	t.Setenv(PaneLogEnv, filepath.Join(t.TempDir(), "no-such-dir", "pane.log"))
	term, err := Open(1, 40, 10, exec.Command("sh", "-c", "echo hi; sleep 5"), HostColors{})
	if err != nil {
		t.Fatalf("an unwritable log path stopped the pane: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitScreen(t, term, "hi")
}

// TestTheFrameTeeStaysATerminal.
//
// Bubble Tea asks the writer it was handed whether it is a terminal, by
// asserting term.File on it, and the window size, raw mode and colour profile
// all follow from the answer. So a recorder that wrapped the terminal in a
// plain io.Writer would change the frames it exists to observe: the capture
// would show a program painting for a monochrome 80x24 screen that is not
// there, and whatever was being investigated would be absent from the
// recording.
func TestTheFrameTeeStaysATerminal(t *testing.T) {
	t.Setenv(FrameLogEnv, filepath.Join(t.TempDir(), "frames"))
	f, err := os.Create(filepath.Join(t.TempDir(), "tty"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	tapped := TapTerminal(f)
	if tapped == io.Writer(f) {
		t.Fatal("nothing was wrapped, so nothing is being recorded")
	}
	file, ok := tapped.(xterm.File)
	if !ok {
		t.Fatalf("the tee is a %T, not a term.File — Bubble Tea will decide it has no terminal", tapped)
	}
	if file.Fd() != f.Fd() {
		t.Errorf("the tee reports fd %d, the terminal is fd %d", file.Fd(), f.Fd())
	}
}

// Unset means the deck hands Bubble Tea its own stdout, byte for byte, which is
// every ordinary run.
func TestNoFrameLogMeansNoWrapper(t *testing.T) {
	t.Setenv(FrameLogEnv, "")
	f, err := os.Create(filepath.Join(t.TempDir(), "tty"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if got := TapTerminal(f); got != io.Writer(f) {
		t.Errorf("TapTerminal returned a %T with no %s set", got, FrameLogEnv)
	}
}

// The frame has to reach both the terminal and the log. A tee that swallowed it
// would be a recorder you cannot run the deck with.
func TestTheFrameTeeRecordsAndForwards(t *testing.T) {
	dir := t.TempDir()
	logPath, ttyPath := filepath.Join(dir, "frames"), filepath.Join(dir, "tty")
	t.Setenv(FrameLogEnv, logPath)

	f, err := os.Create(ttyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	const frame = "\x1b[2mdim\x1b[m"
	if _, err := TapTerminal(f).Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}

	onward, err := os.ReadFile(ttyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onward) != frame {
		t.Errorf("the terminal received %q, want %q", onward, frame)
	}

	recorded, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), `\x1b[2mdim`) {
		t.Errorf("the log does not hold the frame: %q", recorded)
	}
	if !strings.Contains(string(recorded), " tty ") {
		t.Errorf("the log does not say which direction this was: %q", recorded)
	}
}

// One path means one lock, however many tees ask for it. Pointing both
// variables at one file is what makes a pane's chunk and the frame it caused
// readable in order, and two independently opened handles would tear each
// other's lines apart.
func TestBothLogsCanShareOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "both")
	t.Setenv(PaneLogEnv, path)
	t.Setenv(FrameLogEnv, path)

	pane, frame := openLog(PaneLogEnv), openLog(FrameLogEnv)
	if pane == nil || frame == nil {
		t.Fatal("a configured log did not open")
	}
	if pane.mu != frame.mu {
		t.Error("the two tees hold different locks on one file, so their lines can interleave mid-write")
	}
}
