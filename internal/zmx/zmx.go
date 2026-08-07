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
	Labels   map[string]string
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
	return "awp." + sanitize(project) + "." + sanitize(workspace) + "." + sanitize(kind)
}

func sanitize(s string) string {
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

// Lookup finds one session by name.
func (c Client) Lookup(ctx context.Context, name string) (Session, bool, error) {
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
			// Not currently used; kept out of Labels so it does not read as one.
		default:
			s.Labels[key] = value
		}
	}
	if s.Name == "" {
		return Session{}, false
	}
	return s, true
}

// Ensure guarantees a live session called name, running argv in dir, and
// reports whether it had to create one.
//
// A session that is listed but whose command has exited is replaced rather
// than reused: attaching to it would render a dead program's last screen.
func (c Client) Ensure(ctx context.Context, name, dir string, argv []string) (created bool, err error) {
	if name == "" {
		return false, fmt.Errorf("ensure a zmx session: no name given")
	}
	if len(argv) == 0 {
		return false, fmt.Errorf("ensure zmx session %q: no command to run", name)
	}
	existing, found, err := c.Lookup(ctx, name)
	if err != nil {
		return false, err
	}
	if found && existing.Live() {
		return false, nil
	}
	if found {
		if _, err := c.run(ctx, "", "zmx", "kill", name, "--force"); err != nil {
			return false, fmt.Errorf("replace the finished zmx session %q: %w", name, err)
		}
	}
	// `-d` after the name is the non-blocking form; without it zmx waits for
	// the command to finish.
	args := append([]string{"run", name, "-d"}, argv...)
	if _, err := c.run(ctx, dir, "zmx", args...); err != nil {
		return false, fmt.Errorf("start zmx session %q running %q: %w", name, strings.Join(argv, " "), err)
	}
	return true, nil
}

// Label sets key=value metadata on a session, which `zmx ls` reports back.
func (c Client) Label(ctx context.Context, name string, pairs map[string]string) error {
	if len(pairs) == 0 {
		return nil
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

// Kill ends a session and everything attached to it.
func (c Client) Kill(ctx context.Context, name string) error {
	if _, err := c.run(ctx, "", "zmx", "kill", name, "--force"); err != nil {
		return fmt.Errorf("kill zmx session %q: %w", name, err)
	}
	return nil
}

// History returns the session's scrollback, with escape sequences, without
// attaching to it — a peek that costs nothing and needs no client.
func (c Client) History(ctx context.Context, name string) (string, error) {
	out, err := c.run(ctx, "", "zmx", "history", name, "--vt")
	if err != nil {
		return "", fmt.Errorf("read the scrollback of zmx session %q: %w", name, err)
	}
	return out, nil
}

// AttachCmd is the command that hosts a session in a terminal awp owns.
//
// ctrl+\ is zmx's own detach key and is handled by this client rather than
// reaching the program, so it ends the process and awp learns the pane closed
// through the ordinary exit path. There is nothing to intercept.
func AttachCmd(name string, env []string) *exec.Cmd {
	cmd := exec.Command("zmx", "attach", name) //nolint:gosec // name comes from SessionName
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
