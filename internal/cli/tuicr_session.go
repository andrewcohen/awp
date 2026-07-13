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
	"sort"
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

// tuicrSessionFile is the subset of a tuicr session JSON we read. tuicr
// owns the full schema; we deliberately parse only the fields we need so
// added fields don't break us. Comments live in two places: the
// top-level review_comments[] (review- and file-scoped) and, per file,
// files.<path>.{line_comments,file_comments}[].
type tuicrSessionFile struct {
	PRSessionKey struct {
		Number  int    `json:"number"`
		HeadSHA string `json:"head_sha"`
	} `json:"pr_session_key"`
	UpdatedAt      string            `json:"updated_at"`
	ReviewComments []json.RawMessage `json:"review_comments"`
	Files          map[string]struct {
		LineComments []json.RawMessage `json:"line_comments"`
		FileComments []json.RawMessage `json:"file_comments"`
	} `json:"files"`
}

// commentCount totals every stored comment across the review, file, and
// line scopes.
func (s tuicrSessionFile) commentCount() int {
	n := len(s.ReviewComments)
	for _, f := range s.Files {
		n += len(f.LineComments) + len(f.FileComments)
	}
	return n
}

// readSessionFile reads and parses a tuicr session JSON. Returns ok=false
// on any read/parse error so callers can skip a bad file rather than fail.
func readSessionFile(path string) (tuicrSessionFile, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return tuicrSessionFile{}, false
	}
	var s tuicrSessionFile
	if err := json.Unmarshal(raw, &s); err != nil {
		return tuicrSessionFile{}, false
	}
	return s, true
}

// readSessionHeadSHA returns the head SHA a session JSON is anchored to
// (pr_session_key.head_sha), or "" if the file is missing, malformed, or
// not a PR session. Used to tell whether the tuicr pane's current session
// is on the same head as the freshly-fetched PR.
func readSessionHeadSHA(path string) string {
	s, ok := readSessionFile(path)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s.PRSessionKey.HeadSHA)
}

// priorSession names a tuicr session for a PR that is anchored to a head
// other than the current one and still holds unpublished draft comments —
// i.e. comments stranded when the PR head moved.
type priorSession struct {
	Path     string
	HeadSHA  string
	Updated  string
	Comments int
}

// findPriorSessionsWithComments scans tuicr's on-disk session store for
// sessions belonging to prNumber whose head differs from currentHead and
// that still carry at least one comment. Newest first.
//
// This scans reviews/sessions/*.json directly because `tuicr review list`
// collapses to a single entry per PR slug and hides the older-head
// sessions where stranded drafts live — the CLI cannot surface them.
// (Same TODO(tuicr#368) caveat as the rest of this file: it peeks at
// tuicr's data-dir layout.)
func findPriorSessionsWithComments(dataDir string, prNumber int, currentHead string) []priorSession {
	if dataDir == "" || prNumber <= 0 {
		return nil
	}
	currentHead = strings.TrimSpace(currentHead)
	glob := filepath.Join(dataDir, "reviews", "sessions", "*.json")
	matches, err := filepath.Glob(glob)
	if err != nil {
		return nil
	}
	var out []priorSession
	for _, path := range matches {
		s, ok := readSessionFile(path)
		if !ok {
			continue
		}
		if s.PRSessionKey.Number != prNumber {
			continue
		}
		head := strings.TrimSpace(s.PRSessionKey.HeadSHA)
		// Skip the current-head session (that's the migration target, not
		// a source) and any session with no resolvable head.
		if head == "" || head == currentHead {
			continue
		}
		n := s.commentCount()
		if n == 0 {
			continue
		}
		out = append(out, priorSession{
			Path:     path,
			HeadSHA:  head,
			Updated:  strings.TrimSpace(s.UpdatedAt),
			Comments: n,
		})
	}
	// Newest first. tuicr stamps updated_at as RFC3339, whose date/time
	// prefix dominates a lexical compare, so string ordering matches
	// chronological ordering for distinct instants.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Updated > out[j].Updated
	})
	return out
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
