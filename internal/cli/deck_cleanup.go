package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewcohen/awp/internal/tmux"
)

// pendingKillsPath returns the file path used to queue tmux cleanup actions
// for deferred execution, scoped to the given tmux session id. Returns
// ("", false) if the session id is empty.
func pendingKillsPath(sessionID string) (string, bool) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "", false
	}
	id = strings.TrimPrefix(id, "$")
	return filepath.Join(os.TempDir(), "awp-pending-kills-"+id+".txt"), true
}

type pendingAction struct {
	kind   string // "window", "session", "switch"
	target string
}

func appendPendingAction(path, kind, target string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", kind, target)
	return err
}

func appendPendingKill(path, window string) error {
	return appendPendingAction(path, "window", window)
}

func drainPendingActions(path string) ([]pendingAction, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var actions []pendingAction
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ' ')
		if idx <= 0 {
			continue
		}
		actions = append(actions, pendingAction{kind: line[:idx], target: strings.TrimSpace(line[idx+1:])})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	return actions, nil
}

func runDeckCleanup(runner Runner, _ io.Writer) error {
	if os.Getenv("TMUX") == "" {
		return nil
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	tc := tmux.New(runner)
	sessionID, err := tc.CurrentSessionID()
	if err != nil {
		return nil
	}
	path, ok := pendingKillsPath(sessionID)
	if !ok {
		return nil
	}
	actions, err := drainPendingActions(path)
	if err != nil {
		return nil
	}
	if len(actions) == 0 {
		return nil
	}

	var windows, sessions, switches []string
	for _, a := range actions {
		switch a.kind {
		case "window":
			windows = append(windows, a.target)
		case "session":
			sessions = append(sessions, a.target)
		case "switch":
			switches = append(switches, a.target)
		}
	}

	if len(sessions) > 0 {
		current, _ := tc.CurrentSessionName()
		doomed := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			doomed[s] = true
		}
		if doomed[current] {
			switched := false
			for _, target := range switches {
				if doomed[target] {
					continue
				}
				if id, err := tc.SessionIDByName(target); err == nil && id != "" {
					if err := tc.SwitchClient(target); err == nil {
						switched = true
						break
					}
				}
			}
			if !switched {
				_ = tc.SwitchClientLast()
			}
		}
	}

	for _, name := range sessions {
		if id, err := tc.SessionIDByName(name); err == nil && id != "" {
			_ = tc.KillSession(name)
		}
	}
	for _, name := range windows {
		_ = tc.KillWindow(name)
	}
	return nil
}
