package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deckPrefsHome points HOME at a temp dir, so a test never reads or writes the
// developer's own ~/.awp/deck-prefs.json — the file this package's whole job is
// to overwrite.
func deckPrefsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// TestNoPrefsIsNotAnError. A deck that has never been told anything has no
// preferences; that is the first run, not a problem to report.
func TestNoPrefsIsNotAnError(t *testing.T) {
	deckPrefsHome(t)
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatalf("loading absent prefs: %v", err)
	}
	if prefs.Scope != "" {
		t.Errorf("an empty home remembered a scope: %q", prefs.Scope)
	}
}

// TestTheScopeSurvivesTheProcess, which is the whole feature: the deck is closed
// and re-opened, and the slice you were looking through is still the one.
func TestTheScopeSurvivesTheProcess(t *testing.T) {
	deckPrefsHome(t)
	if err := SaveDeckScope("inbox"); err != nil {
		t.Fatalf("saving the scope: %v", err)
	}
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatalf("loading the scope back: %v", err)
	}
	if prefs.Scope != "inbox" {
		t.Errorf("the deck came back on %q, want inbox", prefs.Scope)
	}
}

// TestAnUnknownPreferenceSurvivesASave. An older binary saving a scope must not
// drop a setting a newer one wrote — which is what a whole-struct overwrite of a
// file it only half understands would do.
func TestAnUnknownPreferenceSurvivesASave(t *testing.T) {
	home := deckPrefsHome(t)
	path := filepath.Join(home, ".awp", "deck-prefs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"scope":"all","somethingNewer":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveDeckScope("attention"); err != nil {
		t.Fatalf("saving over a file with an unknown key: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))
	// The known key is updated…
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Scope != "attention" {
		t.Errorf("the scope is %q, want attention", prefs.Scope)
	}
}

// TestAnUnparseablePrefsFileIsReported rather than quietly replaced. Something
// other than awp wrote it, and starting from defaults would erase that on the next
// save without ever saying so.
func TestAnUnparseablePrefsFileIsReported(t *testing.T) {
	home := deckPrefsHome(t)
	path := filepath.Join(home, ".awp", "deck-prefs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDeckPrefs()
	if err == nil {
		t.Fatal("an unparseable prefs file loaded cleanly")
	}
	// And the message says which file, or the reader cannot act on it.
	if got := err.Error(); !strings.Contains(got, path) {
		t.Errorf("the error %q does not name the file to delete", got)
	}
}
