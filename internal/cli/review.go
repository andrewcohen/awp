package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/tmux"
	"github.com/andrewcohen/awp/internal/workspace"
)

func runReviewWithCharm(runner Runner, svc workspace.Service, prNumber int, in io.Reader, out io.Writer) error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("awp review must run inside tmux")
	}
	if prNumber <= 0 {
		return fmt.Errorf("invalid PR number: %d", prNumber)
	}
	if runner == nil {
		runner = NewExecRunner()
	}
	gh := github.New(runner)
	tmuxClient := tmux.New(runner)

	fmt.Fprintf(out, "▶️ Fetching PR #%d from GitHub...\n", prNumber)
	pr, err := gh.FetchPR(prNumber)
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(pr.HeadRef)
	base := strings.TrimSpace(pr.BaseRef)
	if branch == "" || base == "" {
		return fmt.Errorf("PR #%d missing head/base ref", prNumber)
	}
	fmt.Fprintf(out, "✅ PR #%d: %s (%s ← %s)\n", pr.Number, pr.Title, base, branch)

	fmt.Fprintln(out, "▶️ Fetching refs (jj git fetch)...")
	if fetchOut, err := runner.Run(context.Background(), "", "jj", "git", "fetch"); err != nil {
		return fmt.Errorf("jj git fetch: %w: %s", err, fetchOut)
	}

	fmt.Fprintf(out, "▶️ Preparing jj workspace for %q...\n", branch)
	name, wsPath, err := svc.PrepareWorkspace(branch, branch, true)
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
	tuicrCmd := fmt.Sprintf("tuicr -r %s..@", shellSingleQuote(base))

	exists, err := tmuxClient.SessionExists(sessionName)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(out, "▶️ Creating tmux session %q...\n", sessionName)
		if err := tmuxClient.NewSession(sessionName, wsPath, "agent"); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(sessionName+":agent", "pi "+shellSingleQuote(prompt)); err != nil {
			return err
		}
		if err := tmuxClient.NewWindowInSession(sessionName, "tuicr", wsPath); err != nil {
			return err
		}
		if err := tmuxClient.SendCommand(sessionName+":tuicr", tuicrCmd); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "ℹ️ tmux session %q already exists; attaching.\n", sessionName)
	}

	if err := tmuxClient.SwitchClient(sessionName); err != nil {
		return err
	}
	fmt.Fprintf(out, "✅ Review workspace ready for PR #%d\n", pr.Number)
	return nil
}

// repoRootFromPath walks up from a workspace path to find the jj repo root (contains .jj).
func repoRootFromPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		if st, err := os.Stat(filepath.Join(dir, ".jj")); err == nil && st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate jj repo root above %s", abs)
		}
		dir = parent
	}
}

// pickPRNumber lists open PRs via gh and prompts the user to pick one.
func pickPRNumber(runner Runner, picker workspacePicker) (int, error) {
	if runner == nil {
		runner = NewExecRunner()
	}
	gh := github.New(runner)
	prs, err := gh.ListPRs()
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

func buildReviewPrompt(pr github.PRInfo, base string) string {
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
