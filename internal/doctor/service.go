package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/agenthooks"
	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/workspace"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type HookProvider interface {
	PostWorkspaceStart(repoRoot string) ([]string, error)
}

type Service struct {
	runner   Runner
	hooks    HookProvider
	homeDir  string
	out      io.Writer
}

type Dependencies struct {
	Runner  Runner
	Hooks   HookProvider
	HomeDir string
	Out     io.Writer
}

func New(deps Dependencies) *Service {
	hooks := deps.Hooks
	if hooks == nil {
		hooks = config.NewFileHookProvider()
	}
	out := deps.Out
	if out == nil {
		out = os.Stdout
	}
	homeDir := strings.TrimSpace(deps.HomeDir)
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	return &Service{runner: deps.Runner, hooks: hooks, homeDir: homeDir, out: out}
}

// Options tunes a doctor run.
type Options struct {
	// Global skips checks that require being inside a jj repo.
	Global bool
	// Fix attempts to repair issues automatically (reinstall hooks, inject
	// missing tmux env vars).
	Fix bool
}

// Run is the legacy entry point that runs all checks against the cwd repo.
func (s *Service) Run() error {
	return s.RunWithOptions(Options{})
}

// RunGlobal runs cross-repo checks only. Use when invoking doctor outside
// any specific repo.
func (s *Service) RunGlobal(fix bool) error {
	return s.RunWithOptions(Options{Global: true, Fix: fix})
}

// RunRepo runs the full set of checks (global + cwd-repo) with optional fix.
func (s *Service) RunRepo(fix bool) error {
	return s.RunWithOptions(Options{Fix: fix})
}

// RunWithOptions executes the doctor checks selected by opts.
func (s *Service) RunWithOptions(opts Options) error {
	issues := 0

	if _, err := s.runner.Run(context.Background(), "", "jj", "--version"); err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ jj available: %v\n", err)
	} else {
		fmt.Fprintln(s.out, "✅ jj available")
	}
	if _, err := s.runner.Run(context.Background(), "", "tmux", "-V"); err != nil {
		fmt.Fprintf(s.out, "⚠️  tmux available: %v\n", err)
	} else {
		fmt.Fprintln(s.out, "✅ tmux available")
	}

	issues += s.checkAgentHooks(opts.Fix)

	if opts.Global {
		issues += s.checkAwpSessionsEnv(opts.Fix, "")
		if issues > 0 {
			return fmt.Errorf("doctor found %d issue(s)", issues)
		}
		fmt.Fprintln(s.out, "✅ doctor checks passed")
		return nil
	}

	repoRootOut, err := s.runner.Run(context.Background(), "", "jj", "root")
	if err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ jj repo root: %v (use `awp doctor --global` to skip repo checks)\n", err)
		return fmt.Errorf("doctor found %d issue(s)", issues)
	}
	repoRoot := strings.TrimSpace(repoRootOut)
	fmt.Fprintf(s.out, "✅ jj repo root: %s\n", repoRoot)

	// Session-env check is scoped to this repo's project so it doesn't flag
	// other projects' sessions when running without --global.
	projectFilter := filepath.Base(filepath.Clean(repoRoot))
	if normalized, err := workspace.NormalizeName(projectFilter); err == nil {
		projectFilter = normalized
	}
	issues += s.checkAwpSessionsEnv(opts.Fix, projectFilter)

	if err := s.validateHookConfigShape(repoRoot); err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ .awp/config.json shape: %v\n", err)
	}

	hookCommands, err := s.hooks.PostWorkspaceStart(repoRoot)
	if err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ .awp/config.json hooks: %v\n", err)
	} else if len(hookCommands) == 0 {
		fmt.Fprintln(s.out, "⚠️  .awp/config.json hooks: none configured")
	} else {
		fmt.Fprintf(s.out, "✅ .awp/config.json hooks: %d configured\n", len(hookCommands))
	}

	repoName := filepath.Base(filepath.Clean(repoRoot))
	if normalized, err := workspace.NormalizeName(repoName); err == nil {
		repoName = normalized
	}
	managedBase := filepath.Join(s.homeDir, ".awp", "workspaces", repoName)
	if err := os.MkdirAll(managedBase, 0o755); err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ managed workspace dir writable: %v\n", err)
	} else {
		fmt.Fprintf(s.out, "✅ managed workspace dir: %s\n", managedBase)
	}

	workspaceList, err := s.runner.Run(context.Background(), "", "jj", "workspace", "list", "-T", "name ++ \"\\n\"")
	if err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ jj workspace list: %v\n", err)
	} else {
		bad := 0
		for _, name := range parseNames(workspaceList) {
			_, wsErr := s.runner.Run(context.Background(), "", "jj", "log", "-r", name+"@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\"")
			if wsErr != nil {
				bad++
				issues++
				fmt.Fprintf(s.out, "❌ workspace %q has invalid working copy (try: jj workspace forget %s)\n", name, name)
			}
		}
		if bad == 0 {
			fmt.Fprintln(s.out, "✅ jj workspace working-copy checks")
		}
	}

	if issues > 0 {
		return fmt.Errorf("doctor found %d issue(s)", issues)
	}
	fmt.Fprintln(s.out, "✅ doctor checks passed")
	return nil
}

// checkAgentHooks verifies the global Claude Code hooks and the pi.dev
// extension are installed. When fix=true, missing pieces are installed
// (idempotent) and remediation is reported.
func (s *Service) checkAgentHooks(fix bool) int {
	issues := 0

	claudeOK, err := agenthooks.IsClaudeInstalled()
	switch {
	case err != nil:
		issues++
		fmt.Fprintf(s.out, "❌ Claude Code hooks: %v\n", err)
	case claudeOK:
		fmt.Fprintln(s.out, "✅ Claude Code hooks installed (~/.claude/settings.json)")
	default:
		if fix {
			if _, err := agenthooks.InstallClaude(); err != nil {
				issues++
				fmt.Fprintf(s.out, "❌ Claude Code hooks: install failed: %v\n", err)
			} else {
				fmt.Fprintln(s.out, "🔧 Claude Code hooks: installed")
			}
		} else {
			issues++
			fmt.Fprintln(s.out, "❌ Claude Code hooks: missing or stale (run `awp init hooks` or `awp doctor --fix`)")
		}
	}

	piOK, err := agenthooks.IsPiInstalled()
	switch {
	case err != nil:
		issues++
		fmt.Fprintf(s.out, "❌ pi.dev extension: %v\n", err)
	case piOK:
		fmt.Fprintln(s.out, "✅ pi.dev extension installed (~/.pi/agent/extensions/awp-status.ts)")
	default:
		if fix {
			if _, err := agenthooks.InstallPi(); err != nil {
				issues++
				fmt.Fprintf(s.out, "❌ pi.dev extension: install failed: %v\n", err)
			} else {
				fmt.Fprintln(s.out, "🔧 pi.dev extension: installed")
			}
		} else {
			issues++
			fmt.Fprintln(s.out, "❌ pi.dev extension: missing or stale (run `awp init hooks` or `awp doctor --fix`)")
		}
	}
	return issues
}

// checkAwpSessionsEnv inspects every live tmux session whose name starts with
// "[awp]" and verifies the session-level AWP_WORKSPACE / AWP_REPO env vars
// are set so hooks running in those panes can attribute status. With fix=true,
// missing vars are injected via `tmux set-environment`.
//
// Note: tmux set-environment only affects processes spawned *after* the call.
// We surface a warning when an agent process is already running and the env
// was missing — the user must restart the agent to pick up the new env.
func (s *Service) checkAwpSessionsEnv(fix bool, projectFilter string) int {
	out, err := s.runner.Run(context.Background(), "", "tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		// No tmux server / no sessions — not an issue here.
		return 0
	}
	awpSessions := []string{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if !strings.HasPrefix(name, "[awp]") {
			continue
		}
		if projectFilter != "" {
			repo, _, ok := parseAwpSession(name)
			if !ok || repo != projectFilter {
				continue
			}
		}
		awpSessions = append(awpSessions, name)
	}
	if len(awpSessions) == 0 {
		if projectFilter != "" {
			fmt.Fprintf(s.out, "⚠️  no live awp tmux sessions for project %q\n", projectFilter)
		} else {
			fmt.Fprintln(s.out, "⚠️  no live awp tmux sessions to check")
		}
		return 0
	}

	issues := 0
	for _, name := range awpSessions {
		repo, ws, ok := parseAwpSession(name)
		if !ok {
			continue
		}
		ws = strings.TrimSpace(ws)
		repo = strings.TrimSpace(repo)
		envWS := s.tmuxShowEnv(name, "AWP_WORKSPACE")
		envRepo := s.tmuxShowEnv(name, "AWP_REPO")
		envRoot := s.tmuxShowEnv(name, "AWP_REPO_ROOT")

		desiredOK := envWS == ws && envRepo == repo
		if desiredOK && envRoot != "" {
			fmt.Fprintf(s.out, "✅ %s: AWP_WORKSPACE=%s AWP_REPO=%s\n", name, envWS, envRepo)
			continue
		}
		if desiredOK && envRoot == "" {
			fmt.Fprintf(s.out, "⚠️  %s: AWP_REPO_ROOT not set (hooks fall back to repo basename)\n", name)
			if fix {
				// We don't know the repo root from the session name; leave for the
				// next deck summon to fill in.
				fmt.Fprintf(s.out, "   (re-summon the workspace from the deck to populate AWP_REPO_ROOT)\n")
			}
			continue
		}

		if !fix {
			issues++
			fmt.Fprintf(s.out, "❌ %s: missing AWP_WORKSPACE/AWP_REPO (run `awp doctor --fix` to inject)\n", name)
			continue
		}

		if err := s.tmuxSetEnv(name, "AWP_WORKSPACE", ws); err != nil {
			issues++
			fmt.Fprintf(s.out, "❌ %s: set AWP_WORKSPACE: %v\n", name, err)
			continue
		}
		if err := s.tmuxSetEnv(name, "AWP_REPO", repo); err != nil {
			issues++
			fmt.Fprintf(s.out, "❌ %s: set AWP_REPO: %v\n", name, err)
			continue
		}
		fmt.Fprintf(s.out, "🔧 %s: injected AWP_WORKSPACE=%s AWP_REPO=%s\n", name, ws, repo)

		// If an agent is already running in the agent pane, warn that it
		// won't pick up the new env until restarted.
		if cmd := s.tmuxPaneCmd(name + ":agent"); cmd != "" && !isShellName(cmd) {
			fmt.Fprintf(s.out, "   ⚠️  agent already running (%s) — restart it to pick up env\n", cmd)
		}
	}
	return issues
}

func (s *Service) tmuxShowEnv(session, key string) string {
	out, err := s.runner.Run(context.Background(), "", "tmux", "show-environment", "-t", session, key)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(out)
	if line == "" || strings.HasPrefix(line, "-") {
		return ""
	}
	if idx := strings.IndexByte(line, '='); idx >= 0 {
		return line[idx+1:]
	}
	return ""
}

func (s *Service) tmuxSetEnv(session, key, value string) error {
	_, err := s.runner.Run(context.Background(), "", "tmux", "set-environment", "-t", session, key, value)
	return err
}

func (s *Service) tmuxPaneCmd(target string) string {
	out, err := s.runner.Run(context.Background(), "", "tmux", "display-message", "-p", "-t", target, "#{pane_current_command}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func parseAwpSession(name string) (string, string, bool) {
	const prefix = "[awp]"
	if !strings.HasPrefix(name, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, prefix)
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+2:], true
}

func isShellName(name string) bool {
	switch name {
	case "bash", "zsh", "fish", "sh", "dash":
		return true
	default:
		return false
	}
}

func (s *Service) validateHookConfigShape(repoRoot string) error {
	configPath := filepath.Join(repoRoot, ".awp", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	var cfg struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	if len(cfg.Hooks) == 0 {
		return nil
	}
	if _, ok := cfg.Hooks["bootstrap"]; ok {
		return nil
	}
	keys := make([]string, 0, len(cfg.Hooks))
	for key := range cfg.Hooks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Errorf("unsupported hooks key(s): %s (expected hooks.bootstrap)", strings.Join(keys, ", "))
}

func parseNames(out string) []string {
	lines := strings.Split(out, "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		names = append(names, line)
	}
	return names
}
