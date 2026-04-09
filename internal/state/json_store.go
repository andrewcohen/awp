package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

func (s *JSONStore) Save(repoRoot string, entries map[string]workspace.Entry) error {
	normalizedRepoRoot, err := normalizeRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	state, err := s.readGlobalState()
	if err != nil {
		return err
	}
	state[normalizedRepoRoot] = cloneEntries(entries)
	return s.writeGlobalState(state)
}

func (s *JSONStore) readGlobalState() (globalState, error) {
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

func (s *JSONStore) writeGlobalState(state globalState) error {
	path, err := globalStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write workspace state: %w", err)
	}
	return nil
}

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
	return filepath.Clean(abs), nil
}

func cloneEntries(in map[string]workspace.Entry) map[string]workspace.Entry {
	out := map[string]workspace.Entry{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
