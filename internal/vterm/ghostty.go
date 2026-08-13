//go:build ghosttyvt

// Package-level note: this file is the libghostty-vt implementation of Hosted,
// compiled only with -tags ghosttyvt because it is cgo against an archive built
// by Zig. A plain `go build ./...` must keep working for anyone who has not built
// one, which is what ghostty_off.go is for.
//
// Building it:
//
//	CGO_CFLAGS=-I$DIR/include CGO_LDFLAGS=$DIR/lib/libghostty-vt.a \
//	  go build -tags ghosttyvt -o $TMPDIR/awp-ghostty ./cmd/awp
//
// where $DIR is a prefix produced by, in a libghostty-vt source tree:
//
//	zig build -Demit-lib-vt=true -Demit-xcframework=false -Dsimd=false \
//	  -Doptimize=ReleaseFast --prefix $DIR
//
// Then AWP_PANE_VT=ghostty selects it for a pane. See internal/vterm/hosted.go.
//
// It deliberately mirrors Start's pty plumbing rather than sharing it. Sharing
// would mean abstracting the emulator inside Term, which is a larger change than
// the question this spike asks; if libghostty wins, the two collapse into one
// then. What it does NOT duplicate is the byte log — AWP_PANE_LOG records both
// directions here too, because that log is how the unexplained rendering defects
// get diagnosed and it would be useless if it only worked on the emulator being
// compared against.
package vterm

/*
#include <stdlib.h>
#include <ghostty/vt.h>

// The reply path. A terminal answers device queries — DA1, DSR, XTGETTCAP — by
// calling this, and the answer has to reach the pty or the program that asked is
// negotiating with a terminal that never responds. awpGhosttyWritePty is the
// exported Go half, in ghostty_callback.go.
//
// The terminal handle identifies which pane called, so userdata is not used at
// all. It is the right key for two reasons: cgo forbids handing C a Go pointer it
// holds onto, which rules out the obvious thing; and smuggling an integer id
// through a void* means converting a uintptr back into an unsafe.Pointer, which
// is a misuse `go vet` flags whether or not the value was ever a pointer.
extern void awpGhosttyWritePty(void *terminal, const uint8_t *data, size_t len);
static void awpWritePtyShim(GhosttyTerminal t, void *userdata, const uint8_t *data, size_t len) {
	(void)userdata;
	awpGhosttyWritePty((void *)t, data, len);
}
static GhosttyTerminalWritePtyFn awpWritePtyFn(void) { return awpWritePtyShim; }
*/
import "C"

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

// ghosttyTerm is a process on a pty with libghostty-vt interpreting its output.
type ghosttyTerm struct {
	gen  int
	ptmx *os.File
	cmd  *exec.Cmd

	// in is the pty's write side with the byte log wrapped around it, so Send and
	// the terminal's own replies go through the same tap the output does.
	in io.Writer

	dirty chan struct{}
	done  chan error

	// mu guards every call into the terminal and the encoders. libghostty has no
	// SafeEmulator equivalent — its header warns against concurrent vt_write — and
	// awp writes from the pty goroutine while rendering from Bubble Tea's.
	mu      sync.Mutex
	vt      C.GhosttyTerminal
	keyEnc  C.GhosttyKeyEncoder
	mouseNc C.GhosttyMouseEncoder
	fmtBuf  []byte
	closed  bool
	w, h    int

	// rs is a render state, held only to read the cursor's visual style.
	//
	// libghostty does not expose the shape on the terminal itself:
	// GHOSTTY_TERMINAL_DATA_CURSOR_STYLE is the SGR style for newly printed cells,
	// and the DECSCUSR shape is render-state data. So there is one of these per
	// pane, updated from the terminal when there is something new to read.
	rs C.GhosttyRenderState
	// writes counts chunks the pty has delivered, and rsWrites is the count the
	// render state was last updated at. A cursor shape cannot change without bytes
	// arriving, so comparing them is what keeps a render-state update off the
	// frames where nothing happened — and the update is a screen snapshot, on top
	// of the formatter pass View already makes every frame.
	writes   atomic.Uint64
	rsWrites uint64
	rsShape  tea.CursorShape
	rsBlink  bool
}

// hosts is every live ghosttyTerm by terminal handle, so the write_pty
// trampoline can find the one that called it.
var hosts struct {
	sync.Mutex
	byTerm map[uintptr]*ghosttyTerm
}

// succeeded reads a libghostty result. Named rather than compared inline at each
// call site because every one of these calls returns the same enum and the check
// is the same check — and because a comparison against a cgo enum constant reads
// to gocritic as a comparison of an expression with itself.
func succeeded(rc C.GhosttyResult) bool { return rc == C.GHOSTTY_SUCCESS }

func startGhostty(gen, w, h int, c *exec.Cmd, host HostColors) (Hosted, error) {
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

	t := &ghosttyTerm{
		gen:    gen,
		ptmx:   ptmx,
		cmd:    c,
		dirty:  make(chan struct{}, 1),
		done:   make(chan error, 1),
		fmtBuf: make([]byte, 64*1024),
		w:      w,
		h:      h,
	}

	rc := C.ghostty_terminal_new(nil, &t.vt, C.uint16_t(w), C.uint16_t(h)) //nolint:gosec // size bounded by the caller's viewport
	if !succeeded(rc) {
		_ = ptmx.Close()
		return nil, fmt.Errorf("vterm: ghostty_terminal_new(%dx%d): %d — is libghostty-vt the version this was built against?", w, h, rc)
	}
	if rc = C.ghostty_key_encoder_new(nil, &t.keyEnc); !succeeded(rc) {
		t.free()
		return nil, fmt.Errorf("vterm: ghostty_key_encoder_new: %d", rc)
	}
	if rc = C.ghostty_mouse_encoder_new(nil, &t.mouseNc); !succeeded(rc) {
		t.free()
		return nil, fmt.Errorf("vterm: ghostty_mouse_encoder_new: %d", rc)
	}
	if rc = C.ghostty_render_state_new(nil, &t.rs); !succeeded(rc) {
		t.free()
		return nil, fmt.Errorf("vterm: ghostty_render_state_new: %d", rc)
	}
	// The block is what a program that has said nothing gets, so it is the honest
	// starting value rather than a placeholder.
	t.rsShape, t.rsBlink = tea.CursorBlock, true

	// Registered before the callback is installed, because the first thing a
	// full-screen program does is ask questions.
	hosts.Lock()
	if hosts.byTerm == nil {
		hosts.byTerm = map[uintptr]*ghosttyTerm{}
	}
	hosts.byTerm[t.key()] = t
	hosts.Unlock()

	C.ghostty_terminal_set(t.vt, C.GHOSTTY_TERMINAL_OPT_WRITE_PTY, unsafe.Pointer(C.awpWritePtyFn()))
	t.setHostColors(host)

	// Both directions optionally recorded, the same way Start does it. The tap
	// sits where every byte in and every byte out passes through.
	sink := openLog(PaneLogEnv)
	toEmulator, toProcess := tapPair(sink, ghosttyWriter{t: t}, ptmx)
	t.in = toProcess

	go func() {
		_, _ = io.Copy(toEmulator, ptmx)
		t.done <- t.cmd.Wait()
	}()

	register(t)
	return t, nil
}

// ghosttyWriter feeds the terminal and raises the repaint flag. It is the
// io.Writer end of the pty read loop, so the lock is taken per chunk rather than
// per byte.
type ghosttyWriter struct{ t *ghosttyTerm }

func (g ghosttyWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	g.t.mu.Lock()
	if !g.t.closed {
		C.ghostty_terminal_vt_write(g.t.vt, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
	}
	g.t.mu.Unlock()
	g.t.writes.Add(1)
	select {
	case g.t.dirty <- struct{}{}:
	default: // a repaint is already pending; one is enough
	}
	return len(p), nil
}

// setHostColors tells the terminal what the screen it is really on looks like,
// so its answers to OSC 10 / 11 / 12 describe that screen rather than a default
// nobody chose. Only what is known is stated.
func (t *ghosttyTerm) setHostColors(host HostColors) {
	set := func(opt C.GhosttyTerminalOption, c color.Color) {
		if c == nil {
			return
		}
		r, g, b, _ := c.RGBA() // 16-bit per channel; the library wants 8
		col := C.GhosttyColorRgb{
			r: C.uint8_t(r >> 8),
			g: C.uint8_t(g >> 8),
			b: C.uint8_t(b >> 8),
		}
		C.ghostty_terminal_set(t.vt, opt, unsafe.Pointer(&col))
	}
	set(C.GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, host.Fg)
	set(C.GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, host.Bg)
	set(C.GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, host.Cursor)
}

// key identifies this terminal to the reply trampoline. The handle is a C
// pointer, used only for identity.
func (t *ghosttyTerm) key() uintptr { return uintptr(unsafe.Pointer(t.vt)) }

// Emulator is EmulatorGhostty, which is what the pane header reports so nobody
// has to ask a running deck which emulator it is on.
func (t *ghosttyTerm) Emulator() string { return EmulatorGhostty }

func (t *ghosttyTerm) Gen() int { return t.gen }

func (t *ghosttyTerm) Size() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w, t.h
}

func (t *ghosttyTerm) AwaitOutput() tea.Cmd {
	gen, dirty := t.gen, t.dirty
	return func() tea.Msg {
		<-dirty
		return OutputMsg{Gen: gen}
	}
}

func (t *ghosttyTerm) AwaitExit() tea.Cmd {
	gen, done := t.gen, t.done
	return func() tea.Msg {
		return ExitMsg{Gen: gen, Err: <-done}
	}
}

// View is the screen as exactly h lines.
//
// The padding is not cosmetic. The formatter drops trailing blank rows — 20 rows
// of terminal come back as however many are occupied — and every caller of
// Hosted.View places the result in a layout sized for h. A short block silently
// shifts the footer up.
func (t *ghosttyTerm) View() string {
	t.mu.Lock()
	screen, h := t.render(), t.h
	t.mu.Unlock()

	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r") // the formatter ends rows CRLF
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// render formats the current screen. Caller holds mu.
func (t *ghosttyTerm) render() string {
	if t.closed {
		return ""
	}
	var opts C.GhosttyFormatterTerminalOptions
	opts.size = C.size_t(unsafe.Sizeof(opts))
	opts.emit = C.GHOSTTY_FORMATTER_FORMAT_VT
	opts.extra.size = C.size_t(unsafe.Sizeof(opts.extra))
	opts.extra.screen.size = C.size_t(unsafe.Sizeof(opts.extra.screen))
	opts.extra.screen.style = C.bool(true)
	opts.extra.screen.hyperlink = C.bool(true)

	var f C.GhosttyFormatter
	if rc := C.ghostty_formatter_terminal_new(nil, &f, t.vt, opts); rc != C.GHOSTTY_SUCCESS {
		return ""
	}
	defer C.ghostty_formatter_free(f)

	for {
		var n C.size_t
		rc := C.ghostty_formatter_format_buf(f,
			(*C.uint8_t)(unsafe.Pointer(&t.fmtBuf[0])), C.size_t(len(t.fmtBuf)), &n)
		switch rc {
		case C.GHOSTTY_SUCCESS:
			return string(t.fmtBuf[:n])
		case C.GHOSTTY_OUT_OF_SPACE:
			// Grown once and kept, so a wide screen does not re-allocate per frame.
			t.fmtBuf = make([]byte, int(n))
		default:
			return ""
		}
	}
}

func (t *ghosttyTerm) LastLine() string {
	lines := strings.Split(ansi.Strip(t.View()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

func (t *ghosttyTerm) Send(b []byte) error {
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

// SendKey encodes a key press the way the hosted program asked for it.
//
// The encoder is synced from the terminal on every press rather than once at
// startup: which encoding applies depends on modes the program turns on and off
// as it runs — application cursor keys, and the Kitty keyboard flags awp
// otherwise has to sniff out of the output stream by hand. Asking the terminal
// is the difference between shift+enter arriving and not.
func (t *ghosttyTerm) SendKey(k tea.KeyPressMsg) {
	if b := t.encodeKey(k); len(b) > 0 {
		_ = t.Send(b)
	}
}

// encodeKey is the encoding on its own, returning what the program would receive
// and nil for a key that encodes to nothing.
//
// Separate from SendKey so a test can pin the bytes. Every input defect in a pane
// is either the encoding or the delivery, and until this was separable the only
// way to tell them apart was to type into a real pane and see whether anything
// happened — which is how three dead keys shipped.
func (t *ghosttyTerm) encodeKey(k tea.KeyPressMsg) []byte {
	key := tea.Key(k)

	var ev C.GhosttyKeyEvent
	if rc := C.ghostty_key_event_new(nil, &ev); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_key_event_free(ev)

	C.ghostty_key_event_set_action(ev, C.GHOSTTY_KEY_ACTION_PRESS)
	C.ghostty_key_event_set_key(ev, ghosttyKey(key.Code))
	C.ghostty_key_event_set_mods(ev, ghosttyMods(key.Mod))
	if key.Text != "" {
		utf8 := C.CString(key.Text)
		defer C.free(unsafe.Pointer(utf8))
		C.ghostty_key_event_set_utf8(ev, utf8, C.size_t(len(key.Text)))
	}
	if cp := unshiftedCodepoint(key); cp != 0 {
		C.ghostty_key_event_set_unshifted_codepoint(ev, C.uint32_t(cp))
	}

	var buf [128]C.char
	var n C.size_t
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	C.ghostty_key_encoder_setopt_from_terminal(t.keyEnc, t.vt)
	// Everything else the encoder needs comes from the terminal; this one cannot,
	// and the call above resets it, so it has to be re-stated on every press.
	//
	// It has to be TRUE here. The option is for a native macOS app, which is
	// handed a raw modifier and has to decide whether the user meant alt or meant
	// to type ø — awp is downstream of a terminal that already decided, so a key
	// arriving with alt set is alt. Left FALSE, the modifier is dropped and every
	// alt binding in the hosted program is dead: alt+b and alt+f, which is
	// word-motion in readline, so in every shell and every agent prompt.
	asAlt := C.GhosttyOptionAsAlt(C.GHOSTTY_OPTION_AS_ALT_TRUE)
	C.ghostty_key_encoder_setopt(t.keyEnc, C.GHOSTTY_KEY_ENCODER_OPT_MACOS_OPTION_AS_ALT, unsafe.Pointer(&asAlt))
	rc := C.ghostty_key_encoder_encode(t.keyEnc, ev, &buf[0], C.size_t(len(buf)), &n)
	t.mu.Unlock()

	if rc != C.GHOSTTY_SUCCESS || n == 0 {
		return nil
	}
	return []byte(C.GoStringN(&buf[0], C.int(n)))
}

// SendText delivers printable text, as a paste would.
func (t *ghosttyTerm) SendText(s string) {
	if s == "" {
		return
	}
	_ = t.Send([]byte(s))
}

func (t *ghosttyTerm) SendMouse(msg tea.MouseMsg) {
	if b := t.encodeMouse(msg); len(b) > 0 {
		_ = t.Send(b)
	}
}

// encodeMouse is SendMouse's encoding on its own, for the reason encodeKey is
// separate from SendKey: so a test can say what the program receives.
func (t *ghosttyTerm) encodeMouse(msg tea.MouseMsg) []byte {
	m := msg.Mouse()

	var ev C.GhosttyMouseEvent
	if rc := C.ghostty_mouse_event_new(nil, &ev); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	defer C.ghostty_mouse_event_free(ev)

	action, ok := ghosttyMouseAction(msg)
	if !ok {
		return nil
	}
	C.ghostty_mouse_event_set_action(ev, action)
	if button, known := ghosttyMouseButton(m.Button); known {
		C.ghostty_mouse_event_set_button(ev, button)
	} else {
		C.ghostty_mouse_event_clear_button(ev)
	}
	C.ghostty_mouse_event_set_mods(ev, ghosttyMods(m.Mod))
	// A position here is a pixel on the surface, not a cell: libghostty's mouse
	// encoder is written for a renderer that knows where its glyphs are, and it
	// divides by the cell geometry itself. awp only ever has cells, so it declares
	// a cell one pixel square below and this is the identity.
	C.ghostty_mouse_event_set_position(ev, C.GhosttyMousePosition{
		x: C.float(max(m.X, 0)),
		y: C.float(max(m.Y, 0)),
	})

	var buf [64]C.char
	var n C.size_t
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	C.ghostty_mouse_encoder_setopt_from_terminal(t.mouseNc, t.vt)
	// The terminal supplies the tracking mode and the report format; the geometry
	// and the button state it cannot know, and are not reset by the call above —
	// but the encoder starts with a zero cell size, which is how every event was
	// arriving at cell 1;1 no matter where the pointer was.
	//
	// One pixel per cell. The encoder wants a renderer's geometry and awp is not
	// one: there are no glyphs here to be halfway across, so the pane declares the
	// smallest cell there is and its pixels and its cells are the same numbers.
	// The one thing this gives up is SGR-pixel reporting (DEC mode 1016), where a
	// program asking for sub-cell precision is told the cell — which is all a pane
	// knows, and what the other emulator reports too.
	size := C.GhosttyMouseEncoderSize{
		size:          C.size_t(C.sizeof_GhosttyMouseEncoderSize),
		screen_width:  C.uint32_t(t.w),
		screen_height: C.uint32_t(t.h),
		cell_width:    1,
		cell_height:   1,
	}
	C.ghostty_mouse_encoder_setopt(t.mouseNc, C.GHOSTTY_MOUSE_ENCODER_OPT_SIZE, unsafe.Pointer(&size))
	// Whether a button is down is the embedder's to report, and it is what turns a
	// bare pointer move into a drag.
	pressed := C.bool(m.Button != tea.MouseNone)
	C.ghostty_mouse_encoder_setopt(t.mouseNc, C.GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, unsafe.Pointer(&pressed))
	rc := C.ghostty_mouse_encoder_encode(t.mouseNc, ev, &buf[0], C.size_t(len(buf)), &n)
	t.mu.Unlock()

	if rc != C.GHOSTTY_SUCCESS || n == 0 {
		return nil
	}
	return []byte(C.GoStringN(&buf[0], C.int(n)))
}

func (t *ghosttyTerm) Cursor() (int, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, 0, false
	}
	var x, y C.size_t
	var visible C.bool
	C.ghostty_terminal_get(t.vt, C.GHOSTTY_TERMINAL_DATA_CURSOR_X, unsafe.Pointer(&x))
	C.ghostty_terminal_get(t.vt, C.GHOSTTY_TERMINAL_DATA_CURSOR_Y, unsafe.Pointer(&y))
	C.ghostty_terminal_get(t.vt, C.GHOSTTY_TERMINAL_DATA_CURSOR_VISIBLE, unsafe.Pointer(&visible))
	return int(x), int(y), bool(visible)
}

// CursorShape is what the program asked its cursor to look like, which for an
// editor is which mode it is in — nvim's insert-mode bar, and back to a block on
// escape.
//
// Read off a render state rather than the terminal, because that is where
// libghostty keeps it: the terminal's own CURSOR_STYLE is the SGR style for
// newly printed cells, not the DECSCUSR shape.
//
// Updated only when the pty has delivered something since the last read. The
// update is a snapshot of the screen and this is called once per frame, on top of
// the formatter pass View already makes — and a shape cannot change without bytes
// arriving, so the frames where nothing happened get the cached answer. The
// counter is read before the work and stored after, both under the lock, so a
// chunk that lands mid-update is seen by the next call rather than lost.
//
// A hollow block is reported as a block. It is a real ghostty shape with no
// DECSCUSR spelling and no equivalent in tea's three, and of those three the
// block is the one it is.
func (t *ghosttyTerm) CursorShape() (tea.CursorShape, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return t.rsShape, t.rsBlink
	}
	at := t.writes.Load()
	if at == t.rsWrites {
		return t.rsShape, t.rsBlink
	}
	if !succeeded(C.ghostty_render_state_update(t.rs, t.vt)) {
		// Keep the last good answer. A failed update means the render state was not
		// refreshed, not that the cursor became a block.
		return t.rsShape, t.rsBlink
	}
	t.rsWrites = at

	var style C.GhosttyRenderStateCursorVisualStyle
	if succeeded(C.ghostty_render_state_get(t.rs, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_VISUAL_STYLE, unsafe.Pointer(&style))) {
		switch style {
		case C.GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR:
			t.rsShape = tea.CursorBar
		case C.GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE:
			t.rsShape = tea.CursorUnderline
		default:
			t.rsShape = tea.CursorBlock
		}
	}
	var blink C.bool
	if succeeded(C.ghostty_render_state_get(t.rs, C.GHOSTTY_RENDER_STATE_DATA_CURSOR_BLINKING, unsafe.Pointer(&blink))) {
		t.rsBlink = bool(blink)
	}
	return t.rsShape, t.rsBlink
}

// WantsMouse asks the terminal rather than tracking the five DEC modes by hand,
// which is what the x/vt path has to do.
func (t *ghosttyTerm) WantsMouse() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	var tracking C.GhosttyMouseTrackingMode
	if rc := C.ghostty_terminal_get(t.vt, C.GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING,
		unsafe.Pointer(&tracking)); rc != C.GHOSTTY_SUCCESS {
		return false
	}
	return tracking != C.GHOSTTY_MOUSE_TRACKING_NONE
}

// Resize moves the pty and the terminal together, for the reason Term.Resize
// does: the program lays out for the pty size and the terminal interprets what
// comes back, so a mismatch is wrapping off by however far they drifted.
func (t *ghosttyTerm) Resize(w, h int) error {
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
	if rc := C.ghostty_terminal_resize(t.vt, C.uint16_t(w), C.uint16_t(h), 0, 0); rc != C.GHOSTTY_SUCCESS { //nolint:gosec // bounded by the caller's viewport
		return fmt.Errorf("vterm: resize the terminal to %dx%d: %d", w, h, rc)
	}
	t.w, t.h = w, h
	return nil
}

func (t *ghosttyTerm) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	unregister(t)
	hosts.Lock()
	delete(hosts.byTerm, t.key())
	hosts.Unlock()

	_ = t.ptmx.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}

	// Freed after closed is set and the id is unreachable, so nothing can be in a
	// call on the terminal when it goes away.
	t.mu.Lock()
	defer t.mu.Unlock()
	t.free()
	return nil
}

// free releases the C side. Caller holds mu, or holds the only reference.
func (t *ghosttyTerm) free() {
	if t.rs != nil {
		C.ghostty_render_state_free(t.rs)
		t.rs = nil
	}
	if t.mouseNc != nil {
		C.ghostty_mouse_encoder_free(t.mouseNc)
		t.mouseNc = nil
	}
	if t.keyEnc != nil {
		C.ghostty_key_encoder_free(t.keyEnc)
		t.keyEnc = nil
	}
	if t.vt != nil {
		C.ghostty_terminal_free(t.vt)
		t.vt = nil
	}
}
