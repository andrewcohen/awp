package vterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The recorder has to capture both directions, or a capture proves nothing:
// the open question is whether a stray byte was one we wrote or one the
// session produced, and only having both halves answers it.
func TestThePaneLogRecordsBothDirections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pane.log")
	t.Setenv(PaneLogEnv, path)

	term, err := Start(1, 40, 10, exec.Command("sh", "-c", "cat"))
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
	term, err := Start(1, 40, 10, exec.Command("sh", "-c", "echo hi; sleep 5"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	if term.log != nil {
		t.Error("a pane opened a log with no path configured")
	}
	awaitScreen(t, term, "hi")
}

// A path that cannot be opened must not stop the pane: a debugging aid is
// never a reason a pane will not start.
func TestABadPaneLogPathStillStartsThePane(t *testing.T) {
	t.Setenv(PaneLogEnv, filepath.Join(t.TempDir(), "no-such-dir", "pane.log"))
	term, err := Start(1, 40, 10, exec.Command("sh", "-c", "echo hi; sleep 5"))
	if err != nil {
		t.Fatalf("an unwritable log path stopped the pane: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	awaitScreen(t, term, "hi")
}
