package deckui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyRunes presses a single-rune key (letters, including shifted
// uppercase, arrive as KeyRunes with one rune).
func keyRunes(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func pinnedModel() Model {
	return New([]Item{
		{ProjectName: "alpha", WorkspaceName: "main"},
		{ProjectName: "beta", WorkspaceName: "dev", PinGroup: "b"},
		{ProjectName: "gamma", WorkspaceName: "hot", PinGroup: "a"},
		{ProjectName: "gamma", WorkspaceName: "feat", PinGroup: "default"},
	}, nil)
}

func TestItemsFloatsPinnedFirstInRegisterOrder(t *testing.T) {
	m := pinnedModel()
	items := m.items()
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	// default register first, then a, then b, then the unpinned row.
	wantGroups := []string{"default", "a", "b", ""}
	for i, want := range wantGroups {
		if items[i].PinGroup != want {
			t.Fatalf("item %d: want PinGroup %q, got %q (ws=%s)", i, want, items[i].PinGroup, items[i].WorkspaceName)
		}
	}
	if items[3].WorkspaceName != "main" {
		t.Fatalf("expected unpinned row last, got %q", items[3].WorkspaceName)
	}
}

func TestBodyRowsEmitsPinnedSections(t *testing.T) {
	m := pinnedModel()
	items := m.items()
	rows := m.bodyRows(items)

	var pinHeaders []string
	for _, r := range rows {
		if r.kind == deckRowPinHeader {
			pinHeaders = append(pinHeaders, r.project)
		}
	}
	want := []string{"default", "a", "b"}
	if len(pinHeaders) != len(want) {
		t.Fatalf("want pin headers %v, got %v", want, pinHeaders)
	}
	for i := range want {
		if pinHeaders[i] != want[i] {
			t.Fatalf("pin header %d: want %q got %q", i, want[i], pinHeaders[i])
		}
	}
	// The unpinned "alpha" project still gets a normal project header.
	sawAlpha := false
	for _, r := range rows {
		if r.kind == deckRowHeader && r.project == "alpha" {
			sawAlpha = true
		}
	}
	if !sawAlpha {
		t.Fatal("expected a normal project header for the unpinned alpha project")
	}
}

func TestPinGroupLabelUsesAlias(t *testing.T) {
	m := New(nil, nil).WithPinGroupAliases(map[string]string{"a": "auth", "default": "core"})
	if got := m.pinGroupLabel("a"); got != "auth" {
		t.Fatalf("aliased letter: got %q", got)
	}
	if got := m.pinGroupLabel("default"); got != "core" {
		t.Fatalf("aliased default: got %q", got)
	}
	if got := m.pinGroupLabel("b"); got != "b" {
		t.Fatalf("unaliased letter: got %q", got)
	}
	if got := New(nil, nil).pinGroupLabel("default"); got != "pinned" {
		t.Fatalf("unaliased default: got %q", got)
	}
}

// chordPin drives the g-chord and returns the (group) the handler was
// asked to persist, plus the resulting model.
func chordPin(t *testing.T, m Model, second rune) (string, Model) {
	t.Helper()
	var got string
	called := false
	m = m.WithPinGroupHandler(func(item Item, group string) error {
		got = group
		called = true
		return nil
	}).WithRefresher(func() tea.Cmd { return nil })

	updated, _ := m.Update(keyRunes('g'))
	m = updated.(Model)
	if !m.gChordMode {
		t.Fatal("expected g chord to be pending after g")
	}
	updated, _ = m.Update(keyRunes(second))
	m = updated.(Model)
	if m.gChordMode {
		t.Fatal("expected g chord to clear after the second key")
	}
	if !called {
		t.Fatalf("pin handler was not called for g%c", second)
	}
	return got, m
}

func TestPinChordDefaultAndLetter(t *testing.T) {
	// After the pinned-first sort the unpinned "main" row is last (index 3).
	m := pinnedModel()
	m.cursor = 3
	if got := m.items()[3].PinGroup; got != "" {
		t.Fatalf("test setup: expected unpinned cursor row, got %q", got)
	}
	if got, _ := chordPin(t, m, 'g'); got != "default" {
		t.Fatalf("gg on unpinned row: want default, got %q", got)
	}
	m2 := pinnedModel()
	m2.cursor = 3
	if got, _ := chordPin(t, m2, 'c'); got != "c" {
		t.Fatalf("gc on unpinned row: want c, got %q", got)
	}
}

func TestPinChordToggleAndMove(t *testing.T) {
	// Select the row already pinned to "a" (gamma/hot). After sort it's
	// index 1 (default, a, b, unpinned).
	m := pinnedModel()
	m.cursor = 1
	if got := m.items()[1].PinGroup; got != "a" {
		t.Fatalf("test setup: expected cursor row pinned to a, got %q", got)
	}
	// Same register again → unpin.
	if got, _ := chordPin(t, m, 'a'); got != "" {
		t.Fatalf("ga on a-pinned row: want unpin, got %q", got)
	}
	// Different register → move.
	m2 := pinnedModel()
	m2.cursor = 1
	if got, _ := chordPin(t, m2, 'z'); got != "z" {
		t.Fatalf("gz on a-pinned row: want move to z, got %q", got)
	}
}

func TestPinChordUnpinWithCapitalD(t *testing.T) {
	m := pinnedModel()
	m.cursor = 2 // the "b" register row
	if got := m.items()[2].PinGroup; got != "b" {
		t.Fatalf("test setup: expected cursor row pinned to b, got %q", got)
	}
	if got, _ := chordPin(t, m, 'D'); got != "" {
		t.Fatalf("gD: want unpin, got %q", got)
	}
}

func TestPinChordRenameOpensAliasInput(t *testing.T) {
	m := pinnedModel()
	m.cursor = 1 // register "a"
	saved := ""
	savedAlias := ""
	m = m.WithPinGroupAliasHandler(func(group, alias string) error {
		saved = group
		savedAlias = alias
		return nil
	})
	updated, _ := m.Update(keyRunes('g'))
	m = updated.(Model)
	updated, _ = m.Update(keyRunes('R'))
	m = updated.(Model)
	if !m.pinAliasMode {
		t.Fatal("expected alias input mode after gR on a pinned row")
	}
	if m.pinAliasTarget != "a" {
		t.Fatalf("expected alias target a, got %q", m.pinAliasTarget)
	}
	// Type "auth" and submit.
	for _, r := range "auth" {
		updated, _ = m.Update(keyRunes(r))
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.pinAliasMode {
		t.Fatal("expected alias mode to close on enter")
	}
	if saved != "a" || savedAlias != "auth" {
		t.Fatalf("alias handler got (%q,%q), want (a,auth)", saved, savedAlias)
	}
	if m.pinGroupAliases["a"] != "auth" {
		t.Fatalf("in-memory alias not updated: %+v", m.pinGroupAliases)
	}
}

func TestPinChordRenameNoOpWhenUnpinned(t *testing.T) {
	m := pinnedModel()
	m.cursor = 3 // the unpinned "main" row after the pinned-first sort
	updated, _ := m.Update(keyRunes('g'))
	m = updated.(Model)
	updated, _ = m.Update(keyRunes('R'))
	m = updated.(Model)
	if m.pinAliasMode {
		t.Fatal("gR on an unpinned row should not open the alias input")
	}
}
