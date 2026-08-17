package deckui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var keyN = runeKey("n")

// freeTextDeck is a deck whose `n` opens the free-text box, with a resolver
// that answers however the test says.
func freeTextDeck(resolver IntentResolver) Model {
	m := New(
		[]Item{{ProjectName: "awp", WorkspaceName: "w", RepoRoot: "/repos/awp"}},
		func(ActionRequest) error { return nil },
	)
	if resolver != nil {
		m = m.WithIntentResolver(resolver)
	}
	return m
}

// answering is a resolver that returns a fixed intent for whatever it is
// asked, recording the request.
func answering(intent WorkspaceIntent, err error, gotText, gotRepo *string) IntentResolver {
	return func(text string, repo string) tea.Cmd {
		if gotText != nil {
			*gotText = text
		}
		if gotRepo != nil {
			*gotRepo = repo
		}
		return func() tea.Msg {
			return IntentDoneMsg{Text: text, Intent: intent, Err: err}
		}
	}
}

// typeAndSubmit drives the box the way a user would.
func typeAndSubmit(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(keyN)
	dm := updated.(Model)
	if !dm.freeTextMode {
		t.Fatal("`n` did not open the free-text box")
	}
	// Activate huh's group, then type.
	if cmd != nil {
		if msg := cmd(); msg != nil {
			u, _ := dm.Update(msg)
			dm = u.(Model)
		}
	}
	for _, r := range text {
		u, _ := dm.Update(runeKey(string(r)))
		dm = u.(Model)
	}
	u, c := dm.Update(keySubmit)
	dm = u.(Model)
	return dm, c
}

// `n` with no resolver behaves exactly the way it always did.
func TestNWithoutResolverOpensTheStructuredForm(t *testing.T) {
	updated, _ := freeTextDeck(nil).Update(keyN)
	dm := updated.(Model)
	if dm.freeTextMode {
		t.Error("opened the free-text box with no resolver to drive it")
	}
	if !dm.newWorkspaceMode {
		t.Error("`n` did not open the structured form")
	}
}

func TestNOpensTheFreeTextBox(t *testing.T) {
	updated, _ := freeTextDeck(answering(WorkspaceIntent{}, nil, nil, nil)).Update(keyN)
	dm := updated.(Model)
	if !dm.freeTextMode {
		t.Fatal("`n` did not open the free-text box")
	}
	if dm.freeTextRepo != "/repos/awp" {
		t.Errorf("freeTextRepo = %q, want the selected row's repo", dm.freeTextRepo)
	}
}

// Submitting asks the resolver for the typed text, against the row's repo,
// and leaves the box up in its busy state rather than blocking.
func TestSubmitResolvesAndWaits(t *testing.T) {
	var gotText, gotRepo string
	m := freeTextDeck(answering(WorkspaceIntent{}, nil, &gotText, &gotRepo))
	dm, _ := typeAndSubmit(t, m, "fix the sidebar cursor bug")

	if !dm.freeTextMode {
		t.Fatal("the box closed before the answer arrived")
	}
	if !dm.freeTextForm.busy {
		t.Error("the box is not in its in-flight state")
	}
	if gotText != "fix the sidebar cursor bug" {
		t.Errorf("resolver asked for %q", gotText)
	}
	if gotRepo != "/repos/awp" {
		t.Errorf("resolver given repo %q", gotRepo)
	}
}

// The resolution creates the workspace, with no confirmation step.
func TestResolutionCreatesTheWorkspace(t *testing.T) {
	intent := WorkspaceIntent{
		Name:     "sidebar-cursor-bug",
		Label:    "Sidebar cursor bug",
		Prompt:   "Fix the cursor in the sidebar.",
		Project:  "storefront",
		RepoRoot: "/repos/storefront",
	}
	var got AsyncJobSpec
	m := freeTextDeck(answering(intent, nil, nil, nil))
	m.bookmarkPrefix = "andrew"
	m.trunkResolver = func(string) string { return "main" }
	m.asyncJobLauncher = func(spec AsyncJobSpec) error { got = spec; return nil }

	dm, _ := typeAndSubmit(t, m, "fix the sidebar cursor bug")
	u, cmd := dm.Update(IntentDoneMsg{Text: "fix the sidebar cursor bug", Intent: intent})
	dm = u.(Model)
	// The launcher runs inside the returned command, the way Bubble Tea
	// would run it.
	execCmd(t, cmd)

	if dm.freeTextMode {
		t.Error("the box is still open after resolving")
	}
	if dm.newWorkspaceMode {
		t.Error("the form opened — a resolved intent is created, not confirmed")
	}
	if got.Name != "sidebar-cursor-bug" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Label != "Sidebar cursor bug" {
		t.Errorf("Label = %q", got.Label)
	}
	if got.Prompt != "Fix the cursor in the sidebar." {
		t.Errorf("Prompt = %q", got.Prompt)
	}
	// Created in the project the model chose, not the row's.
	if got.RepoRoot != "/repos/storefront" {
		t.Errorf("RepoRoot = %q, want the resolved project", got.RepoRoot)
	}
	// The two fields the box never asks about have to match what the form
	// would have proposed, or the quick path builds a different workspace
	// than the confirmed one would have.
	if got.Bookmark != "main" {
		t.Errorf("Bookmark = %q, want trunk", got.Bookmark)
	}
	if got.BookmarkToCreate != "andrew/sidebar-cursor-bug" {
		t.Errorf("BookmarkToCreate = %q, want the prefixed auto-bookmark", got.BookmarkToCreate)
	}
}

// A failed resolution is not a dead end: the same form, filled in from the
// local fallback, with the reason on the status line.
func TestFailedResolutionStillOpensTheForm(t *testing.T) {
	fallback := WorkspaceIntent{
		Name:     "fix-the-sidebar-cursor-bug",
		Label:    "fix the sidebar cursor bug",
		Prompt:   "fix the sidebar cursor bug",
		Project:  "awp",
		RepoRoot: "/repos/awp",
	}
	m := freeTextDeck(answering(fallback, errors.New("claude did not answer within 30s"), nil, nil))
	dm, _ := typeAndSubmit(t, m, "fix the sidebar cursor bug")

	u, _ := dm.Update(IntentDoneMsg{
		Text:   "fix the sidebar cursor bug",
		Intent: fallback,
		Err:    errors.New("claude did not answer within 30s"),
	})
	dm = u.(Model)

	if !dm.newWorkspaceMode {
		t.Fatal("a failed resolution left the user with no form")
	}
	if dm.freeTextMode {
		t.Error("the box is still open after a failed resolution")
	}
	if got := dm.newWorkspaceForm.request().Prompt; got != "fix the sidebar cursor bug" {
		t.Errorf("Prompt = %q, want the typed text", got)
	}
	if !strings.Contains(dm.status, "did not answer") {
		t.Errorf("status = %q, want the reason the fields were guessed", dm.status)
	}
}

// A resolution for text the box is no longer showing must not open a form
// the user did not ask for.
func TestStaleResolutionIsDropped(t *testing.T) {
	m := freeTextDeck(answering(WorkspaceIntent{}, nil, nil, nil))
	dm, _ := typeAndSubmit(t, m, "fix the sidebar cursor bug")

	u, _ := dm.Update(IntentDoneMsg{
		Text:   "something else entirely",
		Intent: WorkspaceIntent{Name: "x", RepoRoot: "/repos/awp"},
	})
	dm = u.(Model)

	if dm.newWorkspaceMode {
		t.Error("a stale resolution opened the form")
	}
	if !dm.freeTextMode {
		t.Error("the box was closed by a resolution that was not its own")
	}
}

// esc during a slow call abandons the whole thing.
func TestEscWhileResolvingClosesTheBox(t *testing.T) {
	m := freeTextDeck(answering(WorkspaceIntent{}, nil, nil, nil))
	dm, _ := typeAndSubmit(t, m, "look at PR 2320")

	u, _ := dm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	dm = u.(Model)
	if dm.freeTextMode || dm.newWorkspaceMode {
		t.Error("esc did not return to the deck")
	}
	if dm.status != "" {
		t.Errorf("status = %q, want nothing — the user pressed esc and knows", dm.status)
	}
}

// The power-user door: straight to the form, no model call, with the
// sentence carried across as the prompt.
func TestFallbackKeySkipsTheModelCall(t *testing.T) {
	var asked string
	m := freeTextDeck(answering(WorkspaceIntent{}, nil, &asked, nil))

	updated, cmd := m.Update(keyN)
	dm := updated.(Model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			u, _ := dm.Update(msg)
			dm = u.(Model)
		}
	}
	for _, r := range "spike a jj backed undo" {
		u, _ := dm.Update(runeKey(string(r)))
		dm = u.(Model)
	}
	u, _ := dm.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	dm = u.(Model)

	if asked != "" {
		t.Errorf("the resolver was called with %q — ctrl+f must skip it", asked)
	}
	if dm.freeTextMode || !dm.newWorkspaceMode {
		t.Fatal("ctrl+f did not open the structured form")
	}
	req := dm.newWorkspaceForm.request()
	if req.Prompt != "spike a jj backed undo" {
		t.Errorf("Prompt = %q, want the typed sentence", req.Prompt)
	}
	if req.Name != "spike-a-jj-backed-undo" {
		t.Errorf("Name = %q, want a slug of the sentence", req.Name)
	}
}

// The box must not freeze the deck's background refresh loop open-ended,
// and must not be refreshed out from under the user either.
func TestFreeTextBoxSuspendsBackgroundRefresh(t *testing.T) {
	m := freeTextDeck(answering(WorkspaceIntent{}, nil, nil, nil))
	m.refresher = func() tea.Cmd { return nil }
	updated, _ := m.Update(keyN)
	if updated.(Model).canBackgroundRefresh() {
		t.Error("the row list can refresh under the open box")
	}
}
