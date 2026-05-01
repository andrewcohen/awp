package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs external commands.
type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

// ExecRunner is the production command runner.
type ExecRunner struct{}

func NewExecRunner() *ExecRunner {
	return &ExecRunner{}
}

func (r *ExecRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), explainExecError(name, string(out), err)
}

// explainExecError converts an opaque exec error into a message a user can
// act on. "executable not found" → suggests install/PATH fix; non-zero exit
// codes get the trailing stderr/stdout attached so the deck popup doesn't
// just show a bare "exit status 1".
func explainExecError(name, out string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s\n\n%s", notFoundHeadline(name), pathHint(name))
	}
	var perr *exec.Error
	if errors.As(err, &perr) {
		return fmt.Errorf("could not run %q: %w\n\n%s", name, perr.Err, pathHint(name))
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		snippet := strings.TrimSpace(out)
		if len(snippet) > 800 {
			snippet = snippet[:800] + "…"
		}
		switch code {
		case 127:
			lead := fmt.Sprintf("%q exited 127 — the shell that ran it could not find the binary.", name)
			if snippet != "" {
				lead += "\n\nOutput:\n  " + snippet
			}
			return fmt.Errorf("%s\n\n%s", lead, pathHint(name))
		case 126:
			lead := fmt.Sprintf("%q exited 126 — file found but not executable (wrong permissions, wrong CPU arch, or it's a directory).", name)
			if snippet != "" {
				lead += "\n\nOutput:\n  " + snippet
			}
			lead += "\n\nFix: try `chmod +x` on the binary, or reinstall for your platform (e.g. `go install …` on this machine rather than copying the binary across architectures)."
			return errors.New(lead)
		}
		if snippet != "" {
			return fmt.Errorf("%q exited %d:\n%s", name, code, snippet)
		}
		return fmt.Errorf("%q exited %d (no output)", name, code)
	}
	return err
}

func notFoundHeadline(name string) string {
	return fmt.Sprintf("%q is not on $PATH for this process.", name)
}

// pathHint returns a multi-line, copy-pasteable explanation of why a binary
// might be missing from PATH (especially in tmux popup / run-shell contexts)
// and the two concrete fixes. Kept as plain text — printed to stderr so users
// see it directly when something blows up.
func pathHint(name string) string {
	return strings.Join([]string{
		"Why this can happen inside a tmux popup or run-shell:",
		"  tmux's popup/run-shell runs under a non-interactive /bin/sh that does NOT",
		"  source your ~/.zshrc / ~/.bashrc / fish config. The tmux server captures",
		"  PATH once when it starts; if it was launched from a context where your",
		"  shell rc had not yet added ~/go/bin (or wherever " + name + " lives), it never",
		"  will. That's why the same binding can work for one teammate and fail for",
		"  another with `exit 127`.",
		"",
		"Fixes (pick one):",
		"  1. Inject PATH into the tmux server (covers all popups). Add to ~/.tmux.conf:",
		"       set-environment -g PATH \"$HOME/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin\"",
		"     Then reload: `tmux source-file ~/.tmux.conf`.",
		"  2. Use an absolute path in the binding, e.g.:",
		"       bind a display-popup -E \"$HOME/go/bin/awp deck\"",
		"",
		"Verify by running a popup that prints what tmux actually sees:",
		"  tmux display-popup -E 'echo \"$PATH\"; which " + name + "; read'",
		"(`tmux show-environment` does NOT answer this — it only lists vars set via",
		"set-environment, and PATH usually isn't there even when it works fine.)",
		"If the popup's $PATH doesn't include the install dir, fix #1 above resolves it.",
	}, "\n")
}
