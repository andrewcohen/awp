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
)

//go:embed assets/awp-status.ts
var piAwpStatusExtension []byte

// HookMarkerVersion bumps when the hook block schema changes; the installer
// rewrites entries whose version differs.
const HookMarkerVersion = 2

// HookCommand returns the shell snippet each Claude hook runs. It gates on
// $TMUX so global installation never affects non-tmux Claude usage. The awp
// CLI itself falls back to reading session env from tmux when its own env is
// missing, so this works for processes that predate env injection.
func HookCommand(state string) string {
	return `[ -n "$TMUX" ] && awp internal report-status --state ` + state + ` >/dev/null 2>&1 || true`
}

// DesiredClaudeHooks returns the event → state mapping awp installs into
// Claude's global settings.
func DesiredClaudeHooks() map[string]string {
	return map[string]string{
		"UserPromptSubmit": "working",
		"PreToolUse":       "working",
		"Stop":             "idle",
		"Notification":     "waiting",
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
		entries, evtChanged := upsertAwpEntry(entries, state)
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
		if !awpEntryMatches(entries, state) {
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

func upsertAwpEntry(entries []any, state string) ([]any, bool) {
	desired := desiredEntry(state)
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

func awpEntryMatches(entries []any, state string) bool {
	desired := desiredEntry(state)
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

func desiredEntry(state string) map[string]any {
	return map[string]any{
		"x-awp": map[string]any{
			"version": float64(HookMarkerVersion),
			"state":   state,
		},
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": HookCommand(state),
			},
		},
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
