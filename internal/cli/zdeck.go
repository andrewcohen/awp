package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
	"github.com/andrewcohen/awp/internal/zdeck"
	"github.com/andrewcohen/awp/internal/zmx"
)

// runZdeck opens the navigation-flow proof of concept.
//
// It shares the deck's item loader on purpose: the thing being tried out is
// the layout and the panes, and re-deriving the workspace list would be a
// second source of truth for no benefit. Everything else is separate, so the
// working deck is never in a half-migrated state while this moves.
func runZdeck(runner Runner, svc workspace.Service, in io.Reader, out io.Writer) error {
	if _, err := exec.LookPath("zmx"); err != nil {
		return fmt.Errorf("zdeck needs zmx on PATH for long-lived panes — install it, or use `awp deck` (%w)", err)
	}

	j := jj.New(runner)
	tmuxClient := tmux.New(runner)
	repoRoot, err := j.RepoRoot()
	if err != nil {
		return fmt.Errorf("zdeck: not a jj repository: %w", err)
	}
	if workspace.IsHomeDir(repoRoot) {
		return fmt.Errorf("zdeck: refusing to open at $HOME — cd into a project first")
	}

	items, err := loadDeckItems(nil, tmuxClient, true, svc, repoRoot, filepath.Base(repoRoot), in, out)
	if err != nil {
		return fmt.Errorf("zdeck: load workspaces: %w", err)
	}

	client := zmx.New(func(ctx context.Context, dir, name string, args ...string) (string, error) {
		return runner.Run(ctx, dir, name, args...)
	})

	p := tea.NewProgram(zdeck.New(items, client), tea.WithInput(in), tea.WithOutput(out))
	_, err = p.Run()
	return err
}
