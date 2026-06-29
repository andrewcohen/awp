package cli

// TODO(tuicr#368): every helper in this file peeks at tuicr's internal
// state files (active_sessions.json, index.json) and guesses at the
// directories-crate data-dir layout. The supported, forge-aware surface
// now exists — `tuicr review list --repo <owner/repo>` (and `--all`)
// emits JSON with slug→path for PR-mode sessions — and the review prompt
// already tells the agent to resolve/verify the session that way. This
// Go-side file peeking remains only as the fast path for the brief window
// before a freshly-launched `tuicr pr` session registers; consider
// replacing it with a `tuicr review list` shell-out once the async
// registration timing is confirmed to be covered. The path we resolve is
// now validated to exist (sessionFileExists) before we inject it.
// https://github.com/agavra/tuicr/issues/368

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// tuicrSessionSlug returns the deterministic slug tuicr uses for a PR
// session, e.g. "gh:Fast-Growing-Trees-LLC/grove/pr/430". It parses
// owner/repo from the PR URL (https://github.com/<owner>/<repo>/pull/N).
// Returns "" when prURL doesn't match the expected shape.
func tuicrSessionSlug(prURL string, prNum int) string {
	owner, repo := ownerRepoFromPRURL(prURL)
	if owner == "" || repo == "" || prNum <= 0 {
		return ""
	}
	return fmt.Sprintf("gh:%s/%s/pr/%d", owner, repo, prNum)
}

func ownerRepoFromPRURL(prURL string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(prURL))
	if err != nil || u.Path == "" {
		return "", ""
	}
	// /<owner>/<repo>/pull/<n>
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", ""
	}
	return parts[0], parts[1]
}

// tuicrDataDir returns the absolute path to tuicr's data dir (the
// directory containing reviews/active_sessions.json). Mirrors the
// directories crate's project_dirs resolution that tuicr uses
// internally:
//   - darwin:  $HOME/Library/Application Support/tuicr
//   - linux:   $XDG_DATA_HOME/tuicr, defaulting to $HOME/.local/share/tuicr
//   - other:   $HOME/.config/tuicr (best-effort fallback)
//
// Empty string when $HOME is unset.
func tuicrDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "tuicr")
	case "linux":
		if x := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); x != "" {
			return filepath.Join(x, "tuicr")
		}
		return filepath.Join(home, ".local", "share", "tuicr")
	default:
		return filepath.Join(home, ".config", "tuicr")
	}
}

// resolveTuicrSessionPath looks up the absolute path of the session
// JSON file for a given slug. Tries active_sessions.json first (newest
// signal — what's currently running in a TUI), then index.json (the
// persistent registry of all known sessions). Returns "" if neither
// holds a matching entry.
//
// We resolve to a file path rather than relying on the slug because
// `tuicr review {comments,add}` look up sessions via --repo, and
// PR-mode sessions store repo_path as "forge:github.com/..." which no
// local checkout will match. The --session flag accepts an absolute
// path as a documented escape hatch; that's what we use.
func resolveTuicrSessionPath(dataDir, slug string) string {
	if dataDir == "" || slug == "" {
		return ""
	}
	// Validate the file exists before returning it: tuicr's index can
	// outlive the session JSON it points at (pruned / moved), and we must
	// never inject a --session path the agent will then fail to open.
	if p := readActiveSessionsPath(filepath.Join(dataDir, "reviews", "active_sessions.json"), slug); sessionFileExists(p) {
		return p
	}
	if p := readIndexPath(filepath.Join(dataDir, "reviews", "index.json"), dataDir, slug); sessionFileExists(p) {
		return p
	}
	return ""
}

// sessionFileExists reports whether p names an existing regular file. Used
// to confirm a resolved session path is real before we hand it to the
// agent as --session.
func sessionFileExists(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

type tuicrActiveSessionsFile struct {
	Sessions []struct {
		Slug string `json:"slug"`
		Path string `json:"path"`
	} `json:"sessions"`
}

func readActiveSessionsPath(file, slug string) string {
	raw, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var data tuicrActiveSessionsFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	for _, s := range data.Sessions {
		if s.Slug == slug && strings.TrimSpace(s.Path) != "" {
			return s.Path
		}
	}
	return ""
}

type tuicrIndexFile struct {
	Entries map[string][]struct {
		Path string `json:"path"`
	} `json:"entries"`
}

func readIndexPath(file, dataDir, slug string) string {
	raw, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	var data tuicrIndexFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	entries, ok := data.Entries[slug]
	if !ok || len(entries) == 0 {
		return ""
	}
	rel := strings.TrimSpace(entries[0].Path)
	if rel == "" {
		return ""
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(dataDir, "reviews", rel)
}

// awaitTuicrSessionPath polls resolveTuicrSessionPath until it returns
// a non-empty path or the timeout elapses. Used after launching
// `tuicr pr <n>` in its tmux window — the TUI writes active_sessions.json
// asynchronously, so the slug may not be registered for ~1 second.
func awaitTuicrSessionPath(ctx context.Context, dataDir, slug string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if p := resolveTuicrSessionPath(dataDir, slug); p != "" {
			return p
		}
		if time.Now().After(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(100 * time.Millisecond):
		}
	}
}
