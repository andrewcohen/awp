package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

// runUnreadSummary prints a tmux-status-bar friendly summary of workspace
// activity. Empty output (no newline) when there's nothing to show, so
// `status-right` strings collapse cleanly. Counts:
//
//	● N — working (green; live, shown regardless of the unread flag)
//	▲ N — waiting on user (yellow)
//	● N — notified (idle after a turn, grey)
func runUnreadSummary(out io.Writer) error {
	store := state.NewJSONStore()
	all, err := store.LoadAll()
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, formatUnreadSummary(all))
	return err
}

// formatUnreadSummary renders the tmux badge string from the full entry set.
// Buckets are mutually exclusive and working wins first: a workspace that
// resumed work but still carries a stale unread flag from a prior waiting
// turn counts as working, not double-counted as notified. Working mirrors
// the deck's always-on green dot — counted by status, not gated on unread.
// Exited workspaces never count: the agent is gone, so there's nothing to
// act on (and old state files may still carry a stale unread flag).
func formatUnreadSummary(all map[string]map[string]workspace.Entry) string {
	var working, waiting, notified int
	for _, entries := range all {
		for _, e := range entries {
			switch {
			case isWorkingStatus(e.Status):
				working++
			case workspace.IsExited(e.Status):
				continue
			case !e.Unread:
				continue
			case strings.EqualFold(strings.TrimSpace(e.Status), "waiting"):
				waiting++
			default:
				notified++
			}
		}
	}
	if working == 0 && waiting == 0 && notified == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	if working > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=green]● %d#[default]", working))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=yellow]▲ %d#[default]", waiting))
	}
	if notified > 0 {
		parts = append(parts, fmt.Sprintf("● %d", notified))
	}
	return strings.Join(parts, "  ")
}

// isWorkingStatus reports whether a status is an active "agent is doing
// work" state. Mirrors deckui.alwaysShownStatus so the badge's green count
// matches the deck's always-on green dot.
func isWorkingStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return true
	default:
		return false
	}
}

// runMarkRead clears the Unread flag for a single workspace. Resolves the
// workspace via flags (--workspace, --repo-root, --repo) or env (same vars
// as report-status). Silent no-op on missing identification so it's safe to
// wire into a tmux session-changed hook.
func runMarkRead(args []string) error {
	workspaceName, repoName, repoRoot := resolveWorkspaceIdent()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--workspace":
			if i+1 >= len(args) {
				return fmt.Errorf("--workspace requires a value")
			}
			workspaceName = args[i+1]
			i++
		case strings.HasPrefix(arg, "--workspace="):
			workspaceName = strings.TrimPrefix(arg, "--workspace=")
		case arg == "--repo-root":
			if i+1 >= len(args) {
				return fmt.Errorf("--repo-root requires a value")
			}
			repoRoot = args[i+1]
			i++
		case strings.HasPrefix(arg, "--repo-root="):
			repoRoot = strings.TrimPrefix(arg, "--repo-root=")
		case arg == "--repo":
			if i+1 >= len(args) {
				return fmt.Errorf("--repo requires a value")
			}
			repoName = args[i+1]
			i++
		case strings.HasPrefix(arg, "--repo="):
			repoName = strings.TrimPrefix(arg, "--repo=")
		default:
			return fmt.Errorf("unknown argument %q", arg)
		}
	}
	if strings.TrimSpace(workspaceName) == "" {
		return nil
	}
	clear := func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		entry, ok := entries[workspaceName]
		if !ok {
			return entries
		}
		entry.Unread = false
		entries[workspaceName] = entry
		return entries
	}
	store := state.NewJSONStore()
	if repoRoot != "" {
		return store.Update(repoRoot, clear)
	}
	if repoName == "" {
		return nil
	}
	all, err := store.LoadAll()
	if err != nil {
		return err
	}
	for root := range all {
		if pathBase(root) != repoName {
			continue
		}
		return store.Update(root, clear)
	}
	return nil
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
