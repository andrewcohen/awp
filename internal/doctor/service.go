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

func (s *Service) Run() error {
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

	repoRootOut, err := s.runner.Run(context.Background(), "", "jj", "root")
	if err != nil {
		issues++
		fmt.Fprintf(s.out, "❌ jj repo root: %v\n", err)
		return fmt.Errorf("doctor found %d issue(s)", issues)
	}
	repoRoot := strings.TrimSpace(repoRootOut)
	fmt.Fprintf(s.out, "✅ jj repo root: %s\n", repoRoot)

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
