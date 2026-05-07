package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

// projectFinderFromRoots returns a deckui.ProjectFinder that walks the given
// root directories (tilde-expanded) up to maxDepth levels deep and reports
// any directory that looks like a repo (.git or .jj). Once a repo is found,
// its subdirectories are skipped.
func projectFinderFromRoots(roots []string, maxDepth int) deckui.ProjectFinder {
	return func() tea.Cmd {
		return func() tea.Msg {
			projects, err := discoverProjects(roots, maxDepth)
			return deckui.ProjectsDoneMsg{Projects: projects, Err: err}
		}
	}
}

func discoverProjects(roots []string, maxDepth int) ([]deckui.ProjectItem, error) {
	if maxDepth <= 0 {
		maxDepth = 4
	}
	seen := map[string]struct{}{}
	var out []deckui.ProjectItem
	home, _ := os.UserHomeDir()
	for _, raw := range roots {
		root := strings.TrimSpace(raw)
		if root == "" {
			continue
		}
		if strings.HasPrefix(root, "~") && home != "" {
			root = filepath.Join(home, strings.TrimPrefix(root, "~"))
		}
		root = filepath.Clean(root)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		walkProjectRoot(root, root, maxDepth, seen, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func walkProjectRoot(root, current string, maxDepth int, seen map[string]struct{}, out *[]deckui.ProjectItem) {
	rel, err := filepath.Rel(root, current)
	if err != nil {
		return
	}
	depth := 0
	if rel != "." {
		depth = strings.Count(rel, string(filepath.Separator)) + 1
	}
	if isRepoDir(current) {
		if workspace.IsHomeDir(current) {
			return
		}
		if _, ok := seen[current]; !ok {
			seen[current] = struct{}{}
			*out = append(*out, deckui.ProjectItem{Path: current, Name: filepath.Base(current)})
		}
		return
	}
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			continue
		}
		walkProjectRoot(root, filepath.Join(current, name), maxDepth, seen, out)
	}
}

func isRepoDir(path string) bool {
	for _, marker := range []string{".jj", ".git"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

// openProjectViaTmux is the deckui.ProjectOpener used by the deck. It
// summons (or creates) a tmux session named [awp]<basename>__default at
// the project path and switches the client to it.
func openProjectViaTmux(runner Runner) deckui.ProjectOpener {
	return func(p deckui.ProjectItem) error {
		path := strings.TrimSpace(p.Path)
		if path == "" {
			return fmt.Errorf("open: empty project path")
		}
		if workspace.IsHomeDir(path) {
			return fmt.Errorf("open: refusing to summon a session at $HOME")
		}
		repoName := strings.TrimSpace(filepath.Base(filepath.Clean(path)))
		if repoName == "" {
			return fmt.Errorf("open: cannot derive repo name from %q", path)
		}
		tc := tmux.New(runner)
		sessionName := DeckSessionName(repoName, "default")
		id, err := tc.SessionIDByName(sessionName)
		if err != nil {
			return fmt.Errorf("open: lookup session: %w", err)
		}
		if id == "" {
			env := workspaceEnvPairs(repoName, "default", path)
			if err := tc.NewSession(sessionName, path, "agent", env); err != nil {
				return fmt.Errorf("open: create session: %w", err)
			}
		}
		return tc.SwitchClient(sessionName)
	}
}
