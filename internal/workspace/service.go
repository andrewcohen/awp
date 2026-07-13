package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/config"
)

// StaleWorkspaceError marks a PrepareWorkspace failure where the jj
// workspace was already registered but couldn't be reconciled to the
// caller's intent (e.g. NewOnRevision rejected the bookmark, or the
// `already exists` retry path couldn't recover). The deck recognizes
// this via IsStaleWorkspaceError and offers a "delete the workspace
// and retry the job" affordance on the failed job in the jobs overlay.
type StaleWorkspaceError struct {
	Name  string
	Cause error
}

func (e *StaleWorkspaceError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return fmt.Sprintf("workspace %q is in a stale state", e.Name)
	}
	return fmt.Sprintf("workspace %q is in a stale state: %v", e.Name, e.Cause)
}

func (e *StaleWorkspaceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsStaleWorkspaceError reports whether err (or anything it wraps) is a
// StaleWorkspaceError. Lets callers in other packages branch on the
// "recoverable by deleting the workspace" condition without importing
// the error type directly.
func IsStaleWorkspaceError(err error) bool {
	var s *StaleWorkspaceError
	return errors.As(err, &s)
}

// StaleWorkspaceName extracts the workspace name from a wrapped
// StaleWorkspaceError so callers can dispatch a delete-and-retry on the
// right workspace without re-parsing the spec. Returns ("", false)
// when err isn't a stale-workspace error.
func StaleWorkspaceName(err error) (string, bool) {
	var s *StaleWorkspaceError
	if !errors.As(err, &s) || s == nil {
		return "", false
	}
	return s.Name, true
}

type JJClient interface {
	RepoRoot() (string, error)
	SourceRepoRoot() (string, error)
	WorkspaceExists(name string) (bool, error)
	ListWorkspaceNames() ([]string, error)
	AddWorkspace(name string, path string, revision string) error
	NewOnRevision(path, revision string) error
	TrackBookmark(bookmarkName string) error
	RenameWorkspace(path string, newName string) error
	ForgetWorkspace(name string) error
	WorkspaceRevision(name string) (string, error)
	BookmarksAtRevision(revision string) ([]string, error)
	Trunk() (string, error)
	ForgetBookmark(name string) error
	IsRevisionEmpty(revision string) (bool, error)
	AbandonRevision(revision string) error
	UpdateStale() error
}

type TmuxClient interface {
	WindowExists(name string) (bool, error)
	NewWindow(name string, dir string) error
	SendCommand(name string, command string) error
	SwitchToWindow(name string) error
	RenameWindow(oldName string, newName string) error
	KillWindow(name string) error
	CurrentWindow() (string, error)
}

type Store interface {
	Load(repoRoot string) (map[string]Entry, error)
	Save(repoRoot string, entries map[string]Entry) error
}

// UpdaterStore is an optional interface a Store may implement to provide an
// atomic read-modify-write that holds an OS-level lock for the whole sequence.
// Mutations should prefer this when available so concurrent writers from
// agent hooks + the deck don't drop each other's changes.
type UpdaterStore interface {
	Update(repoRoot string, fn func(map[string]Entry) map[string]Entry) error
}

type AllStore interface {
	LoadAll() (map[string]map[string]Entry, error)
}

type HookConfigProvider interface {
	PostWorkspaceStart(repoRoot string) ([]string, error)
}

type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type Entry struct {
	Name     string
	Path     string
	Bookmark string `json:",omitempty"`
	// PRNumber pins this workspace to a specific PR. Set by `awp review`
	// (the PR being reviewed), the `p s` chord, and the `B` link flow
	// when the chosen bookmark resolves to exactly one PR. Zero means
	// the workspace isn't associated with a PR — its bookmark is
	// resolved via the bulk pr-status cache, or it simply doesn't have a
	// PR yet.
	//
	// The JSON tag accepts the legacy field name "PROverride" via the
	// custom UnmarshalJSON below — state files written before the
	// rename keep loading.
	PRNumber      int    `json:",omitempty"`
	SessionID     string `json:",omitempty"`
	SessionName   string `json:",omitempty"`
	AgentWindowID string `json:",omitempty"`
	AgentPaneID   string `json:",omitempty"`
	ActivePrompt  string `json:",omitempty"`
	Status        string `json:",omitempty"`
	// Unread is set when the agent transitions to a state that wants the
	// user's attention (waiting/idle) and cleared when the user summons
	// the workspace or the agent exits. Surfaces as a tmux status badge
	// and the "notified" dot in the deck.
	Unread bool `json:",omitempty"`
	// PinGroup pins this workspace to a register that floats it to a
	// section at the top of the deck. "" means unpinned; "default" is the
	// register bound to the `gg` chord; otherwise a single lowercase
	// letter a–z. Register display aliases are stored globally (see
	// state.PinGroupAliases) since a register spans repos in the deck's
	// merged view.
	PinGroup string `json:",omitempty"`
	// DevLoop is the last dev-loop progress snapshot the deck computed for
	// this workspace's agent (see internal/watch). It is a cache, refreshed
	// by the deck's background loader while the agent is working, so the
	// next deck open can render the row's progress on the fast first paint
	// without re-scanning the transcript — avoiding a branch/port → progress
	// "flash". Nil when no snapshot has been recorded.
	DevLoop *DevLoopSnapshot `json:",omitempty"`
}

// DevLoopSnapshot is the persisted form of the deck's dev-loop meta line:
// the same done/total + phase + current-unit projection carried on a deck
// row, cached in the state file so it survives across deck processes. It
// mirrors deckui.DevLoopSummary; the deck loader maps between the two.
type DevLoopSnapshot struct {
	Done  int    `json:",omitempty"`
	Total int    `json:",omitempty"`
	Phase string `json:",omitempty"`
	Task  string `json:",omitempty"`
}

// UnmarshalJSON keeps reading old state files that still use the
// "PROverride" key. The field was renamed to "PRNumber" as part of
// collapsing the bookmark+PROverride lookup into a single PRNumber
// path. Reads accept either key; writes always emit "PRNumber".
func (e *Entry) UnmarshalJSON(data []byte) error {
	type entryAlias Entry
	aux := struct {
		*entryAlias
		PROverride int `json:",omitempty"`
	}{entryAlias: (*entryAlias)(e)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if e.PRNumber == 0 && aux.PROverride != 0 {
		e.PRNumber = aux.PROverride
	}
	return nil
}

type ListEntry struct {
	Name         string
	Path         string
	TmuxWindow   string
	Active       bool
	SessionID    string
	SessionName  string
	ActivePrompt string
	Status       string
	Unread       bool
}

type InfoEntry struct {
	Name         string
	Path         string
	Managed      bool
	JJExists     bool
	TmuxWindow   string
	TmuxExists   bool
	ActiveWindow bool
}

type Service interface {
	List() ([]ListEntry, error)
	ListAll() ([]CrossRepoEntry, error)
	Info(name string) (InfoEntry, error)
	Open(name string, bookmark string, prompt string, yes bool) error
	PrepareWorkspace(name string, bookmark string, runHooks bool) (normalized string, path string, err error)
	Bootstrap(name string) error
	BootstrapAll() error
	Rename(oldName, newName string) error
	Delete(name string, force bool) error
	DeleteWithOptions(name string, opts DeleteOptions) error
	RecordSession(workspaceName, sessionID, sessionName string) error
	RecordBookmark(workspaceName, bookmark string) error
	RecordPROverride(workspaceName string, prNumber int) error
	UpdatePrompt(workspaceName, prompt string) error
	UpdateStatus(workspaceName, status string) error
	MarkRead(workspaceName string) error
	ClearSession(workspaceName string) error
	PruneOrphans(dryRun bool) ([]string, error)
}

type CrossRepoEntry struct {
	RepoRoot     string
	ProjectName  string
	Name         string
	Path         string
	SessionID    string
	SessionName  string
	ActivePrompt string
	Status       string
	Unread       bool
}

type Dependencies struct {
	JJ            JJClient
	Tmux          TmuxClient
	Store         Store
	Hooks         HookConfigProvider
	Runner        CommandRunner
	InvocationDir string
	Input         io.Reader
	Out           io.Writer
}

type service struct {
	jj            JJClient
	tmux          TmuxClient
	store         Store
	hooks         HookConfigProvider
	runner        CommandRunner
	invocationDir string
	in            io.Reader
	out           io.Writer
}

type defaultCommandRunner struct{}

func (r *defaultCommandRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), explainCmdError(name, string(out), err)
}

// explainCmdError turns opaque exec errors into actionable messages. Mirrors
// cli.explainExecError; kept here to avoid a workspace→cli import cycle. The
// long-form PATH hint lives in cli.pathHint — this layer only sees jj/tmux
// invocations from the service, where the most common failure is a missing
// binary or a non-zero exit.
func explainCmdError(name, out string, err error) error {
	if err == nil {
		return nil
	}
	hint := cmdRunnerPathHint(name)
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%q is not on $PATH for this process.\n\n%s", name, hint)
	}
	var perr *exec.Error
	if errors.As(err, &perr) {
		return fmt.Errorf("could not run %q: %w\n\n%s", name, perr.Err, hint)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		snippet := strings.TrimSpace(out)
		if len(snippet) > 800 {
			snippet = snippet[:800] + "…"
		}
		if code == 127 {
			lead := fmt.Sprintf("%q exited 127 — the shell that ran it could not find the binary.", name)
			if snippet != "" {
				lead += "\n\nOutput:\n  " + snippet
			}
			return fmt.Errorf("%s\n\n%s", lead, hint)
		}
		if snippet != "" {
			return fmt.Errorf("%q exited %d:\n%s", name, code, snippet)
		}
		return fmt.Errorf("%q exited %d (no output)", name, code)
	}
	return err
}

func cmdRunnerPathHint(name string) string {
	return strings.Join([]string{
		"Why this can happen inside a tmux popup or run-shell:",
		"  tmux's popup/run-shell runs under a non-interactive /bin/sh that does NOT",
		"  source your shell rc. The tmux server captures PATH once when it starts;",
		"  if it was launched from a context where your shell rc had not yet added",
		"  the install dir for " + name + ", it never will. That's why the same binding",
		"  can work for one teammate and fail for another with `exit 127`.",
		"",
		"Fixes (pick one):",
		"  1. Inject PATH into the tmux server (covers all popups). Add to ~/.tmux.conf:",
		"       set-environment -g PATH \"$HOME/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin\"",
		"     Then reload: `tmux source-file ~/.tmux.conf`.",
		"  2. Use an absolute path in your tmux bindings or the awp invocation.",
		"",
		"Verify with a popup that prints what tmux actually sees:",
		"  tmux display-popup -E 'echo \"$PATH\"; which " + name + "; read'",
		"(`tmux show-environment` does not answer this; it only lists explicit",
		"set-environment overrides, not the inherited PATH.)",
	}, "\n")
}

func NewService(deps Dependencies) *service {
	in := deps.Input
	if in == nil {
		in = os.Stdin
	}
	out := deps.Out
	if out == nil {
		out = os.Stdout
	}
	runner := deps.Runner
	if runner == nil {
		runner = &defaultCommandRunner{}
	}
	invocationDir := strings.TrimSpace(deps.InvocationDir)
	if invocationDir == "" {
		if wd, err := os.Getwd(); err == nil {
			invocationDir = wd
		}
	}
	if invocationDir != "" {
		if abs, err := filepath.Abs(invocationDir); err == nil {
			invocationDir = abs
		}
	}
	return &service{
		jj:            deps.JJ,
		tmux:          deps.Tmux,
		store:         deps.Store,
		hooks:         deps.Hooks,
		runner:        runner,
		invocationDir: invocationDir,
		in:            in,
		out:           out,
	}
}

// PrepareWorkspace runs the jj + store + hooks portion of workspace creation
// without touching tmux. Returns the normalized name and workspace path.
// Callers (e.g. `awp review`) can then build their own tmux layout.
func (s *service) PrepareWorkspace(name string, bookmark string, runHooks bool) (string, string, error) {
	normalized, workspacePath, _, err := s.prepareWorkspaceInternal(name, bookmark, runHooks)
	return normalized, workspacePath, err
}

// prepareWorkspaceInternal reconciles the workspace's state with the
// caller's intent: at exit, jj has a workspace named `normalized` at
// `workspacePath`, the working copy is on `bookmark` (when one is
// given), the store has a matching entry, the bookmark is tracked, and
// builtin bootstrap has run. Returns (normalized, path, alreadyExisted,
// err) so callers can distinguish "first run for this workspace" from
// "re-open" — e.g. post-start hooks only run on first creation.
//
// Each step is idempotent. No branch skips reconciliation work just
// because the workspace already existed — that's the regression we hit
// when a half-finished review left a workspace at the wrong revision
// and the next review attempt happily re-attached without aligning the
// working copy to the requested bookmark.
func (s *service) prepareWorkspaceInternal(name string, bookmark string, runHooks bool) (string, string, bool, error) {
	s.logf("▶️ Preparing workspace (name=%q, bookmark=%q)", strings.TrimSpace(name), strings.TrimSpace(bookmark))
	currentRoot, err := s.jj.RepoRoot()
	if err != nil {
		return "", "", false, fmt.Errorf("not a jj repository: %w", err)
	}
	repoRoot, sErr := s.jj.SourceRepoRoot()
	if sErr != nil || strings.TrimSpace(repoRoot) == "" {
		repoRoot = currentRoot
	}
	if err := s.guardRepoRoot(repoRoot); err != nil {
		return "", "", false, err
	}
	if strings.TrimSpace(name) == "" && strings.TrimSpace(bookmark) != "" {
		name = bookmark
	}
	normalized, err := s.resolveName(name)
	if err != nil {
		return "", "", false, err
	}
	workspacePath := s.defaultWorkspacePath(repoRoot, normalized)
	trimmedBookmark := strings.TrimSpace(bookmark)

	existedBefore, err := s.jj.WorkspaceExists(normalized)
	if err != nil {
		return "", "", false, err
	}

	// Step 1: ensure the bookmark is tracked locally before any
	// operation that names it as a revision (AddWorkspace -r, NewOnRevision).
	if trimmedBookmark != "" {
		if err := s.trackBookmark(trimmedBookmark); err != nil {
			return "", "", false, err
		}
	}

	// Step 2: ensure the jj workspace exists at workspacePath. On a
	// race where jj remembers the workspace but our state says it's
	// gone, forget + retry once.
	if !existedBefore {
		if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
			return "", "", false, fmt.Errorf("create workspace parent dir: %w", err)
		}
		if err := s.prepareWorkspacePath(workspacePath); err != nil {
			return "", "", false, err
		}
		startRevision := "@"
		if trimmedBookmark != "" {
			startRevision = trimmedBookmark
		}
		if err := s.jj.AddWorkspace(normalized, workspacePath, startRevision); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				return "", "", false, err
			}
			s.logf("⚠️ jj reported workspace already exists; retrying after forget")
			_ = s.jj.ForgetWorkspace(normalized)
			if prepErr := s.prepareWorkspacePath(workspacePath); prepErr != nil {
				return "", "", false, prepErr
			}
			if err2 := s.jj.AddWorkspace(normalized, workspacePath, startRevision); err2 != nil {
				return "", "", false, err2
			}
		}
	}

	// Step 3: anchor the working copy on the requested bookmark. We
	// always run `jj new <bookmark>` from the workspace dir — a fresh
	// empty commit on top of the bookmark — instead of `jj edit
	// <bookmark>`, which moves @ ONTO the bookmark and so refuses
	// whenever the bookmark sits on an immutable commit (PR review of
	// someone else's branch is the canonical case). The cost is that
	// awp no longer auto-advances the bookmark as the user edits; if
	// they want the bookmark to track their work they run
	// `jj bookmark set <bm> -r @` themselves.
	//
	// Idempotency note: `jj new` always creates a fresh empty commit,
	// so reconciliation on an existing workspace abandons the prior @
	// rather than no-op'ing. That's the intended behavior for the
	// drift-recovery case (e.g. an aborted review left @ somewhere
	// weird). The abandoned commit is still reachable via
	// `jj op log` / `jj op restore` if it had real work in it.
	if trimmedBookmark != "" {
		if err := s.jj.NewOnRevision(workspacePath, trimmedBookmark); err != nil {
			wrapped := fmt.Errorf("anchor workspace %q on bookmark %q: %w", normalized, trimmedBookmark, err)
			if existedBefore {
				return "", "", true, &StaleWorkspaceError{Name: normalized, Cause: wrapped}
			}
			return "", "", false, wrapped
		}
	}

	// Step 4: upsert the store entry. Only writes when something
	// changed so we don't churn the JSON file on every reopen.
	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return "", "", existedBefore, err
	}
	entry, ok := entries[normalized]
	mutated := false
	if !ok {
		entry = Entry{Name: normalized, Path: workspacePath}
		mutated = true
	}
	if entry.Path == "" {
		entry.Path = workspacePath
		mutated = true
	}
	if trimmedBookmark != "" && entry.Bookmark != trimmedBookmark {
		entry.Bookmark = trimmedBookmark
		mutated = true
	}
	if mutated {
		entries[normalized] = entry
		if err := s.store.Save(repoRoot, entries); err != nil {
			return "", "", existedBefore, err
		}
	}

	// Step 5: builtin bootstrap (project rc loading, etc.). Idempotent
	// — runs on every reconcile because its body is cheap and may pick
	// up newly-added config.
	if err := s.runBuiltinBootstrap(repoRoot, entry.Path); err != nil {
		return "", "", existedBefore, err
	}

	// Step 6: post-start hooks (pnpm install and similar). Only on
	// first creation — these are expensive and shouldn't re-run on
	// every reopen. Failure rolls back the new workspace so a botched
	// install doesn't leave half-state behind.
	if !existedBefore && runHooks {
		if err := s.runPostWorkspaceStartHooks(repoRoot, normalized, entry.Path); err != nil {
			if cleanupErr := s.rollbackNewWorkspaceStart(repoRoot, normalized, entry.Path); cleanupErr != nil {
				return "", "", false, fmt.Errorf("%w (rollback failed: %v)", err, cleanupErr)
			}
			return "", "", false, err
		}
	}

	return normalized, entry.Path, existedBefore, nil
}

func (s *service) createWorkspace(name string, bookmark string, prompt string, runHooks bool) error {
	s.logf("▶️ Starting workspace create flow (name=%q, bookmark=%q)", strings.TrimSpace(name), strings.TrimSpace(bookmark))
	currentRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	repoRoot, sErr := s.jj.SourceRepoRoot()
	if sErr != nil || strings.TrimSpace(repoRoot) == "" {
		repoRoot = currentRoot
	}
	if err := s.guardRepoRoot(repoRoot); err != nil {
		return err
	}
	s.logf("✅ Resolved repo root: %s", repoRoot)

	if strings.TrimSpace(name) == "" && strings.TrimSpace(bookmark) != "" {
		name = bookmark
		s.logf("ℹ️ Using bookmark as default workspace name: %q", name)
	}

	normalized, err := s.resolveName(name)
	if err != nil {
		return err
	}
	s.logf("✅ Resolved workspace name: %q", normalized)

	s.logf("▶️ Checking whether workspace %q already exists", normalized)
	exists, err := s.jj.WorkspaceExists(normalized)
	if err != nil {
		return err
	}
	if exists {
		s.logf("ℹ️ Workspace %q already exists; opening existing workspace", normalized)
		if err := s.openByName(repoRoot, normalized); err != nil {
			return err
		}
		return s.trackBookmark(bookmark)
	}

	workspacePath := s.defaultWorkspacePath(repoRoot, normalized)
	s.logf("✅ Target workspace path: %s", workspacePath)
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return fmt.Errorf("create workspace parent dir: %w", err)
	}
	s.logf("▶️ Preparing workspace path")
	if err := s.prepareWorkspacePath(workspacePath); err != nil {
		return err
	}
	startRevision := "@"
	if strings.TrimSpace(bookmark) != "" {
		s.logf("▶️ Tracking bookmark %q before workspace creation", strings.TrimSpace(bookmark))
		if err := s.trackBookmark(bookmark); err != nil {
			return err
		}
		startRevision = strings.TrimSpace(bookmark)
	}
	s.logf("▶️ Creating jj workspace %q at revision %q", normalized, startRevision)
	if err := s.jj.AddWorkspace(normalized, workspacePath, startRevision); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return err
		}
		s.logf("⚠️ jj reported workspace already exists; retrying after cleanup")
		_ = s.jj.ForgetWorkspace(normalized)
		if prepErr := s.prepareWorkspacePath(workspacePath); prepErr != nil {
			return prepErr
		}
		if err2 := s.jj.AddWorkspace(normalized, workspacePath, startRevision); err2 != nil {
			existsNow, existsErr := s.jj.WorkspaceExists(normalized)
			if existsErr == nil && existsNow {
				s.logf("ℹ️ Workspace %q exists after retry; opening", normalized)
				if err := s.openByName(repoRoot, normalized); err != nil {
					return err
				}
				return s.trackBookmark(bookmark)
			}
			return err2
		}
	}

	s.logf("▶️ Recording workspace in awp state")
	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return err
	}
	entries[normalized] = Entry{Name: normalized, Path: workspacePath, Bookmark: strings.TrimSpace(bookmark)}
	if err := s.store.Save(repoRoot, entries); err != nil {
		return err
	}
	if err := s.runBuiltinBootstrap(repoRoot, workspacePath); err != nil {
		return err
	}
	if runHooks {
		s.logf("▶️ Running bootstrap hooks")
		if err := s.runPostWorkspaceStartHooks(repoRoot, normalized, workspacePath); err != nil {
			s.logf("⚠️ Bootstrap hooks failed; rolling back new workspace")
			if cleanupErr := s.rollbackNewWorkspaceStart(repoRoot, normalized, workspacePath); cleanupErr != nil {
				return fmt.Errorf("%w (rollback failed: %v)", err, cleanupErr)
			}
			return err
		}
	}
	s.logf("▶️ Ensuring tmux window %q", normalized)
	if err := s.ensureWindow(normalized, workspacePath); err != nil {
		return err
	}
	if err := s.maybeRunPrompt(normalized, prompt); err != nil {
		return err
	}
	s.logf("▶️ Switching to tmux window %q", normalized)
	return s.switchWhenReady(normalized)
}

func (s *service) List() ([]ListEntry, error) {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return nil, fmt.Errorf("not a jj repository: %w", err)
	}
	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return nil, err
	}
	entries, changed := s.canonicalizeEntries(repoRoot, entries)

	workspaceNames, err := s.jj.ListWorkspaceNames()
	if err != nil {
		return nil, err
	}
	workspaceSet := make(map[string]struct{}, len(workspaceNames))
	for _, name := range workspaceNames {
		workspaceSet[name] = struct{}{}
		if _, ok := entries[name]; !ok {
			entries[name] = Entry{Name: name, Path: s.defaultWorkspacePath(repoRoot, name)}
			changed = true
		}
	}
	for name := range entries {
		if _, ok := workspaceSet[name]; !ok {
			delete(entries, name)
			changed = true
		}
	}
	if changed {
		if err := s.store.Save(repoRoot, entries); err != nil {
			return nil, err
		}
	}

	current, _ := s.tmux.CurrentWindow()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make([]ListEntry, 0, len(entries))
	for _, name := range names {
		entry := entries[name]
		hasWindow, _ := s.tmux.WindowExists(name)
		windowName := ""
		if hasWindow {
			windowName = name
		}
		out = append(out, ListEntry{
			Name:         name,
			Path:         entry.Path,
			TmuxWindow:   windowName,
			Active:       current == name && hasWindow,
			SessionID:    entry.SessionID,
			SessionName:  entry.SessionName,
			ActivePrompt: entry.ActivePrompt,
			Status:       entry.Status,
			Unread:       entry.Unread,
		})
	}
	return out, nil
}

func (s *service) Info(name string) (InfoEntry, error) {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return InfoEntry{}, fmt.Errorf("not a jj repository: %w", err)
	}
	normalized, err := NormalizeName(name)
	if err != nil {
		return InfoEntry{}, err
	}

	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return InfoEntry{}, err
	}
	entries, changed := s.canonicalizeEntries(repoRoot, entries)
	if changed {
		if err := s.store.Save(repoRoot, entries); err != nil {
			return InfoEntry{}, err
		}
	}
	entry, managed := entries[normalized]
	if !managed {
		entry = Entry{Name: normalized, Path: s.defaultWorkspacePath(repoRoot, normalized)}
	}

	jjExists, err := s.jj.WorkspaceExists(normalized)
	if err != nil {
		return InfoEntry{}, err
	}
	if !managed && !jjExists {
		return InfoEntry{}, fmt.Errorf("workspace %q not found", normalized)
	}

	tmuxExists, err := s.tmux.WindowExists(normalized)
	if err != nil {
		return InfoEntry{}, err
	}
	current, _ := s.tmux.CurrentWindow()
	windowName := ""
	if tmuxExists {
		windowName = normalized
	}

	return InfoEntry{
		Name:         normalized,
		Path:         entry.Path,
		Managed:      managed,
		JJExists:     jjExists,
		TmuxWindow:   windowName,
		TmuxExists:   tmuxExists,
		ActiveWindow: tmuxExists && current == normalized,
	}, nil
}

func (s *service) Open(name string, bookmark string, prompt string, yes bool) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	if err := s.guardRepoRoot(repoRoot); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" && strings.TrimSpace(bookmark) != "" {
		name = bookmark
	}
	normalized, err := NormalizeName(name)
	if err != nil {
		return err
	}
	exists, err := s.jj.WorkspaceExists(normalized)
	if err != nil {
		return err
	}
	if !exists {
		if !yes {
			ok, err := charm.Confirm(s.in, s.out, fmt.Sprintf("Workspace %q does not exist. Create it?", normalized), false)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("open cancelled")
			}
		}
		return s.createWorkspace(normalized, bookmark, prompt, true)
	}
	return s.openByName(repoRoot, normalized)
}

func (s *service) ListAll() ([]CrossRepoEntry, error) {
	all, ok := s.store.(AllStore)
	if !ok {
		entries, err := s.List()
		if err != nil {
			return nil, err
		}
		repoRoot, _ := s.jj.RepoRoot()
		projectName := strings.TrimSpace(filepath.Base(filepath.Clean(repoRoot)))
		out := make([]CrossRepoEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, CrossRepoEntry{
				RepoRoot: repoRoot, ProjectName: projectName, Name: e.Name, Path: e.Path,
				SessionID: e.SessionID, SessionName: e.SessionName,
				ActivePrompt: e.ActivePrompt, Status: e.Status, Unread: e.Unread,
			})
		}
		return out, nil
	}
	repoMap, err := all.LoadAll()
	if err != nil {
		return nil, err
	}
	var out []CrossRepoEntry
	type repoRow struct {
		repo, project string
	}
	repos := make([]repoRow, 0, len(repoMap))
	for r := range repoMap {
		repos = append(repos, repoRow{repo: r, project: strings.TrimSpace(filepath.Base(filepath.Clean(r)))})
	}
	slices.SortFunc(repos, func(a, b repoRow) int {
		if a.project != b.project {
			if a.project < b.project {
				return -1
			}
			return 1
		}
		if a.repo < b.repo {
			return -1
		}
		if a.repo > b.repo {
			return 1
		}
		return 0
	})
	for _, r := range repos {
		entries := repoMap[r.repo]
		names := make([]string, 0, len(entries))
		for n := range entries {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			e := entries[n]
			out = append(out, CrossRepoEntry{
				RepoRoot: r.repo, ProjectName: r.project, Name: e.Name, Path: e.Path,
				SessionID: e.SessionID, SessionName: e.SessionName,
				ActivePrompt: e.ActivePrompt, Status: e.Status, Unread: e.Unread,
			})
		}
	}
	return out, nil
}

func (s *service) UpdatePrompt(workspaceName, prompt string) error {
	return s.mutateEntry(workspaceName, func(e *Entry) { e.ActivePrompt = prompt })
}

func (s *service) UpdateStatus(workspaceName, status string) error {
	return s.mutateEntry(workspaceName, func(e *Entry) {
		e.Status = status
		switch {
		case WantsAttention(status):
			e.Unread = true
		case IsExited(status):
			// The agent process is gone — there is nothing to respond to, so
			// a stale unread flag from a prior waiting/idle turn shouldn't
			// keep badging the row.
			e.Unread = false
		}
	})
}

func (s *service) MarkRead(workspaceName string) error {
	return s.mutateEntry(workspaceName, func(e *Entry) { e.Unread = false })
}

// WantsAttention reports whether a status transition into `status` should mark
// the workspace unread (badge it for the user). `working` does not — the agent
// is busy and the user has nothing to act on yet. `exited` does not either —
// the agent is gone, so there is no one to respond to and nothing actionable.
func WantsAttention(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "waiting", "idle":
		return true
	default:
		return false
	}
}

// IsExited reports whether a status means the agent process is gone. Exited
// workspaces never surface to the user (no unread badge, no status glyph) —
// there is nothing to act on.
func IsExited(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "exited")
}

func (s *service) ClearSession(workspaceName string) error {
	return s.mutateEntry(workspaceName, func(e *Entry) {
		e.SessionID = ""
		e.SessionName = ""
		e.AgentWindowID = ""
		e.AgentPaneID = ""
	})
}

func (s *service) mutateEntry(workspaceName string, fn func(*Entry)) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	normalized, err := NormalizeName(workspaceName)
	if err != nil {
		return err
	}
	updater, ok := s.store.(UpdaterStore)
	if ok {
		return updater.Update(repoRoot, func(entries map[string]Entry) map[string]Entry {
			entries, _ = s.canonicalizeEntries(repoRoot, entries)
			entry, exists := entries[normalized]
			if !exists {
				entry = Entry{Name: normalized, Path: s.defaultWorkspacePath(repoRoot, normalized)}
			}
			fn(&entry)
			entries[normalized] = entry
			return entries
		})
	}
	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return err
	}
	entries, _ = s.canonicalizeEntries(repoRoot, entries)
	entry, exists := entries[normalized]
	if !exists {
		entry = Entry{Name: normalized, Path: s.defaultWorkspacePath(repoRoot, normalized)}
	}
	fn(&entry)
	entries[normalized] = entry
	return s.store.Save(repoRoot, entries)
}

func (s *service) RecordSession(workspaceName, sessionID, sessionName string) error {
	return s.mutateEntry(workspaceName, func(e *Entry) {
		e.SessionID = sessionID
		e.SessionName = sessionName
		// Summon (or any record-session) implies the user is opening this
		// workspace, so clear the attention badge.
		e.Unread = false
	})
}

func (s *service) RecordBookmark(workspaceName, bookmark string) error {
	bookmark = strings.TrimSpace(bookmark)
	return s.mutateEntry(workspaceName, func(e *Entry) {
		e.Bookmark = bookmark
	})
}

// RecordPROverride pins (or clears, via prNumber == 0) the PR number
// for this workspace. Drives the deck `p s` chord's persistence and
// the awp review write-through. Kept as "RecordPROverride" for service
// interface stability — the underlying field is now Entry.PRNumber.
func (s *service) RecordPROverride(workspaceName string, prNumber int) error {
	if prNumber < 0 {
		prNumber = 0
	}
	return s.mutateEntry(workspaceName, func(e *Entry) {
		e.PRNumber = prNumber
	})
}

func (s *service) Rename(oldName, newName string) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	if err := s.guardRepoRoot(repoRoot); err != nil {
		return err
	}
	oldNormalized, err := NormalizeName(oldName)
	if err != nil {
		return err
	}
	newNormalized, err := NormalizeName(newName)
	if err != nil {
		return err
	}
	if oldNormalized == newNormalized {
		return errors.New("old and new workspace names are the same after normalization")
	}

	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return err
	}
	entries, changed := s.canonicalizeEntries(repoRoot, entries)
	if changed {
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
	}
	entry, ok := entries[oldNormalized]
	if !ok {
		return fmt.Errorf("workspace %q is not managed by awp", oldNormalized)
	}
	if _, exists := entries[newNormalized]; exists {
		return fmt.Errorf("workspace %q already exists", newNormalized)
	}

	if err := s.jj.RenameWorkspace(entry.Path, newNormalized); err != nil {
		return err
	}
	if hasWindow, _ := s.tmux.WindowExists(oldNormalized); hasWindow {
		if err := s.tmux.RenameWindow(oldNormalized, newNormalized); err != nil {
			return err
		}
	}

	delete(entries, oldNormalized)
	entry.Name = newNormalized
	entries[newNormalized] = entry
	return s.store.Save(repoRoot, entries)
}

// DeleteOptions controls Delete behavior.
type DeleteOptions struct {
	Force bool
	// DeferTmuxKill, when non-nil, is called with the tmux window name in
	// place of an immediate KillWindow. Callers can queue the kill to run
	// after their UI (e.g. a tmux popup) has closed.
	DeferTmuxKill func(window string)
}

func (s *service) Delete(name string, force bool) error {
	return s.DeleteWithOptions(name, DeleteOptions{Force: force})
}

// removeWorkspaceTree deletes a managed workspace directory with hard
// guards so a delete can never destroy the source repo or the source's
// real .awp (the default workspace's config dir). Returns true when the
// directory was actually removed.
//
// A workspace's .awp is normally a symlink into the shared source .awp.
// os.RemoveAll unlinks symlinks rather than following them, but we unlink
// it explicitly first as belt-and-suspenders: there must be no path by
// which removing a workspace can recurse into and wipe the source config.
// unlinkAwpSymlink removes <dir>/.awp only when it is a symlink (the
// shared-source-.awp link a workspace gets at bootstrap). This is called
// before recursively removing a workspace dir so os.RemoveAll can never
// follow the link into — and delete — the source repo's real .awp.
func unlinkAwpSymlink(dir string) {
	awp := filepath.Join(dir, ".awp")
	if fi, err := os.Lstat(awp); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(awp)
	}
}

func (s *service) removeWorkspaceTree(path, sourceRepo string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if !s.isUnderManagedWorkspaceBase(path) {
		s.logf("⏭️ Skipped workspace directory removal (%q outside managed base)", path)
		return false
	}
	if strings.TrimSpace(sourceRepo) != "" && sameDir(path, sourceRepo) {
		s.logf("🛑 Refusing to remove source repo %q as a workspace directory", path)
		return false
	}
	// Unlink a .awp symlink before the recursive remove so the walk can
	// never follow it into the shared source .awp.
	unlinkAwpSymlink(path)
	if err := os.RemoveAll(path); err != nil {
		s.logf("⚠️ Could not remove workspace directory %q: %v", path, err)
		return false
	}
	s.logf("✅ Removed workspace directory %q", path)
	return true
}

func (s *service) DeleteWithOptions(name string, opts DeleteOptions) error {
	force := opts.Force
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	if err := s.guardRepoRoot(repoRoot); err != nil {
		return err
	}
	// The source repo's .awp (the default workspace's real config dir) must
	// never be removed by a workspace delete. Resolve it so removeWorkspaceTree
	// can guard against it; fall back to repoRoot when not in a workspace.
	sourceRepo, sErr := s.jj.SourceRepoRoot()
	if sErr != nil || strings.TrimSpace(sourceRepo) == "" {
		sourceRepo = repoRoot
	}
	normalized, err := NormalizeName(name)
	if err != nil {
		return err
	}
	if IsProtected(normalized) {
		return fmt.Errorf("workspace %q cannot be removed", normalized)
	}

	if !force {
		ok, err := charm.Confirm(s.in, s.out, fmt.Sprintf("Delete workspace %q?", normalized), false)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("delete cancelled")
		}
	}

	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return err
	}
	entries, changed := s.canonicalizeEntries(repoRoot, entries)
	if changed {
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
	}
	entry, hasEntry := entries[normalized]
	revision, _ := s.jj.WorkspaceRevision(normalized)

	if err := s.jj.ForgetWorkspace(normalized); err != nil {
		return err
	}
	s.logf("✅ Forgot jj workspace %q", normalized)

	storedBookmark := ""
	if hasEntry {
		storedBookmark = strings.TrimSpace(entry.Bookmark)
	}
	forgottenBookmarks, err := s.cleanupWorkspaceBookmarks(normalized, storedBookmark, revision)
	if err != nil {
		return err
	}
	if forgottenBookmarks > 0 {
		s.logf("✅ Forgot %d matching bookmark(s)", forgottenBookmarks)
	} else {
		s.emit("⏭️ Skipped bookmark cleanup (no matching bookmarks)")
	}

	if hasEntry {
		delete(entries, normalized)
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
		s.logf("✅ Removed workspace state entry %q", normalized)
		if s.removeWorkspaceTree(entry.Path, sourceRepo) {
			pruneEmptyParents(entry.Path, s.managedWorkspaceBase())
		}
		if err := unmarkClaudeWorkspaceTrusted(entry.Path); err != nil {
			s.logf("⚠️ Could not remove ~/.claude.json trust entry: %v", err)
		} else {
			s.logf("✅ Removed ~/.claude.json trust entry (if present)")
		}
	} else {
		s.logf("⏭️ Skipped workspace state cleanup (%q not managed by awp)", normalized)
	}

	// Review workspaces stash their rendered prompt at
	// ~/.awp/review-prompts/<repo>/<name>.md (outside the workspace tree,
	// since the in-workspace .awp is a symlink to the shared source .awp).
	// Remove it here so prompts don't accumulate after the workspace is gone.
	s.removeReviewPrompt(repoRoot, normalized)

	abandoned, err := s.cleanupEmptyRevision(revision)
	if err != nil {
		return err
	}
	if abandoned {
		s.emit("✅ Abandoned empty workspace revision")
	} else {
		s.emit("⏭️ Skipped revision cleanup (revision not empty or unavailable)")
	}

	hasWindow, _ := s.tmux.WindowExists(normalized)
	if hasWindow {
		if opts.DeferTmuxKill != nil {
			opts.DeferTmuxKill(normalized)
			s.logf("⏳ Queued tmux window %q for removal after popup exits", normalized)
		} else {
			if err := s.tmux.KillWindow(normalized); err != nil {
				return err
			}
			s.logf("✅ Removed tmux window %q", normalized)
		}
	} else {
		s.logf("⏭️ Skipped tmux window removal (%q not present)", normalized)
	}

	s.logf("✅ Workspace %q removed", normalized)
	return nil
}

func (s *service) resolveName(name string) (string, error) {
	candidate := strings.TrimSpace(name)
	if candidate != "" {
		return NormalizeName(candidate)
	}
	line, err := charm.ReadLine(s.in, s.out, "Name")
	if err != nil {
		return "", err
	}
	return NormalizeName(line)
}

func (s *service) maybeRunPrompt(workspaceName, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	if err := s.UpdatePrompt(workspaceName, prompt); err != nil {
		return fmt.Errorf("persist prompt for %q: %w", workspaceName, err)
	}
	command := "pi " + shellQuote(prompt)
	s.logf("▶️ Starting agent prompt in tmux window %q", workspaceName)
	if err := s.tmux.SendCommand(workspaceName, command); err != nil {
		return fmt.Errorf("start prompt in tmux window %q: %w", workspaceName, err)
	}
	s.logf("✅ Agent prompt started")
	return nil
}

// Bootstrap re-runs built-in + user bootstrap hooks for the given workspace.
// If name is empty, resolves the current workspace from cwd (via jj).
// <root> in user hook commands resolves to the source repo root.
func (s *service) Bootstrap(name string) error {
	sourceRepo, err := s.jj.SourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve source repo root: %w", err)
	}
	currentRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("resolve current jj root: %w", err)
	}
	if err := s.guardRepoRoot(sourceRepo); err != nil {
		return err
	}

	workspaceName := strings.TrimSpace(name)
	var workspacePath string
	if workspaceName == "" {
		if sameDir(sourceRepo, currentRoot) {
			return fmt.Errorf("workspace bootstrap must run inside a secondary workspace or be given a name")
		}
		workspacePath = currentRoot
		entries, _ := s.store.Load(sourceRepo)
		for wsName, entry := range entries {
			absEntry, _ := filepath.Abs(entry.Path)
			if sameDir(absEntry, workspacePath) {
				workspaceName = wsName
				break
			}
		}
		if workspaceName == "" {
			workspaceName = filepath.Base(workspacePath)
		}
	} else {
		entries, err := s.store.Load(sourceRepo)
		if err != nil {
			return err
		}
		if entry, ok := entries[workspaceName]; ok {
			workspacePath = entry.Path
		} else {
			workspacePath = s.defaultWorkspacePath(sourceRepo, workspaceName)
		}
	}

	s.logf("▶️ Bootstrapping workspace %q (path=%s, root=%s)", workspaceName, workspacePath, sourceRepo)
	if err := s.runBuiltinBootstrap(sourceRepo, workspacePath); err != nil {
		return err
	}
	return s.runPostWorkspaceStartHooksWithRoot(sourceRepo, workspaceName, workspacePath, sourceRepo)
}

// BootstrapAll re-runs built-in + user bootstrap hooks for every tracked
// workspace in the current source repo. Errors from individual workspaces are
// logged and collected; the call returns a non-nil error if any failed.
func (s *service) BootstrapAll() error {
	sourceRepo, err := s.jj.SourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve source repo root: %w", err)
	}
	if err := s.guardRepoRoot(sourceRepo); err != nil {
		return err
	}
	entries, err := s.store.Load(sourceRepo)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		s.logf("ℹ️ No workspaces to bootstrap for %s", sourceRepo)
		return nil
	}
	s.logf("▶️ Bootstrapping %d workspace(s) for %s", len(names), sourceRepo)
	var failed []string
	var firstErr error
	for _, name := range names {
		entry := entries[name]
		s.logf("▶️ Bootstrapping workspace %q (path=%s)", name, entry.Path)
		if err := s.runBuiltinBootstrap(sourceRepo, entry.Path); err != nil {
			s.logf("❌ Built-in bootstrap failed for %q: %v", name, err)
			failed = append(failed, name)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.runPostWorkspaceStartHooksWithRoot(sourceRepo, name, entry.Path, sourceRepo); err != nil {
			s.logf("❌ Bootstrap hooks failed for %q: %v", name, err)
			failed = append(failed, name)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("bootstrap failed for %d workspace(s): %s: %w", len(failed), strings.Join(failed, ", "), firstErr)
	}
	s.logf("✅ Bootstrapped %d workspace(s)", len(names))
	return nil
}

// runBuiltinBootstrap copies files from the source repo that external tools
// (gh, git) expect to find inside a workspace. Runs before any user hooks.
// Silently skips pieces that don't exist in the source repo.
func (s *service) runBuiltinBootstrap(sourceRepo, workspacePath string) error {
	if strings.TrimSpace(sourceRepo) == "" || strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	if sameDir(sourceRepo, workspacePath) {
		return nil
	}
	s.logf("▶️ Running built-in bootstrap")

	gitSrc := filepath.Join(sourceRepo, ".git")
	if st, err := os.Stat(gitSrc); err == nil {
		var gitdirTarget string
		if st.IsDir() {
			gitdirTarget = gitSrc
		} else if data, readErr := os.ReadFile(gitSrc); readErr == nil {
			raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
			if !filepath.IsAbs(raw) {
				raw = filepath.Join(sourceRepo, raw)
			}
			gitdirTarget = filepath.Clean(raw)
		}
		if gitdirTarget != "" {
			gitDst := filepath.Join(workspacePath, ".git")
			_ = os.RemoveAll(gitDst)
			content := fmt.Sprintf("gitdir: %s\n", gitdirTarget)
			if err := os.WriteFile(gitDst, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write .git gitfile: %w", err)
			}
			s.logf("✅ Wrote .git gitfile → %s", gitdirTarget)
		}
	}

	awpSrc := filepath.Join(sourceRepo, ".awp")
	if st, err := os.Stat(awpSrc); err == nil && st.IsDir() {
		awpDst := filepath.Join(workspacePath, ".awp")
		// Never remove the source's own .awp. The sameDir(sourceRepo,
		// workspacePath) guard above already prevents this, but guard the
		// destructive RemoveAll directly too — wiping the source config is
		// not worth risking on a future refactor of that early return.
		if sameDir(awpDst, awpSrc) {
			s.logf("🛑 Refusing to relink .awp onto the source repo itself (%q)", awpSrc)
		} else {
			_ = os.RemoveAll(awpDst)
			if err := os.Symlink(awpSrc, awpDst); err != nil {
				return fmt.Errorf("symlink .awp: %w", err)
			}
			s.logf("✅ Linked .awp/ → %s", awpSrc)
		}
	}
	if err := markClaudeWorkspaceTrusted(workspacePath); err != nil {
		s.logf("⚠️ Could not mark workspace trusted in ~/.claude.json: %v", err)
	} else {
		s.logf("✅ Marked workspace trusted in ~/.claude.json")
	}
	return nil
}

func sameDir(a, b string) bool {
	aa, aerr := filepath.Abs(a)
	bb, berr := filepath.Abs(b)
	if aerr != nil || berr != nil {
		return false
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func (s *service) trackBookmark(bookmark string) error {
	bookmark = strings.TrimSpace(bookmark)
	if bookmark == "" {
		s.logf("⏭️ Skipping bookmark tracking (no bookmark provided)")
		return nil
	}
	s.logf("▶️ Tracking bookmark %q", bookmark)
	if err := s.jj.TrackBookmark(bookmark); err != nil {
		return err
	}
	s.logf("✅ Bookmark %q is now tracked", bookmark)
	return nil
}

func (s *service) openByName(repoRoot, name string) error {
	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return err
	}
	entries, changed := s.canonicalizeEntries(repoRoot, entries)
	if changed {
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
	}
	entry, ok := entries[name]
	if !ok {
		entry = Entry{Name: name, Path: s.defaultWorkspacePath(repoRoot, name)}
		entries[name] = entry
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
	}
	return s.ensureWindowAndSwitch(name, entry.Path)
}

func (s *service) ensureWindowAndSwitch(name, path string) error {
	if err := s.ensureWindow(name, path); err != nil {
		return err
	}
	return s.tmux.SwitchToWindow(name)
}

func (s *service) ensureWindow(name, path string) error {
	hasWindow, err := s.tmux.WindowExists(name)
	if err != nil {
		return err
	}
	if !hasWindow {
		if err := s.tmux.NewWindow(name, path); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) switchWhenReady(name string) error {
	if !charm.IsInteractiveReader(s.in) {
		return s.tmux.SwitchToWindow(name)
	}
	cue := fmt.Sprintf("✅ Setup complete for %q. Press any key to switch to tmux window...", name)
	if err := charm.PressAnyKey(s.in, s.out, cue); err != nil {
		return err
	}
	return s.tmux.SwitchToWindow(name)
}

func (s *service) runPostWorkspaceStartHooks(repoRoot, workspaceName, workspacePath string) error {
	return s.runPostWorkspaceStartHooksWithRoot(repoRoot, workspaceName, workspacePath, s.invocationDir)
}

func (s *service) runPostWorkspaceStartHooksWithRoot(repoRoot, workspaceName, workspacePath, rootOverride string) error {
	if s.hooks == nil {
		return nil
	}
	commands, err := s.hooks.PostWorkspaceStart(repoRoot)
	if err != nil {
		return err
	}
	if len(commands) == 0 {
		s.emit("⏭️ Skipped bootstrap hooks (none configured)")
		return nil
	}

	s.logf("✅ Running %d bootstrap hook(s)", len(commands))
	root := strings.TrimSpace(rootOverride)
	executed := 0
	for _, command := range commands {
		raw := strings.TrimSpace(command)
		if raw == "" {
			continue
		}
		executed++
		expanded := strings.ReplaceAll(raw, "<root>", root)
		s.logf("▶️ [%d/%d] %s", executed, len(commands), raw)
		cmd := "cd " + shellQuote(workspacePath) + " && " + expanded
		out, runErr := s.runShellCommand(cmd)
		output := strings.TrimSpace(out)
		if output == "" {
			s.emitOutput("(no output)")
		} else {
			for _, line := range strings.Split(output, "\n") {
				s.emitOutput(line)
			}
		}
		if runErr != nil {
			if output == "" {
				return fmt.Errorf("bootstrap hook failed for workspace %q: %q: %w", workspaceName, raw, runErr)
			}
			return fmt.Errorf("bootstrap hook failed for workspace %q: %q: %w\n%s", workspaceName, raw, runErr, output)
		}
	}
	s.emit("✅ Bootstrap hooks completed")
	return nil
}

func (s *service) rollbackNewWorkspaceStart(repoRoot, name, path string) error {
	var errs []error
	revision, _ := s.jj.WorkspaceRevision(name)

	if err := s.jj.ForgetWorkspace(name); err != nil {
		errs = append(errs, fmt.Errorf("forget workspace %q: %w", name, err))
	}

	hasWindow, err := s.tmux.WindowExists(name)
	if err != nil {
		errs = append(errs, fmt.Errorf("check tmux window %q: %w", name, err))
	} else if hasWindow {
		if err := s.tmux.KillWindow(name); err != nil {
			errs = append(errs, fmt.Errorf("kill tmux window %q: %w", name, err))
		}
	}

	entries, err := s.store.Load(repoRoot)
	if err != nil {
		errs = append(errs, fmt.Errorf("load workspace state: %w", err))
	} else {
		delete(entries, name)
		if err := s.store.Save(repoRoot, entries); err != nil {
			errs = append(errs, fmt.Errorf("save workspace state: %w", err))
		}
	}

	if s.isUnderManagedWorkspaceBase(path) {
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("remove workspace path %q: %w", path, err))
		}
	}
	if _, err := s.cleanupEmptyRevision(revision); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// removeReviewPrompt deletes a review workspace's rendered prompt file
// (~/.awp/review-prompts/<repo>/<name>.md) if present, then prunes the
// now-empty per-repo directory. Best-effort: only review workspaces ever
// have a prompt, so a missing file is the normal case for other workspaces
// and is not logged as an error.
func (s *service) removeReviewPrompt(repoRoot, name string) {
	path := config.ReviewPromptPath(repoRoot, name)
	if path == "" {
		return
	}
	if err := os.Remove(path); err == nil {
		s.logf("✅ Removed review prompt %q", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		s.logf("⚠️ Could not remove review prompt %q: %v", path, err)
		return
	}
	// Remove the per-repo dir if it's now empty; ignore the error when it
	// still holds other repos' prompts (os.Remove refuses non-empty dirs).
	_ = os.Remove(filepath.Dir(path))
}

func (s *service) cleanupWorkspaceBookmarks(workspaceName, storedBookmark, revision string) (int, error) {
	forgotten := 0
	seen := map[string]struct{}{}
	// Resolve trunk so we never delete it as a side effect of cleaning up
	// a workspace's bookmarks. Best-effort: if Trunk() fails (older jj, no
	// trunk() revset configured, etc.) we still skip a literal "main"
	// fallback to keep the historical default safe.
	protected := map[string]struct{}{"main": {}, "master": {}, "trunk": {}}
	if trunk, err := s.jj.Trunk(); err == nil {
		if t := strings.TrimSpace(trunk); t != "" {
			protected[t] = struct{}{}
		}
	}
	forget := func(name string) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		if _, dup := seen[name]; dup {
			return nil
		}
		seen[name] = struct{}{}
		if _, isProtected := protected[name]; isProtected {
			s.logf("⏭️ Skipped bookmark %q (trunk — protected from cleanup)", name)
			return nil
		}
		err := s.jj.ForgetBookmark(name)
		if err != nil && isStaleWorkingCopyError(err) {
			if updateErr := s.jj.UpdateStale(); updateErr != nil {
				return fmt.Errorf("forget bookmark %q: working copy stale and recovery failed: %w", name, updateErr)
			}
			err = s.jj.ForgetBookmark(name)
		}
		if err != nil {
			return fmt.Errorf("forget bookmark %q: %w", name, err)
		}
		forgotten++
		return nil
	}

	// jj bookmarks don't auto-advance with @, so the stored bookmark is
	// usually on an ancestor of the workspace's working-copy commit, not
	// on @ itself. Forget it by name directly — a revision scan would
	// only find it in the narrow case where the user never committed
	// past the original branch point.
	if err := forget(storedBookmark); err != nil {
		return forgotten, err
	}

	revision = strings.TrimSpace(revision)
	if revision == "" {
		return forgotten, nil
	}
	bookmarks, err := s.jj.BookmarksAtRevision(revision)
	if err != nil {
		return forgotten, fmt.Errorf("list bookmarks at revision %q: %w", revision, err)
	}
	for _, bookmark := range bookmarks {
		if !bookmarkMatchesWorkspace(workspaceName, storedBookmark, bookmark) {
			continue
		}
		if err := forget(bookmark); err != nil {
			return forgotten, err
		}
	}
	return forgotten, nil
}

func isStaleWorkingCopyError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "working copy is stale") || strings.Contains(text, "workspace update-stale")
}

func bookmarkMatchesWorkspace(workspaceName, storedBookmark, bookmark string) bool {
	trimmed := strings.TrimSpace(bookmark)
	if trimmed == "" {
		return false
	}
	if storedBookmark != "" && trimmed == storedBookmark {
		return true
	}
	normalized, err := NormalizeName(trimmed)
	if err != nil {
		return false
	}
	return normalized == workspaceName
}

func (s *service) cleanupEmptyRevision(revision string) (bool, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return false, nil
	}
	empty, err := s.jj.IsRevisionEmpty(revision)
	if err != nil {
		return false, fmt.Errorf("check whether revision %q is empty: %w", revision, err)
	}
	if !empty {
		return false, nil
	}
	if err := s.jj.AbandonRevision(revision); err != nil {
		return false, fmt.Errorf("abandon empty revision %q: %w", revision, err)
	}
	return true, nil
}

func (s *service) defaultWorkspacePath(repoRoot string, name string) string {
	if strings.TrimSpace(name) == "default" {
		return repoRoot
	}
	return filepath.Join(s.repoWorkspaceBase(repoRoot), name)
}

func (s *service) prepareWorkspacePath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("check workspace path %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path %q exists and is not a directory", path)
	}
	if !s.isUnderManagedWorkspaceBase(path) {
		return fmt.Errorf("workspace path %q exists outside managed base; refusing to modify", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove stale workspace path %q: %w", path, err)
	}
	return nil
}

// PruneOrphans walks the managed workspace base and removes every
// <repo>/<workspace> directory not referenced by any entry in the global
// state. Returns the list of paths that were (or would have been, if dryRun)
// removed. Empty parent dirs are also pruned.
func (s *service) PruneOrphans(dryRun bool) ([]string, error) {
	base := s.managedWorkspaceBase()
	info, err := os.Stat(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat managed workspace base: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("managed workspace base %q is not a directory", base)
	}

	known := map[string]struct{}{}
	if all, ok := s.store.(AllStore); ok {
		repoMap, err := all.LoadAll()
		if err != nil {
			return nil, err
		}
		for _, entries := range repoMap {
			for _, e := range entries {
				p := strings.TrimSpace(e.Path)
				if p == "" {
					continue
				}
				if abs, err := filepath.Abs(p); err == nil {
					known[filepath.Clean(abs)] = struct{}{}
				}
			}
		}
	}

	var removed []string
	repoDirs, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("read managed workspace base: %w", err)
	}
	for _, repoDir := range repoDirs {
		if !repoDir.IsDir() {
			continue
		}
		repoPath := filepath.Join(base, repoDir.Name())
		wsDirs, err := os.ReadDir(repoPath)
		if err != nil {
			continue
		}
		for _, wsDir := range wsDirs {
			if !wsDir.IsDir() {
				continue
			}
			wsPath := filepath.Join(repoPath, wsDir.Name())
			if _, ok := known[filepath.Clean(wsPath)]; ok {
				continue
			}
			removed = append(removed, wsPath)
			if !dryRun {
				unlinkAwpSymlink(wsPath)
				if err := os.RemoveAll(wsPath); err != nil {
					return removed, fmt.Errorf("remove %q: %w", wsPath, err)
				}
				// Drop the orphan's review prompt too, if any. The repo
				// and workspace dir names here are already normalized, so
				// they resolve to the same review-prompts path the review
				// flow wrote (see config.ReviewPromptPath).
				s.removeReviewPrompt(repoDir.Name(), wsDir.Name())
			}
		}
		if !dryRun {
			pruneEmptyParents(filepath.Join(repoPath, ".sentinel"), base)
		}
	}
	return removed, nil
}

// pruneEmptyParents removes the parent directory of removed (and any
// further-up empty ancestors) up to and excluding root. Best-effort.
func pruneEmptyParents(removed, root string) {
	if removed == "" || root == "" {
		return
	}
	dir := filepath.Dir(removed)
	for {
		cleaned := filepath.Clean(dir)
		if cleaned == "" || cleaned == "." || cleaned == filepath.Clean(root) {
			return
		}
		if !strings.HasPrefix(cleaned, filepath.Clean(root)+string(filepath.Separator)) {
			return
		}
		entries, err := os.ReadDir(cleaned)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(cleaned); err != nil {
			return
		}
		dir = filepath.Dir(cleaned)
	}
}

func (s *service) isUnderManagedWorkspaceBase(path string) bool {
	base := s.managedWorkspaceBase()
	return strings.HasPrefix(path, base+string(filepath.Separator)) || path == base
}

func (s *service) managedWorkspaceBase() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".awp", "workspaces")
	}
	return filepath.Join(home, ".awp", "workspaces")
}

func (s *service) repoWorkspaceBase(repoRoot string) string {
	repoName := strings.TrimSpace(filepath.Base(filepath.Clean(repoRoot)))
	if repoName == "" || repoName == "." || repoName == string(filepath.Separator) {
		repoName = "repo"
	}
	if normalized, err := NormalizeName(repoName); err == nil {
		repoName = normalized
	}
	return filepath.Join(s.managedWorkspaceBase(), repoName)
}

func (s *service) canonicalizeEntries(repoRoot string, entries map[string]Entry) (map[string]Entry, bool) {
	canonical := map[string]Entry{}
	changed := false

	for key, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = deriveName(key)
			changed = true
		}
		normalizedName, err := NormalizeName(name)
		if err != nil {
			changed = true
			continue
		}
		path := strings.TrimSpace(entry.Path)
		if path == "" {
			if looksLikePath(key) {
				path = key
			} else {
				path = s.defaultWorkspacePath(repoRoot, normalizedName)
			}
			changed = true
		}
		if normalizedName == "default" && path != repoRoot {
			path = repoRoot
			changed = true
		}
		// Canonicalization only normalizes the key/name/path — every other
		// field carries over verbatim. Copy the full entry and overwrite the
		// normalized fields so newly added Entry fields can't be silently
		// dropped here (Unread and PRNumber once were).
		canonicalEntry := entry
		canonicalEntry.Name = normalizedName
		canonicalEntry.Path = path
		canonical[normalizedName] = canonicalEntry
		if key != normalizedName || entry.Name != normalizedName || entry.Path != path {
			changed = true
		}
	}
	return canonical, changed
}

func deriveName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if looksLikePath(trimmed) {
		return filepath.Base(trimmed)
	}
	return trimmed
}

func looksLikePath(value string) bool {
	return strings.Contains(value, "/") || strings.Contains(value, string(filepath.Separator)) || strings.HasPrefix(value, "~")
}

func IsProtected(name string) bool {
	return strings.TrimSpace(name) == "default"
}

// IsHomeDir reports whether path resolves to the user's home directory.
// Returns false on resolution errors.
func IsHomeDir(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return false
	}
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return false
	}
	return filepath.Clean(abs) == filepath.Clean(home)
}

// errRepoIsHome guards against treating $HOME as an awp-managed repo, which
// would scatter workspace dirs and bookmarks all over the user's home.
var errRepoIsHome = errors.New("refusing to operate on $HOME as a repo (awp is not allowed at your home directory)")

func (s *service) guardRepoRoot(repoRoot string) error {
	if IsHomeDir(repoRoot) {
		return errRepoIsHome
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *service) runShellCommand(command string) (string, error) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell != "" {
		return s.runner.Run(context.Background(), "", shell, "-lc", command)
	}
	return s.runner.Run(context.Background(), "", "sh", "-c", command)
}

func (s *service) logf(format string, args ...any) {
	s.emit(fmt.Sprintf(format, args...))
}

func (s *service) emit(line string) {
	if s.out == nil {
		return
	}
	if charm.IsInteractiveWriter(s.out) {
		fmt.Fprintln(s.out, charm.RenderProgressLine(line))
		return
	}
	fmt.Fprintln(s.out, line)
}

func (s *service) emitOutput(line string) {
	if s.out == nil {
		return
	}
	if charm.IsInteractiveWriter(s.out) {
		fmt.Fprintln(s.out, charm.RenderProgressOutputLine(line))
		return
	}
	fmt.Fprintf(s.out, "   ↳ %s\n", line)
}
