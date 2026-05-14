package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type Client struct {
	runner Runner
}

func New(runner Runner) *Client {
	return &Client{runner: runner}
}

func (c *Client) WindowExists(name string) (bool, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-windows", "-F", "#{window_name}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) NewWindow(name string, dir string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "new-window", "-d", "-n", name, "-c", dir)
	if err != nil {
		return fmt.Errorf("create tmux window %q: %w", name, err)
	}
	return nil
}

// envFlags expands a slice of "KEY=VALUE" pairs into a flat []string of
// `-e KEY=VALUE` arguments for tmux. Empty / malformed entries are skipped.
func envFlags(env []string) []string {
	out := make([]string, 0, 2*len(env))
	for _, kv := range env {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		if !strings.ContainsRune(kv, '=') {
			continue
		}
		out = append(out, "-e", kv)
	}
	return out
}

func (c *Client) SendCommand(name string, command string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "send-keys", "-t", name, "-l", command)
	if err != nil {
		return fmt.Errorf("send command to tmux window %q: %w", name, err)
	}
	_, err = c.runner.Run(context.Background(), "", "tmux", "send-keys", "-t", name, "Enter")
	if err != nil {
		return fmt.Errorf("submit command in tmux window %q: %w", name, err)
	}
	return nil
}

func (c *Client) SwitchToWindow(name string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "select-window", "-t", name)
	if err != nil {
		return fmt.Errorf("switch to tmux window %q: %w", name, err)
	}
	return nil
}

func (c *Client) RenameWindow(oldName string, newName string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "rename-window", "-t", oldName, newName)
	if err != nil {
		return fmt.Errorf("rename tmux window %q to %q: %w", oldName, newName, err)
	}
	return nil
}

func (c *Client) KillWindow(name string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "kill-window", "-t", name)
	if err != nil {
		return fmt.Errorf("kill tmux window %q: %w", name, err)
	}
	return nil
}

type Session struct {
	ID   string
	Name string
}

// ListSessions returns all sessions with ids and names.
func (c *Client) ListSessions() ([]Session, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, err
	}
	var sessions []Session
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		sessions = append(sessions, Session{ID: parts[0], Name: parts[1]})
	}
	return sessions, nil
}

// SessionIDByName returns the live session id for a name, or empty string if absent.
func (c *Client) SessionIDByName(name string) (string, error) {
	sessions, err := c.ListSessions()
	if err != nil {
		return "", err
	}
	for _, s := range sessions {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", nil
}

func (c *Client) SessionExists(name string) (bool, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// SetSessionEnv sets a tmux session-level environment variable. The value is
// inherited by new panes/processes spawned in the session; it does not
// retroactively apply to already-running processes.
func (c *Client) SetSessionEnv(sessionName, key, value string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "set-environment", "-t", sessionName, key, value)
	if err != nil {
		return fmt.Errorf("set-environment %s on %q: %w", key, sessionName, err)
	}
	return nil
}

// GetSessionEnv returns the value of a session-level env var, or empty string
// if unset.
func (c *Client) GetSessionEnv(sessionName, key string) (string, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "show-environment", "-t", sessionName, key)
	if err != nil {
		// tmux exits non-zero with "unknown variable" when not set.
		if strings.Contains(err.Error(), "exit status") {
			return "", nil
		}
		return "", err
	}
	line := strings.TrimSpace(out)
	if line == "" || strings.HasPrefix(line, "-") {
		// "-FOO" means the var is explicitly unset in the session env.
		return "", nil
	}
	if idx := strings.IndexByte(line, '='); idx >= 0 {
		return line[idx+1:], nil
	}
	return "", nil
}

// NewSession creates a detached tmux session. env is a slice of "KEY=VALUE"
// pairs passed via tmux `-e`, which (unlike `set-environment`) is inherited
// by the session's first pane process — critical for hooks running inside
// the agent that need AWP_WORKSPACE et al. at fork time.
func (c *Client) NewSession(name string, dir string, firstWindowName string, env []string) error {
	args := []string{"new-session", "-d", "-s", name}
	if firstWindowName != "" {
		args = append(args, "-n", firstWindowName)
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, envFlags(env)...)
	_, err := c.runner.Run(context.Background(), "", "tmux", args...)
	if err != nil {
		return fmt.Errorf("create tmux session %q: %w", name, err)
	}
	return nil
}

func (c *Client) SwitchClient(sessionName string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "switch-client", "-t", sessionName)
	if err != nil {
		return fmt.Errorf("switch-client to session %q: %w", sessionName, err)
	}
	return nil
}

// SwitchClientLast switches the current client to the previously active session
// (tmux `switch-client -l`).
func (c *Client) SwitchClientLast() error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "switch-client", "-l")
	if err != nil {
		return fmt.Errorf("switch-client to last session: %w", err)
	}
	return nil
}

func (c *Client) KillSession(name string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "kill-session", "-t", name)
	if err != nil {
		return fmt.Errorf("kill tmux session %q: %w", name, err)
	}
	return nil
}

func (c *Client) RenameSession(oldName, newName string) error {
	_, err := c.runner.Run(context.Background(), "", "tmux", "rename-session", "-t", oldName, newName)
	if err != nil {
		return fmt.Errorf("rename tmux session %q to %q: %w", oldName, newName, err)
	}
	return nil
}

// NewWindowInSession creates a window in the named session (target form
// "session:window"). env is a slice of "KEY=VALUE" pairs passed via tmux
// `-e` so the window's first process inherits them.
func (c *Client) NewWindowInSession(sessionName, windowName, dir string, env []string) error {
	args := []string{"new-window", "-d", "-t", sessionName + ":", "-n", windowName}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, envFlags(env)...)
	_, err := c.runner.Run(context.Background(), "", "tmux", args...)
	if err != nil {
		return fmt.Errorf("create tmux window %q in session %q: %w", windowName, sessionName, err)
	}
	return nil
}

// NewShellWindowInSession creates a new window without specifying a name, so
// tmux applies its default (the running command / shell). env is a slice of
// "KEY=VALUE" pairs passed via tmux `-e`. Returns the target id
// (session:index) of the created window.
func (c *Client) NewShellWindowInSession(sessionName, dir string, env []string) (string, error) {
	args := []string{"new-window", "-d", "-t", sessionName + ":", "-P", "-F", "#{session_name}:#{window_index}"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, envFlags(env)...)
	out, err := c.runner.Run(context.Background(), "", "tmux", args...)
	if err != nil {
		return "", fmt.Errorf("create tmux shell window in session %q: %w", sessionName, err)
	}
	return strings.TrimSpace(out), nil
}

// SplitPaneInSession splits the target window within a session.
func (c *Client) SplitPaneInSession(sessionName, windowName, dir string, horizontal bool) error {
	args := []string{"split-window", "-d", "-t", sessionName + ":" + windowName}
	if horizontal {
		args = append(args, "-h")
	}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	_, err := c.runner.Run(context.Background(), "", "tmux", args...)
	if err != nil {
		return fmt.Errorf("split-pane %q:%q: %w", sessionName, windowName, err)
	}
	return nil
}

// DisplayPopup runs a command in a tmux popup. If sessionName is provided, the popup
// starts in that session context (so -t target resolves there).
func (c *Client) DisplayPopup(dir string, cmd string) error {
	args := []string{"display-popup", "-E"}
	if dir != "" {
		args = append(args, "-d", dir)
	}
	args = append(args, "-w", "80%", "-h", "80%", cmd)
	_, err := c.runner.Run(context.Background(), "", "tmux", args...)
	if err != nil {
		return fmt.Errorf("display-popup: %w", err)
	}
	return nil
}

type Window struct {
	ID      string
	Name    string
	Session string
}

// ListWindowsInSession returns windows for a session.
func (c *Client) ListWindowsInSession(sessionName string) ([]Window, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-windows", "-t", sessionName, "-F", "#{window_id}\t#{window_name}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, err
	}
	var windows []Window
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		windows = append(windows, Window{ID: parts[0], Name: parts[1], Session: sessionName})
	}
	return windows, nil
}

func (c *Client) CurrentWindow() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "display-message", "-p", "#{window_name}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Pane is a single tmux pane row, used by ListPanes to deliver
// session/window/command tuples in one fork/exec.
type Pane struct {
	Session string
	Window  string
	Command string
}

// ListPanes returns one row per pane across all sessions, with the
// pane's current command. A single shell-out replaces N per-session
// `display-message` calls when the deck enriches rows on refresh.
func (c *Client) ListPanes() ([]Pane, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-panes", "-a", "-F", "#{session_name}\t#{window_name}\t#{pane_current_command}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		panes = append(panes, Pane{Session: parts[0], Window: parts[1], Command: parts[2]})
	}
	return panes, nil
}

// PanePIDsBySession returns the shell PID of every pane across every
// live tmux session, bucketed by session name. One shell-out replaces
// N per-session list calls; the deck uses this to feed the dev-URL
// port discoverer (internal/portcapture). Empty map and nil error
// when tmux has no sessions.
func (c *Client) PanePIDsBySession() (map[string][]int, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "list-panes", "-a", "-F", "#{session_name}\t#{pane_pid}")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, err
	}
	by := map[string][]int{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		pid, perr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if perr != nil {
			continue
		}
		by[parts[0]] = append(by[parts[0]], pid)
	}
	return by, nil
}

func (c *Client) PaneCurrentCommand(target string) (string, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "display-message", "-p", "-t", target, "#{pane_current_command}")
	if err != nil {
		return "", fmt.Errorf("pane current command for %q: %w", target, err)
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) CurrentSessionID() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "display-message", "-p", "#{session_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) CurrentSessionName() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "tmux", "display-message", "-p", "#{session_name}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
