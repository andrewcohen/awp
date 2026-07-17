package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// DevLoop defines the per-unit-of-work development loop that `awp
	// watch` visualizes: the ordered phases a unit passes through and the
	// gates (named checks awp recognizes in the agent's transcript) that
	// gate each phase. Unset = an inferred default loop (see
	// watch.DefaultLoop).
	DevLoop struct {
		Phases []string      `json:"phases,omitempty"`
		Gates  []DevLoopGate `json:"gates,omitempty"`
		// Nudge controls how chatty the `awp gate record` PostToolUse hook is
		// when it feeds an in-context reminder back to the agent (rung 2 of the
		// enforcement ladder). One of "off", "transitions" (default), or
		// "verbose": off never speaks; transitions speaks only when a gate
		// flips red or the unit's gates all go green; verbose also acknowledges
		// each intermediate pass. Empty = "transitions".
		Nudge string `json:"nudge,omitempty"`
	} `json:"dev_loop,omitempty"`
}

// DevLoopGate is one gate in the dev loop: a named check whose shell command
// awp recognizes in the agent's transcript, tied to the phase it belongs to.
// Match is a regular expression tested against the bash command the agent
// ran; a paired tool_result exit code decides pass/fail.
type DevLoopGate struct {
	Name  string `json:"name"`
	Phase string `json:"phase,omitempty"`
	Match string `json:"match"`
	// Command is the human-facing command shown in the generated preamble
	// (awp watch --preamble). It's distinct from Match (a detection regex):
	// use it to express the intended invocation, e.g. "pnpm lint <files you
	// changed>". Falls back to the first alternative of Match when unset.
	Command string `json:"command,omitempty"`
	// NotMatch, when set, excludes commands that also match this regex even
	// if they match Match — e.g. a commit marker that ignores "wip:" commits.
	NotMatch string `json:"not_match,omitempty"`
	// Marker entries detect a phase transition (e.g. reaching "commit") but
	// are not pass/fail checks — they advance the loop's phase without
	// appearing in the gate-lights row.
	Marker bool `json:"marker,omitempty"`
	// Optional marks an advisory gate: it still records pass/fail and shows in
	// the gate-lights row, but a red (or not-yet-run) optional gate does NOT
	// block marking a unit complete. Instead the completion check allows the
	// TaskUpdate and feeds a reminder about the still-red optional gate back to
	// the agent. Use it for checks you want tracked but not enforced (e.g. a
	// slow integration suite). Ignored on marker gates (they never block).
	Optional bool `json:"optional,omitempty"`
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

func awpHome() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".awp")
}

// ReviewPromptDir is where rendered PR-review prompts are written:
// ~/.awp/review-prompts. The files deliberately live here rather than
// inside the review workspace's own .awp/ — that directory is replaced
// with a symlink to the shared source-repo .awp during workspace prep, so
// a per-PR prompt written there would be shared across every review and
// clobbered by the next one. Prompts are filed under a per-repo
// subdirectory (see ReviewPromptPath) so workspace names that collide
// across repos (e.g. pr-1-main) don't clobber each other, and so workspace
// delete/prune can remove exactly the matching file.
func ReviewPromptDir() string {
	return filepath.Join(awpHome(), "review-prompts")
}

var reviewPromptUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// reviewPromptComponent sanitizes a path component to the same [a-z0-9-]
// charset workspace names use, so the value is filesystem-safe and the
// write side (review.go) and delete side (workspace.Delete) always agree.
func reviewPromptComponent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reviewPromptUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		s = "repo"
	}
	return s
}

// ReviewPromptPath returns the prompt file path for a review workspace:
// ~/.awp/review-prompts/<repo>/<workspace>.md, where <repo> is derived from
// repoRoot's base name. Both the write and delete sides call this with the
// same (repoRoot, workspace) pair so they resolve to the same file. Returns
// "" for an empty workspace name so callers can skip cleanup safely.
func ReviewPromptPath(repoRoot, workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	repo := reviewPromptComponent(filepath.Base(filepath.Clean(repoRoot)))
	return filepath.Join(ReviewPromptDir(), repo, reviewPromptComponent(workspace)+".md")
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
	if len(out.DevLoop.Phases) == 0 {
		out.DevLoop.Phases = global.DevLoop.Phases
	}
	if len(out.DevLoop.Gates) == 0 {
		out.DevLoop.Gates = global.DevLoop.Gates
	}
	if strings.TrimSpace(out.DevLoop.Nudge) == "" {
		out.DevLoop.Nudge = global.DevLoop.Nudge
	}
	return out
}
