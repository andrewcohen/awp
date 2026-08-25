package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/awplog"
	"github.com/andrewcohen/awp/internal/review"
)

// Every failure this surface reports has to end up somewhere it can still be read.
//
// The status line is one row of a TUI: it cannot be copied, cannot be scrolled back
// to, and the next keystroke replaces it. When the reason GitHub refused something
// is GitHub's own sentence, "it said something went wrong" is what is left of it an
// hour later — which is exactly how much there was to go on the first time this
// mattered.

// logLines is everything written to the log so far.
func logLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(awplog.Path())
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestAFailureReachesBothTheStatusLineAndTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)

	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.fail("reply: %v", "GitHub refused: thread is gone")

	if !m.statusErr || !strings.Contains(m.status, "thread is gone") {
		t.Fatalf("expected the failure on the status line, got %q", m.status)
	}
	got := strings.Join(logLines(t), "\n")
	if !strings.Contains(got, "thread is gone") {
		t.Fatalf("expected the reason in the log, got %q", got)
	}
	// Marked as an error and attributed to the surface, so a log holding several
	// subsystems can still be read.
	if !strings.Contains(got, "ERR") || !strings.Contains(got, "diff:") {
		t.Fatalf("expected a level and a source, got %q", got)
	}
}

// The real thing, through the keys: a reply GitHub refuses must leave its reason in
// the log, not only in a status line that is gone on the next keystroke.
func TestAFailedReplyLeavesItsReasonInTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)

	rec := &replyRecorder{err: errTestRefused}
	m, _ := threadReplyModel(t, rec)
	m = cursorToComment(t, m)
	m = press(m, "c")
	m, cmd := typeAndSave(m, "fixed")
	updated, _ := m.Update(cmd())
	m = updated.(Model)

	// And the status line is then cleared by the very next thing that happens, which
	// is the point: after this, the log is the only record.
	m.status = ""
	if got := strings.Join(logLines(t), "\n"); !strings.Contains(got, errTestRefused.Error()) {
		t.Fatalf("expected GitHub's reason in the log, got %q", got)
	}
}

// The invariant, enforced rather than trusted: reporting a failure means calling
// fail(), which is what puts it in the log. A site that sets statusErr itself is a
// failure that vanishes with the keystroke, and it would be found by someone trying
// to diagnose it — the worst possible time.
func TestNothingSetsTheErrorFlagWithoutLogging(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package: %v", err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "statusErr = true") {
				continue
			}
			// The one legitimate site is inside fail() itself.
			if name == "model.go" && strings.Contains(string(b), "func (m *Model) fail(") &&
				withinFail(string(b), i) {
				continue
			}
			t.Errorf("%s:%d sets statusErr directly — report failures through m.fail(), "+
				"which is what puts the reason in the log:\n\t%s", name, i+1, strings.TrimSpace(line))
		}
	}
	// A guard that checked nothing would pass forever.
	if checked < 5 {
		t.Fatalf("expected to scan the package's sources, only read %d files", checked)
	}
}

// withinFail reports whether line index i falls inside fail()'s body — the one place
// allowed to set the flag.
func withinFail(src string, i int) bool {
	lines := strings.Split(src, "\n")
	start := -1
	for n, line := range lines {
		if strings.HasPrefix(line, "func (m *Model) fail(") {
			start = n
			break
		}
	}
	if start < 0 {
		return false
	}
	for n := start; n < len(lines); n++ {
		if n == i {
			return true
		}
		if lines[n] == "}" {
			return false
		}
	}
	return false
}

// errTestRefused stands in for the kind of message that has to survive: GitHub's
// own, which is the only thing that says what to do about the failure.
var errTestRefused = &testError{"replying to the thread: thread is gone"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// Success is not an error. A status line reporting something that worked must not
// land in the log as a failure, or the log stops being a list of things to look at.
func TestASuccessfulReplyIsNotLoggedAsAFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)

	rec := &replyRecorder{id: "PRRC_new"}
	m, _ := threadReplyModel(t, rec)
	m = cursorToComment(t, m)
	m = press(m, "c")
	m, cmd := typeAndSave(m, "fixed")
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.statusErr {
		t.Fatalf("expected no error status, got %q", m.status)
	}
	for _, line := range logLines(t) {
		if strings.Contains(line, "ERR") {
			t.Fatalf("a successful reply was logged as a failure: %q", line)
		}
	}
	_ = review.Published
}
