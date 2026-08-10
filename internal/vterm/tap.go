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

// FrameLogEnv names a file to record what a program writes to its own terminal
// into — the other end of the same question PaneLogEnv answers.
//
// PaneLogEnv covers everything between the deck and a hosted process, and
// nothing about the frame the deck then paints. That gap is where the
// ghost-text investigation stalled: a hosted program's dim hint was measured
// intact at every stage awp could reach, and there was no capture of the bytes
// the terminal actually received to say whether it left the process at all.
//
// Point both variables at one path to read a pane's output and the frame it
// produced in order.
const FrameLogEnv = "AWP_FRAME_LOG"

// tap records bytes flowing one way past the deck.
//
// Every direction lands in one file so the interleaving is visible: a reply
// generated while nothing is reading, and a byte surfacing later at a prompt,
// are the same event seen twice, and only the ordering says so. The same goes
// across the two logs — a pane's chunk and the frame it caused only mean
// something together.
type tap struct {
	mu   *sync.Mutex
	f    *os.File
	dir  string // "out" = process to us, "in" = us to the process, "tty" = us to our terminal
	next io.Writer
}

// logSink is one recorder file plus the lock that serializes writes to it.
type logSink struct {
	mu *sync.Mutex
	f  *os.File
}

// openLogs is every recorder opened so far, keyed by path.
//
// Package-level state, which this repo otherwise avoids, and it earns the
// exception: the useful configuration is both variables naming one file, and
// two independently opened handles would tear each other's lines apart. Keyed
// by path so one path means one lock no matter how many tees ask for it.
var openLogs struct {
	sync.Mutex
	byPath map[string]*logSink
}

// openLog opens the recorder named by env, or returns nil when it is unset.
//
// The path comes from the environment, so it is treated as untrusted: the file
// is opened with 0600 (it will contain whatever an agent typed, including
// anything pasted into it), appended to rather than truncated, and any failure
// is silent — a debugging aid must never be the reason a pane will not start.
func openLog(env string) *logSink {
	path := strings.TrimSpace(os.Getenv(env))
	if path == "" {
		return nil
	}
	openLogs.Lock()
	defer openLogs.Unlock()
	if sink, ok := openLogs.byPath[path]; ok {
		return sink
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	sink := &logSink{mu: &sync.Mutex{}, f: f}
	if openLogs.byPath == nil {
		openLogs.byPath = map[string]*logSink{}
	}
	openLogs.byPath[path] = sink
	return sink
}

// tapPair returns writers that record each direction of a pane's traffic, or
// the originals when no log is configured.
func tapPair(sink *logSink, toEmulator, toProcess io.Writer) (io.Writer, io.Writer) {
	if sink == nil {
		return toEmulator, toProcess
	}
	return &tap{mu: sink.mu, f: sink.f, dir: "out", next: toEmulator},
		&tap{mu: sink.mu, f: sink.f, dir: "in", next: toProcess}
}

// TapTerminal returns out wrapped to record every byte a program writes to its
// terminal, or out unchanged when FrameLogEnv is unset.
//
// The wrapper stays the terminal it wraps. Bubble Tea decides whether it has a
// tty by asserting term.File — an io.ReadWriteCloser with an Fd — on the writer
// it was handed, and the window size, raw mode and colour profile all follow
// from that answer. A plain io.Writer wrapper would therefore change the frames
// it exists to observe, which is the one thing a recorder must not do.
func TapTerminal(out io.Writer) io.Writer {
	sink := openLog(FrameLogEnv)
	if sink == nil {
		return out
	}
	t := &tap{mu: sink.mu, f: sink.f, dir: "tty", next: out}
	if f, ok := out.(*os.File); ok {
		return terminalTap{File: f, tap: t}
	}
	return t
}

// terminalTap is a recorded terminal: the embedded file answers everything
// about the tty, and only Write is intercepted.
type terminalTap struct {
	*os.File
	tap *tap
}

func (t terminalTap) Write(p []byte) (int, error) { return t.tap.Write(p) }

func (t *tap) Write(p []byte) (int, error) {
	t.mu.Lock()
	// %q rather than raw bytes: the point is to read escape sequences by eye,
	// and a file full of real escapes would reprogram the terminal that cats it.
	_, _ = fmt.Fprintf(t.f, "%s %-3s %q\n", time.Now().Format("15:04:05.000"), t.dir, p)
	t.mu.Unlock()
	return t.next.Write(p)
}
