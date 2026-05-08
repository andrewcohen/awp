package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/andrewcohen/awp/internal/workspace"
)

type JSONStore struct{}

func NewJSONStore() *JSONStore {
	return &JSONStore{}
}

type globalState map[string]map[string]workspace.Entry

func (s *JSONStore) Load(repoRoot string) (map[string]workspace.Entry, error) {
	normalizedRepoRoot, err := normalizeRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}

	state, err := s.readGlobalState()
	if err != nil {
		return nil, err
	}
	if entries, ok := state[normalizedRepoRoot]; ok {
		return cloneEntries(entries), nil
	}

	legacyEntries, err := readLegacyRepoState(normalizedRepoRoot)
	if err != nil {
		return nil, err
	}
	if legacyEntries != nil {
		return legacyEntries, nil
	}
	return map[string]workspace.Entry{}, nil
}

// LoadAll returns all entries across repos keyed by repoRoot.
func (s *JSONStore) LoadAll() (map[string]map[string]workspace.Entry, error) {
	state, err := s.readGlobalState()
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]workspace.Entry{}
	for repo, entries := range state {
		out[repo] = cloneEntries(entries)
	}
	return out, nil
}

func (s *JSONStore) Save(repoRoot string, entries map[string]workspace.Entry) error {
	normalizedRepoRoot, err := normalizeRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return s.withLock(func() error {
		state, err := s.readGlobalStateLocked()
		if err != nil {
			return err
		}
		state[normalizedRepoRoot] = cloneEntries(entries)
		return s.writeGlobalStateLocked(state)
	})
}

// DeleteRepo removes the entire entry for repoRoot from the global
// state file. Used by project-level deletion to make a project
// disappear from the deck.
func (s *JSONStore) DeleteRepo(repoRoot string) error {
	normalizedRepoRoot, err := normalizeRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return s.withLock(func() error {
		state, err := s.readGlobalStateLocked()
		if err != nil {
			return err
		}
		if _, ok := state[normalizedRepoRoot]; !ok {
			return nil
		}
		delete(state, normalizedRepoRoot)
		return s.writeGlobalStateLocked(state)
	})
}

// Update atomically applies fn to the entries map for repoRoot. fn receives a
// mutable copy of the current entries; the returned map (or the same map after
// in-place mutation) is persisted. The whole read-modify-write sequence is
// guarded by an OS-level advisory lock so concurrent writers can't drop each
// other's changes.
func (s *JSONStore) Update(repoRoot string, fn func(map[string]workspace.Entry) map[string]workspace.Entry) error {
	normalizedRepoRoot, err := normalizeRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	return s.withLock(func() error {
		state, err := s.readGlobalStateLocked()
		if err != nil {
			return err
		}
		current := cloneEntries(state[normalizedRepoRoot])
		updated := fn(current)
		if updated == nil {
			updated = map[string]workspace.Entry{}
		}
		state[normalizedRepoRoot] = cloneEntries(updated)
		return s.writeGlobalStateLocked(state)
	})
}

func (s *JSONStore) readGlobalState() (globalState, error) {
	return s.readGlobalStateLocked()
}

func (s *JSONStore) readGlobalStateLocked() (globalState, error) {
	path, err := globalStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return globalState{}, nil
		}
		return nil, fmt.Errorf("read workspace state: %w", err)
	}
	var state globalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse workspace state: %w", err)
	}
	if state == nil {
		state = globalState{}
	}
	return state, nil
}

func (s *JSONStore) writeGlobalStateLocked(state globalState) error {
	path, err := globalStorePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".workspace-state.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write workspace state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync workspace state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close workspace state: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod workspace state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename workspace state: %w", err)
	}
	return nil
}

// withLock acquires an advisory exclusive lock on the state file (creating a
// sidecar lock file so we don't have to re-open the state file every read),
// runs fn, and releases. Times out after lockTimeout to keep agent hooks from
// stalling on a stuck holder.
func (s *JSONStore) withLock(fn func() error) error {
	path, err := globalStorePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open state lock: %w", err)
	}
	defer f.Close()

	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("flock state: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("flock state: timed out after %s", lockTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

const lockTimeout = 2 * time.Second

// GlobalStorePath returns the path of the global workspace state JSON file.
func GlobalStorePath() (string, error) { return globalStorePath() }

func globalStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".awp", "workspace-state.json"), nil
}

func legacyRepoStorePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".awp", "workspace-state.json")
}

func readLegacyRepoState(repoRoot string) (map[string]workspace.Entry, error) {
	data, err := os.ReadFile(legacyRepoStorePath(repoRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace state: %w", err)
	}
	var entries map[string]workspace.Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse workspace state: %w", err)
	}
	if entries == nil {
		entries = map[string]workspace.Entry{}
	}
	return entries, nil
}

func normalizeRepoRoot(repoRoot string) (string, error) {
	if repoRoot == "" {
		return "", errors.New("repo root is empty")
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	return canonicalizeRepoRoot(filepath.Clean(abs)), nil
}

// canonicalizeRepoRoot resolves a jj secondary workspace dir to its source repo root
// by reading `<path>/.jj/repo` pointer file. Returns input unchanged if no pointer.
func canonicalizeRepoRoot(path string) string {
	data, err := os.ReadFile(filepath.Join(path, ".jj", "repo"))
	if err != nil {
		return path
	}
	pointer := trimSpace(string(data))
	if pointer == "" {
		return path
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(path, ".jj", pointer)
	}
	pointer = filepath.Clean(pointer)
	if filepath.Base(pointer) == "repo" && filepath.Base(filepath.Dir(pointer)) == ".jj" {
		pointer = filepath.Dir(filepath.Dir(pointer))
	}
	if pointer == "" {
		return path
	}
	return pointer
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func cloneEntries(in map[string]workspace.Entry) map[string]workspace.Entry {
	out := map[string]workspace.Entry{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
