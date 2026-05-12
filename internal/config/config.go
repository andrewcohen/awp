package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type UserAction struct {
	Command string `json:"command"`
	Alias   string `json:"alias"`
	// Background runs the action detached via the jobs subsystem instead of
	// opening a tmux window. Output is captured to the job log; the deck
	// surfaces pass/fail in the right panel's Recent activity list.
	Background bool `json:"background,omitempty"`
	// Focus controls whether the deck switches the tmux client to the new
	// window after spawning a foreground action. nil/unset = true (default
	// behavior). Set to false to spawn the window in the workspace's
	// session but keep the deck focused. Ignored when Background is true.
	Focus *bool `json:"focus,omitempty"`
}

type Config struct {
	Hooks struct {
		Bootstrap []string `json:"bootstrap"`
	} `json:"hooks"`
	Actions map[string]UserAction `json:"actions"`
	// Agent is the command name used to launch the workspace agent. It is
	// invoked as `<agent> [agent_options] <prompt>` (or just
	// `<agent> [agent_options]` when no prompt is passed). Defaults to "pi"
	// when unset. Project config overrides global.
	Agent string `json:"agent,omitempty"`
	// AgentOptions are extra flags passed to the agent before the prompt,
	// e.g. "--model claude-opus-4-7" or "--resume". Inserted verbatim into
	// the shell command, so the user owns quoting.
	AgentOptions string `json:"agent_options,omitempty"`
	Deck         struct {
		// ProjectRoots are directories under which the deck's project
		// picker (`o`) searches for git/jj repos. Tilde-expanded.
		// Example: ["~/p", "~/go/src"].
		ProjectRoots []string `json:"project_roots,omitempty"`
		// BookmarkPrefix, when set, causes new workspaces created via
		// the deck or CLI new flow with no explicit bookmark to auto-
		// create a jj bookmark named "<prefix>/<workspace-name>" on the
		// new workspace's revision. The bookmark is persisted to the
		// workspace entry so the deck's PR glyph can match it against
		// a PR's headRefName. Unset = no auto-create (default).
		BookmarkPrefix string `json:"bookmark_prefix,omitempty"`
	} `json:"deck,omitempty"`
}

// DefaultAgent is the agent command used when neither global nor project
// config sets one.
const DefaultAgent = "pi"

// AgentInvocation returns the configured agent command joined with its
// agent_options (project overrides global, command falling back to
// DefaultAgent). Suitable for prepending to a prompt: `<invocation>
// '<prompt>'`, or for sending to a fresh agent window on its own.
func AgentInvocation(repoRoot string) string {
	cfg, _ := Load(repoRoot)
	cmd := strings.TrimSpace(cfg.Agent)
	if cmd == "" {
		cmd = DefaultAgent
	}
	if opts := strings.TrimSpace(cfg.AgentOptions); opts != "" {
		return cmd + " " + opts
	}
	return cmd
}

func Load(repoRoot string) (Config, error) {
	global, globalErr := loadFile(globalConfigPath())
	project, projectErr := loadFile(ProjectConfigPath(repoRoot))

	if globalErr != nil && !errors.Is(globalErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("global config: %w", globalErr)
	}
	if projectErr != nil && !errors.Is(projectErr, os.ErrNotExist) {
		return Config{}, fmt.Errorf("project config: %w", projectErr)
	}

	return merge(global, project), nil
}

// GlobalConfigPath returns the canonical global config location:
// $XDG_CONFIG_HOME/awp/config.json (defaulting to ~/.config/awp/config.json).
func GlobalConfigPath() string {
	return globalConfigPath()
}

// ProjectConfigPath returns the per-repo config path: <repoRoot>/.awp/config.json.
func ProjectConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".awp", "config.json")
}

func globalConfigPath() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "awp", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".config", "awp", "config.json")
}

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %q: %w", path, err)
	}
	return cfg, nil
}

func merge(global, project Config) Config {
	out := project
	if out.Actions == nil {
		out.Actions = make(map[string]UserAction)
	}
	for name, action := range global.Actions {
		if _, exists := out.Actions[name]; !exists {
			out.Actions[name] = action
		}
	}
	if len(out.Hooks.Bootstrap) == 0 {
		out.Hooks.Bootstrap = global.Hooks.Bootstrap
	}
	if strings.TrimSpace(out.Agent) == "" {
		out.Agent = global.Agent
	}
	if strings.TrimSpace(out.AgentOptions) == "" {
		out.AgentOptions = global.AgentOptions
	}
	if len(out.Deck.ProjectRoots) == 0 {
		out.Deck.ProjectRoots = global.Deck.ProjectRoots
	}
	if strings.TrimSpace(out.Deck.BookmarkPrefix) == "" {
		out.Deck.BookmarkPrefix = global.Deck.BookmarkPrefix
	}
	return out
}
