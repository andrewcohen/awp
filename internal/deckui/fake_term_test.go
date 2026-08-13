package deckui

import (
	"os/exec"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/vterm"
)

// fakeTerm is a vterm.Hosted that runs the process and emulates nothing.
//
// Almost every test in this package is about what the deck does with a terminal —
// which key leaves one, what is remembered about an arrangement, whether a close
// hook runs, where a pane's box is — and none of that needs bytes interpreted. The
// real emulator is cgo against an archive built by Zig, so requiring one would
// make all of those tests unrunnable in a plain checkout, which is the wrong price
// for coverage of something they are not testing.
//
// The process is still started, because a pane whose program never ran is a
// different thing from a pane, and several tests are about a program exiting. What
// it writes goes nowhere: View is whatever the test set.
type fakeTerm struct {
	gen  int
	w, h int
	cmd  *exec.Cmd

	mu     sync.Mutex
	view   string
	closed bool
	sent   []byte
	keys   []tea.KeyPressMsg
	mice   []tea.MouseMsg
	// scroll is how far up the fake's view is, in rows, as a non-positive number —
	// zero is the tail, which is where a terminal starts.
	scroll  int
	exited  chan struct{}
	exitErr error
	// painted is pinged whenever the screen changes, so AwaitOutput behaves the
	// way the real one does: a command that resolves when there is a new frame.
	painted chan struct{}
	// What the hosted program has told this terminal about itself. Settable
	// because that is the half of these questions the deck is responsible for:
	// whether it draws a cursor for a program that wants one, and whether it
	// takes the mouse for a program that asked. Parsing \033[?25l out of a byte
	// stream is the emulator's half, and internal/vterm is where it is checked.
	//
	// The cursor starts visible, which is what a program that has said nothing
	// gets from a real terminal.
	cursorVisible bool
	cursorX       int
	cursorY       int
	wantsMouse    bool
	shape         tea.CursorShape
	shapeBlink    bool
	resized       []([2]int)
}

func newFakeTerm(gen, w, h int, c *exec.Cmd) *fakeTerm {
	t := &fakeTerm{
		gen: gen, w: w, h: h, cmd: c,
		exited:        make(chan struct{}),
		painted:       make(chan struct{}, 1),
		cursorVisible: true,
	}
	if c == nil {
		close(t.exited)
		return t
	}
	// The program's output lands on the screen, unparsed: appended as text, which
	// is enough for the one thing the deck asks a screen for besides drawing it —
	// the last line, which is how a pane reports why its process died. Started and
	// then waited on in the background, the same division the real terminal has
	// between starting a process and AwaitExit reaping it.
	c.Stdout, c.Stderr = writerTo(t), writerTo(t)
	if err := c.Start(); err != nil {
		t.exitErr = err
		close(t.exited)
		return t
	}
	go func() {
		err := c.Wait()
		t.mu.Lock()
		t.exitErr = err
		t.mu.Unlock()
		close(t.exited)
	}()
	return t
}

// openFakeTerm is the Model.openTerm substitute. It never fails: a test about a
// terminal that refuses to start says so by returning its own error.
func openFakeTerm(gen, w, h int, c *exec.Cmd, _ vterm.HostColors) (vterm.Hosted, error) {
	return newFakeTerm(gen, w, h, c), nil
}

func (t *fakeTerm) Emulator() string { return "fake" }
func (t *fakeTerm) Gen() int         { return t.gen }
func (t *fakeTerm) Size() (int, int) { return t.w, t.h }

func (t *fakeTerm) WantsMouse() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wantsMouse
}

func (t *fakeTerm) Cursor() (int, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cursorX, t.cursorY, t.cursorVisible
}

func (t *fakeTerm) CursorShape() (tea.CursorShape, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.shape, t.shapeBlink
}

// The setters, each standing in for a sequence the hosted program would have
// written: DECTCEM for the cursor, DECSET 1000 for the mouse. What the program
// prints reaches the screen on its own (see screenWriter), so there is no setter
// for that.

func (t *fakeTerm) hideCursor() {
	t.mu.Lock()
	t.cursorVisible = false
	t.mu.Unlock()
}

func (t *fakeTerm) askForMouse() {
	t.mu.Lock()
	t.wantsMouse = true
	t.mu.Unlock()
}

// setView and moveCursor state a screen and a cursor on it directly, for the
// tests that are about what the deck does with the pair — where the program is
// beside the point and starting a process to print the text would only make the
// screen arrive whenever it arrived.
func (t *fakeTerm) setView(s string) {
	t.mu.Lock()
	t.view = s
	t.mu.Unlock()
	t.repainted()
}

func (t *fakeTerm) moveCursor(x, y int) {
	t.mu.Lock()
	t.cursorX, t.cursorY = x, y
	t.mu.Unlock()
}

// View is the screen: exactly h lines of w cells, which is what a real terminal
// returns whether or not its program has written anything. The deck's layout
// arithmetic is measured against this, so a fake that returned "" for an
// unwritten screen would collapse every box under test.
func (t *fakeTerm) View() string {
	t.mu.Lock()
	lines := strings.Split(t.view, "\n")
	w, h, scroll := t.w, t.h, t.scroll
	t.mu.Unlock()
	// The window the view is on, the way the real terminal's View slices at its
	// viewport: the tail by default, further up when scrolled.
	from := max(len(lines)-h, 0) + scroll
	if from < 0 {
		from = 0
	}
	if from < len(lines) {
		lines = lines[from:]
	}
	out := make([]string, h)
	for i := range out {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		if n := w - lipgloss.Width(line); n > 0 {
			line += strings.Repeat(" ", n)
		}
		out[i] = line
	}
	return strings.Join(out, "\n")
}

// LastLine is the lowest non-blank line, the way the real one is — it is how a
// pane reports why its process died.
func (t *fakeTerm) LastLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(t.view, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimRight(lines[i], " ")
		}
	}
	return ""
}

func (t *fakeTerm) Send(b []byte) error {
	t.mu.Lock()
	t.sent = append(t.sent, b...)
	t.mu.Unlock()
	return nil
}

func (t *fakeTerm) SendKey(k tea.KeyPressMsg) {
	t.mu.Lock()
	t.keys = append(t.keys, k)
	t.mu.Unlock()
}

func (t *fakeTerm) SendText(s string) { _ = t.Send([]byte(s)) }

// SendMouse records rather than discards. What a real terminal does with a mouse
// event is the emulator's half and is checked in internal/vterm; the deck's half is
// which pane an event reaches at all, and that is only answerable if the pane
// remembers being handed one.
func (t *fakeTerm) SendMouse(msg tea.MouseMsg) {
	t.mu.Lock()
	t.mice = append(t.mice, msg)
	t.mu.Unlock()
}

// miceSeen is the mouse events this terminal was handed, in order.
func (t *fakeTerm) miceSeen() []tea.MouseMsg {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]tea.MouseMsg(nil), t.mice...)
}

// SelectionText slices the fake's screen the way a real one slices its cells: the
// rows between the two points, with the first and last cut at their columns. No
// unwrapping, because the fake has no soft wraps — it never parsed anything.
func (t *fakeTerm) SelectionText(x0, y0, x1, y1 int) string {
	if y1 < y0 || (y1 == y0 && x1 < x0) {
		x0, y0, x1, y1 = x1, y1, x0, y0
	}
	lines := strings.Split(t.View(), "\n")
	if y0 < 0 || y0 >= len(lines) {
		return ""
	}
	y1 = min(y1, len(lines)-1)
	out := make([]string, 0, y1-y0+1)
	for y := y0; y <= y1; y++ {
		from, to := 0, lipgloss.Width(lines[y])
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = x1 + 1
		}
		out = append(out, strings.TrimRight(ansi.Cut(lines[y], from, to), " "))
	}
	return strings.Join(out, "\n")
}

// Scrolling. The fake keeps a scroll offset over the lines it was given, so the
// deck's half of the question — does the wheel move the view, does a pane say it is
// behind the tail — is answerable without an emulator. What a real terminal keeps
// in its scrollback is checked in internal/vterm.
func (t *fakeTerm) ScrollBy(rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	above := max(len(strings.Split(t.view, "\n"))-t.h, 0)
	t.scroll = min(max(t.scroll+rows, -above), 0)
}

func (t *fakeTerm) ScrollToBottom() {
	t.mu.Lock()
	t.scroll = 0
	t.mu.Unlock()
}

func (t *fakeTerm) Scrollback() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	above := max(len(strings.Split(t.view, "\n"))-t.h, 0)
	return above + t.scroll, t.scroll == 0
}

func (t *fakeTerm) Resize(w, h int) error {
	t.mu.Lock()
	t.w, t.h = w, h
	t.resized = append(t.resized, [2]int{w, h})
	t.mu.Unlock()
	return nil
}

func (t *fakeTerm) Close() error {
	t.mu.Lock()
	already := t.closed
	t.closed = true
	t.mu.Unlock()
	if !already && t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return nil
}

// AwaitOutput resolves on the next change to the screen, and never on a screen
// that never changes — a command that resolved immediately would have the deck
// repainting in a loop.
func (t *fakeTerm) AwaitOutput() tea.Cmd {
	gen, painted := t.gen, t.painted
	return func() tea.Msg {
		<-painted
		return vterm.OutputMsg{Gen: gen}
	}
}

func (t *fakeTerm) AwaitExit() tea.Cmd {
	gen, exited := t.gen, t.exited
	return func() tea.Msg {
		<-exited
		t.mu.Lock()
		err := t.exitErr
		t.mu.Unlock()
		return vterm.ExitMsg{Gen: gen, Err: err}
	}
}

// screenWriter appends what the hosted process writes to the fake's screen.
type screenWriter struct{ t *fakeTerm }

func writerTo(t *fakeTerm) screenWriter { return screenWriter{t} }

func (w screenWriter) Write(b []byte) (int, error) {
	t := w.t
	t.mu.Lock()
	t.view += string(b)
	t.mu.Unlock()
	t.repainted()
	return len(b), nil
}

// repainted wakes one waiting AwaitOutput, dropping the ping when nobody is
// waiting — a frame nobody asked for is a frame nobody has to hear about.
func (t *fakeTerm) repainted() {
	select {
	case t.painted <- struct{}{}:
	default:
	}
}

// keysSent is what the deck typed at this terminal, as strings.
func (t *fakeTerm) keysSent() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.keys))
	for _, k := range t.keys {
		out = append(out, k.String())
	}
	return out
}

// The test binary's panes run on fakeTerm unless a test says otherwise.
//
// Set here rather than at each deck a test builds, because decks are built at
// twenty-odd sites by calling New directly, and a terminal is not what any of
// them is about. See defaultTermOpener.
func init() { defaultTermOpener = openFakeTerm }

// fakeOf is the fake behind a pane, for the tests that drive it — a program
// hiding its cursor, asking for the mouse, painting a screen.
func fakeOf(t *testing.T, p *panePopover) *fakeTerm {
	t.Helper()
	f, ok := p.term.(*fakeTerm)
	if !ok {
		t.Fatalf("this pane's terminal is a %T, not the fake", p.term)
	}
	return f
}

// withRealTerm points a deck at the actual emulator, and skips when this build
// has none.
//
// For the tests that are about a hosted program talking to a terminal rather than
// about the deck: what bytes reach it, and whether what it paints survives the
// journey back out through the deck's own renderer. The emulator is cgo against an
// archive built by Zig (internal/vterm/ghostty.go), so a plain checkout has none
// and must not fail on these.
func withRealTerm(t *testing.T, m Model) Model {
	t.Helper()
	probe, err := vterm.Open(0, 20, 4, exec.Command("true"), vterm.HostColors{})
	if err != nil {
		t.Skipf("this build has no terminal emulator, so a pane can host nothing: %v", err)
	}
	_ = probe.Close()
	m.openTerm = vterm.Open
	return m
}

// openedRealPane is openedPane on the actual emulator, for the fidelity tests.
func openedRealPane(t *testing.T, backend *fakePanes) (Model, *panePopover) {
	t.Helper()
	m := withRealTerm(t, paneModel(t, backend))
	next, _ := m.trigger(ActionOpenWindow, "agent")
	m = next.(Model)
	p, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("no pane opened; status %q", m.status)
	}
	t.Cleanup(func() { p.close(&m) })
	return m, p
}
