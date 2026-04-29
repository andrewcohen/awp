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
// (basename of each known repo root) when the root is unknown.
func writeWorkspaceStatus(workspaceName, repoName, repoRoot, status string) error {
	store := stateStore()

	if repoRoot != "" {
		entries, err := store.Load(repoRoot)
		if err != nil {
			return err
		}
		entry, ok := entries[workspaceName]
		if !ok {
			return nil
		}
		entry.Status = status
		entries[workspaceName] = entry
		return store.Save(repoRoot, entries)
	}

	if repoName == "" {
		return nil
	}
	all, err := store.LoadAll()
	if err != nil {
		return err
	}
	for root, entries := range all {
		if filepath.Base(root) != repoName {
			continue
		}
		entry, ok := entries[workspaceName]
		if !ok {
			continue
		}
		entry.Status = status
		entries[workspaceName] = entry
		if err := store.Save(root, entries); err != nil {
			return err
		}
		return nil
	}
	return nil
}

// stateStore returns a JSONStore. Indirection exists so tests can swap it.
var stateStore = func() reportStatusStore { return state.NewJSONStore() }

type reportStatusStore interface {
	Load(repoRoot string) (map[string]workspace.Entry, error)
	LoadAll() (map[string]map[string]workspace.Entry, error)
	Save(repoRoot string, entries map[string]workspace.Entry) error
}
