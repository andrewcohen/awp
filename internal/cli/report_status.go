package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

// validReportStates is the closed set of states agents may report.
var validReportStates = map[string]struct{}{
	"working": {},
	"idle":    {},
	"waiting": {},
	"exited":  {},
}

// runReportStatus is the entry point for `awp internal report-status`.
//
// It is invoked by per-agent hooks/extensions installed globally via
// `awp init hooks`. The hook command resolves the workspace via
// $AWP_WORKSPACE (workspace name) and one of:
//   - $AWP_REPO_ROOT (preferred, absolute repo root path)
//   - $AWP_REPO      (project basename; ambiguous if multiple repos share it)
//
// When env vars are missing the command exits 0 silently so a misconfigured
// hook never breaks an agent turn.
func runReportStatus(args []string, out io.Writer) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(out, "Usage: awp internal report-status --state <working|idle|waiting|exited>")
		return nil
	}
	state := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--state":
			if i+1 >= len(args) {
				return fmt.Errorf("--state requires a value")
			}
			state = args[i+1]
			i++
		case strings.HasPrefix(arg, "--state="):
			state = strings.TrimPrefix(arg, "--state=")
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		return errors.New("--state is required")
	}
	if _, ok := validReportStates[state]; !ok {
		return fmt.Errorf("invalid --state %q (want working|idle|waiting|exited)", state)
	}

	workspaceName, repoName, repoRoot := resolveWorkspaceIdent()
	if workspaceName == "" {
		return nil
	}
	return writeWorkspaceStatus(workspaceName, repoName, repoRoot, state)
}

// resolveWorkspaceIdent returns (AWP_WORKSPACE, AWP_REPO, AWP_REPO_ROOT) with
// a tmux fallback. When the process env is empty (e.g. the calling Claude/pi
// was launched before the tmux session env was injected), we ask tmux for the
// session-level values. This makes hooks robust against stale process
// environments.
func resolveWorkspaceIdent() (workspace, repo, repoRoot string) {
	workspace = strings.TrimSpace(os.Getenv("AWP_WORKSPACE"))
	repo = strings.TrimSpace(os.Getenv("AWP_REPO"))
	repoRoot = strings.TrimSpace(os.Getenv("AWP_REPO_ROOT"))
	if workspace != "" {
		return
	}
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return
	}
	if v := tmuxLocalEnv("AWP_WORKSPACE"); v != "" {
		workspace = v
	}
	if repo == "" {
		repo = tmuxLocalEnv("AWP_REPO")
	}
	if repoRoot == "" {
		repoRoot = tmuxLocalEnv("AWP_REPO_ROOT")
	}
	return
}

// tmuxLocalEnv reads a single session-level env var from the tmux server,
// using the current pane's session as the target. Empty on any error or
// when the variable is unset.
func tmuxLocalEnv(key string) string {
	out, err := exec.Command("tmux", "show-environment", key).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.HasPrefix(line, "-") {
		return ""
	}
	if idx := strings.IndexByte(line, '='); idx >= 0 {
		return strings.TrimSpace(line[idx+1:])
	}
	return ""
}

// writeWorkspaceStatus mutates Status on the matching entry. It prefers
// repoRoot (absolute path) for an exact match; falls back to repoName
// (basename of each known repo root) when the root is unknown. It also
// flips Unread=true on transitions into "attention" states so the tmux
// badge surfaces the change.
func writeWorkspaceStatus(workspaceName, repoName, repoRoot, status string) error {
	store := stateStore()

	// Suppress the badge when the user is literally looking at this
	// workspace's session — same logic as the deck's auto-clear, applied
	// at write time so the tmux status bar stays accurate without waiting
	// for a deck refresh.
	viewing := sessionHasAttachedClient(repoName, workspaceName)
	apply := func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		entry, ok := entries[workspaceName]
		if !ok {
			return entries
		}
		entry.Status = status
		if workspace.WantsAttention(status) {
			if viewing {
				entry.Unread = false
			} else {
				entry.Unread = true
			}
		}
		entries[workspaceName] = entry
		return entries
	}

	if repoRoot != "" {
		if u, ok := store.(updater); ok {
			return u.Update(repoRoot, apply)
		}
		entries, err := store.Load(repoRoot)
		if err != nil {
			return err
		}
		entries = apply(entries)
		return store.Save(repoRoot, entries)
	}

	if repoName == "" {
		return nil
	}
	all, err := store.LoadAll()
	if err != nil {
		return err
	}
	for root := range all {
		if filepath.Base(root) != repoName {
			continue
		}
		if u, ok := store.(updater); ok {
			return u.Update(root, apply)
		}
		entries, err := store.Load(root)
		if err != nil {
			return err
		}
		entries = apply(entries)
		return store.Save(root, entries)
	}
	return nil
}

type updater interface {
	Update(repoRoot string, fn func(map[string]workspace.Entry) map[string]workspace.Entry) error
}

// sessionHasAttachedClient reports whether at least one tmux client is
// currently attached to the workspace's session — i.e. the user is looking
// at it. Best-effort: any tmux/exec error returns false (we'd rather badge
// than silently miss).
func sessionHasAttachedClient(repoName, workspaceName string) bool {
	repoName = strings.TrimSpace(repoName)
	workspaceName = strings.TrimSpace(workspaceName)
	if repoName == "" || workspaceName == "" {
		return false
	}
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		return false
	}
	session := "[awp]" + repoName + "__" + workspaceName
	out, err := exec.Command("tmux", "list-clients", "-t", session, "-F", "#{client_name}").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// stateStore returns a JSONStore. Indirection exists so tests can swap it.
var stateStore = func() reportStatusStore { return state.NewJSONStore() }

type reportStatusStore interface {
	Load(repoRoot string) (map[string]workspace.Entry, error)
	LoadAll() (map[string]map[string]workspace.Entry, error)
	Save(repoRoot string, entries map[string]workspace.Entry) error
}
