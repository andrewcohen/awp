package vterm

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// PaneLogEnv names a file to record a pane's traffic into. Unset means no
// recording, which is the normal case.
//
// It exists because two pane defects — a stray uppercase letter appearing in
// the bottom-left corner, and text left behind when a line is rewritten
// shorter — both come down to a question nobody could answer from the code:
// which bytes actually flowed. Every plausible cause was eliminated by
// measurement (the emulator's attributes, widths, erase primitives and
// resumption across writes are all correct, and zmx propagates a new client's
// size), which leaves capture as the only way forward.
const PaneLogEnv = "AWP_PANE_LOG"

// tap records bytes flowing one way between the deck and a hosted process.
//
// Both directions land in one file so their interleaving is visible: a reply
// generated while nothing is reading, and a byte surfacing later at a prompt,
// are the same event seen twice, and only the ordering says so.
type tap struct {
	mu   *sync.Mutex
	f    *os.File
	dir  string // "out" = process to us, "in" = us to the process
	next io.Writer
}

// openPaneLog opens the recorder named by PaneLogEnv, or returns nil when the
// variable is unset.
//
// The path comes from the environment, so it is treated as untrusted: the file
// is opened with 0600 (it will contain whatever an agent typed, including
// anything pasted into it), appended to rather than truncated, and any failure
// is silent — a debugging aid must never be the reason a pane will not start.
func openPaneLog() *os.File {
	path := strings.TrimSpace(os.Getenv(PaneLogEnv))
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// tapPair returns writers that record each direction into one file, or the
// originals when no log is configured.
func tapPair(f *os.File, toEmulator, toProcess io.Writer) (io.Writer, io.Writer) {
	if f == nil {
		return toEmulator, toProcess
	}
	mu := &sync.Mutex{}
	return &tap{mu: mu, f: f, dir: "out", next: toEmulator},
		&tap{mu: mu, f: f, dir: "in", next: toProcess}
}

func (t *tap) Write(p []byte) (int, error) {
	t.mu.Lock()
	// %q rather than raw bytes: the point is to read escape sequences by eye,
	// and a file full of real escapes would reprogram the terminal that cats it.
	_, _ = fmt.Fprintf(t.f, "%s %-3s %q\n", time.Now().Format("15:04:05.000"), t.dir, p)
	t.mu.Unlock()
	return t.next.Write(p)
}
