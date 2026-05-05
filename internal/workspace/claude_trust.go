package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// markClaudeWorkspaceTrusted patches ~/.claude.json so Claude Code treats the
// given workspace directory as already-trusted, skipping the per-directory
// trust dialog on first launch. Claude Code itself writes this file, so the
// read-modify-write is guarded by an advisory lock and we preserve unknown
// fields by round-tripping through map[string]any.
func markClaudeWorkspaceTrusted(workspacePath string) error {
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("resolve user home dir: %w", err)
	}
	path := filepath.Join(home, ".claude.json")

	// Only patch if Claude Code is in use here. Absence of ~/.claude.json
	// means the user's agent is something else (codex, cursor, aider, …);
	// don't litter $HOME on their behalf.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat claude.json: %w", err)
	}

	lockPath := path + ".awp.lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open claude.json lock: %w", err)
	}
	defer lf.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("flock claude.json: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("flock claude.json: timed out")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	root := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read claude.json: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse claude.json: %w", err)
		}
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}
	entry, _ := projects[abs].(map[string]any)
	if entry == nil {
		entry = map[string]any{
			"allowedTools":             []any{},
			"mcpContextUris":           []any{},
			"mcpServers":               map[string]any{},
			"enabledMcpjsonServers":    []any{},
			"disabledMcpjsonServers":   []any{},
			"projectOnboardingSeenCount": 0,
		}
		projects[abs] = entry
	}
	if entry["hasTrustDialogAccepted"] == true && entry["hasCompletedProjectOnboarding"] == true {
		return nil
	}
	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode claude.json: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".claude.json.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp claude.json: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write claude.json: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync claude.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close claude.json: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod claude.json: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename claude.json: %w", err)
	}
	return nil
}
