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
	prCache, _, _ := loadPRStatusCache()
	rows := buildMiniDeckRows(all, snap, prCache)

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
// row list, using the same deckui.AttentionIncluded predicate that the
// regular deck's ScopeAttention applies. The only mini-deck-specific
// work here is computing the `active` flag from the tmux snapshot:
// a workspace counts as active when its [awp]<repo>__<workspace>
// session exists and the :agent pane is not a bare shell. When the
// snapshot is unknown (fast first paint), we trust the stored status
// and let a later refresh correct it.
//
// Sorted by project name then workspace name so the list is stable.
func buildMiniDeckRows(all map[string]map[string]workspace.Entry, snap deckTmuxSnapshot, prCache map[string]map[string]deckui.PRStatus) []deckui.MiniRow {
	var rows []deckui.MiniRow
	for repoRoot, entries := range all {
		project := projectNameForRepo(repoRoot)
		for name, e := range entries {
			active := true
			if snap.known {
				sessionName := DeckSessionName(project, name)
				_, alive := snap.liveByName[sessionName]
				active = alive && !snap.agentShell[sessionName]
			}
			if !deckui.AttentionIncluded(e.Status, e.Unread, active) {
				continue
			}
			prTitle, prNum := resolvePR(prCache, repoRoot, e.PRNumber, e.Bookmark)
			rows = append(rows, deckui.MiniRow{
				Project:   project,
				Workspace: name,
				RepoRoot:  repoRoot,
				Path:      e.Path,
				Status:    e.Status,
				Unread:    e.Unread,
				PRTitle:   prTitle,
				PRNumber:  prNum,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Project != rows[j].Project {
			return rows[i].Project < rows[j].Project
		}
		return strings.ToLower(miniDisplayLabel(rows[i])) < strings.ToLower(miniDisplayLabel(rows[j]))
	})
	return rows
}

// miniDisplayLabel returns the text that will render on a mini-deck row:
// "#N PRTitle" when a PR is resolved from the cache, workspace name
// otherwise.
func miniDisplayLabel(r deckui.MiniRow) string {
	if t := strings.TrimSpace(r.PRTitle); t != "" && r.PRNumber > 0 {
		return fmt.Sprintf("#%d %s", r.PRNumber, t)
	}
	return r.Workspace
}

// resolvePR mirrors deckui.Model.resolvePRStatus: prefer the pinned
// PRNumber, fall back to bookmark-keyed lookup. Returns the PR title
// and number, or ("", 0) when no matching PR is present in the cache.
func resolvePR(cache map[string]map[string]deckui.PRStatus, repo string, prNumber int, bookmark string) (string, int) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", 0
	}
	byHead, ok := cache[repo]
	if !ok {
		return "", 0
	}
	if prNumber > 0 {
		for _, s := range byHead {
			if s.Number == prNumber {
				return s.Title, s.Number
			}
		}
		return "", 0
	}
	if bm := strings.TrimSpace(bookmark); bm != "" {
		if s, ok := byHead[bm]; ok {
			return s.Title, s.Number
		}
	}
	return "", 0
}
