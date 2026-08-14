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
// Sidebar is whether the attention strip is up beside a pane. Not per workspace:
// the strip answers "what wants me", which is a question about the whole deck, so
// a per-workspace answer would make it appear and vanish as you moved between
// panes.
//
// No omitempty, unlike Scope. false is a choice here — you pressed the key to
// turn it off — and an omitted field is indistinguishable from one this build
// never wrote, so omitting it would make turning the strip off the one setting
// that does not stick.
// Split is where the divider sits in a split, as the left half's share of the
// width — a fraction rather than a column count, or the same preference would mean
// a different layout on a different terminal.
//
// omitempty, unlike Sidebar: zero is not a choice here, it is the absence of one.
// A left half of no width is not something a key can ask for (the divider clamps
// well before that), so "0" and "never set" are the same thing, and both mean the
// even split a fresh deck opens at.
type DeckPrefs struct {
	Scope   string  `json:"scope,omitempty"`
	Sidebar bool    `json:"sidebar"`
	Split   float64 `json:"split,omitempty"`
}

// The keys, named once. A save merges one key into the file's own object rather
// than marshalling this struct over it (see saveDeckPref), so the name travels as a
// string — and a typo would write a second key beside the real one, silently,
// visible only as a preference that stopped sticking.
//
// TestTheKeysAreTheStructsOwnTags walks the struct by reflection and checks each of
// these is a tag it really has, which is what makes the string safe to pass.
const (
	deckPrefScope   = "scope"
	deckPrefSidebar = "sidebar"
	deckPrefSplit   = "split"
)

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
	return saveDeckPref(deckPrefScope, scope)
}

// SaveDeckSidebar records whether the attention strip should be up next time.
//
// A no-op when unchanged, for the same reason SaveDeckScope is.
func SaveDeckSidebar(on bool) error {
	prefs, err := LoadDeckPrefs()
	if err != nil {
		return err
	}
	if prefs.Sidebar == on {
		return nil
	}
	return saveDeckPref(deckPrefSidebar, on)
}

// SaveDeckSplit records where the split's divider should sit next time, as the left
// half's share of the width.
//
// Not validated on the way in or out. splitCol already clamps the divider so neither
// half falls below a pane's minimum, so a fraction saved on a 400-column screen
// degrades to "as far left as this terminal allows" on a 100-column one rather than
// needing a range check that would have to guess the terminal.
//
// A no-op when unchanged, for the same reason SaveDeckScope is: the divider moves in
// 5% steps under a held-ish key, and a file rewrite per tap is a write the user
// cannot see the point of.
func SaveDeckSplit(frac float64) error {
	prefs, err := LoadDeckPrefs()
	if err != nil {
		return err
	}
	if prefs.Split == frac {
		return nil
	}
	return saveDeckPref(deckPrefSplit, frac)
}

// saveDeckPref writes one preference, leaving every other key in the file exactly
// as it found it — including keys this build has never heard of.
//
// That last part is the whole reason this is a merge into the file's own object
// rather than a read into DeckPrefs, a field assignment, and a marshal back. The
// struct is a *subset* of the file: json.Unmarshal drops what it has no field for,
// so the round trip through it deletes anything a newer build wrote. Two settings
// in, that is no longer hypothetical — an older binary cycling `P` would erase the
// sidebar flag, and nothing would say so, because the older binary does not know
// there was anything there to lose.
//
// The old code documented this guarantee and did not have it. So did its test,
// which asserted the known key was updated and never looked for the unknown one.
func saveDeckPref(key string, value any) error {
	raw, err := loadDeckPrefsObject()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode deck pref %s: %w", key, err)
	}
	raw[key] = encoded
	return writeDeckPrefs(raw)
}

// loadDeckPrefsObject reads the file as the object it is, with every value left
// undecoded. A missing file is an empty object, for the reason LoadDeckPrefs treats
// it as the zero value.
func loadDeckPrefsObject() (map[string]json.RawMessage, error) {
	path, err := deckPrefsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, fmt.Errorf("read deck prefs: %w", err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse deck prefs at %s: %w (delete the file to start over)", path, err)
	}
	return raw, nil
}

func writeDeckPrefs(prefs map[string]json.RawMessage) error {
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
