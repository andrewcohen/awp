package awplog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// logInto points the log at a file this test owns.
func logInto(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "awp.log")
	SetPathForTest(t, path)
	return path
}

func TestErrorfRecordsTheMessage(t *testing.T) {
	path := logInto(t)
	Errorf("reply: %v", "GitHub said no")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	line := string(b)
	if !strings.Contains(line, "ERR") || !strings.Contains(line, "reply: GitHub said no") {
		t.Fatalf("unexpected line: %q", line)
	}
	// Timestamped, because the first question about a failure is when it happened —
	// specifically, whether it was the one you just saw.
	if !strings.Contains(line, ":") || len(strings.Fields(line)) < 3 {
		t.Fatalf("expected a timestamp and a level: %q", line)
	}
}

// A log where one entry can span lines is a log you cannot grep — and the messages
// most worth keeping are exactly the multi-line ones: gh's stderr, and GraphQL
// error payloads.
func TestAMultiLineMessageStaysOneLine(t *testing.T) {
	path := logInto(t)
	Errorf("gh failed: %s", "line one\nline two\r\nline three\rline four")
	b, _ := os.ReadFile(path)
	if n := strings.Count(strings.TrimRight(string(b), "\n"), "\n"); n != 0 {
		t.Fatalf("expected one line, got %d extra:\n%q", n, string(b))
	}
	if !strings.Contains(string(b), "line four") {
		t.Fatalf("the tail of the message was lost: %q", string(b))
	}
}

// An always-on log must not be able to eat a disk.
func TestTheLogRotatesAtTheCap(t *testing.T) {
	path := logInto(t)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxBytes+1)), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	Errorf("after the cap")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), "after the cap") {
		t.Fatalf("expected the new line in a fresh file: %q", string(b))
	}
	if len(b) > 200 {
		t.Fatalf("expected the oversized log rotated away, still %d bytes", len(b))
	}
	// One generation is kept, so a failure right after a rotation still has its
	// context somewhere.
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected the previous generation kept: %v", err)
	}
}

// Logging is a diagnostic. It must never be the thing that breaks the program it
// is diagnosing, so an unwritable path is silently nothing rather than a panic.
func TestAnUnwritablePathIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	SetPathForTest(t, filepath.Join(blocker, "awp.log"))
	Errorf("into the void")
	Infof("also nothing")
}

// The deck logs from its update loop, from background jobs and from the pr-status
// goroutines at once. Interleaved half-lines would be worse than no lines.
func TestConcurrentWritesStayWholeLines(t *testing.T) {
	path := logInto(t)
	var wg sync.WaitGroup
	const writers, each = 8, 20
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				Errorf("writer %d entry %d %s", w, i, strings.Repeat("padding ", 20))
			}
		}(w)
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != writers*each {
		t.Fatalf("expected %d lines, got %d", writers*each, len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(line, "ERR writer ") || !strings.HasSuffix(line, "padding ") {
			t.Fatalf("line %d is torn: %q", i, line)
		}
	}
}

func TestTailReturnsTheLastLines(t *testing.T) {
	logInto(t)
	for i := 0; i < 5; i++ {
		Infof("entry %d", i)
	}
	got, err := Tail(2)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(got) != 2 || !strings.Contains(got[0], "entry 3") || !strings.Contains(got[1], "entry 4") {
		t.Fatalf("expected the last two entries oldest-first, got %q", got)
	}
}

func TestTailOnAMissingLogIsAnError(t *testing.T) {
	SetPathForTest(t, filepath.Join(t.TempDir(), "never-written.log"))
	if _, err := Tail(10); err == nil {
		t.Fatal("expected a missing log to report itself, so `awp logs` can say so")
	}
}

// The default has to be safe, not merely documented as unsafe.
//
// Every package whose code path logs would otherwise have to remember to redirect
// the log in a TestMain, and the one that forgets does not fail — it quietly appends
// its fixtures to the log the user reads when something actually breaks. Three
// packages in, that had already happened once.
func TestATestBinaryDoesNotWriteToTheRealLog(t *testing.T) {
	if !testBinary() {
		t.Fatal("expected to be recognised as a test binary")
	}
	// With no override, this is the real path — and nothing must appear there.
	real := Path()
	before, _ := os.Stat(real)
	Errorf("this must not be written")
	after, err := os.Stat(real)
	switch {
	case before == nil && err == nil:
		t.Fatalf("a test wrote to the user's log at %s", real)
	case before != nil && err == nil && after.Size() != before.Size():
		t.Fatalf("a test appended to the user's log at %s", real)
	}
}
