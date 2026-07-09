package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/andrewcohen/awp/internal/agenthooks"
)

// runInitHooks installs/updates Claude Code hooks and the pi.dev extension
// globally so that any agent run in an awp-managed tmux session reports its
// status back to awp.
func runInitHooks(args []string, out io.Writer) error {
	if isHelpArgSlice(args) {
		_, _ = fmt.Fprintln(out, "Usage: awp init hooks\nInstalls global Claude Code hooks and pi.dev extension so awp-managed agents report status.")
		return nil
	}
	if len(args) > 0 {
		return errors.New("init hooks takes no arguments")
	}
	changed, err := agenthooks.InstallClaude()
	if err != nil {
		return fmt.Errorf("install Claude hooks: %w", err)
	}
	if changed {
		_, _ = fmt.Fprintln(out, "Claude Code: hooks installed/updated in ~/.claude/settings.json")
	} else {
		_, _ = fmt.Fprintln(out, "Claude Code: hooks already up to date")
	}

	piChanged, err := agenthooks.InstallPi()
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(out, "pi.dev: skipped (%v)\n", err)
	case piChanged:
		_, _ = fmt.Fprintln(out, "pi.dev: extension installed/updated")
	default:
		_, _ = fmt.Fprintln(out, "pi.dev: extension already up to date")
	}
	return nil
}
