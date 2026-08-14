package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	// The known key is updated…
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Scope != "attention" {
		t.Errorf("the scope is %q, want attention", prefs.Scope)
	}
	// …and the unknown one is still on disk, which is the half this test was
	// written for and did not check. It read the file, logged it, and asserted
	// nothing about it — so it passed for as long as the guarantee was false.
	raw := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("the saved file does not parse: %v\n%s", err, data)
	}
	got, ok := raw["somethingNewer"]
	if !ok {
		t.Fatalf("saving the scope erased a key this build does not know:\n%s", data)
	}
	if string(got) != "42" {
		t.Errorf("the unknown key came back as %s, want 42", got)
	}
}

// TestAnUnknownPreferenceSurvivesTheOtherSaveToo. Both savers go through one merge,
// and this is what says so — a second one written the old way would pass every test
// above.
func TestAnUnknownPreferenceSurvivesTheOtherSaveToo(t *testing.T) {
	home := deckPrefsHome(t)
	path := filepath.Join(home, ".awp", "deck-prefs.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"sidebar":false,"somethingNewer":"kept"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveDeckSidebar(true); err != nil {
		t.Fatalf("saving the sidebar over a file with an unknown key: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("the saved file does not parse: %v\n%s", err, data)
	}
	if _, ok := raw["somethingNewer"]; !ok {
		t.Fatalf("saving the sidebar erased a key this build does not know:\n%s", data)
	}
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.Sidebar {
		t.Error("the sidebar did not come back on")
	}
}

// TestTheKeysAreTheStructsOwnTags. The merge names its key as a string, so a typo
// would write a second key beside the real one and the preference would simply stop
// sticking — nothing fails, and the file even looks plausible.
//
// Walked by reflection rather than listed, in the spirit of
// internal/github/dir_test.go: the point is that no key can be spelled somewhere
// the struct does not have a field for.
func TestTheKeysAreTheStructsOwnTags(t *testing.T) {
	tags := map[string]bool{}
	fields := reflect.TypeOf(DeckPrefs{})
	for i := range fields.NumField() {
		name, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		tags[name] = true
	}
	for _, key := range []string{deckPrefScope, deckPrefSidebar, deckPrefSplit} {
		if !tags[key] {
			t.Errorf("%q is saved but DeckPrefs has no field tagged with it, so loading it back reads nothing", key)
		}
	}
	// And every field has a key, or a setting can be written by the struct and
	// never by a save.
	if len(tags) != 3 {
		t.Errorf("DeckPrefs has %d fields and this test knows about 3 keys; a new field needs a deckPref* constant", len(tags))
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

// TestTheSidebarSurvivesTheProcess, in both directions. Off has to round-trip as
// deliberately as on: it is a key you pressed, and if it were stored with
// omitempty a deck told to hide the strip would come back with it up.
func TestTheSidebarSurvivesTheProcess(t *testing.T) {
	deckPrefsHome(t)
	for _, want := range []bool{true, false} {
		if err := SaveDeckSidebar(want); err != nil {
			t.Fatalf("saving sidebar=%v: %v", want, err)
		}
		prefs, err := LoadDeckPrefs()
		if err != nil {
			t.Fatalf("loading sidebar=%v back: %v", want, err)
		}
		if prefs.Sidebar != want {
			t.Errorf("saved sidebar=%v, loaded %v", want, prefs.Sidebar)
		}
	}
}

// TestTheSidebarAndTheScopeDoNotOverwriteEachOther. Both are read-modify-write
// over one file, so saving either has to leave the other alone — the failure
// otherwise is that changing the scope silently turns the strip off.
func TestTheSidebarAndTheScopeDoNotOverwriteEachOther(t *testing.T) {
	deckPrefsHome(t)
	if err := SaveDeckSidebar(true); err != nil {
		t.Fatal(err)
	}
	if err := SaveDeckScope("inbox"); err != nil {
		t.Fatal(err)
	}
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.Sidebar {
		t.Error("saving the scope turned the sidebar off")
	}
	if prefs.Scope != "inbox" {
		t.Errorf("the scope is %q, want inbox", prefs.Scope)
	}
}

// TestTheSplitDividerSurvivesTheProcess. Stored as a fraction, so the same
// preference means the same layout on a terminal of another width.
func TestTheSplitDividerSurvivesTheProcess(t *testing.T) {
	deckPrefsHome(t)
	if err := SaveDeckSplit(0.62); err != nil {
		t.Fatalf("saving the divider: %v", err)
	}
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Split != 0.62 {
		t.Errorf("the divider came back at %v, want 0.62", prefs.Split)
	}
}

// TestTheThreePreferencesDoNotOverwriteEachOther. Three read-modify-writes over one
// file: saving any of them has to leave the other two alone.
func TestTheThreePreferencesDoNotOverwriteEachOther(t *testing.T) {
	deckPrefsHome(t)
	if err := SaveDeckSidebar(true); err != nil {
		t.Fatal(err)
	}
	if err := SaveDeckSplit(0.7); err != nil {
		t.Fatal(err)
	}
	if err := SaveDeckScope("inbox"); err != nil {
		t.Fatal(err)
	}
	prefs, err := LoadDeckPrefs()
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.Sidebar {
		t.Error("a later save turned the sidebar off")
	}
	if prefs.Split != 0.7 {
		t.Errorf("the divider is %v, want 0.7", prefs.Split)
	}
	if prefs.Scope != "inbox" {
		t.Errorf("the scope is %q, want inbox", prefs.Scope)
	}
}
