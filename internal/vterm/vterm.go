// Package vterm hosts a live terminal — a real process on a real PTY — as
// something a Bubble Tea view can render.
//
// It exists so awp can own layout. Today peeking at an agent, opening it in a
// window, and summoning it are three different mechanisms; a Term is one
// mechanism at whatever size the caller draws it.
//
// Following the repo's sub-component rule, Term is a plain struct with a
// View, not a tea.Model — there is exactly one tea.Program and a hosted
// terminal must never become a second one.
package vterm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// OutputMsg reports that a Term's screen changed and the view should repaint.
// Gen identifies which Term produced it: a Term that has been closed can still
// have a frame in flight, and it must not repaint the one that replaced it.
type OutputMsg struct{ Gen int }

// ExitMsg reports that the hosted process is gone. Err is nil for a clean
// exit.
type ExitMsg struct {
	Gen int
	Err error
}

// Term is a process running on a PTY, with a terminal emulator interpreting
// its output into a screen the caller can render.
type Term struct {
	gen  int
	emu  *vt.SafeEmulator
	ptmx *os.File
	cmd  *exec.Cmd

	// dirty carries at most one pending repaint. Coalescing is deliberate: a
	// chatty process must not be able to outrun the renderer by queueing a
	// message per write.
	dirty chan struct{}
	done  chan error

	mu     sync.Mutex
	closed bool
	w, h   int
}

// Start runs c on a w×h PTY and begins interpreting its output.
//
// gen is the caller's generation counter, echoed back on every message so a
// stale Term's frames can be discarded rather than painted over a new one.
func Start(gen, w, h int, c *exec.Cmd) (*Term, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("vterm: %dx%d is not a usable size (want both > 0)", w, h)
	}
	if c == nil {
		return nil, errors.New("vterm: no command to run")
	}
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)}) //nolint:gosec // bounded by the caller's viewport
	if err != nil {
		return nil, fmt.Errorf("vterm: start %s on a pty: %w", c.Path, err)
	}
	t := &Term{
		gen:   gen,
		emu:   vt.NewSafeEmulator(w, h),
		ptmx:  ptmx,
		cmd:   c,
		dirty: make(chan struct{}, 1),
		done:  make(chan error, 1),
		w:     w,
		h:     h,
	}

	// Pane output into the emulator, flagging a repaint after each chunk.
	go func() {
		_, _ = io.Copy(notifier{w: t.emu, dirty: t.dirty}, ptmx)
		t.done <- t.cmd.Wait()
	}()

	// The emulator's answers back out to the process. This is not optional and
	// not symmetry for its own sake: a terminal application asks questions —
	// tmux sends CSI c (Primary Device Attributes) the moment it attaches — and
	// the emulator replies on its own read side. With nobody draining it, that
	// write blocks *inside the parser, holding the emulator's lock*, so the
	// next View deadlocks. It fires on the first query, and it presents as a
	// hang rather than an error.
	go func() { _, _ = io.Copy(ptmx, t.emu) }()

	return t, nil
}

// notifier writes through to the emulator and raises the repaint flag.
type notifier struct {
	w     io.Writer
	dirty chan struct{}
}

func (n notifier) Write(p []byte) (int, error) {
	written, err := n.w.Write(p)
	select {
	case n.dirty <- struct{}{}:
	default: // a repaint is already pending; one is enough
	}
	return written, err
}

// Gen is the generation this Term was started with.
func (t *Term) Gen() int { return t.gen }

// Size is the Term's current geometry.
func (t *Term) Size() (w, h int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w, t.h
}

// AwaitOutput blocks until the screen changes, then reports it. Re-issue it
// from Update after each OutputMsg to keep the terminal live.
func (t *Term) AwaitOutput() tea.Cmd {
	gen, dirty := t.gen, t.dirty
	return func() tea.Msg {
		<-dirty
		return OutputMsg{Gen: gen}
	}
}

// AwaitExit blocks until the hosted process ends.
func (t *Term) AwaitExit() tea.Cmd {
	gen, done := t.gen, t.done
	return func() tea.Msg {
		return ExitMsg{Gen: gen, Err: <-done}
	}
}

// View renders the current screen as a block of exactly h lines, ANSI intact,
// ready to drop into a lipgloss layout.
func (t *Term) View() string { return t.emu.Render() }

// Send writes bytes to the process as if they had been typed.
func (t *Term) Send(b []byte) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return errors.New("vterm: the terminal is closed")
	}
	if _, err := t.ptmx.Write(b); err != nil {
		return fmt.Errorf("vterm: send %d bytes to the pty: %w", len(b), err)
	}
	return nil
}

// SendKey delivers a key press to the process, encoded the way a real
// terminal would encode it.
//
// The encoding is the emulator's, not ours. Arrows, function keys, ctrl and
// alt combinations, and the application-cursor-key mode a full-screen program
// switches on all have escape sequences that depend on the terminal's current
// modes — which the emulator is already tracking and we are not. Reproducing
// that table here would be a second, worse copy that drifts.
//
// The reply path is the same one the drain in Start serves: the emulator emits
// the encoded bytes on its read side, and the drain carries them to the PTY.
func (t *Term) SendKey(k tea.KeyPressMsg) {
	t.emu.SendKey(vt.KeyPressEvent(uv.Key(tea.Key(k))))
}

// SendText delivers printable text, as a paste would.
func (t *Term) SendText(s string) { t.emu.SendText(s) }

// Resize changes the PTY window and the emulator together. They have to move
// as one: the process lays out for the PTY size, and the emulator interprets
// what comes back, so a mismatch shows up as wrapping that is off by however
// far apart they drifted.
func (t *Term) Resize(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("vterm: %dx%d is not a usable size (want both > 0)", w, h)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("vterm: the terminal is closed")
	}
	if t.w == w && t.h == h {
		return nil
	}
	if err := pty.Setsize(t.ptmx, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)}); err != nil { //nolint:gosec // bounded by the caller's viewport
		return fmt.Errorf("vterm: resize the pty to %dx%d: %w", w, h, err)
	}
	t.emu.Resize(w, h)
	t.w, t.h = w, h
	return nil
}

// Close tears the terminal down. It is safe to call more than once.
func (t *Term) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	_ = t.ptmx.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return t.emu.Close()
}
