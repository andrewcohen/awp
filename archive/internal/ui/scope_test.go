package ui

import (
	"errors"
	"strings"
	"testing"
)

// errRevisionsUnavailable stands in for a jj that would not answer.
var errRevisionsUnavailable = errors.New("list recent changes: jj exited 1")

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

// A key that does nothing has to say why (#155).

// TestTheScopeKeySaysWhenTheViewWasOpenedOnOneRange. `awp diff -r <revset>`
// installs no scopes at all, deliberately — you named the range. `-` used to
// return in silence there, which is how a diagnosis became a conversation instead
// of a glance at the footer.
func TestTheScopeKeySaysWhenTheViewWasOpenedOnOneRange(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	if len(m.scopes) != 0 {
		t.Fatalf("fixture is wrong: expected no scopes, got %d", len(m.scopes))
	}
	m = press(m, "-")
	if m.scopePick {
		t.Fatal("- opened a menu with nothing in it")
	}
	if m.status == "" {
		t.Fatal("- did nothing and said nothing")
	}
	if !strings.Contains(m.status, "-r") {
		t.Errorf("status %q does not say why there is nothing to pick", m.status)
	}
}

// TestTheScopeKeyNamesTheOnlyRangeWhenThereIsOne. A menu with one answer is not a
// menu, and the useful half of what it would have said is the range you are on —
// so the two situations get different wording rather than one that implies a menu
// exists.
func TestTheScopeKeyNamesTheOnlyRangeWhenThereIsOne(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.scopes = []ScopeOption{{Key: "w", Label: "working copy"}}
	m = press(m, "-")
	if m.scopePick {
		t.Fatal("- opened a menu with one answer in it")
	}
	if !strings.Contains(m.status, "working copy") {
		t.Errorf("status %q does not name the range this view is on", m.status)
	}
}

// TestTwoScopesStillOpenTheMenu, which is the case the silence was protecting.
func TestTwoScopesStillOpenTheMenu(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.WithScopes([]ScopeOption{
		{Key: "w", Label: "working copy"},
		{Key: "t", Label: "vs trunk"},
	})
	m = press(m, "-")
	if !m.scopePick {
		t.Fatalf("- did not open the menu (status %q)", m.status)
	}
}

// TestTheEditorKeySaysWhenNothingCanOpenAFile. Same shape as `-`: a host that
// wired no opener left `e` doing nothing in silence.
func TestTheEditorKeySaysWhenNothingCanOpenAFile(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.OpenFile = nil
	m = press(m, "e")
	if m.status == "" {
		t.Fatal("e did nothing and said nothing")
	}
	if !strings.Contains(m.status, "EDITOR") {
		t.Errorf("status %q does not say what is missing", m.status)
	}
}

// The `- r` submenu: one entry standing for every individual commit, because the
// fixed ranges all end at @ and so cannot reach a change that has already landed.

// revisionScopeModel is scopeModel plus the submenu entry, whose choices are two
// changes.
func revisionScopeModel(t *testing.T, asked *[]string) Model {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.WithScopes([]ScopeOption{
		{Key: "c", Label: "vs stack base",
			Load: func(int) (string, error) { *asked = append(*asked, "base"); return sampleDiff, nil }},
		{Key: "r", Label: "a revision…", Choices: func() ([]ScopeOption, error) {
			return []ScopeOption{
				{Key: "qpvuntsm", Label: "wip: the one being written",
					Load: func(int) (string, error) { *asked = append(*asked, "qpvuntsm"); return sampleDiff, nil }},
				{Key: "kntqzsrx", Label: "feat: one that landed",
					Load: func(int) (string, error) { *asked = append(*asked, "kntqzsrx"); return sampleDiff, nil }},
			}, nil
		}},
	})
	return m
}

func TestScopeSubmenuPicksARevision(t *testing.T) {
	var asked []string
	m := revisionScopeModel(t, &asked)
	m = press(m, "-")
	m = press(m, "r")
	if m.scopePick {
		t.Fatal("the chord should be closed once its key was answered")
	}
	if !m.scopeListing {
		t.Fatal("expected the revision list up after - r")
	}
	// Down one, then take it: the second entry, so the test distinguishes picking
	// from defaulting to whatever was first.
	m = press(m, "down")
	m = press(m, "enter")
	if m.scopeListing {
		t.Fatal("expected the list closed once a revision was picked")
	}
	if got := m.ScopeLabel(); got != "feat: one that landed" {
		t.Fatalf("chrome says the range is %q, want the picked revision — a picked revision is not one of the installed scopes and has no index to be found at", got)
	}
	// LoadDiff is now that revision's reader, so the refresh tick keeps reading the
	// commit you picked. Invoked directly because the reload rides a tea.Cmd, which
	// press does not run — same as TestScopeChordSwitchesTheRange.
	if _, err := m.LoadDiff(contextDefault); err != nil {
		t.Fatalf("LoadDiff: %v", err)
	}
	if len(asked) == 0 || asked[len(asked)-1] != "kntqzsrx" {
		t.Fatalf("loads were %v, want the picked revision's reader to be the one installed", asked)
	}
}

func TestScopeSubmenuEscapesWithoutPicking(t *testing.T) {
	var asked []string
	m := revisionScopeModel(t, &asked)
	before := m.ScopeLabel()
	m = press(m, "-")
	m = press(m, "r")
	m = press(m, "esc")
	if m.scopeListing {
		t.Fatal("esc should close the revision list")
	}
	if got := m.ScopeLabel(); got != before {
		t.Fatalf("range moved to %q on a cancelled pick, want %q", got, before)
	}
}

// A submenu entry that cannot resolve says so, rather than putting up an empty
// list — "nothing to pick" and "the command that would have told us failed" are
// answered differently.
func TestScopeSubmenuReportsAResolveFailure(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.WithScopes([]ScopeOption{
		{Key: "c", Label: "vs stack base", Load: func(int) (string, error) { return sampleDiff, nil }},
		{Key: "r", Label: "a revision…", Choices: func() ([]ScopeOption, error) {
			return nil, errRevisionsUnavailable
		}},
	})
	m = press(m, "-")
	m = press(m, "r")
	if m.scopeListing {
		t.Fatal("expected no list when the choices could not be read")
	}
	if !m.statusErr || !strings.Contains(m.status, errRevisionsUnavailable.Error()) {
		t.Fatalf("status is %q (err=%v), want it to name what failed", m.status, m.statusErr)
	}
}
