package jj

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type Client struct {
	runner Runner
}

func New(runner Runner) *Client {
	return &Client{runner: runner}
}

func (c *Client) RepoRoot() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "root")
	if err != nil {
		return "", formatCommandError("resolve repo root", err, out)
	}
	return strings.TrimSpace(out), nil
}

// SourceRepoRoot returns the canonical source repo root, resolving jj secondary
// workspaces to their owning repo. For a primary repo, returns the same as RepoRoot.
// For a secondary workspace whose `.jj/repo` file points at `<source>/.jj/repo`,
// returns `<source>`. Falls back to RepoRoot on any resolution failure.
func (c *Client) SourceRepoRoot() (string, error) {
	root, err := c.RepoRoot()
	if err != nil {
		return "", err
	}
	data, readErr := os.ReadFile(filepath.Join(root, ".jj", "repo"))
	if readErr != nil {
		return root, nil
	}
	pointer := strings.TrimSpace(string(data))
	if pointer == "" {
		return root, nil
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(root, ".jj", pointer)
	}
	pointer = filepath.Clean(pointer)
	// Strip trailing "/.jj/repo" to get repo root.
	if strings.HasSuffix(pointer, string(filepath.Separator)+filepath.Join(".jj", "repo")) {
		pointer = strings.TrimSuffix(pointer, string(filepath.Separator)+filepath.Join(".jj", "repo"))
	} else if base := filepath.Base(pointer); base == "repo" && filepath.Base(filepath.Dir(pointer)) == ".jj" {
		pointer = filepath.Dir(filepath.Dir(pointer))
	}
	if pointer == "" {
		return root, nil
	}
	return pointer, nil
}

func (c *Client) DiffGit(dir string, revision string) (string, error) {
	args := []string{"diff", "--git"}
	revision = strings.TrimSpace(revision)
	if revision != "" {
		args = append(args, "-r", revision)
	}
	out, err := c.runner.Run(context.Background(), dir, "jj", args...)
	if err != nil {
		return "", formatCommandError("load diff", err, out)
	}
	return out, nil
}

func (c *Client) WorkspaceExists(name string) (bool, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "log", "-r", name+"@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\"")
	if err != nil {
		if isMissingRevisionError(out, err) {
			return false, nil
		}
		return false, formatCommandError(fmt.Sprintf("check workspace %q", name), err, out)
	}
	return strings.TrimSpace(out) != "", nil
}

func (c *Client) ListWorkspaceNames() ([]string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "--ignore-working-copy", "workspace", "list", "-T", "name ++ \"\\n\"")
	if err != nil {
		return nil, formatCommandError("list workspaces", err, out)
	}
	return parseWorkspaceNames(out), nil
}

func (c *Client) UpdateStale() error {
	out, err := c.runner.Run(context.Background(), "", "jj", "workspace", "update-stale")
	if err != nil {
		return formatCommandError("update stale working copy", err, out)
	}
	return nil
}

func (c *Client) AddWorkspace(name string, path string, revision string) error {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		revision = "@"
	}
	out, err := c.runner.Run(context.Background(), "", "jj", "workspace", "add", "--name", name, "-r", revision, path)
	if err == nil {
		return nil
	}

	if revision != "@" {
		for _, candidate := range trackCandidates(revision) {
			_, _ = c.runner.Run(context.Background(), "", "jj", "bookmark", "track", candidate)
		}
		out2, err2 := c.runner.Run(context.Background(), "", "jj", "workspace", "add", "--name", name, "-r", revision, path)
		if err2 == nil {
			return nil
		}
		if strings.TrimSpace(out2) != "" {
			out = out2
			err = err2
		}
	}
	return formatCommandError(fmt.Sprintf("create workspace %q", name), err, out)
}

// CreateBookmark creates a new local bookmark at the given revision. If
// revision is empty, the bookmark is created at @ (the current working-copy
// commit of whichever workspace this runner is rooted in). Returns an error
// from jj — including the "bookmark already exists" case, which the caller
// should treat as non-fatal for the auto-bookmark flow (a workspace re-open
// shouldn't fail just because its bookmark is already there).
func (c *Client) CreateBookmark(name, revision string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty bookmark name")
	}
	rev := strings.TrimSpace(revision)
	if rev == "" {
		rev = "@"
	}
	out, err := c.runner.Run(context.Background(), "", "jj", "bookmark", "create", name, "-r", rev)
	if err != nil {
		return formatCommandError(fmt.Sprintf("create bookmark %q at %s", name, rev), err, out)
	}
	return nil
}

func (c *Client) TrackBookmark(bookmarkName string) error {
	bookmarkName = strings.TrimSpace(bookmarkName)
	if bookmarkName == "" {
		return nil
	}
	var lastOut string
	var lastErr error
	for _, candidate := range bookmarkTrackCandidates(bookmarkName) {
		out, err := c.runner.Run(context.Background(), "", "jj", "bookmark", "track", candidate)
		if err == nil {
			return nil
		}
		lastOut = out
		lastErr = err
	}
	if lastErr == nil {
		return nil
	}
	return formatCommandError(fmt.Sprintf("track bookmark %q", bookmarkName), lastErr, lastOut)
}

func (c *Client) RenameWorkspace(path string, newName string) error {
	out, err := c.runner.Run(context.Background(), path, "jj", "workspace", "rename", newName)
	if err != nil {
		return formatCommandError(fmt.Sprintf("rename workspace to %q", newName), err, out)
	}
	return nil
}

func (c *Client) ForgetWorkspace(name string) error {
	out, err := c.runner.Run(context.Background(), "", "jj", "--ignore-working-copy", "workspace", "forget", name)
	if err != nil {
		return formatCommandError(fmt.Sprintf("forget workspace %q", name), err, out)
	}
	return nil
}

func (c *Client) WorkspaceRevision(name string) (string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "log", "-r", name+"@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\"")
	if err != nil {
		return "", formatCommandError(fmt.Sprintf("resolve workspace revision for %q", name), err, out)
	}
	return strings.TrimSpace(out), nil
}

// HeadDescription returns the first line of the working-copy commit's
// description at dir. --ignore-working-copy skips the snapshot pass so this
// is safe to call repeatedly during deck refresh without churning state.
// Returns ("", nil) for an empty / no-description commit.
func (c *Client) HeadDescription(dir string) (string, error) {
	out, err := c.runner.Run(context.Background(), dir, "jj", "--ignore-working-copy", "log", "-r", "@", "--no-graph", "-T", "description.first_line()")
	if err != nil {
		return "", formatCommandError("resolve head description", err, out)
	}
	return strings.TrimSpace(out), nil
}

// AllBookmarksByRecency lists every bookmark deduped to a logical name, sorted
// by the committer timestamp of the bookmark's target commit (most-recent
// first). Bookmarks whose target cannot be resolved (e.g. conflicted, or a
// remote-only bookmark with no fetched commit) sort to the bottom keeping
// their original order. Used by the bookmark picker so recently-touched
// branches surface first.
func (c *Client) AllBookmarksByRecency() ([]string, error) {
	// Template: "<unix-seconds>\t<name>\n". Falling back to "0" when the
	// bookmark has no normal_target() keeps the line shape stable so the
	// parser doesn't need a special-case.
	const tmpl = `if(self.normal_target(), self.normal_target().committer().timestamp().format("%s"), "0") ++ "\t" ++ name ++ "\n"`
	out, err := c.runner.Run(context.Background(), "",
		"jj", "bookmark", "list", "--all-remotes", "-T", tmpl)
	if err != nil {
		return nil, formatCommandError("list bookmarks by recency", err, out)
	}
	type entry struct {
		ts   int64
		name string
		idx  int
	}
	var entries []entry
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		rawName := strings.TrimSpace(parts[1])
		if rawName == "" {
			continue
		}
		name := rawName
		if idx := strings.Index(name, "@"); idx > 0 {
			name = name[:idx]
		}
		ts, _ := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		entries = append(entries, entry{ts: ts, name: name, idx: i})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].ts != entries[j].ts {
			return entries[i].ts > entries[j].ts
		}
		return entries[i].idx < entries[j].idx
	})
	seen := make(map[string]struct{})
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.name]; ok {
			continue
		}
		seen[e.name] = struct{}{}
		names = append(names, e.name)
	}
	return names, nil
}

// AllBookmarks lists every bookmark visible to jj, deduped to a logical name.
// A remote bookmark "main@origin" is folded into "main"; if a local bookmark
// of the same name exists it wins. The returned slice preserves the first-seen
// order from `jj bookmark list --all`.
func (c *Client) AllBookmarks() ([]string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "bookmark", "list", "--all-remotes", "-T", "name ++ \"\\n\"")
	if err != nil {
		return nil, formatCommandError("list bookmarks", err, out)
	}
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, raw := range parseWorkspaceNames(out) {
		name := raw
		if idx := strings.Index(name, "@"); idx > 0 {
			name = name[:idx]
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

func (c *Client) BookmarksAtRevision(revision string) ([]string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "bookmark", "list", "-r", revision, "-T", "name ++ \"\\n\"")
	if err != nil {
		return nil, formatCommandError(fmt.Sprintf("list bookmarks at revision %q", revision), err, out)
	}
	return parseWorkspaceNames(out), nil
}

func (c *Client) ForgetBookmark(name string) error {
	out, err := c.runner.Run(context.Background(), "", "jj", "bookmark", "forget", "--include-remotes", name)
	if err != nil {
		text := strings.ToLower(strings.TrimSpace(out + "\n" + err.Error()))
		if strings.Contains(text, "no bookmarks matched") {
			return nil
		}
		return formatCommandError(fmt.Sprintf("forget bookmark %q", name), err, out)
	}
	return nil
}

func (c *Client) IsRevisionEmpty(revision string) (bool, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "diff", "-r", revision)
	if err != nil {
		return false, formatCommandError(fmt.Sprintf("inspect revision %q", revision), err, out)
	}
	return strings.TrimSpace(out) == "", nil
}

func (c *Client) AbandonRevision(revision string) error {
	out, err := c.runner.Run(context.Background(), "", "jj", "abandon", revision)
	if err != nil {
		return formatCommandError(fmt.Sprintf("abandon revision %q", revision), err, out)
	}
	return nil
}

func formatCommandError(action string, err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w\n%s", action, err, output)
}

func trackCandidates(revision string) []string {
	revision = strings.TrimSpace(revision)
	if revision == "" || revision == "@" {
		return nil
	}
	candidates := []string{revision}
	if !strings.Contains(revision, "@") {
		candidates = append(candidates, revision+"@origin")
	}
	return candidates
}

func bookmarkTrackCandidates(bookmark string) []string {
	bookmark = strings.TrimSpace(bookmark)
	if bookmark == "" {
		return nil
	}
	if strings.Contains(bookmark, "@") {
		return []string{bookmark}
	}
	return []string{bookmark + "@origin", bookmark}
}

func isMissingRevisionError(output string, err error) bool {
	text := strings.ToLower(strings.TrimSpace(output + "\n" + err.Error()))
	return strings.Contains(text, "doesn't exist") || strings.Contains(text, "does not exist") || strings.Contains(text, "no revisions to show") || strings.Contains(text, "doesn't have a working-copy commit")
}

func IsStaleWorkingCopyError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "working copy is stale") || strings.Contains(text, "workspace update-stale")
}

func parseWorkspaceNames(out string) []string {
	lines := strings.Split(out, "\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			line = strings.TrimSpace(line[:idx])
		}
		names = append(names, line)
	}
	return names
}
