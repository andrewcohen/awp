// Package awplog is awp's diagnostic log.
//
// It exists because of where awp reports its failures. Almost everything the user
// does happens inside a TUI, and a TUI's error channel is a one-line status bar:
// it cannot be copied, it cannot be scrolled back to, and the next keystroke
// replaces it. A reply that GitHub refused said so for as long as it took to read,
// and then the reason was gone — "it said something went wrong" was the whole bug
// report available, for a message that had GitHub's own explanation in it.
//
// So the rule here is that the *underlying* error goes to the file even when a
// short version goes to the screen. The status line is for the person reading it
// now; the log is for working out what happened afterwards.
//
// Three properties, all in service of that:
//
//   - **Always on.** A log you have to enable is not there when you need it, since
//     the failure that matters is the one you have already had. There is no
//     AWP_LOG=1.
//   - **Never fatal.** Every call is best-effort and returns nothing. Logging is a
//     diagnostic, and a diagnostic that can break the program it is diagnosing is
//     worse than no diagnostic.
//   - **Bounded.** It rotates at a size cap, so an always-on log cannot quietly
//     eat a disk over months of use.
package awplog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andrewcohen/awp/internal/config"
)

// maxBytes is when the log rotates. Big enough to hold a long session's worth of
// context around a failure, small enough to be a file you can open.
const maxBytes = 2 << 20 // 2MiB

// mu serialises writes. The deck logs from its update loop, from background jobs
// and from the pr-status fetcher's goroutines at once, and interleaved half-lines
// are worse than no lines.
var mu sync.Mutex

// pathOverride redirects the log, for tests. Empty means the real one.
var pathOverride string

// testBinary reports whether this process is a `go test` binary.
//
// Detected from the executable's name rather than by importing testing, which would
// put the test flag set into the real binary. Go names a test binary <pkg>.test, and
// `go test` also runs it from a temp build directory — either signal alone is enough
// to be sure, and neither can be true of an installed awp.
func testBinary() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.HasSuffix(filepath.Base(exe), ".test")
}

// SetPath redirects the log and returns a function restoring it.
//
// Tests must never append to the log the user is going to read: a suite that writes
// there is indistinguishable from the program doing it, and this log's whole value is
// being a trustworthy record of what actually happened. That is enforced by default
// — see testBinary and the check in write — so this exists for the tests that want to
// *read* what was logged, not to make the rest of them safe.
func SetPath(path string) (restore func()) {
	mu.Lock()
	previous := pathOverride
	pathOverride = path
	mu.Unlock()
	return func() {
		mu.Lock()
		pathOverride = previous
		mu.Unlock()
	}
}

// SetPathForTest is SetPath scoped to one test.
func SetPathForTest(t interface{ Cleanup(func()) }, path string) {
	t.Cleanup(SetPath(path))
}

// Path is the file being written to.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	if pathOverride != "" {
		return pathOverride
	}
	return config.LogPath()
}

// Errorf records something that went wrong. This is the one every caller owes:
// whatever a user is shown, the error itself belongs here.
func Errorf(format string, args ...any) { write("ERR", format, args...) }

// Infof records something worth knowing that is not a failure — what a subprocess
// was asked to do, which review resolved, how a run finished.
func Infof(format string, args ...any) { write("INF", format, args...) }

// write appends one line: a timestamp, a level, and the message with newlines
// folded out.
//
// Folded rather than written as-is because a log where one entry can span lines is
// a log you cannot grep. GitHub's GraphQL errors and gh's stderr both arrive
// multi-line, and those are exactly the messages being kept.
func write(level, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	path := pathOverride
	if path == "" {
		if testBinary() {
			// A test that has not asked for a log does not get one. Opt-in rather than
			// opt-out, because the failure mode of the other arrangement is silent: a new
			// package's tests quietly start appending fixtures to the user's log, and
			// nobody notices until they are reading it for a real failure.
			return
		}
		path = config.LogPath()
	}
	if strings.TrimSpace(path) == "" {
		// No home directory to write under. Nothing to be done about it, and it must
		// not become an error the caller has to handle.
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	rotateIfBig(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	msg := fmt.Sprintf(format, args...)
	msg = strings.ReplaceAll(msg, "\r\n", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	_, _ = fmt.Fprintf(f, "%s %s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), level, msg)
}

// rotateIfBig moves the log aside once it passes the cap, keeping one generation.
//
// One, not many: the value of an old log falls off a cliff, and the point of the
// previous generation is only that a failure right after a rotation still has its
// context somewhere. Called with mu held.
func rotateIfBig(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxBytes {
		return
	}
	// Rename rather than truncate, so a session already holding the file open keeps
	// writing somewhere real instead of into a hole.
	_ = os.Rename(path, path+".1")
}

// Tail is the last n lines of the log, oldest first, for `awp logs` to print.
//
// The whole file is read: at the size cap that is a couple of megabytes, this runs
// when a human asks for it, and seeking backwards for a line count is code worth
// not having.
func Tail(n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
