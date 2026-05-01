package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

// runUnreadSummary prints a tmux-status-bar friendly summary of workspaces
// that need attention. Empty output (no newline) when nothing's unread, so
// `status-right` strings collapse cleanly. Counts:
//
//	▲ N — waiting on user (yellow)
//	● N — notified (idle/exited after a turn, grey)
func runUnreadSummary(out io.Writer) error {
	store := state.NewJSONStore()
	all, err := store.LoadAll()
	if err != nil {
		return err
	}
	var waiting, notified int
	for _, entries := range all {
		for _, e := range entries {
			if !e.Unread {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(e.Status), "waiting") {
				waiting++
			} else {
				notified++
			}
		}
	}
	if waiting == 0 && notified == 0 {
		return nil
	}
	parts := make([]string, 0, 2)
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("#[fg=yellow]▲ %d#[default]", waiting))
	}
	if notified > 0 {
		parts = append(parts, fmt.Sprintf("● %d", notified))
	}
	_, err = fmt.Fprint(out, strings.Join(parts, "  "))
	return err
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
