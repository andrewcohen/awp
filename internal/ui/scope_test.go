package ui

import (
	"strings"
	"testing"
)

// The `-` chord. It lives here rather than in a host because there are two hosts:
// the deck's modal had its own copy and standalone `awp diff` had none, so the same
// surface answered the same key two different ways depending on which door you came
// through.

// scopeModel is a view with two ranges installed, the first being the one it opens
// on.
func scopeModel(t *testing.T, asked *[]string) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.WithScopes([]ScopeOption{
		{Key: "c", Label: "vs stack base",
			Load: func(int) (string, error) { *asked = append(*asked, "base"); return sampleDiff, nil },
			Base: func() string { return "main" }},
		{Key: "w", Label: "working copy",
			Load: func(int) (string, error) { *asked = append(*asked, "working"); return sampleDiff, nil }},
	})
	return m
}

func TestScopeChordSwitchesTheRange(t *testing.T) {
	var asked []string
	m := scopeModel(t, &asked)
	if got := m.ScopeLabel(); got != "vs stack base" {
		t.Fatalf("expected the first option to be the one open, got %q", got)
	}
	m = press(m, "-")
	if !m.scopePick {
		t.Fatal("expected the chord up after -")
	}
	m = press(m, "w")
	if m.scopePick {
		t.Fatal("expected the chord closed once answered")
	}
	if got := m.ScopeLabel(); got != "working copy" {
		t.Fatalf("expected the switched scope, got %q", got)
	}
	// LoadDiff is now the new scope's reader, so the refresh tick keeps reading the
	// range you switched to rather than reverting to the one you opened on. Invoked
	// directly because the reload itself rides a tea.Cmd, which press does not run.
	if _, err := m.LoadDiff(contextDefault); err != nil {
		t.Fatalf("LoadDiff: %v", err)
	}
	if len(asked) != 1 || asked[0] != "working" {
		t.Fatalf("expected LoadDiff to be the working-copy reader, got %v", asked)
	}
	// The base label goes with it: the old scope's answer would name the wrong
	// thing, and this scope has no base to name at all.
	if m.baseLabel != "" {
		t.Fatalf("expected the stale base cleared, got %q", m.baseLabel)
	}
}

// Re-picking what is already open is a no-op: reloading would drop the reading
// position to arrive at the same diff.
func TestScopeChordIgnoresTheCurrentScope(t *testing.T) {
	var asked []string
	m := scopeModel(t, &asked)
	m = press(m, "-")
	m = press(m, "c")
	if len(asked) != 0 {
		t.Fatalf("expected no reload, got %v", asked)
	}
	if got := m.ScopeLabel(); got != "vs stack base" {
		t.Fatalf("scope changed under a no-op: %q", got)
	}
}

// Anything that is not a scope key cancels, esc included — a mistyped key must not
// fall through to the view and do something else instead.
func TestScopeChordCancelsOnAnythingElse(t *testing.T) {
	for _, key := range []string{"esc", "x", "j"} {
		var asked []string
		m := scopeModel(t, &asked)
		before := m.cursorRow
		m = press(m, "-")
		m = press(m, key)
		if m.scopePick {
			t.Fatalf("%s: expected the chord closed", key)
		}
		if len(asked) != 0 {
			t.Fatalf("%s: expected no reload, got %v", key, asked)
		}
		if m.cursorRow != before {
			t.Fatalf("%s: the key reached the view and moved the cursor", key)
		}
	}
}

// With no scopes installed the key does nothing rather than opening a menu with one
// answer in it.
func TestScopeChordIsInertWithoutOptions(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m = press(m, "-")
	if m.scopePick {
		t.Fatal("expected no chord without scopes installed")
	}
	if m.ScopeLabel() != "" {
		t.Fatalf("expected no scope label, got %q", m.ScopeLabel())
	}
	// One option is the same situation: there is nothing to switch to.
	m.WithScopes([]ScopeOption{{Key: "c", Label: "only one"}})
	m = press(m, "-")
	if m.scopePick {
		t.Fatal("expected no chord for a single option")
	}
}

// While the chord is up the footer is the menu, with the current scope marked so it
// says where you are as well as where you can go.
func TestScopeMenuFooterListsTheAlternatives(t *testing.T) {
	var asked []string
	m := scopeModel(t, &asked)
	m = press(m, "-")
	footer := stripANSI(m.renderFooter())
	for _, want := range []string{"c vs stack base (current)", "w working copy", "esc cancel"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("expected %q in the menu:\n%s", want, footer)
		}
	}
	// It replaces the ordinary footer for that one keypress rather than crowding in
	// beside it.
	if strings.Contains(footer, "? help") {
		t.Fatalf("expected the menu to own the footer:\n%s", footer)
	}
}

// The `?` reference lists the chord with its scopes spelled out — and says nothing
// when a host installed none, so it never advertises a key that does nothing.
func TestScopeChordIsDocumentedWhenItExists(t *testing.T) {
	var asked []string
	m := scopeModel(t, &asked)
	row := m.scopeHelpRow()
	if len(row) != 1 || row[0][0] != "-" {
		t.Fatalf("expected one row for -, got %#v", row)
	}
	for _, want := range []string{"c vs stack base", "w working copy"} {
		if !strings.Contains(row[0][1], want) {
			t.Fatalf("expected %q spelled out, got %q", want, row[0][1])
		}
	}
	if got := commentModel(t, fileWith("a.go", 1, "alpha")).scopeHelpRow(); got != nil {
		t.Fatalf("expected no row without scopes, got %#v", got)
	}
}

// The header names the base once it resolves, and the scope's own wording until
// then — "vs main" says what you are reading against, where "vs stack base" only
// says how it was picked.
func TestStandaloneHeaderFallsBackToTheScopeLabel(t *testing.T) {
	var asked []string
	m := scopeModel(t, &asked)
	m.RepoRoot = "/src/awp"
	if header := stripANSI(m.renderHeader()); !strings.Contains(header, "vs stack base") {
		t.Fatalf("expected the scope's wording before the base resolves:\n%s", header)
	}
	m.baseLabel = "main"
	header := stripANSI(m.renderHeader())
	if !strings.Contains(header, "vs main") {
		t.Fatalf("expected the resolved base:\n%s", header)
	}
	if strings.Contains(header, "vs stack base") {
		t.Fatalf("expected the generic wording gone once resolved:\n%s", header)
	}
}
