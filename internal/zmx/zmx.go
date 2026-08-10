// Package zmx talks to the zmx session store.
//
// zmx keeps a process alive on a PTY and lets a client attach to it. Unlike
// tmux it has no windows, no panes and no status bar, so a client *is* the
// program — attaching renders the bare process, and the session's size follows
// whatever single client is looking at it. That is what lets awp own layout
// instead of negotiating for it.
//
// This package is the only thing in awp that knows the session substrate is
// zmx. Everything above it deals in names and commands.
package zmx

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/vterm"
)

// RunFunc runs a command and returns its combined output, matching the shape
// the rest of awp uses for injectable command execution.
type RunFunc func(ctx context.Context, dir, name string, args ...string) (string, error)

// Client issues zmx commands.
type Client struct{ run RunFunc }

// New builds a Client over the given runner.
func New(run RunFunc) Client { return Client{run: run} }

// Session is one entry from `zmx ls`.
type Session struct {
	Name     string
	PID      int
	Clients  int
	StartDir string
	// Ended is true once the session's command has exited. zmx keeps the
	// session listed afterwards so its output can still be read, so "listed"
	// and "running" are different questions.
	Ended    bool
	ExitCode int
	// Created is when zmx started the session, and Cmd the command it is
	// running. Both are only interesting to something displaying the session
	// to a human — `zmx ls` prints created as a unix stamp, which is not a
	// thing to show anyone.
	Created time.Time
	Cmd     string
	Labels  map[string]string
}

// Live reports whether the session's process is still running.
func (s Session) Live() bool { return !s.Ended }

// SessionName is the only way to name an awp session, so that every call site
// spells it the same and `zmx ls` stays readable.
//
// zmx rejects a name containing a slash — measured, it silently declines to
// create the session — so segments are joined with dots and anything outside
// a conservative set is replaced.
func SessionName(project, workspace, kind string) string {
	return "awp." + Sanitize(project) + "." + Sanitize(workspace) + "." + Sanitize(kind)
}

// ParseSessionName reads a name SessionName produced back into its parts, and
// reports whether it was one of ours at all — `zmx ls` lists every session on
// the machine, including ones awp did not create.
//
// The split is safe because sanitize replaces a dot with an underscore, so no
// segment can contain one. The cost is that a project or workspace whose real
// name had a dot comes back with an underscore; the parts are for display and
// for finding the matching deck row, not for addressing anything.
func ParseSessionName(name string) (project, workspace, kind string, ok bool) {
	parts := strings.Split(name, ".")
	if len(parts) != 4 || parts[0] != "awp" {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

// Sanitize is the spelling of a name segment that survives being written into a
// session name and read back out of one.
//
// Exported because a caller that has to recognise a segment coming back — a
// user-action pane matching its kind against the configured actions — has to
// compare against the same reduction, not against the original name.
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// List returns every session zmx knows about.
func (c Client) List(ctx context.Context) ([]Session, error) {
	out, err := c.run(ctx, "", "zmx", "ls")
	if err != nil {
		return nil, fmt.Errorf("list zmx sessions (is zmx installed and on PATH?): %w", err)
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		if s, ok := parseSession(line); ok {
			sessions = append(sessions, s)
		}
	}
	return sessions, nil
}

// named checks that an operation addressing one session was given one to
// address, and names the operation when it wasn't.
//
// Every method below takes a name a caller computed, usually from SessionName
// over an Item's fields — and an Item arriving with a field missing is a thing
// this codebase has been bitten by more than once. A name that came out empty
// is a bug upstream, and the useful behaviour is to say so rather than to hand
// a process manager an empty argument and find out what it decides that means.
func named(op, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s a zmx session: no name given", op)
	}
	return nil
}

// Lookup finds one session by name.
func (c Client) Lookup(ctx context.Context, name string) (Session, bool, error) {
	if err := named("look up", name); err != nil {
		return Session{}, false, err
	}
	all, err := c.List(ctx)
	if err != nil {
		return Session{}, false, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, true, nil
		}
	}
	return Session{}, false, nil
}

// parseSession reads one `zmx ls` line: tab-separated key=value pairs, with
// anything unrecognised kept as a label.
func parseSession(line string) (Session, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Session{}, false
	}
	s := Session{Labels: map[string]string{}}
	for _, field := range strings.Split(line, "\t") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		switch key {
		case "name":
			s.Name = value
		case "pid":
			s.PID, _ = strconv.Atoi(value)
		case "clients":
			s.Clients, _ = strconv.Atoi(value)
		case "start_dir":
			s.StartDir = value
		case "ended":
			s.Ended = true
		case "exit_code":
			s.ExitCode, _ = strconv.Atoi(value)
		case "created":
			// A unix stamp. Parsed here so nothing downstream has to know that,
			// and so a display can show an age instead of a number.
			if secs, err := strconv.ParseInt(value, 10, 64); err == nil && secs > 0 {
				s.Created = time.Unix(secs, 0)
			}
		case "cmd":
			s.Cmd = value
		default:
			s.Labels[key] = value
		}
	}
	if s.Name == "" {
		return Session{}, false
	}
	return s, true
}

// Reap clears the way for a fresh session called name, and reports whether it
// had to remove one.
//
// A session that is listed but whose command has exited is not reusable:
// attaching to it would render a dead program's last screen. A live session is
// left alone — attaching to that one is the point.
//
// Creating the session is AttachCmd's job, not this one's. zmx has no way to
// start a session detached with a given command as its own process (see
// AttachCmd), so the only correct order is reap, then attach.
func (c Client) Reap(ctx context.Context, name string) (removed bool, err error) {
	if err := named("reap", name); err != nil {
		return false, err
	}
	existing, found, err := c.Lookup(ctx, name)
	if err != nil {
		return false, err
	}
	if !found || existing.Live() {
		return false, nil
	}
	if _, err := c.run(ctx, "", "zmx", "kill", name, "--force"); err != nil {
		return false, fmt.Errorf("remove the finished zmx session %q: %w", name, err)
	}
	return true, nil
}

// Label sets key=value metadata on a session, which `zmx ls` reports back.
func (c Client) Label(ctx context.Context, name string, pairs map[string]string) error {
	if len(pairs) == 0 {
		return nil
	}
	if err := named("label", name); err != nil {
		return err
	}
	args := []string{"set", name}
	for k, v := range pairs {
		args = append(args, k+"="+v)
	}
	if _, err := c.run(ctx, "", "zmx", args...); err != nil {
		return fmt.Errorf("label zmx session %q: %w", name, err)
	}
	return nil
}

// pasteSettle gives the receiving program time to finish ingesting a
// bracketed paste before the submit that follows it. Without the gap a large
// paste swallows the Enter instead of being submitted by it — the same
// hazard, and the same remedy, as the tmux path this mirrors.
const pasteSettle = 150 * time.Millisecond

// Paste delivers text to the session's program as one bracketed paste and
// then submits it.
//
// Bracketed rather than typed: a prompt with newlines sent as raw input is a
// stream of submits, so an agent would receive the first line as a message and
// the rest as separate ones. The markers tell it the whole block arrived at
// once.
func (c Client) Paste(ctx context.Context, name, text string) error {
	if err := named("paste to", name); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("paste to zmx session %q: nothing to send", name)
	}
	if _, err := c.run(ctx, "", "zmx", "send", name, "\x1b[200~"+text+"\x1b[201~"); err != nil {
		return fmt.Errorf("paste into zmx session %q: %w", name, err)
	}
	time.Sleep(pasteSettle)
	if _, err := c.run(ctx, "", "zmx", "send", name, "\r"); err != nil {
		return fmt.Errorf("submit the paste in zmx session %q: %w", name, err)
	}
	return nil
}

// Kill ends a session and everything attached to it.
func (c Client) Kill(ctx context.Context, name string) error {
	if err := named("kill", name); err != nil {
		return err
	}
	if _, err := c.run(ctx, "", "zmx", "kill", name, "--force"); err != nil {
		return fmt.Errorf("kill zmx session %q: %w", name, err)
	}
	return nil
}

// History returns the session's scrollback, with escape sequences, without
// attaching to it — a peek that costs nothing and needs no client.
func (c Client) History(ctx context.Context, name string) (string, error) {
	if err := named("read the scrollback of", name); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "", "zmx", "history", name, "--vt")
	if err != nil {
		return "", fmt.Errorf("read the scrollback of zmx session %q: %w", name, err)
	}
	return out, nil
}

// AttachCmd is the command that hosts a session in a terminal awp owns,
// creating it to run argv in dir if it is not there yet. An empty argv asks
// zmx for a login $SHELL instead.
//
// This is `zmx attach`, and deliberately not `zmx run`. run spawns a login
// bash and *types the command at its prompt*: the session's process is the
// shell, so the pane shows bash's banner, bash's prompt, the command echoed
// back, and an exit-code marker after it — none of which is the program you
// asked for. attach makes argv the session's own process, with zmx as its
// parent and nothing in between.
//
// argv is ignored when the session already exists, which is what makes one
// call both "create it" and "attach to it".
func AttachCmd(dir, name string, argv, env []string) *exec.Cmd {
	args := append([]string{"attach", name}, argv...)
	cmd := exec.Command("zmx", args...) //nolint:gosec // name comes from SessionName, argv from awp's own config
	cmd.Dir = dir
	cmd.Env = vterm.Env(env)
	return cmd
}

// Command is a process awp hosts directly, with no session behind it, for
// panes that should die when you stop looking at them.
func Command(dir string, argv []string, env []string) *exec.Cmd {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv is awp's own configuration
	cmd.Dir = dir
	cmd.Env = vterm.Env(env)
	return cmd
}
