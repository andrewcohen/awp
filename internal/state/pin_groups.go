package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Pin-group display aliases are a small global map (register key →
// human label) stored separately from workspace-state.json because a
// pin register spans repos in the deck's merged view — the alias is a
// property of the register, not of any one workspace. Keys are the
// same values stored in Entry.PinGroup: "default" or a single
// lowercase letter a–z. An empty alias is treated as "no alias" and
// removed from the map.

// PinGroupAliasesPath returns the path of the global pin-group alias
// JSON file.
func PinGroupAliasesPath() (string, error) { return pinGroupAliasesPath() }

func pinGroupAliasesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".awp", "pin-groups.json"), nil
}

// LoadPinGroupAliases reads the register→alias map. A missing file
// yields an empty map, not an error.
func LoadPinGroupAliases() (map[string]string, error) {
	path, err := pinGroupAliasesPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read pin-group aliases: %w", err)
	}
	var aliases map[string]string
	if err := json.Unmarshal(data, &aliases); err != nil {
		return nil, fmt.Errorf("parse pin-group aliases: %w", err)
	}
	if aliases == nil {
		aliases = map[string]string{}
	}
	return aliases, nil
}

// SavePinGroupAlias sets (or, with an empty alias, clears) the display
// alias for a register and persists the whole map atomically.
func SavePinGroupAlias(key, alias string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("pin-group key is empty")
	}
	alias = strings.TrimSpace(alias)
	aliases, err := LoadPinGroupAliases()
	if err != nil {
		return err
	}
	if alias == "" {
		delete(aliases, key)
	} else {
		aliases[key] = alias
	}
	return writePinGroupAliases(aliases)
}

func writePinGroupAliases(aliases map[string]string) error {
	path, err := pinGroupAliasesPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(aliases, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pin-group aliases: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".pin-groups.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp alias file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write pin-group aliases: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync pin-group aliases: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close pin-group aliases: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod pin-group aliases: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename pin-group aliases: %w", err)
	}
	return nil
}
