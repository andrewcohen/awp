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

// WorkspaceExists reports whether jj has a workspace registered under
// this name, regardless of whether its working-copy commit currently
// resolves. We check the workspace registry (`jj workspace list`) and
// NOT `jj log -r <name>@` — the latter returns "no revisions to show"
// for orphaned workspaces whose @ was abandoned, which makes the
// caller think the workspace is gone and try to create it again. jj
// then rejects the create with "already exists" because the name is
// still in the registry. The registry view is what reflects the state
// we'd collide with.
func (c *Client) WorkspaceExists(name string) (bool, error) {
	names, err := c.ListWorkspaceNames()
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
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

// TrackBookmark attempts to make `bookmarkName` resolvable as a local
// bookmark by tracking it from a known remote. Tries `--remote=origin`
// first (the modern syntax — `<name>@<remote>` is deprecated and emits
// a warning), then falls through to a bare-name track for the case
// where the bookmark already exists locally.
//
// jj's `bookmark track` exits 0 even when no remote bookmark matched
// ("Nothing changed."), which previously fooled this method into
// reporting success. We now scan the output for the unmatched warning
// and treat it as a soft failure so the next candidate gets a chance.
// Returning nil only when at least one candidate actually established
// the bookmark.
func (c *Client) TrackBookmark(bookmarkName string) error {
	bookmarkName = strings.TrimSpace(bookmarkName)
	if bookmarkName == "" {
		return nil
	}
	var lastOut string
	var lastErr error
	for _, candidate := range bookmarkTrackCandidates(bookmarkName) {
		args := candidate.args()
		out, err := c.runner.Run(context.Background(), "", "jj", args...)
		if err == nil && !trackOutputIndicatesNoMatch(out) {
			return nil
		}
		if err == nil {
			// jj exited 0 but with "no matching remote bookmarks" —
			// pretend it was a failure so the loop tries the next
			// candidate (and, on exhaustion, reports something useful).
			lastErr = fmt.Errorf("no matching remote bookmark for %s", candidate.describe(bookmarkName))
		} else {
			lastErr = err
		}
		lastOut = out
	}
	if lastErr == nil {
		return nil
	}
	return formatCommandError(fmt.Sprintf("track bookmark %q", bookmarkName), lastErr, lastOut)
}

// trackOutputIndicatesNoMatch detects the jj output that says "I ran
// successfully but the bookmark you named doesn't exist on the remote
// you named." Without this, jj's exit-0 + warning behavior silently
// makes our tracker think the bookmark is now local when it isn't.
func trackOutputIndicatesNoMatch(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "no matching remote bookmarks")
}

// NewOnRevision lands the working-copy commit of the workspace rooted
// at `path` on a fresh empty commit whose parent is `revision` (a
// bookmark name, change id, or any jj revset resolving to one commit).
// Equivalent to `jj new <revision>` from the workspace dir. Used by
// the workspace-preparation pipeline to anchor a workspace on a
// bookmark without mutating it — works regardless of whether the
// target is in jj's immutable set (which `jj edit` does not).
func (c *Client) NewOnRevision(path, revision string) error {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return fmt.Errorf("empty revision")
	}
	out, err := c.runner.Run(context.Background(), path, "jj", "new", revision)
	if err != nil {
		return formatCommandError(fmt.Sprintf("new on revision %q", revision), err, out)
	}
	return nil
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
	out, err := c.runner.Run(context.Background(), "", "jj", "--ignore-working-copy", "log", "-r", name+"@", "--no-graph", "-T", "commit_id.short() ++ \"\\n\"")
	if err != nil {
		return "", formatCommandError(fmt.Sprintf("resolve workspace revision for %q", name), err, out)
	}
	return strings.TrimSpace(out), nil
}

// HeadDescription returns the working-copy commit's short change-id and
// first description line at dir, tab-separated in the underlying jj
// call. --ignore-working-copy skips the snapshot pass so this is safe to
// call repeatedly during deck refresh without churning state. Either
// field may be empty; both are when jj errors.
func (c *Client) HeadDescription(dir string) (changeID, description string, err error) {
	const tmpl = `change_id.shortest(8) ++ "\t" ++ description.first_line()`
	out, runErr := c.runner.Run(context.Background(), dir, "jj", "--ignore-working-copy", "log", "-r", "@", "--no-graph", "-T", tmpl)
	if runErr != nil {
		return "", "", formatCommandError("resolve head description", runErr, out)
	}
	line := strings.TrimRight(out, "\n")
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), nil
	}
	return strings.TrimSpace(line), "", nil
}

// BookmarkCommitID returns the full hex commit-id of the remote-tracking
// ref `name@origin` at dir — i.e. the commit origin pointed at the last
// time this repo fetched. Used to detect "behind remote": comparing this
// against the PR head SHA from GitHub answers whether the last-fetched
// state of this branch matches what's actually on the PR right now.
//
// Resolves remote_bookmarks instead of local_bookmarks because the
// typical re-review case is a collaborator's PR: the branch only exists
// as a remote-tracking ref on the user's machine, with no true local
// bookmark of the same name. Returns "" with no error when the bookmark
// has never been pushed/fetched (revset matches nothing); errors from
// jj invocation are returned.
func (c *Client) BookmarkCommitID(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	out, runErr := c.runner.Run(context.Background(), dir, "jj", "--ignore-working-copy", "log",
		"-r", `remote_bookmarks(exact:"`+name+`", exact:"origin")`, "--no-graph", "-T", "commit_id")
	if runErr != nil {
		return "", formatCommandError("resolve bookmark commit-id", runErr, out)
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
		"jj", "--ignore-working-copy", "bookmark", "list", "--all-remotes", "-T", tmpl)
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
	out, err := c.runner.Run(context.Background(), "", "jj", "--ignore-working-copy", "bookmark", "list", "--all-remotes", "-T", "name ++ \"\\n\"")
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

// Trunk returns the name of the bookmark at jj's `trunk()` revset (the
// repo's integration branch — defaults to main/master/trunk, overridable
// per-repo via revset-aliases). When multiple bookmarks sit at the trunk
// revision, the first one is returned. Empty string + nil error signals
// "no bookmark resolved" so callers can fall back to a literal default
// without aborting the form open.
func (c *Client) Trunk() (string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "--ignore-working-copy", "log", "--no-graph", "-r", "trunk()", "-T", `bookmarks.map(|b| b.name()).join("\n")`)
	if err != nil {
		return "", formatCommandError("resolve trunk", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		// Drop jj diagnostics (and their indented continuation lines) that
		// CombinedOutput merges in from stderr — otherwise a leaked
		// "Warning: Refused to snapshot some files:" line is returned as
		// the trunk bookmark name.
		if line != strings.TrimLeft(line, " \t") {
			continue
		}
		name := strings.TrimSpace(line)
		if name == "" || isJJDiagnosticLine(name) {
			continue
		}
		if idx := strings.Index(name, "@"); idx > 0 {
			name = name[:idx]
		}
		return name, nil
	}
	return "", nil
}

func (c *Client) BookmarksAtRevision(revision string) ([]string, error) {
	out, err := c.runner.Run(context.Background(), "", "jj", "--ignore-working-copy", "bookmark", "list", "-r", revision, "-T", "name ++ \"\\n\"")
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

// trackCandidate is one attempt at `jj bookmark track <args>`. We model
// the args as a slice so we can use the modern `<name> --remote=<remote>`
// form (which doesn't emit jj's deprecation warning) while still being
// able to describe the candidate compactly in error messages.
type trackCandidate struct {
	bookmark string
	remote   string // empty = no --remote arg (bare-name fallback)
}

func (t trackCandidate) args() []string {
	args := []string{"bookmark", "track", t.bookmark}
	if t.remote != "" {
		args = append(args, "--remote="+t.remote)
	}
	return args
}

func (t trackCandidate) describe(bookmark string) string {
	if t.remote == "" {
		return bookmark
	}
	return fmt.Sprintf("%s@%s", bookmark, t.remote)
}

func bookmarkTrackCandidates(bookmark string) []trackCandidate {
	bookmark = strings.TrimSpace(bookmark)
	if bookmark == "" {
		return nil
	}
	// Honor the legacy `<name>@<remote>` form when the caller passed it
	// explicitly. Otherwise fan out from origin (the common case) before
	// trying the bare name (for bookmarks that are already local).
	if at := strings.LastIndexByte(bookmark, '@'); at > 0 {
		return []trackCandidate{{bookmark: bookmark[:at], remote: bookmark[at+1:]}}
	}
	return []trackCandidate{
		{bookmark: bookmark, remote: "origin"},
		{bookmark: bookmark},
	}
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
	for _, raw := range lines {
		// jj writes diagnostics to stderr, which CombinedOutput merges
		// into this stream. Template output (workspace/bookmark names) is
		// never indented, so an indented line is a warning's continuation
		// (e.g. the "  <file> exceeds the maximum size" detail under
		// "Warning: Refused to snapshot some files:") — skip it before the
		// ":" split below mistakes it for a "name: description" row.
		if raw != strings.TrimLeft(raw, " \t") {
			continue
		}
		line := strings.TrimSpace(raw)
		if line == "" || isJJDiagnosticLine(line) {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			line = strings.TrimSpace(line[:idx])
		}
		names = append(names, line)
	}
	return names
}

// isJJDiagnosticLine reports whether a line is a jj diagnostic (warning,
// hint, or error) rather than command output. These reach output parsers
// because the command runner uses CombinedOutput, merging jj's stderr
// into stdout; without filtering they surface as bogus picker rows (the
// "Refused to snapshot some files" warning being the common offender).
func isJJDiagnosticLine(line string) bool {
	low := strings.ToLower(line)
	return strings.HasPrefix(low, "warning:") ||
		strings.HasPrefix(low, "hint:") ||
		strings.HasPrefix(low, "error:")
}
