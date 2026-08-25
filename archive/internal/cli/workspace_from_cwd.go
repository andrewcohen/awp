package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

// Which workspace am I in?
//
// Everything that reports status, records a gate, or clears an unread mark has to
// answer that, and until now it could only be *told*: AWP_WORKSPACE in the process
// environment, with a tmux session-env fallback. Both are injections, and an
// injection is a thing a launcher can forget.
//
// A zmx session made the failure permanent. Its environment is fixed at creation and
// attaching re-injects nothing, so a session created by an older awp keeps that
// environment for as long as it lives — it simply stops reporting status and
// recording gates, with no signal at all. The first fix considered was stamping an
// env-contract version as a zmx label and replacing a mismatch on reap; that cannot
// work, because `zmx attach` creates the session lazily, so at the moment we would
// label it there is no session, and afterwards "no label" is indistinguishable from
// "we just made it". `zmx set` writes labels — it cannot reach a running process's
// environment either.
//
// So ask a question nobody has to remember to answer. An agent in a workspace pane is
// running *in* the workspace's directory, and the state file already records every
// workspace's path. Walking up from cwd and matching against that list retires the
// whole class — a stale session, a session started outside awp, any future change to
// the pane environment — and it inverts the contract: instead of every launcher
// having to inject the workspace, the code that needs the answer can find it.

// workspaceFromCwd answers the identity triple from the process's working directory,
// or reports false when the directory is not inside a workspace awp knows about.
//
// Reads the state file directly rather than going through workspace.Service.ListAll:
// this runs in a hook, several times per agent turn, and a Service needs a jj client
// it would have no other use for. The paths are the same paths ListAll reports —
// both read what the deck wrote.
func workspaceFromCwd() (workspaceName, repo, repoRoot string, ok bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", "", false
	}
	byRepo, err := state.NewJSONStore().LoadAll()
	if err != nil {
		return "", "", "", false
	}
	return workspaceForDir(dir, byRepo)
}

// workspaceForDir is workspaceFromCwd's decision, given a directory and the recorded
// workspaces. Separated so it can be tested without a home directory or a cwd.
//
// Walks *up* from dir, so the deepest workspace containing it wins. That matters
// because a repo's own root is itself a workspace record (`default`), and other
// workspaces may live beneath it: matching shallowest-first would report every agent
// as being in `default`.
//
// Symlinks are resolved on both sides. On macOS the obvious place to put a working
// copy is under /tmp, which is a symlink to /private/tmp, so a recorded path and a
// process's cwd routinely name the same directory in two spellings — and a string
// comparison says they are different workspaces.
//
// The repo root it answers with is the *recorded* one, in the state file's own
// spelling rather than the resolved form matching used. It is a lookup key —
// writeWorkspaceStatus matches it against the state's repo keys — so handing back
// another spelling of the same directory would find nothing.
func workspaceForDir(dir string, byRepo map[string]map[string]workspace.Entry) (workspaceName, repo, repoRoot string, ok bool) {
	dir = resolveDir(dir)
	if dir == "" {
		return "", "", "", false
	}
	type owner struct {
		workspace, repo, root string
	}
	paths := map[string]owner{}
	for root, entries := range byRepo {
		project := strings.TrimSpace(filepath.Base(filepath.Clean(root)))
		for name, e := range entries {
			path := resolveDir(e.Path)
			if path == "" {
				continue
			}
			// First writer wins per path, and a map is iterated in no order — so a path
			// claimed by two repos is a coin toss. It is also a state file that
			// contradicts itself, and either answer beats reporting none.
			if _, taken := paths[path]; taken {
				continue
			}
			paths[path] = owner{workspace: name, repo: project, root: root}
		}
	}
	for at := dir; ; {
		if o, hit := paths[at]; hit {
			return o.workspace, o.repo, o.root, true
		}
		parent := filepath.Dir(at)
		if parent == at {
			return "", "", "", false
		}
		at = parent
	}
}

// resolveDir is a path in the one spelling comparisons can use: absolute, cleaned,
// and with symlinks resolved where the directory exists.
//
// A path that will not resolve is cleaned rather than dropped: a workspace whose
// directory has been removed matches nothing, but it is not a reason to give up on
// the rest of the list.
func resolveDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(path)
}
