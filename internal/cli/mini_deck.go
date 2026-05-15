package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// runMiniDeck is the entrypoint for `awp mini-deck`: a quick-jump
// list of every workspace that has an active agent or an unread
// notification. Picking a row summons its tmux session (creating it
// if necessary) and switches the client to it, then exits — so the
// command is naturally one keystroke long when bound under tmux.
func runMiniDeck(runner Runner, in io.Reader, out io.Writer) error {
	store := state.NewJSONStore()
	all, err := store.LoadAll()
	if err != nil {
		return fmt.Errorf("load workspace state: %w", err)
	}
	tc := tmux.New(runner)
	snap := captureDeckTmuxSnapshot(tc, false)
	rows := buildMiniDeckRows(all, snap)

	// Always launch the TUI, even with zero rows — the empty-state
	// view renders "Nothing waiting on you." and lets the user
	// dismiss with q/esc. Exiting early here would close a tmux
	// `display-popup -E` popup before the message could be read.
	model := deckui.NewMiniModel(rows)
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return err
	}
	finalModel, ok := final.(deckui.MiniModel)
	if !ok {
		return nil
	}
	chosen := finalModel.Chosen()
	if chosen == nil {
		return nil
	}

	return jumpToMiniDeckRow(tc, store, *chosen)
}

// jumpToMiniDeckRow summons (creates if missing) the workspace's tmux
// session, marks the entry read, and switches the active client to it.
// Mirrors summonWorkspaceSession's behavior but operates from a
// state-store-derived row instead of a deckui.Item.
func jumpToMiniDeckRow(tc *tmux.Client, store *state.JSONStore, row deckui.MiniRow) error {
	sessionName := DeckSessionName(row.Project, row.Workspace)
	id, err := tc.SessionIDByName(sessionName)
	if err != nil {
		return err
	}
	if id == "" {
		if row.Path == "" {
			return fmt.Errorf("workspace %q has no recorded path; open it from the deck first", row.Workspace)
		}
		env := workspaceEnvPairs(row.Project, row.Workspace, row.RepoRoot)
		if _, err := createWorkspaceSession(tc, sessionName, row.Path, row.RepoRoot, env); err != nil {
			return err
		}
	}
	_ = ensureWorkspaceSessionEnvForItem(tc, sessionName, row.Project, row.Workspace, row.RepoRoot)
	_ = store.Update(row.RepoRoot, func(entries map[string]workspace.Entry) map[string]workspace.Entry {
		if e, ok := entries[row.Workspace]; ok {
			e.Unread = false
			entries[row.Workspace] = e
		}
		return entries
	})
	return tc.SwitchClient(sessionName)
}

// buildMiniDeckRows projects a global state snapshot into the mini-deck
// row list. A workspace shows up only if all of:
//
//  1. Its name is not "default" (jj-created stub workspace; skipped to
//     avoid one nuisance row per project from stale Claude prompts).
//  2. MiniIncluded says its (status, unread) tuple is actionable — see
//     that function for the per-status rules.
//  3. For "working" rows only, the tmux state (when known) confirms a
//     live agent: a session named [awp]<repo>__<workspace> exists and
//     its `:agent` pane is not a bare shell.
//
// (3) is the freshness check, and it's scoped to "working" because
// that's the one status that can go stale silently: Claude has no exit
// hook, so a crashed agent leaves status=working forever. Idle / waiting
// statuses are written by hooks that fire AFTER the work, so they
// accurately reflect a turn the user hasn't acknowledged — surface them
// regardless of whether the agent process is still alive (jumpToMiniDeckRow
// recreates the session if needed).
//
// Sorted by project name then workspace name so the list is stable.
func buildMiniDeckRows(all map[string]map[string]workspace.Entry, snap deckTmuxSnapshot) []deckui.MiniRow {
	var rows []deckui.MiniRow
	for repoRoot, entries := range all {
		project := projectNameForRepo(repoRoot)
		for name, e := range entries {
			// The "default" workspace is the jj-created stub per
			// project — users don't intentionally run agents in it,
			// and its tmux session almost always lingers with a
			// stale "waiting" status from a prior Claude
			// permission prompt. Filtering it out is a much better
			// default than surfacing one nuisance row per project.
			if strings.EqualFold(strings.TrimSpace(name), "default") {
				continue
			}
			if !deckui.MiniIncluded(e.Status, e.Unread) {
				continue
			}
			if snap.known && isWorkingStatus(e.Status) {
				sessionName := DeckSessionName(project, name)
				if _, alive := snap.liveByName[sessionName]; !alive {
					continue
				}
				if snap.agentShell[sessionName] {
					continue
				}
			}
			rows = append(rows, deckui.MiniRow{
				Project:   project,
				Workspace: name,
				RepoRoot:  repoRoot,
				Path:      e.Path,
				Status:    e.Status,
				Unread:    e.Unread,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Project != rows[j].Project {
			return rows[i].Project < rows[j].Project
		}
		return rows[i].Workspace < rows[j].Workspace
	})
	return rows
}

// isWorkingStatus reports whether the stored status indicates active
// work that should be cross-checked against tmux. Mirrors the working
// branch of deckui.MiniIncluded.
func isWorkingStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return true
	}
	return false
}
