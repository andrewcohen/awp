package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

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
// Which entry lands in which bucket is workspace.Classify's call, not this
// function's — the badge and the deck have to report the same numbers, so
// there is one place that decides and this only formats the answer.
func formatUnreadSummary(all map[string]map[string]workspace.Entry) string {
	var working, waiting, notified int
	for _, entries := range all {
		for _, e := range entries {
			switch workspace.Classify(e.Status, e.Unread) {
			case workspace.AttentionWorking:
				working++
			case workspace.AttentionWaiting:
				waiting++
			case workspace.AttentionNotified:
				notified++
			case workspace.AttentionNone:
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
		// Same reasoning as workspace.MarkRead: clearing the badge happens
		// because you went and looked, which is the activity being recorded.
		entry.Touch(time.Now())
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
