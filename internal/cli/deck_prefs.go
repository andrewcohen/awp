package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/andrewcohen/awp/internal/deckui"
)

type deckPrefs struct {
	Scope string `json:"scope,omitempty"`
}

func deckPrefsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".awp", "deck-prefs.json")
}

func loadDeckScope() deckui.Scope {
	path := deckPrefsPath()
	if path == "" {
		return deckui.ScopeCurrent
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return deckui.ScopeCurrent
	}
	var p deckPrefs
	if err := json.Unmarshal(data, &p); err != nil {
		return deckui.ScopeCurrent
	}
	return deckui.ParseScope(p.Scope)
}

func saveDeckScope(s deckui.Scope) {
	path := deckPrefsPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(deckPrefs{Scope: s.String()}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
