package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Deck preferences are the choices you make with a key and expect to still be
// true tomorrow, stored globally (~/.awp/deck-prefs.json) rather than per repo:
// the deck's merged view spans every project, so which slice of it you are
// looking through is a property of the deck rather than of any one workspace.
//
// A separate file from workspace-state.json for the same reason pin-group
// aliases are: nothing here belongs to a workspace, and a state file that mixes
// "what the deck knows about your repos" with "how you like it to open" cannot be
// deleted or reasoned about as one thing.

// DeckPrefs is what the deck remembers between runs.
//
// Scope is the scope name as `--scope` spells it (deckdata.ParseScope reads it
// back), not the enum's number: the numbers are declaration order, so a new
// scope inserted in the middle would silently re-point every saved preference at
// a different slice.
type DeckPrefs struct {
	Scope string `json:"scope,omitempty"`
}

// DeckPrefsPath returns the path of the global deck-preferences file.
func DeckPrefsPath() (string, error) { return deckPrefsPath() }

func deckPrefsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".awp", "deck-prefs.json"), nil
}

// LoadDeckPrefs reads the saved preferences. A missing file yields the zero
// value, not an error — a deck that has never been told anything has no
// preferences, which is a normal state rather than a problem to report.
//
// A file that will not parse *is* an error. It was written by this program, so
// unparseable means something else has been writing to it, and quietly starting
// from defaults would erase whatever that was on the next save.
func LoadDeckPrefs() (DeckPrefs, error) {
	path, err := deckPrefsPath()
	if err != nil {
		return DeckPrefs{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DeckPrefs{}, nil
		}
		return DeckPrefs{}, fmt.Errorf("read deck prefs: %w", err)
	}
	var prefs DeckPrefs
	if err := json.Unmarshal(data, &prefs); err != nil {
		return DeckPrefs{}, fmt.Errorf("parse deck prefs at %s: %w (delete the file to start over)", path, err)
	}
	return prefs, nil
}

// SaveDeckScope records the scope the deck should open in next time.
//
// Read-modify-write rather than a whole-struct overwrite, so a preference this
// build does not know about survives being saved by it — the alternative is that
// an older binary silently drops a newer one's settings.
func SaveDeckScope(scope string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return errors.New("deck prefs: scope name is empty")
	}
	prefs, err := LoadDeckPrefs()
	if err != nil {
		return err
	}
	if prefs.Scope == scope {
		// Nothing to write. Cycling `P` all the way round lands back where it
		// started, and a write per keypress for a value that did not change is a
		// file rewrite the user cannot see the point of.
		return nil
	}
	prefs.Scope = scope
	return writeDeckPrefs(prefs)
}

func writeDeckPrefs(prefs DeckPrefs) error {
	path, err := deckPrefsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode deck prefs: %w", err)
	}
	// Written the way every other file under ~/.awp is: to a temp file in the
	// same directory and renamed over, so a deck killed mid-write leaves the old
	// preferences rather than half of the new ones.
	tmp, err := os.CreateTemp(dir, ".deck-prefs.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp deck prefs file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write deck prefs: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync deck prefs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close deck prefs: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod deck prefs: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename deck prefs: %w", err)
	}
	return nil
}
