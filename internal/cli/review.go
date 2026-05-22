package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/forge"
	"github.com/andrewcohen/awp/internal/jj"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

type writerReporter struct{ out io.Writer }

func (w writerReporter) Step(label string) {
	if w.out != nil {
		fmt.Fprintf(w.out, "▶️ %s\n", label)
	}
}
func (w writerReporter) Log(line string) {
	if w.out != nil {
		fmt.Fprintln(w.out, line)
	}
}

func runReviewWithCharm(runner Runner, svc workspace.Service, prNumber int, in io.Reader, out io.Writer) error {
	return runReviewWithReporter(runner, svc, prNumber, in, writerReporter{out: out})
}

// runReviewWithReporter prepares (or attaches to) the PR-review tmux
// session and switches the user's client to it.
func runReviewWithReporter(runner Runner, svc workspace.Service, prNumber int, in io.Reader, reporter deckui.Reporter) error {
	return runReviewOpts(runner, svc, prNumber, in, reporter, false)
}

// runReviewAsync runs the same setup as runReviewWithReporter but
// suppresses the final SwitchToWindow + SwitchClient — used by the
// async job path so the user's tmux focus is not yanked away.
func runReviewAsync(runner Runner, svc workspace.Service, prNumber int, reporter deckui.Reporter) error {
	return runReviewOpts(runner, svc, prNumber, nil, reporter, true)
}

func runReviewOpts(runner Runner, svc workspace.Service, prNumber int, in io.Reader, reporter deckui.Reporter, noSwitch bool) error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("awp review must run inside tmux")
	}
	if prNumber <= 0 {
		return fmt.Errorf("invalid PR number: %d", prNumber)
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	// Always run jj/git operations from the default workspace (the source repo
	// root) so a stale secondary workspace can't interfere with the new PR
	// workspace's bookmark resolution.
	defaultRoot, derr := jj.New(runner).SourceRepoRoot()
	if derr != nil || strings.TrimSpace(defaultRoot) == "" {
		return fmt.Errorf("resolve default workspace: %w", derr)
	}
	runner = fixedDirRunner{base: runner, dir: defaultRoot}
	f, err := detectForge(runner, defaultRoot)
	if err != nil {
		return err
	}
	tmuxClient := tmux.New(runner)

	reporter.Step(fmt.Sprintf("Fetch PR #%d from %s", prNumber, f.Name()))
	pr, err := f.FetchPR(prNumber)
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(pr.HeadRef)
	base := strings.TrimSpace(pr.BaseRef)
	if branch == "" || base == "" {
		return fmt.Errorf("PR #%d missing head/base ref", prNumber)
	}
	reporter.Log(fmt.Sprintf("PR #%d: %s (%s ← %s)", pr.Number, pr.Title, base, branch))

	reporter.Step("jj git fetch")
	if fetchOut, err := runner.Run(context.Background(), defaultRoot, "jj", "git", "fetch"); err != nil {
		return fmt.Errorf("jj git fetch: %w: %s", err, fetchOut)
	}

	wsName := fmt.Sprintf("pr-%d-%s", pr.Number, branch)
	reporter.Step(fmt.Sprintf("Prepare jj workspace %s (bookmark %s)", wsName, branch))
	name, wsPath, err := svc.PrepareWorkspace(wsName, branch, true)
	if err != nil {
		return fmt.Errorf("prepare workspace from bookmark %q: %w", branch, err)
	}
	if strings.TrimSpace(wsPath) == "" {
		return fmt.Errorf("workspace %q has empty path", name)
	}

	repoRoot, rerr := repoRootFromPath(wsPath)
	if rerr != nil {
		return rerr
	}
	project := filepath.Base(repoRoot)
	sessionName := DeckSessionName(project, name)

	prompt := buildReviewPrompt(pr, base)
	reviewCmd := fmt.Sprintf("tuicr -r %s..@", shellSingleQuote(base))
	prDescWindow := "pr description"
	prDescTarget := sessionName + ":" + prDescWindow
	prDescCmd := f.PRDescriptionCommand(pr.Number)

	exists, err := tmuxClient.SessionExists(sessionName)
	if err != nil {
		return err
	}
	env := workspaceEnvPairs(project, name, repoRoot)
	if !exists {
		reporter.Step(fmt.Sprintf("Create tmux session %s", sessionName))
		if err := tmuxClient.NewSession(sessionName, wsPath, prDescWindow, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(prDescTarget, prDescCmd); err != nil {
			return err
		}
	} else {
		reporter.Log(fmt.Sprintf("tmux session %s already exists; ensuring review windows", sessionName))
	}

	// Add whichever of the three review windows is missing. Necessary
	// because the session may have been created out-of-band (e.g. an
	// earlier `enter` on the workspace row summons a session with only
	// an `agent` window) — without this idempotent setup, review.go
	// would attach to that bare session and leave the user without the
	// `pr description` and `review` windows.
	windows, werr := tmuxClient.ListWindowsInSession(sessionName)
	if werr != nil {
		return werr
	}
	have := make(map[string]bool, len(windows))
	for _, w := range windows {
		have[w.Name] = true
	}
	if !have[prDescWindow] {
		reporter.Step("Open PR description window")
		if err := tmuxClient.NewWindowInSession(sessionName, prDescWindow, wsPath, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(prDescTarget, prDescCmd); err != nil {
			return err
		}
	}
	if !have["agent"] {
		reporter.Step("Open agent window")
		if err := tmuxClient.NewWindowInSession(sessionName, "agent", wsPath, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(sessionName+":agent", config.AgentInvocation(repoRoot)+" "+shellSingleQuote(prompt)); err != nil {
			return err
		}
	} else {
		// Agent window pre-existed (typically from a prior summon
		// that launched the default agent without a prompt). Feed the
		// review prompt to it as user input so the agent picks up the
		// review context. Use PasteText so the prompt's embedded
		// newlines don't each submit as separate messages — bracketed
		// paste lets the agent receive the whole block in one go.
		reporter.Step("Send review prompt to agent")
		if err := tmuxClient.PasteText(sessionName+":agent", prompt); err != nil {
			return err
		}
	}
	if !have["review"] {
		reporter.Step("Open review window")
		if err := tmuxClient.NewWindowInSession(sessionName, "review", wsPath, env); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(sessionName+":review", reviewCmd); err != nil {
			return err
		}
	}
	// Set the session's current-window pointer to pr-description so a
	// later switch into the session lands the user on the PR view
	// instead of whatever window was last focused (commonly `agent`,
	// when the session was summoned out-of-band before review ran).
	if err := tmuxClient.SwitchToWindow(prDescTarget); err != nil {
		return err
	}

	if noSwitch {
		return nil
	}
	reporter.Step(fmt.Sprintf("Switch to %s", sessionName))
	if err := tmuxClient.SwitchClient(sessionName); err != nil {
		return err
	}
	return nil
}

// repoRootFromPath walks up from a workspace path to find the jj repo root (contains .jj).
// For secondary jj workspaces, .jj/repo is a file whose contents point to the main repo's
// .jj/repo directory; follow that pointer so the result is the source repo root, not the
// workspace dir (otherwise filepath.Base would return the workspace/branch name).
func repoRootFromPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		jjDir := filepath.Join(dir, ".jj")
		if st, err := os.Stat(jjDir); err == nil && st.IsDir() {
			repoEntry := filepath.Join(jjDir, "repo")
			rst, rerr := os.Stat(repoEntry)
			if rerr == nil && rst.IsDir() {
				return dir, nil
			}
			if rerr == nil && !rst.IsDir() {
				data, ferr := os.ReadFile(repoEntry)
				if ferr != nil {
					return "", fmt.Errorf("read %s: %w", repoEntry, ferr)
				}
				target := strings.TrimSpace(string(data))
				if !filepath.IsAbs(target) {
					target = filepath.Join(jjDir, target)
				}
				// target is .../<mainRepo>/.jj/repo — main repo root is two levels up.
				mainRepo := filepath.Clean(filepath.Join(target, "..", ".."))
				return mainRepo, nil
			}
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate jj repo root above %s", abs)
		}
		dir = parent
	}
}

// pickPRNumber lists open PRs/MRs via the detected forge and prompts the user to pick one.
func pickPRNumber(runner Runner, picker workspacePicker) (int, error) {
	if runner == nil {
		runner = NewExecRunner()
	}
	root, _ := jj.New(runner).SourceRepoRoot()
	f, err := detectForge(runner, strings.TrimSpace(root))
	if err != nil {
		return 0, err
	}
	prs, err := f.ListPRs()
	if err != nil {
		return 0, err
	}
	if len(prs) == 0 {
		return 0, fmt.Errorf("no open PRs found")
	}
	options := make([]string, 0, len(prs))
	byLabel := make(map[string]int, len(prs))
	for _, pr := range prs {
		draft := ""
		if pr.IsDraft {
			draft = " [draft]"
		}
		author := pr.Author.Login
		if author == "" {
			author = "?"
		}
		label := fmt.Sprintf("#%d%s %s (@%s, %s)", pr.Number, draft, pr.Title, author, pr.HeadRef)
		options = append(options, label)
		byLabel[label] = pr.Number
	}
	selected, err := picker("Select PR to review", options)
	if err != nil {
		return 0, err
	}
	n, ok := byLabel[strings.TrimSpace(selected)]
	if !ok {
		return 0, fmt.Errorf("picker returned unknown label %q", selected)
	}
	return n, nil
}

func buildReviewPrompt(pr forge.PRInfo, base string) string {
	body := strings.TrimSpace(pr.Body)
	if body == "" {
		body = "(no description)"
	}
	return fmt.Sprintf(
		"Please review PR #%d: %s\n\n%s\n\nDiff range: %s..@",
		pr.Number, pr.Title, body, base,
	)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func parsePRNumber(arg string) (int, error) {
	arg = strings.TrimSpace(arg)
	arg = strings.TrimPrefix(arg, "#")
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("invalid PR number %q", arg)
	}
	return n, nil
}
