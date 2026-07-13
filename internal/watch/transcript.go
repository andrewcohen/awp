package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StableWindow is how long the current transcript must be idle before the
// live watcher switches to a newer session file. Long enough to ride out
// modtime jitter between concurrent session files in the same project dir
// (which would otherwise flicker/blank the view), short enough to follow a
// genuine session handoff.
const StableWindow = 5 * time.Second

// LocateSticky chooses which transcript the live watcher should read. It
// prefers to stay on `current` to avoid flickering between concurrent session
// files: it switches to a newer file only once `current` has gone idle for
// StableWindow (a real handoff), not on momentary modtime jitter. On a
// lookup error it keeps `current` rather than dropping the view.
func LocateSticky(workspacePath, current string, now time.Time) (string, error) {
	newest, err := Locate(workspacePath)
	if err != nil {
		return current, err
	}
	var currentMod time.Time
	if info, statErr := os.Stat(current); statErr == nil {
		currentMod = info.ModTime()
	}
	return stickyChoice(current, newest, currentMod, now), nil
}

// stickyChoice is the pure switch/stay decision, split out for testing.
func stickyChoice(current, newest string, currentMod, now time.Time) string {
	if current == "" || newest == current {
		return newest
	}
	if !currentMod.IsZero() && now.Sub(currentMod) < StableWindow {
		return current // current session still active — don't jump
	}
	return newest
}

// Locate returns the path to the newest Claude Code transcript for the given
// workspace directory. Claude Code stores transcripts under
// ~/.claude/projects/<slug>/<session>.jsonl, where <slug> is the workspace's
// absolute path with '/' and '.' replaced by '-'.
func Locate(workspacePath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(workspacePath)
	if err != nil {
		abs = workspacePath
	}
	dir := filepath.Join(home, ".claude", "projects", slugify(abs))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no transcripts for %s (looked in %s): %w", workspacePath, dir, err)
	}

	var newest string
	var newestMod int64 = -1
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); mod > newestMod {
			newestMod = mod
			newest = filepath.Join(dir, e.Name())
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no .jsonl transcripts in %s", dir)
	}
	return newest, nil
}

// slugify mirrors Claude Code's project-directory naming: each '/' and '.'
// in the absolute path becomes '-'.
func slugify(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
}
