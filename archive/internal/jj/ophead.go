package jj

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OpHead returns an identifier for the operation the repo behind dir is
// currently at, read from the filesystem without running jj.
//
// It exists so a caller that reads the same repo over and over — the deck's
// refresh does, once per workspace every few seconds — can ask "has anything
// happened here" for two syscalls instead of a subprocess. Every jj command
// that changes what a repo says records an operation and writes a new head, so
// an unchanged head means an unchanged answer to any `--ignore-working-copy`
// query: the working-copy commit, its description, where a bookmark points.
// (Without that flag the answer also depends on the files on disk, which this
// does not see — so this is a signal for those reads only.)
//
// The head is repo-wide, not workspace-wide, so a `jj` in any one of a repo's
// workspaces invalidates the lot. That is the conservative direction: it costs
// a re-read that turns out to be unnecessary, never a stale one.
//
// Returns "" with a nil error when dir is not in a jj repo — the caller has
// nothing to invalidate and nothing to read. A non-nil error means the repo is
// there but its head could not be read, which callers should treat as "assume
// it changed" rather than as a reason to fail.
func OpHead(dir string) (string, error) {
	repo, err := repoDir(dir)
	if err != nil || repo == "" {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(repo, "op_heads", "heads"))
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// Concurrent operations leave more than one head until something merges
	// them, and ReadDir's order is the filesystem's. Sorting makes the same
	// repo state produce the same string.
	sort.Strings(names)
	return strings.Join(names, " "), nil
}

// repoDir resolves dir/.jj to the directory holding the repo's op log.
//
// In the repo the workspace was created from, .jj/repo is that directory. In a
// workspace added with `jj workspace add` it is a file holding the path to it,
// relative to the .jj directory the file lives in — which is why this resolves
// against jjDir rather than against dir.
//
// Returns "" when there is no .jj at all, which includes the common case of a
// workspace whose directory has been deleted out from under the state file.
func repoDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	jjDir := filepath.Join(dir, ".jj")
	repoPath := filepath.Join(jjDir, "repo")
	info, err := os.Stat(repoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if info.IsDir() {
		return repoPath, nil
	}
	raw, err := os.ReadFile(repoPath) //nolint:gosec // path derived from the caller's own workspace dir
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(string(raw))
	if target == "" {
		return "", nil
	}
	if filepath.IsAbs(target) {
		return target, nil
	}
	return filepath.Join(jjDir, target), nil
}
