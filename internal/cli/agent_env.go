package cli

import (
	"path/filepath"
	"strings"

	"github.com/andrewcohen/awp/internal/tmux"
)

// ensureWorkspaceSessionEnv idempotently sets AWP_WORKSPACE, AWP_REPO, and
// AWP_REPO_ROOT on a tmux session so that hooks running inside the session
// can attribute status reports to the right workspace.
//
// Per `tmux set-environment`, values only apply to processes spawned after
// the call — a long-running agent already in a pane does not pick them up.
// Returns staleAgent=true when the agent pane is currently running a non-shell
// process and the session env was previously unset/mismatched, so callers can
// surface a "restart agent" hint.
func ensureWorkspaceSessionEnv(tmuxClient *tmux.Client, sessionName, projectName, workspaceName, repoRoot, agentTarget string) (staleAgent bool, err error) {
	current, _ := tmuxClient.GetSessionEnv(sessionName, "AWP_WORKSPACE")
	currentRepo, _ := tmuxClient.GetSessionEnv(sessionName, "AWP_REPO")
	currentRoot, _ := tmuxClient.GetSessionEnv(sessionName, "AWP_REPO_ROOT")

	matches := strings.TrimSpace(current) == workspaceName &&
		strings.TrimSpace(currentRepo) == projectName &&
		(repoRoot == "" || strings.TrimSpace(currentRoot) == repoRoot)
	if matches {
		return false, nil
	}

	if err := tmuxClient.SetSessionEnv(sessionName, "AWP_WORKSPACE", workspaceName); err != nil {
		return false, err
	}
	if projectName != "" {
		if err := tmuxClient.SetSessionEnv(sessionName, "AWP_REPO", projectName); err != nil {
			return false, err
		}
	}
	if repoRoot != "" {
		if err := tmuxClient.SetSessionEnv(sessionName, "AWP_REPO_ROOT", repoRoot); err != nil {
			return false, err
		}
	}

	// Detect a pre-existing agent process that won't pick up the new env.
	if agentTarget != "" {
		if cmd, err := tmuxClient.PaneCurrentCommand(agentTarget); err == nil {
			if !isShellName(strings.TrimSpace(cmd)) {
				return true, nil
			}
		}
	}
	return false, nil
}

// ensureWorkspaceSessionEnvForItem is a convenience that pulls fields from a
// deckui.Item-shaped tuple. We pass them explicitly to avoid an import cycle.
func ensureWorkspaceSessionEnvForItem(tmuxClient *tmux.Client, sessionName, projectName, workspaceName, repoRoot string) bool {
	stale, err := ensureWorkspaceSessionEnv(tmuxClient, sessionName, projectName, workspaceName, repoRoot, sessionName+":agent")
	if err != nil {
		return false
	}
	return stale
}

func isShellName(name string) bool {
	switch name {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	default:
		return false
	}
}

// projectNameForRepo returns a stable project name from a repo root path.
func projectNameForRepo(repoRoot string) string {
	return filepath.Base(strings.TrimRight(repoRoot, "/"))
}

// workspaceEnvPairs returns the AWP_* env pairs (KEY=VALUE) for a workspace.
// Used with tmux new-session/new-window `-e` so the first pane process
// inherits them at fork time — set-environment alone does not retroactively
// apply to a pane that's already running.
func workspaceEnvPairs(projectName, workspaceName, repoRoot string) []string {
	var env []string
	if strings.TrimSpace(workspaceName) != "" {
		env = append(env, "AWP_WORKSPACE="+workspaceName)
	}
	if strings.TrimSpace(projectName) != "" {
		env = append(env, "AWP_REPO="+projectName)
	}
	if strings.TrimSpace(repoRoot) != "" {
		env = append(env, "AWP_REPO_ROOT="+repoRoot)
	}
	return env
}
