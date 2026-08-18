package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/andrewcohen/awp/internal/zmx"
)

// Panes attaches the webview to a live zmx session.
//
// The deck interprets a pane's bytes in Go — vterm.Hosted spawns the process,
// libghostty-vt reads the pty, and the deck asks the result for a screen to put
// in a layout. None of that happens here. The emulator is in the browser, so
// this side is a pipe: bytes off the pty go up as events, keystrokes and resizes
// come back down, and nothing in Go looks at what any of it means.
//
// That is why this does not go through vterm.Hosted despite being the same job.
// Hosted's whole surface is a terminal's answers — View, Cursor, CursorShape,
// SelectionText — and a pipe has none of them to give. Reaching for it here
// would mean running a second emulator in Go purely to discard its output.
//
// One session at a time, deliberately. The POC is asking whether a pane feels
// right, and a pane multiplexer is a different question that would have to be
// answered again once the sidebar exists.
type Panes struct {
	mu      sync.Mutex
	current *livePane
}

// livePane is one attached session: the child holding it open and the pty it
// speaks through.
type livePane struct {
	session string
	cmd     *exec.Cmd
	tty     *os.File
	// done closes when the reader goroutine has seen EOF, so Close can tell a
	// pane that exited on its own from one it is ending.
	done chan struct{}
}

// Event names the frontend listens on. Output is base64 because a pty carries
// bytes, not text: a frame can split a UTF-8 sequence in half, and a JSON string
// cannot hold the half.
const (
	paneDataEvent = "pane:data"
	paneExitEvent = "pane:exit"
)

// Sessions lists what zmx is holding, so the frontend has something real to
// attach to.
func (p *Panes) Sessions() ([]zmx.Session, error) {
	sessions, err := zmxClient().List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("listing zmx sessions: %w", err)
	}
	return sessions, nil
}

// LaunchedFrom names the zmx session gdeck itself was started from, or "" when
// it was not started from one.
//
// Read before anything strips it, and offered to the frontend so that row can be
// marked. Attaching to it is not a crash — vterm.Env keeps `zmx attach` from
// hijacking the calling client — but it is still a second client on the terminal
// the developer is sitting in, and the session will take gdeck's pane size. The
// first thing a POC gets clicked on is whatever is at the top of the list.
func (p *Panes) LaunchedFrom() string { return os.Getenv("ZMX_SESSION") }

// Open attaches to a session and starts streaming it. Any pane already open is
// closed first — see the note on Panes about why there is only ever one.
//
// Attaching is not read-only, and the POC should not pretend otherwise: a zmx
// session takes its size from the client looking at it, so opening a pane
// reflows the session's program to the pane's shape, and closing it reflows
// again for whatever client is left. On an agent mid-answer that is visible and
// unwelcome. Nothing here can avoid it — it is what attaching means — so the
// frontend says so before the click rather than after.
func (p *Panes) Open(session string, cols, rows int) error {
	if session == "" {
		return errors.New("pane open: no session named; pass one of the names Sessions returns")
	}
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane open %s: want a positive size, got %dx%d", session, cols, rows)
	}
	if err := p.Close(); err != nil {
		return err
	}

	// os.Environ() rather than a bare environment, and AttachCmd rather than a
	// hand-built exec.Cmd, because `zmx attach` branches on ZMX_SESSION: from
	// outside a session it creates an independent client, and from inside one it
	// tells the daemon to switch the *calling* client's session — which would
	// steal the terminal gdeck was launched from and lose whatever agent was in
	// it. AttachCmd runs the environment through vterm.Env, which drops that
	// marker along with the other "you are already inside me" variables.
	cmd := zmx.AttachCmd("", session, nil, os.Environ())
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:gosec // cols and rows are checked positive above and bounded by the caller's terminal size.
	if err != nil {
		return fmt.Errorf("pane open %s: starting `zmx attach` on a pty: %w", session, err)
	}

	live := &livePane{session: session, cmd: cmd, tty: tty, done: make(chan struct{})}
	p.mu.Lock()
	p.current = live
	p.mu.Unlock()

	go live.stream()
	return nil
}

// stream pumps the pty to the frontend until it ends.
//
// The read buffer is large on purpose. A pane's worst case is not a prompt, it
// is a build log arriving as fast as the kernel will hand it over, and every
// chunk costs a base64 encode plus an event across the bridge — so the size of
// the read is the size of that overhead's denominator.
func (l *livePane) stream() {
	defer close(l.done)

	app := application.Get()
	buf := make([]byte, 64*1024)
	for {
		n, err := l.tty.Read(buf)
		if n > 0 && app != nil {
			app.Event.Emit(paneDataEvent, base64.StdEncoding.EncodeToString(buf[:n]))
		}
		if err != nil {
			// A pty reports its child's exit as EIO rather than EOF, so neither is
			// an error worth reporting as one.
			if app != nil {
				app.Event.Emit(paneExitEvent, l.session)
			}
			return
		}
	}
}

// Send writes base64-encoded bytes to the pane as if they had been typed.
func (p *Panes) Send(dataB64 string) error {
	p.mu.Lock()
	live := p.current
	p.mu.Unlock()
	if live == nil {
		return errors.New("pane send: no pane is open; call Open first")
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("pane send: decoding base64 input: %w", err)
	}
	if _, err := live.tty.Write(data); err != nil {
		return fmt.Errorf("pane send to %s: writing %d bytes to the pty: %w", live.session, len(data), err)
	}
	return nil
}

// Resize changes the pty's size, which is how the session learns its shape: a
// zmx session takes its size from the client looking at it.
func (p *Panes) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane resize: want a positive size, got %dx%d", cols, rows)
	}
	p.mu.Lock()
	live := p.current
	p.mu.Unlock()
	if live == nil {
		return errors.New("pane resize: no pane is open; call Open first")
	}
	if err := pty.Setsize(live.tty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil { //nolint:gosec // checked positive above.
		return fmt.Errorf("pane resize %s to %dx%d: %w", live.session, cols, rows, err)
	}
	return nil
}

// Close ends the attach client. The session outlives it — that is the point of
// zmx, and of not killing what the deck is only looking at.
func (p *Panes) Close() error {
	p.mu.Lock()
	live := p.current
	p.current = nil
	p.mu.Unlock()
	if live == nil {
		return nil
	}

	closeErr := live.tty.Close()
	if live.cmd.Process != nil {
		_ = live.cmd.Process.Kill()
	}
	_ = live.cmd.Wait()
	<-live.done

	if closeErr != nil && !errors.Is(closeErr, os.ErrClosed) && !errors.Is(closeErr, io.EOF) {
		return fmt.Errorf("pane close %s: closing the pty: %w", live.session, closeErr)
	}
	return nil
}

// zmxClient talks to the same daemon the decks do, over plain exec.
//
// gdeck builds its own runner rather than borrowing internal/cli's: that package
// is the TUI, and a webview importing it to get one command runner would pull
// the deck's whole model in behind it.
func zmxClient() zmx.Client {
	return zmx.New(func(ctx context.Context, dir, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name and args come from internal/zmx, not from the webview.
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	})
}
