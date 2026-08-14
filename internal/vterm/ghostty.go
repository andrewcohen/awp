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
	"charm.land/lipgloss/v2"
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

	// modkeys is the one input mode the terminal cannot be asked about, read out
	// of the program's own output instead. Guarded by mu with the encoders.
	modkeys modkeysSniffer

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

	// The last answer displayCol gave, and what it was asked. Same bargain as
	// rsWrites above: the cursor cannot move without bytes arriving.
	colWrites  uint64
	colCellX   int
	colCellY   int
	colDisplay int
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
		// Sniffed under the same lock the encoders are read under, so a mode the
		// program set cannot be half-applied to a key pressed at the same moment.
		g.t.modkeys.feed(p)
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

// View is the h lines the viewport is on.
//
// The formatter emits the whole screen, scrollback included, from the top of the
// history down. So the rows to show are the window the viewport sits at, and the
// offset comes from the same scrollbar state Scrollback reports.
//
// It used to take the first h lines. That is right only for a screen with no
// history — which is every alt-screen program, so every agent, editor and pager
// looked correct — and wrong for a shell, where it meant the pane showed the first
// screenful it ever printed and never advanced. Output kept arriving, the emulator
// kept it, and the pane went on rendering the top of the scrollback: a shell that
// looked frozen after its first page and had, in the same breath, no scrolling and
// no live tail.
//
// The padding is not cosmetic either. The formatter drops trailing blank rows — 20
// rows of terminal come back as however many are occupied — and every caller places
// the result in a layout sized for h, so a short block silently shifts the footer
// up.
func (t *ghosttyTerm) View() string {
	t.mu.Lock()
	screen, h := t.render(), t.h
	offset := t.viewportRow()
	t.mu.Unlock()

	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r") // the formatter ends rows CRLF
	}
	// Clamped rather than trusted: the scrollbar and the formatter are two reads of
	// a terminal that the pty goroutine is still writing to, and a window past the
	// end of what came back would panic rather than show a stale row.
	if offset > max(len(lines)-h, 0) {
		offset = max(len(lines)-h, 0)
	}
	lines = lines[offset:]
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// viewportRow is how many rows of the formatted screen sit above the viewport.
// Caller holds mu.
func (t *ghosttyTerm) viewportRow() int {
	if t.closed {
		return 0
	}
	var bar C.GhosttyTerminalScrollbar
	C.ghostty_terminal_get(t.vt, C.GHOSTTY_TERMINAL_DATA_SCROLLBAR, unsafe.Pointer(&bar))
	return int(bar.offset)
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
	// And modifyOtherKeys, from what the program actually asked for. The call above
	// claims to set this from the terminal and answers `true` unconditionally, so
	// left to it every pane invents ESC[27;2;13~ for shift+enter — see
	// internal/vterm/modkeys.go for why awp tracks the mode itself.
	modOther := C.bool(t.modkeys.state2)
	C.ghostty_key_encoder_setopt(t.keyEnc, C.GHOSTTY_KEY_ENCODER_OPT_MODIFY_OTHER_KEYS_STATE_2, unsafe.Pointer(&modOther))
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
	//
	// The x that arrives is a column of the rendered screen, because that is the
	// screen the pointer was over. Which cell that is is #339 asked backwards, and
	// it has the same answer: on a row where a grapheme's cell footprint and its
	// rendered width differ, the two columns are different numbers and the program
	// is told about a cell the user did not point at.
	t.mu.Lock()
	cellX := t.cellForCol(max(m.X, 0), m.Y)
	t.mu.Unlock()
	C.ghostty_mouse_event_set_position(ev, C.GhosttyMousePosition{
		x: C.float(cellX),
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

// Cursor is where the cursor lands on the screen View draws — a display column,
// not the emulator's cell column.
//
// The two are not the same number, and the difference is #339. A grapheme's
// footprint in the cell grid is the emulator's own measurement; its footprint in
// the string View returns is whatever lipgloss/uniseg measures when the deck
// places that string on its screen. Those disagree: a ZWJ sequence
// (👩‍💻) occupies four cells and renders two columns wide, and glyphs
// exist that go the other way. The deck draws its cursor at an absolute column,
// so on any row whose prefix contains such a grapheme the cursor separated from
// the text — visibly one column off while typing, and worst in a split, where a
// narrow half wraps a program's status line down onto the row being typed on.
//
// So the cell column is translated here, by walking the cursor's row and adding
// up what each cell contributes to the rendered string. That keeps the
// translation next to the emulator that knows the cell widths, rather than
// leaving five callers to measure a row they were handed no cells for.
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
	return t.displayCol(int(x), int(y)), int(y), bool(visible)
}

// displayCol is how many columns the cells left of (cellX, y) occupy once
// rendered. Caller holds mu.
//
// Falls back to the cell column for anything it cannot read: a row the emulator
// will not hand over is a reason to be no worse than before, not a reason to put
// the cursor at zero.
//
// Memoised on the write counter, because libghostty says of the grid-ref lookup
// that it "isn't meant to be used as the core of a render loop" and this makes
// one per cell to the cursor's left. The cursor cannot move without bytes
// arriving, so the frames where nothing happened get the cached answer — the same
// bargain CursorShape makes, for the same reason.
func (t *ghosttyTerm) displayCol(cellX, y int) int {
	if cellX <= 0 {
		return cellX
	}
	writes := t.writes.Load()
	if t.colWrites == writes && t.colCellX == cellX && t.colCellY == y {
		return t.colDisplay
	}
	// Rebuilt as one string and measured once, rather than cell by cell. A ZWJ
	// sequence is stored across two wide cells — 👩 with the joiner, then
	// 💻 — and the formatter puts them back together, so measured apart
	// they are two graphemes of two columns each and together they are one of two.
	// Summing per-cell widths gets 4 where the rendered row has 2, which is the
	// whole defect in miniature.
	var b strings.Builder
	for x := range cellX {
		text, ok := t.cellText(x, y)
		if !ok {
			return cellX
		}
		b.WriteString(text)
	}
	col := lipgloss.Width(b.String())
	t.colWrites, t.colCellX, t.colCellY, t.colDisplay = writes, cellX, y, col
	return col
}

// cellForCol is displayCol's inverse: which cell of row y is drawn at display
// column col. Caller holds mu.
//
// Answers col itself for a row it cannot read, and for a column past the end of
// the row's content — past the text every cell is a blank one column wide, so
// there the two numbering schemes have already converged.
func (t *ghosttyTerm) cellForCol(col, y int) int {
	if col <= 0 || t.closed || y < 0 || y >= t.h {
		return max(col, 0)
	}
	// Measured over the whole prefix at each step, for the reason displayCol is:
	// a cell's contribution is not a width of its own. Two cells holding the
	// halves of a ZWJ sequence are two columns together and four apart, so the
	// cell that covers a column is the first one whose prefix reaches past it —
	// not the one a running total of per-cell widths lands on.
	var b strings.Builder
	for x := range t.w {
		text, ok := t.cellText(x, y)
		if !ok {
			return col
		}
		b.WriteString(text)
		if lipgloss.Width(b.String()) > col {
			return x
		}
	}
	return col
}

// SelectionText is the text of the cells from (x0,y0) to (x1,y1) inclusive, in
// reading order, as it should land on a clipboard.
//
// The range is given in the same display columns Cursor answers in and SendMouse
// takes, because the caller picked it out with a pointer over the rendered screen —
// see displayCol. Endpoints in either order describe the same range: a drag
// upwards or leftwards is still a selection of what lies between.
//
// Plain text, unwrapped and trimmed, which is what libghostty documents as
// matching a terminal's own copy behaviour: a line the shell soft-wrapped is one
// line when you paste it, and the blanks padding a short line to the width of the
// screen are not text you selected.
func (t *ghosttyTerm) SelectionText(x0, y0, x1, y1 int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ""
	}
	// Reading order, so the formatter is handed a forward range whichever way the
	// pointer travelled.
	if y1 < y0 || (y1 == y0 && x1 < x0) {
		x0, y0, x1, y1 = x1, y1, x0, y0
	}
	start, ok := t.gridRef(t.cellForCol(x0, y0), y0)
	if !ok {
		return ""
	}
	end, ok := t.gridRef(t.cellForCol(x1, y1), y1)
	if !ok {
		return ""
	}

	var sel C.GhosttySelection
	sel.size = C.size_t(unsafe.Sizeof(sel))
	sel.start, sel.end = start, end
	sel.rectangle = C.bool(false)

	var opts C.GhosttyTerminalSelectionFormatOptions
	opts.size = C.size_t(unsafe.Sizeof(opts))
	opts.emit = C.GHOSTTY_FORMATTER_FORMAT_PLAIN
	opts.unwrap = C.bool(true)
	opts.trim = C.bool(true)
	opts.selection = &sel

	// Sized by asking first, because a selection can be a screenful and the
	// formatter reports what it needs rather than truncating.
	var n C.size_t
	if rc := C.ghostty_terminal_selection_format_buf(t.vt, opts, nil, 0, &n); rc != C.GHOSTTY_OUT_OF_SPACE || n == 0 {
		if !succeeded(rc) {
			return ""
		}
	}
	buf := make([]byte, int(n))
	if rc := C.ghostty_terminal_selection_format_buf(t.vt, opts,
		(*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &n); !succeeded(rc) {
		return ""
	}
	return string(buf[:n])
}

// gridRef resolves a cell of the active area. Caller holds mu.
func (t *ghosttyTerm) gridRef(x, y int) (C.GhosttyGridRef, bool) {
	var ref C.GhosttyGridRef
	ref.size = C.size_t(unsafe.Sizeof(ref))
	var pt C.GhosttyPoint
	pt.tag = C.GHOSTTY_POINT_TAG_ACTIVE
	coord := (*C.GhosttyPointCoordinate)(unsafe.Pointer(&pt.value))
	coord.x, coord.y = C.uint16_t(max(x, 0)), C.uint32_t(max(y, 0))
	if rc := C.ghostty_terminal_grid_ref(t.vt, pt, &ref); !succeeded(rc) {
		return ref, false
	}
	return ref, true
}

// cellText is what one cell contributes to the rendered row.
//
// An empty cell is a space, because that is what it renders as. A wide
// character's trailing spacer contributes nothing — the formatter does not draw
// it, and the character it belongs to was emitted by the cell that owns it.
// Caller holds mu.
func (t *ghosttyTerm) cellText(x, y int) (string, bool) {
	ref, ok := t.gridRef(x, y)
	if !ok {
		return "", false
	}

	var cell C.GhosttyCell
	if rc := C.ghostty_grid_ref_cell(&ref, &cell); !succeeded(rc) {
		return "", false
	}

	var wide C.GhosttyCellWide
	if rc := C.ghostty_cell_get(cell, C.GHOSTTY_CELL_DATA_WIDE, unsafe.Pointer(&wide)); !succeeded(rc) {
		return "", false
	}
	if wide == C.GHOSTTY_CELL_WIDE_SPACER_TAIL {
		return "", true
	}

	var cp C.uint32_t
	if rc := C.ghostty_cell_get(cell, C.GHOSTTY_CELL_DATA_CODEPOINT, unsafe.Pointer(&cp)); !succeeded(rc) {
		return "", false
	}
	if cp == 0 {
		return " ", true // an empty cell renders as a space
	}

	// A cell can hold more than its primary codepoint: a combining mark, or the
	// joiner that makes a ZWJ sequence one grapheme rather than two emoji. Those
	// are exactly the codepoints that decide the width, so the cluster is what gets
	// returned when there is one. A cluster longer than the buffer falls back to
	// the primary codepoint rather than to nothing.
	var buf [16]C.uint32_t
	var n C.size_t
	if rc := C.ghostty_grid_ref_graphemes(&ref, &buf[0], C.size_t(len(buf)), &n); succeeded(rc) && n > 0 && int(n) <= len(buf) {
		var b strings.Builder
		for _, c := range buf[:n] {
			b.WriteRune(rune(c))
		}
		return b.String(), true
	}
	return string(rune(cp)), true
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
// ScrollBy moves the pane's view up or down through the scrollback. Negative is
// up, into history.
//
// The emulator keeps the history; nothing but this reaches it. A shell pane had no
// scrolling at all — what left the top of the screen was gone, because View renders
// the screen and the screen is the visible rows.
func (t *ghosttyTerm) ScrollBy(rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || rows == 0 {
		return
	}
	var beh C.GhosttyTerminalScrollViewport
	beh.tag = C.GHOSTTY_SCROLL_VIEWPORT_DELTA
	*(*C.intptr_t)(unsafe.Pointer(&beh.value)) = C.intptr_t(rows)
	C.ghostty_terminal_scroll_viewport(t.vt, beh)
}

// ScrollToBottom puts the view back on the live tail.
//
// Its own method rather than a large positive ScrollBy, because "follow the output
// again" is the state a pane returns to when you type into it, and a delta big
// enough to be sure would depend on how much history there is.
func (t *ghosttyTerm) ScrollToBottom() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	var beh C.GhosttyTerminalScrollViewport
	beh.tag = C.GHOSTTY_SCROLL_VIEWPORT_BOTTOM
	C.ghostty_terminal_scroll_viewport(t.vt, beh)
}

// Scrollback is how far back the view is: rows of history above the view, and
// whether the view is on the live tail.
//
// Reported as "above" rather than as libghostty's own offset-from-the-top because
// that is the question a caller has — whether there is anything up there to scroll
// to, and whether to say the pane is not showing the latest output. libghostty's
// header notes there is no change notification for scroll state and that callers
// should poll once a frame, which is what the deck does.
func (t *ghosttyTerm) Scrollback() (above int, atBottom bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, true
	}
	var bar C.GhosttyTerminalScrollbar
	C.ghostty_terminal_get(t.vt, C.GHOSTTY_TERMINAL_DATA_SCROLLBAR, unsafe.Pointer(&bar)) //nolint:gocritic // one read, two answers
	// total is the whole scrollable area, len the visible window, offset where the
	// window sits. The bottom is offset == total-len; anything less is scrolled up.
	tail := int(bar.total) - int(bar.len)
	if tail < 0 {
		tail = 0
	}
	return int(bar.offset), int(bar.offset) >= tail
}

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
