package cli

import (
	"fmt"
	"strings"
)

// `awp review <n> --no-attach` — start a review that nobody has to be watching.
//
// The review flow itself was already non-interactive: it fetches the PR, prepares a
// workspace at its head ref, writes the review brief and starts a reviewing agent.
// Two things around it were not, and both are the kind of failure that looks like
// success until you wait for it.
//
// A bare `awp review` opens a picker over `gh pr list`. For a person that is the
// nicest way to answer "which PR"; for an agent it is a terminal UI waiting on a
// keypress nobody will make. runReview therefore refuses the picker outright once
// either agent-facing flag is present, rather than letting it open and hang.
//
// And the flow ends by switching your tmux client into the review session, which
// means it needs a tmux to switch — `awp review must run inside tmux`. A caller with
// no client wants the half before that: prepare it, start the reviewer, return. That
// is reviewOpts.paneHosted, which exists for the deck that hosts its own panes and
// fits a shell caller for the same reason `awp workspace new` reuses
// openRequest.PaneHosted — nobody here is attaching to a tmux window.

// reviewDetached runs the review flow for a caller that cannot answer questions.
//
// project may be empty, in which case the repo the process is standing in is used —
// the same asymmetry `awp w send` documents, and the same reason it prints what it
// resolved to. noAttach false with a project named still goes through here, because
// reviewing a PR in another project through the tmux path would prepare the
// workspace in the named repo and then try to switch a client into a session named
// for it, which is a different repo's session at best.
func (a *App) reviewDetached(project string, prNumber int, noAttach bool) error {
	svc, projectName, repoRoot, err := a.sendTarget(project)
	if err != nil {
		return err
	}
	// Pinned, like every other named-project caller: the review flow builds its own
	// jj, gh and tmux clients from this runner, and gh's directory is an argument
	// resolved from it.
	runner := Runner(fixedDirRunner{base: a.runner, dir: repoRoot})

	_, _ = fmt.Fprintf(a.out, "reviewing PR #%d in %s\n", prNumber, projectName)
	err = runReviewWithReporter(runner, svc, prNumber, nil, writerReporter{out: a.out}, reviewOpts{
		// noSwitch as well as paneHosted: paneHosted already stops before the switch,
		// and saying both is how this reads as "do not move me" rather than relying on
		// one flag's implementation to imply the other.
		noSwitch:   true,
		paneHosted: noAttach,
	})
	if err != nil {
		return fmt.Errorf("review PR #%d in %s: %w", prNumber, projectName, err)
	}
	if noAttach {
		_, _ = fmt.Fprintf(a.out, "review workspace ready in %s — its agent is reading the PR\n", projectName)
	}
	return nil
}

// parsePRNumberArg is parsePRNumber with a message that names the flag forms, for
// the paths a machine reaches.
func parsePRNumberArg(arg string) (int, error) {
	n, err := parsePRNumber(arg)
	if err != nil {
		return 0, fmt.Errorf("%w — give a PR number, as `awp review 123` or `awp review 123 --no-attach`", err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("PR number must be positive, got %q", strings.TrimSpace(arg))
	}
	return n, nil
}
