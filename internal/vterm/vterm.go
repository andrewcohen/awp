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
	"strings"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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

	// What the hosted program has asked its terminal for. Both are fed by
	// emulator callbacks, which fire on the goroutine writing into the
	// emulator and while it holds its own lock — so they are atomics rather
	// than guarded by mu, and the callback bodies must never call back into
	// the emulator.
	cursorVisible atomic.Bool
	mouseModes    atomic.Int32

	// in is the write side of the pty with the traffic recorder (see
	// PaneLogEnv) wrapped around it — Send has to go through the same tap the
	// emulator's replies do, or a capture would show only half of what reached
	// the process.
	in io.Writer

	// keys is which key encoding the hosted program asked for, read out of its
	// own output because x/vt neither answers those requests nor reports them.
	// See modkeys.go.
	keys keyRequests

	mu     sync.Mutex
	closed bool
	w, h   int
}

// mouseModes are the DEC private modes that mean "send me mouse events". A
// program that has set none of them has no use for the mouse, and the terminal
// hosting it should not take the mouse away from whoever does.
var mouseModes = [...]ansi.DECMode{
	ansi.ModeMouseX10,
	ansi.ModeMouseNormal,
	ansi.ModeMouseHighlight,
	ansi.ModeMouseButtonEvent,
	ansi.ModeMouseAnyEvent,
}

// mouseModeBit maps a mode to its slot in Term.mouseModes, and reports whether
// it is a mouse mode at all. The comparison is against the interface value, so
// a DEC mode never matches an ANSI mode that happens to share its number.
func mouseModeBit(m ansi.Mode) (int32, bool) {
	for i, mm := range mouseModes {
		if m == mm {
			return 1 << i, true
		}
	}
	return 0, false
}

// Start runs c on a w×h PTY and begins interpreting its output.
//
// gen is the caller's generation counter, echoed back on every message so a
// stale Term's frames can be discarded rather than painted over a new one.
//
// host is what the outer terminal looks like, and is a required argument for the
// reason the repo's other ambient values are: what a hosted program is told about
// its terminal has exactly one place to come from, and a caller that forgot to
// say does not fail — it silently hands the program x/vt's white-on-black. Pass
// a zero HostColors to mean "not known".
func Start(gen, w, h int, c *exec.Cmd, host HostColors) (*Term, error) {
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

	// What the outer terminal is, so the emulator's answers to OSC 10 / 11 / 12
	// describe the screen the pane is really on. Only what is known is stated:
	// x/vt's own defaults stand in for the rest, which is the same wrong answer
	// as before but at least not a newly invented one.
	//
	// Set here for the same reason SetCallbacks is — these are promoted from the
	// inner Emulator and so unguarded, and this is the last moment before output
	// starts flowing.
	if host.Fg != nil {
		t.emu.SetDefaultForegroundColor(host.Fg)
	}
	if host.Bg != nil {
		t.emu.SetDefaultBackgroundColor(host.Bg)
	}
	if host.Cursor != nil {
		t.emu.SetDefaultCursorColor(host.Cursor)
	}

	// A cursor is visible until a program hides it (DECTCEM starts set), and
	// nothing wants the mouse until a program asks.
	//
	// SetCallbacks is promoted from the inner Emulator and so is not
	// lock-guarded; this is the one safe moment to call it, before any output
	// has started flowing.
	t.cursorVisible.Store(true)
	t.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { t.cursorVisible.Store(visible) },
		EnableMode: func(m ansi.Mode) {
			if bit, ok := mouseModeBit(m); ok {
				t.mouseModes.Or(bit)
			}
		},
		DisableMode: func(m ansi.Mode) {
			if bit, ok := mouseModeBit(m); ok {
				t.mouseModes.And(^bit)
			}
		},
	})

	// Both directions optionally recorded — see PaneLogEnv. Wrapped here, at
	// the two io.Copy calls, because this is the only place every byte in and
	// every byte out passes through.
	// The sniffer sits between the recorder and the emulator so a capture shows
	// the same bytes it read.
	sniffed := &modeSniffer{next: notifier{w: t.emu, dirty: t.dirty}, keys: &t.keys}
	toEmulator, toProcess := tapPair(openLog(PaneLogEnv), sniffed, ptmx)
	t.in = toProcess

	// Pane output into the emulator, flagging a repaint after each chunk.
	go func() {
		_, _ = io.Copy(toEmulator, ptmx)
		t.done <- t.cmd.Wait()
	}()

	// The emulator's answers back out to the process. This is not optional and
	// not symmetry for its own sake: a terminal application asks questions —
	// tmux sends CSI c (Primary Device Attributes) the moment it attaches — and
	// the emulator replies on its own read side. With nobody draining it, that
	// write blocks *inside the parser, holding the emulator's lock*, so the
	// next View deadlocks. It fires on the first query, and it presents as a
	// hang rather than an error.
	go func() { _, _ = io.Copy(toProcess, t.emu) }()

	// Registered at the only place a Term comes into being, so CloseAll can
	// reach one whose owner lost track of it — see reap.go.
	register(t)
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

// TermType is what a hosted process should be told its terminal is.
//
// It has to be stated rather than inherited, because the process is talking to
// this emulator and not to whatever awp itself is running under. Inheriting it
// while awp runs inside tmux hands the child TERM=tmux-256color, and a
// screen-class terminal quietly loses capabilities the emulator has.
const TermType = "xterm-256color"

// Env prepares the environment for a hosted process: TermType, and no
// inherited multiplexer markers.
//
// A marker says "you are already inside me", and a hosted process is not
// inside whatever awp is inside — it is inside this emulator. Under tmux the
// markers make a nested client refuse to start, which is loud. ZMX_SESSION is
// the quiet one: `zmx attach` reads it and, finding one, tells the daemon to
// switch the *calling* client's session rather than making a new client. So a
// pane that inherited it does not open a session beside awp's, it steals the
// terminal awp is running in, and the session it was pulled off is re-created
// empty — losing whatever agent was in it. zmx sets the variable itself for a
// session's own child, so dropping the inherited value is not information lost.
func Env(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "TERM="),
			strings.HasPrefix(kv, "TMUX="),
			strings.HasPrefix(kv, "TMUX_PANE="),
			strings.HasPrefix(kv, "ZMX_SESSION="):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+TermType)
}

// HostTerm restores the real terminal's TERM in an environment Env prepared.
//
// Env states TERM because the child is talking to this emulator. A child handed
// the terminal itself — see the pane handover — is talking to the same terminal
// awp is, so the honest answer is awp's own TERM: xterm-ghostty describes what
// is on the other end, and claiming xterm-256color there gives up capabilities
// that are genuinely present.
//
// Only TERM is restored. The multiplexer markers Env dropped stay dropped, and
// ZMX_SESSION most of all: `zmx attach` reads it and switches the *calling*
// client's session rather than making a new one, so a handed-over attach that
// inherited it would steal the terminal awp is running in.
func HostTerm(env []string) []string {
	term := os.Getenv("TERM")
	if term == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TERM="+term)
}

// Emulator is EmulatorXVT: a Term is the x/vt implementation of Hosted.
func (t *Term) Emulator() string { return EmulatorXVT }

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

// LastLine is the lowest line with anything on it, as plain text.
//
// It exists for the moment the hosted process has just died. A program that
// refuses to start says why on its way out — `zmx attach` writes its complaint
// to the pty like everything else — and that complaint is only ever on this
// screen, which goes away with the terminal. A caller reporting the exit has
// nowhere else to read the reason from.
//
// The lowest rather than the last written: the emulator renders every row, so
// the rows below the output are blank and the interesting one is the last that
// is not.
func (t *Term) LastLine() string {
	lines := strings.Split(ansi.Strip(t.View()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// Send writes bytes to the process as if they had been typed.
func (t *Term) Send(b []byte) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return errors.New("vterm: the terminal is closed")
	}
	if _, err := t.in.Write(b); err != nil {
		return fmt.Errorf("vterm: send %d bytes to the pty: %w", len(b), err)
	}
	return nil
}

// SendKey delivers a key press to the process, encoded the way a real
// terminal would encode it.
//
// A printable key is sent as the characters it produced. Key.Text holds them —
// "A" for shift+a, "!" for shift+1 — and that is what a terminal puts on the
// wire. Routing them through the emulator's key table instead loses them
// outright: its fallback is `if key.Mod == 0 { seq += string(key.Code) }`, so
// any key carrying a modifier it does not list emits nothing, and shift+a
// would send the unshifted 'a' even if it did.
//
// Everything else is the emulator's to encode, and deliberately so. Arrows,
// function keys, ctrl and alt combinations, and the application-cursor-key
// mode a full-screen program switches on all have escape sequences that depend
// on the terminal's current modes — which the emulator tracks and we do not.
// Reproducing that table here would be a second, worse copy that drifts.
//
// The reply path is the same one the drain in Start serves: the emulator emits
// the encoded bytes on its read side, and the drain carries them to the PTY.
func (t *Term) SendKey(k tea.KeyPressMsg) {
	key := tea.Key(k)
	// Enter with a modifier held is checked before anything else, because it is
	// the one key whose legacy form carries no modifier at all: CR is CR, so
	// shift+enter reaches a program only in an encoding it asked for. See
	// modkeys.go. Agents bind it to "newline, don't submit", which is the whole
	// of multi-line input.
	if key.Code == tea.KeyEnter {
		if seq := enterKeyBytes(key.Mod, t.keys.encoding()); seq != "" {
			t.emu.SendText(seq)
			return
		}
	}
	// Shift is excluded from the modifier check because it is already baked
	// into Text. Ctrl and alt are not, and change the encoding entirely.
	if key.Text != "" && key.Mod&^tea.ModShift == 0 {
		t.emu.SendText(key.Text)
		return
	}
	// Only Code and Mod are handed on, because the emulator's table is a
	// switch over whole KeyPressEvent values — `case KeyPressEvent{Code: 'g',
	// Mod: ModCtrl}`. It zeroes BaseCode and ShiftedCode before matching but
	// not Text, so a ctrl+g that arrived carrying Text "g" equals none of its
	// cases and falls into the same default that drops the key.
	t.emu.SendKey(vt.KeyPressEvent(uv.Key(tea.Key{Code: key.Code, Mod: key.Mod})))
}

// SendText delivers printable text, as a paste would.
func (t *Term) SendText(s string) { t.emu.SendText(s) }

// SendMouse delivers a mouse event to the process.
//
// Without this the wheel never reaches the program at all: a terminal in
// alt-screen with no mouse tracking requested translates wheel movement into
// arrow keys, so scrolling a hosted pane types arrows at it. Asking for mouse
// events and passing them down is what makes a wheel a wheel again.
func (t *Term) SendMouse(msg tea.MouseMsg) {
	m := uv.Mouse(msg.Mouse())
	switch msg.(type) {
	case tea.MouseClickMsg:
		t.emu.SendMouse(uv.MouseClickEvent(m))
	case tea.MouseReleaseMsg:
		t.emu.SendMouse(uv.MouseReleaseEvent(m))
	case tea.MouseWheelMsg:
		t.emu.SendMouse(uv.MouseWheelEvent(m))
	case tea.MouseMotionMsg:
		t.emu.SendMouse(uv.MouseMotionEvent(m))
	}
}

// Cursor is where the hosted program has put its cursor, relative to the top
// left of the terminal, and whether it wants one drawn at all.
//
// The caller has to place it on screen itself: a Term is rendered as a string
// with no idea where that string ends up, and in Bubble Tea v2 the cursor is
// declared on the view rather than emitted into the content.
//
// visible is returned alongside the position rather than offered as a separate
// method so a caller cannot paint the one without consulting the other. A
// full-screen program hides its cursor and moves it wherever is convenient, so
// drawing an unwanted one puts a blinking block at an arbitrary spot.
func (t *Term) Cursor() (x, y int, visible bool) {
	pos := t.emu.CursorPosition()
	return pos.X, pos.Y, t.cursorVisible.Load()
}

// CursorShape is the block, always.
//
// x/vt does report DECSCUSR — Callbacks.CursorStyle fires with the style and,
// despite the parameter's name, the *steady* flag (screen.go passes !blink). It is
// not wired up here on purpose: this emulator is being deleted in favour of
// libghostty-vt, which is the one every pane actually runs on, so the six lines
// that would read it are six lines with a known removal date.
//
// The block is not a guess or a fallback — it is the terminal default, and what a
// program that never asked for a shape gets. So a pane on this emulator behaves
// exactly as it did before Hosted grew the method, rather than reporting something
// invented.
func (t *Term) CursorShape() (tea.CursorShape, bool) { return tea.CursorBlock, true }

// WantsMouse reports whether the hosted program has enabled mouse reporting.
//
// It matters because the host has to ask its own terminal for mouse events to
// have any to forward, and asking costs the terminal's native selection. For a
// program that never wanted the mouse that is a pure loss: the emulator drops
// the events (see SendMouse) and the user loses drag-to-select for nothing.
func (t *Term) WantsMouse() bool { return t.mouseModes.Load() != 0 }

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
	unregister(t)

	_ = t.ptmx.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	// The recorder is deliberately not closed: one file can be shared by every
	// pane and by the frame tee (see FrameLogEnv), so closing it here would take
	// the log out from under whoever is still writing. It is append-mode and
	// goes away with the process.
	return t.emu.Close()
}
