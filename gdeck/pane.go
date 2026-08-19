package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/andrewcohen/awp/internal/state"
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
	// opened is when the attach was started, so the wait for the first frame can
	// be reported separately from the spawn.
	opened time.Time
}

// Event names the frontend listens on. Output is base64 because a pty carries
// bytes, not text: a frame can split a UTF-8 sequence in half, and a JSON string
// cannot hold the half.
const (
	paneDataEvent = "pane:data"
	paneExitEvent = "pane:exit"
)

// Workspace is one row of the sidebar: a workspace, and the agent in it.
//
// The list is workspaces rather than sessions because a session is not what
// anyone is looking for. `zmx ls` returns the editor beside the agent and calls
// both a row, so a workspace being worked in appears twice and the list is
// twice as long as the thing it describes. The deck's own list has always been
// one row per workspace for the same reason.
//
// Agent is the session a click attaches to, and is the only one a click ever
// attaches to — an editor is nvim, and putting someone else's nvim in a pane is
// not a thing this POC is asking about. A workspace whose agent has exited keeps
// its row with Agent empty, because "this workspace has no agent right now" is
// an answer, and silently dropping it would make the list disagree with the deck.
type Workspace struct {
	Project   string
	Workspace string
	// Agent is the zmx session name to attach to, or "" when there is none.
	Agent string
	// Cmd is what the agent is running, for the row's second line.
	Cmd string
	// Others counts the workspace's non-agent sessions, so the row can say the
	// editor is there without listing it as somewhere to go.
	Others int
}

// Workspaces groups zmx's sessions into what the sidebar shows.
//
// Derived from the sessions rather than from deckdata, which is the deck's own
// view-model and the more complete answer: it knows workspaces that exist on
// disk with nothing running in them at all, and it knows which of them want
// attention. Neither is a question this POC asks — a pane needs something live
// to attach to — so the cheaper source is the honest one until the sidebar has
// to say more than "here is what you can open".
func (p *Panes) Workspaces() ([]Workspace, error) {
	sessions, err := zmxClient().List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("listing zmx sessions: %w", err)
	}

	byKey := map[string]*Workspace{}
	var order []string
	for _, s := range sessions {
		project, workspace, kind, ok := s.Identity()
		if !ok {
			// `zmx ls` lists every session on the machine, including ones awp
			// did not create. Those have no workspace to file under.
			continue
		}
		key := project + "\x00" + workspace
		row, seen := byKey[key]
		if !seen {
			row = &Workspace{Project: project, Workspace: workspace}
			byKey[key] = row
			order = append(order, key)
		}
		switch {
		case kind != "agent":
			row.Others++
		case s.Live():
			row.Agent, row.Cmd = s.Name, s.Cmd
		default:
			// An ended agent is listed but not attachable: zmx keeps a session
			// after its command exits so the output can still be read, so
			// "listed" and "running" are different questions.
			row.Cmd = s.Cmd
		}
	}

	rows := make([]Workspace, 0, len(order))
	for _, key := range order {
		rows = append(rows, *byKey[key])
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Project != rows[j].Project {
			return rows[i].Project < rows[j].Project
		}
		return rows[i].Workspace < rows[j].Workspace
	})
	return rows, nil
}

// History is what the session printed before this pane existed, escape
// sequences intact, base64 for the same reason the live stream is.
//
// A pane has no scrollback without it, and that is not a rendering bug however
// much it looks like one: `zmx attach` replays the session's *current screen*,
// so the wheel has nothing above the first frame to scroll to. `zmx history`
// answers the other question — what came before — and the deck has never needed
// to ask it, because a tmux or zmx client brings its own scrollback with it. A
// webview terminal starts empty and has to be told.
//
// Read without attaching, so nothing here resizes the session.
func (p *Panes) History(session string) (string, error) {
	if session == "" {
		return "", errors.New("pane history: no session named; pass one of the names Workspaces returns")
	}
	out, err := zmxClient().History(context.Background(), session)
	if err != nil {
		return "", fmt.Errorf("pane history: %w", err)
	}
	return base64.StdEncoding.EncodeToString([]byte(out)), nil
}

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
	closing := time.Now()
	if err := p.Close(); err != nil {
		return err
	}
	if took := time.Since(closing); took > time.Millisecond {
		slog.Info("gdeck pane closed previous", "ms", took.Milliseconds())
	}

	// os.Environ() rather than a bare environment, and AttachCmd rather than a
	// hand-built exec.Cmd, because `zmx attach` branches on ZMX_SESSION: from
	// outside a session it creates an independent client, and from inside one it
	// tells the daemon to switch the *calling* client's session — which would
	// steal the terminal gdeck was launched from and lose whatever agent was in
	// it. AttachCmd runs the environment through vterm.Env, which drops that
	// marker along with the other "you are already inside me" variables.
	// Split into spawn and first-byte, because "attaching is slow" has two very
	// different causes and they are fixed in different places: forking a process
	// onto a pty is awp's cost, while the wait for the first frame is the zmx
	// daemon deciding to hand this client the session's screen.
	started := time.Now()
	cmd := zmx.AttachCmd("", session, nil, os.Environ())
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //nolint:gosec // cols and rows are checked positive above and bounded by the caller's terminal size.
	if err != nil {
		return fmt.Errorf("pane open %s: starting `zmx attach` on a pty: %w", session, err)
	}
	slog.Info("gdeck pane spawned", "session", session, "ms", time.Since(started).Milliseconds())

	live := &livePane{session: session, cmd: cmd, tty: tty, done: make(chan struct{}), opened: started}
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
	first, drawn := true, false
	total := 0
	for {
		n, err := l.tty.Read(buf)
		total += n
		if n > 0 && first {
			first = false
			slog.Info("gdeck pane first byte", "session", l.session,
				"ms", time.Since(l.opened).Milliseconds(), "bytes", n)
		}
		if !drawn && total > 2048 {
			drawn = true
			slog.Info("gdeck pane screen", "session", l.session,
				"ms", time.Since(l.opened).Milliseconds(), "bytes", total)
		}
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

// AgentStatus is what the agent is doing right now, from the store the deck's
// status dots read.
//
// The chat cannot answer this and never will: a transcript gains a line after
// something finishes, so it is a record rather than a monitor. awp does not read
// status from the transcript either — Claude Code hooks push it
// (UserPromptSubmit, PreToolUse, Stop → `awp internal report-status`) into the
// workspace store, which is event-driven and current. So the answer already
// exists; it just was not being asked for here.
type AgentStatus struct {
	// Status is awp's own vocabulary — "working", "waiting", "idle" and the
	// variants report-status writes. Left as written rather than normalised,
	// because internal/workspace.attention.go is where that vocabulary is
	// interpreted and a second interpretation here would drift from it.
	Status string
	// Prompt is the last thing the user asked, which is what the deck shows
	// beside a working row.
	Prompt string
	Unread bool
}

// Status reports the live state of the workspace a session belongs to.
func (p *Panes) Status(project, workspace string) (AgentStatus, error) {
	repos, err := state.NewJSONStore().LoadAll()
	if err != nil {
		return AgentStatus{}, fmt.Errorf("reading workspace state: %w", err)
	}
	for _, entries := range repos {
		for _, entry := range entries {
			// Matched on the project's directory name and the workspace's own
			// name, which is what the session name is built from — see
			// zmx.SessionName. The store is keyed by repo path, and a session
			// only knows the project as a slug.
			if entry.Name != workspace {
				continue
			}
			return AgentStatus{Status: entry.Status, Prompt: entry.ActivePrompt, Unread: entry.Unread}, nil
		}
	}
	return AgentStatus{}, nil
}
