// Package agenthooks installs and inspects the global agent integrations
// (Claude Code hooks, pi.dev extension) that report agent status to awp.
package agenthooks

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/awp-status.ts
var piAwpStatusExtension []byte

// HookMarkerVersion bumps when the hook block schema changes; the installer
// rewrites entries whose version differs.
const HookMarkerVersion = 6

// BlockingTools lists tool names that block on user input. When a
// PreToolUse hook fires for one of these, awp reports "waiting" instead of
// "working" so the deck row reflects that the agent is paused on the user
// — not actively producing output.
var BlockingTools = []string{"AskUserQuestion"}

// HookCommand returns the shell snippet each Claude hook runs. It gates on
// $TMUX so global installation never affects non-tmux Claude usage, and
// honors $AWP_BIN for users running a non-PATH awp binary. The awp CLI
// itself falls back to reading session env from tmux when its own env is
// missing, so this works for processes that predate env injection.
//
// For UserPromptSubmit we add --prompt-stdin so report-status pulls the
// user's prompt text from the hook's JSON payload and stores it as the
// workspace's ActivePrompt. Other events leave ActivePrompt alone (empty
// prompt means "no update"), so the last user message keeps showing on the
// deck across Stop/idle transitions.
//
// For PreToolUse we add --waiting-when-tool so report-status flips the
// state to "waiting" when the tool being invoked blocks on user input
// (e.g. AskUserQuestion). Without this the row would stay "working" while
// the agent is actually paused on a question to the user.
func HookCommand(event, state string) string {
	extra := ""
	switch event {
	case "UserPromptSubmit":
		extra = " --prompt-stdin"
	case "PreToolUse":
		if len(BlockingTools) > 0 {
			extra = " --waiting-when-tool " + strings.Join(BlockingTools, ",")
		}
	}
	return `[ -n "$TMUX" ] && "${AWP_BIN:-awp}" internal report-status --state ` + state + extra + ` >/dev/null 2>&1 || true`
}

// DesiredClaudeHooks returns the event → state mapping awp installs into
// Claude's global settings. SessionStart marks the workspace idle as soon
// as Claude attaches, so summoned-but-not-yet-prompted agents stop showing
// the previous run's state.
//
// PostToolUse → working flips the row back to "working" once a blocking
// tool (e.g. AskUserQuestion) returns, so the deck doesn't linger in
// "waiting" while the agent is generating its follow-up response.
//
// The three "waiting" events cover every way Claude blocks on the user:
//   - PreToolUse with AskUserQuestion (via --waiting-when-tool below) —
//     Claude's own multiple-choice question.
//   - PermissionRequest — a permission dialog is up (e.g. approve a Bash
//     command or file write in default permission mode). This is the
//     dedicated event; it fires regardless of whether the user has
//     desktop Notifications configured, so it's more reliable than
//     leaning on the Notification event alone.
//   - Elicitation — an MCP server is requesting form input.
//   - Notification — the catch-all (permission_prompt / idle_prompt /
//     elicitation_dialog). Kept as a backstop for older clients and the
//     60s-idle ping.
//
// Each of these resolves back to "working" on the next PreToolUse /
// PostToolUse, or to "idle" on Stop, so the row never sticks in waiting.
//
// Unknown events are ignored by older Claude Code versions, so listing
// PermissionRequest / Elicitation here is safe even if the installed
// client predates them.
func DesiredClaudeHooks() map[string]string {
	return map[string]string{
		"SessionStart":      "idle",
		"UserPromptSubmit":  "working",
		"PreToolUse":        "working",
		"PostToolUse":       "working",
		"Stop":              "idle",
		"Notification":      "waiting",
		"PermissionRequest": "waiting",
		"Elicitation":       "waiting",
	}
}

// InstallClaude merges (or refreshes) awp-managed hook entries into
// ~/.claude/settings.json. Returns true if the file was written.
func InstallClaude() (bool, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}

	var settings map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, err
	}
	if settings == nil {
		settings = map[string]any{}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	changed := false
	for event, state := range DesiredClaudeHooks() {
		entries, _ := hooks[event].([]any)
		entries, evtChanged := upsertAwpEntry(entries, event, state)
		if evtChanged {
			changed = true
		}
		hooks[event] = entries
	}
	if !changed {
		return false, nil
	}
	settings["hooks"] = hooks

	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// IsClaudeInstalled reports whether the awp hook entries are present in
// ~/.claude/settings.json for every event in DesiredClaudeHooks. Missing
// file or partial install returns false.
func IsClaudeInstalled() (bool, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, nil
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return false, nil
	}
	for event, state := range DesiredClaudeHooks() {
		entries, _ := hooks[event].([]any)
		if !awpEntryMatches(entries, event, state) {
			return false, nil
		}
	}
	return true, nil
}

// PiExtensionPath returns the on-disk path where the pi.dev extension lives.
func PiExtensionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent", "extensions", "awp-status.ts"), nil
}

// InstallPi writes the pi.dev extension to its global location. Returns true
// if the file was created or replaced.
func InstallPi() (bool, error) {
	path, err := PiExtensionPath()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create pi extensions dir: %w", err)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if string(existing) == string(piAwpStatusExtension) {
		return false, nil
	}
	if err := os.WriteFile(path, piAwpStatusExtension, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// IsPiInstalled reports whether the pi.dev extension at the canonical path
// matches the embedded content.
func IsPiInstalled() (bool, error) {
	path, err := PiExtensionPath()
	if err != nil {
		return false, err
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return string(existing) == string(piAwpStatusExtension), nil
}

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func upsertAwpEntry(entries []any, event, state string) ([]any, bool) {
	desired := desiredEntry(event, state)
	for i, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := entry["x-awp"]; !ok {
			continue
		}
		if jsonEqual(entry, desired) {
			return entries, false
		}
		entries[i] = desired
		return entries, true
	}
	return append(entries, desired), true
}

func awpEntryMatches(entries []any, event, state string) bool {
	desired := desiredEntry(event, state)
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := entry["x-awp"]; !ok {
			continue
		}
		if jsonEqual(entry, desired) {
			return true
		}
	}
	return false
}

func desiredEntry(event, state string) map[string]any {
	return map[string]any{
		"x-awp": map[string]any{
			"version": float64(HookMarkerVersion),
			"state":   state,
		},
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": HookCommand(event, state),
			},
		},
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
