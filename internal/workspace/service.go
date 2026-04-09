package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type JJClient interface {
	RepoRoot() (string, error)
	WorkspaceExists(name string) (bool, error)
	ListWorkspaceNames() ([]string, error)
	AddWorkspace(name string, path string, revision string) error
	TrackBookmark(bookmarkName string) error
	RenameWorkspace(path string, newName string) error
	ForgetWorkspace(name string) error
	WorkspaceRevision(name string) (string, error)
	BookmarksAtRevision(revision string) ([]string, error)
	ForgetBookmark(name string) error
	IsRevisionEmpty(revision string) (bool, error)
	AbandonRevision(revision string) error
}

type TmuxClient interface {
	WindowExists(name string) (bool, error)
	NewWindow(name string, dir string) error
	SwitchToWindow(name string) error
	RenameWindow(oldName string, newName string) error
	KillWindow(name string) error
	CurrentWindow() (string, error)
}

type Store interface {
	Load(repoRoot string) (map[string]Entry, error)
	Save(repoRoot string, entries map[string]Entry) error
}

type HookConfigProvider interface {
	PostWorkspaceStart(repoRoot string) ([]string, error)
}

type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

type Entry struct {
	Name string
	Path string
}

type ListEntry struct {
	Name       string
	Path       string
	TmuxWindow string
	Active     bool
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
	Info(name string) (InfoEntry, error)
	Open(name string, bookmark string, yes bool) error
	Rename(oldName, newName string) error
	Delete(name string, force bool) error
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
	return string(out), err
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

func (s *service) createWorkspace(name string, bookmark string, runHooks bool) error {
	s.logf("▶️ Starting workspace create flow (name=%q, bookmark=%q)", strings.TrimSpace(name), strings.TrimSpace(bookmark))
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
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
	entries[normalized] = Entry{Name: normalized, Path: workspacePath}
	if err := s.store.Save(repoRoot, entries); err != nil {
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
			Name:       name,
			Path:       entry.Path,
			TmuxWindow: windowName,
			Active:     current == name && hasWindow,
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

func (s *service) Open(name string, bookmark string, yes bool) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
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
			ok, err := s.confirmf("Workspace %q does not exist. Create it? [y/N]: ", normalized)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("open cancelled")
			}
		}
		return s.createWorkspace(normalized, bookmark, true)
	}
	return s.openByName(repoRoot, normalized)
}

func (s *service) Rename(oldName, newName string) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
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

func (s *service) Delete(name string, force bool) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}
	normalized, err := NormalizeName(name)
	if err != nil {
		return err
	}

	if !force {
		fmt.Fprintf(s.out, "Delete workspace %q? [y/N]: ", normalized)
		reader := bufio.NewReader(s.in)
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		answer := strings.TrimSpace(strings.ToLower(line))
		if answer != "y" && answer != "yes" {
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
	fmt.Fprintf(s.out, "✅ Forgot jj workspace %q\n", normalized)

	forgottenBookmarks, err := s.cleanupWorkspaceBookmarks(normalized, revision)
	if err != nil {
		return err
	}
	if forgottenBookmarks > 0 {
		fmt.Fprintf(s.out, "✅ Forgot %d matching bookmark(s)\n", forgottenBookmarks)
	} else {
		fmt.Fprintln(s.out, "⏭️ Skipped bookmark cleanup (no matching bookmarks)")
	}

	if hasEntry {
		delete(entries, normalized)
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "✅ Removed workspace state entry %q\n", normalized)
		managedBase := s.managedWorkspaceBase()
		if strings.HasPrefix(entry.Path, managedBase+string(filepath.Separator)) || entry.Path == managedBase {
			_ = os.RemoveAll(entry.Path)
			fmt.Fprintf(s.out, "✅ Removed workspace directory %q\n", entry.Path)
		} else {
			fmt.Fprintf(s.out, "⏭️ Skipped workspace directory removal (%q outside managed base)\n", entry.Path)
		}
	} else {
		fmt.Fprintf(s.out, "⏭️ Skipped workspace state cleanup (%q not managed by awp)\n", normalized)
	}

	abandoned, err := s.cleanupEmptyRevision(revision)
	if err != nil {
		return err
	}
	if abandoned {
		fmt.Fprintln(s.out, "✅ Abandoned empty workspace revision")
	} else {
		fmt.Fprintln(s.out, "⏭️ Skipped revision cleanup (revision not empty or unavailable)")
	}

	hasWindow, _ := s.tmux.WindowExists(normalized)
	if hasWindow {
		if err := s.tmux.KillWindow(normalized); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "✅ Removed tmux window %q\n", normalized)
	} else {
		fmt.Fprintf(s.out, "⏭️ Skipped tmux window removal (%q not present)\n", normalized)
	}

	fmt.Fprintf(s.out, "✅ Workspace %q removed\n", normalized)
	return nil
}

func (s *service) resolveName(name string) (string, error) {
	candidate := strings.TrimSpace(name)
	if candidate != "" {
		return NormalizeName(candidate)
	}
	fmt.Fprint(s.out, "Name: ")
	reader := bufio.NewReader(s.in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return NormalizeName(strings.TrimSpace(line))
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

func (s *service) confirmf(prompt string, args ...any) (bool, error) {
	fmt.Fprintf(s.out, prompt, args...)
	reader := bufio.NewReader(s.in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes", nil
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
	if !isInteractiveReader(s.in) {
		return s.tmux.SwitchToWindow(name)
	}
	fmt.Fprintf(s.out, "✅ Setup complete for %q. Press any key to switch to tmux window...", name)
	reader := bufio.NewReader(s.in)
	_, err := reader.ReadByte()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	_, _ = fmt.Fprintln(s.out)
	return s.tmux.SwitchToWindow(name)
}

func (s *service) runPostWorkspaceStartHooks(repoRoot, workspaceName, workspacePath string) error {
	if s.hooks == nil {
		return nil
	}
	commands, err := s.hooks.PostWorkspaceStart(repoRoot)
	if err != nil {
		return err
	}
	if len(commands) == 0 {
		fmt.Fprintln(s.out, "⏭️ Skipped bootstrap hooks (none configured)")
		return nil
	}

	fmt.Fprintf(s.out, "✅ Running %d bootstrap hook(s)\n", len(commands))
	root := strings.TrimSpace(s.invocationDir)
	executed := 0
	for _, command := range commands {
		raw := strings.TrimSpace(command)
		if raw == "" {
			continue
		}
		executed++
		expanded := strings.ReplaceAll(raw, "<root>", root)
		fmt.Fprintf(s.out, "▶️ [%d/%d] %s\n", executed, len(commands), raw)
		cmd := "cd " + shellQuote(workspacePath) + " && " + expanded
		out, runErr := s.runner.Run(context.Background(), "", "sh", "-c", cmd)
		output := strings.TrimSpace(out)
		if output == "" {
			fmt.Fprintln(s.out, "   ↳ (no output)")
		} else {
			for _, line := range strings.Split(output, "\n") {
				fmt.Fprintf(s.out, "   ↳ %s\n", line)
			}
		}
		if runErr != nil {
			if output == "" {
				return fmt.Errorf("bootstrap hook failed for workspace %q: %q: %w", workspaceName, raw, runErr)
			}
			return fmt.Errorf("bootstrap hook failed for workspace %q: %q: %w\n%s", workspaceName, raw, runErr, output)
		}
	}
	fmt.Fprintln(s.out, "✅ Bootstrap hooks completed")
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

func (s *service) cleanupWorkspaceBookmarks(workspaceName, revision string) (int, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return 0, nil
	}
	bookmarks, err := s.jj.BookmarksAtRevision(revision)
	if err != nil {
		return 0, fmt.Errorf("list bookmarks at revision %q: %w", revision, err)
	}
	forgotten := 0
	for _, bookmark := range bookmarks {
		if !bookmarkMatchesWorkspace(workspaceName, bookmark) {
			continue
		}
		if err := s.jj.ForgetBookmark(bookmark); err != nil {
			return forgotten, fmt.Errorf("forget bookmark %q: %w", bookmark, err)
		}
		forgotten++
	}
	return forgotten, nil
}

func bookmarkMatchesWorkspace(workspaceName, bookmark string) bool {
	if strings.TrimSpace(bookmark) == "" {
		return false
	}
	normalized, err := NormalizeName(bookmark)
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
		canonical[normalizedName] = Entry{Name: normalizedName, Path: path}
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

func isInteractiveReader(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *service) logf(format string, args ...any) {
	if s.out == nil {
		return
	}
	fmt.Fprintf(s.out, format+"\n", args...)
}
