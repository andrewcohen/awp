package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type JJClient interface {
	RepoRoot() (string, error)
	WorkspaceExists(name string) (bool, error)
	ListWorkspaceNames() ([]string, error)
	AddWorkspace(name string, path string, revision string) error
	SetBookmark(bookmarkName string, workspaceName string) error
	RenameWorkspace(path string, newName string) error
	ForgetWorkspace(name string) error
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
	Start(name string, bookmark string) error
	List() ([]ListEntry, error)
	Info(name string) (InfoEntry, error)
	Open(name string, bookmark string) error
	Rename(oldName, newName string) error
	Delete(name string, force bool) error
}

type Dependencies struct {
	JJ    JJClient
	Tmux  TmuxClient
	Store Store
	Input io.Reader
	Out   io.Writer
}

type service struct {
	jj    JJClient
	tmux  TmuxClient
	store Store
	in    io.Reader
	out   io.Writer
}

func NewService(deps Dependencies) Service {
	in := deps.Input
	if in == nil {
		in = os.Stdin
	}
	out := deps.Out
	if out == nil {
		out = os.Stdout
	}
	return &service{
		jj:    deps.JJ,
		tmux:  deps.Tmux,
		store: deps.Store,
		in:    in,
		out:   out,
	}
}

func (s *service) Start(name string, bookmark string) error {
	repoRoot, err := s.jj.RepoRoot()
	if err != nil {
		return fmt.Errorf("not a jj repository: %w", err)
	}

	if strings.TrimSpace(name) == "" && strings.TrimSpace(bookmark) != "" {
		name = bookmark
	}

	normalized, err := s.resolveName(name)
	if err != nil {
		return err
	}

	exists, err := s.jj.WorkspaceExists(normalized)
	if err != nil {
		return err
	}
	if exists {
		if err := s.openByName(repoRoot, normalized); err != nil {
			return err
		}
		return s.setBookmark(bookmark, normalized)
	}

	workspacePath := s.defaultWorkspacePath(repoRoot, normalized)
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		return fmt.Errorf("create workspace parent dir: %w", err)
	}
	if err := s.prepareWorkspacePath(workspacePath); err != nil {
		return err
	}
	startRevision := "@"
	if strings.TrimSpace(bookmark) != "" {
		startRevision = strings.TrimSpace(bookmark)
	}
	if err := s.jj.AddWorkspace(normalized, workspacePath, startRevision); err != nil {
		return err
	}

	entries, err := s.store.Load(repoRoot)
	if err != nil {
		return err
	}
	entries[normalized] = Entry{Name: normalized, Path: workspacePath}
	if err := s.store.Save(repoRoot, entries); err != nil {
		return err
	}
	if err := s.ensureWindowAndSwitch(normalized, workspacePath); err != nil {
		return err
	}
	return s.setBookmark(bookmark, normalized)
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

func (s *service) Open(name string, bookmark string) error {
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
		if strings.TrimSpace(bookmark) != "" {
			return s.Start(normalized, bookmark)
		}
		ok, err := s.confirmf("Workspace %q does not exist. Start it? [y/N]: ", normalized)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("open cancelled")
		}
		return s.Start(normalized, "")
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

	if err := s.jj.ForgetWorkspace(normalized); err != nil {
		return err
	}
	if hasWindow, _ := s.tmux.WindowExists(normalized); hasWindow {
		if err := s.tmux.KillWindow(normalized); err != nil {
			return err
		}
	}

	if hasEntry {
		delete(entries, normalized)
		if err := s.store.Save(repoRoot, entries); err != nil {
			return err
		}
		managedBase := s.managedWorkspaceBase()
		if strings.HasPrefix(entry.Path, managedBase+string(filepath.Separator)) || entry.Path == managedBase {
			_ = os.RemoveAll(entry.Path)
		}
	}
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

func (s *service) setBookmark(bookmark string, workspaceName string) error {
	bookmark = strings.TrimSpace(bookmark)
	if bookmark == "" {
		return nil
	}
	if err := s.jj.SetBookmark(bookmark, workspaceName); err != nil {
		return err
	}
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
	hasWindow, err := s.tmux.WindowExists(name)
	if err != nil {
		return err
	}
	if !hasWindow {
		if err := s.tmux.NewWindow(name, path); err != nil {
			return err
		}
	}
	return s.tmux.SwitchToWindow(name)
}

func (s *service) defaultWorkspacePath(_ string, name string) string {
	return filepath.Join(s.managedWorkspaceBase(), name)
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
