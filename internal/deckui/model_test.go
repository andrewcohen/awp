package deckui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEnterInvokesOpenActionAndUpdatesStatus(t *testing.T) {
	called := false
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa", Status: "in progress"}}, func(req ActionRequest) error {
		called = true
		if req.Item.WorkspaceName != "qa" {
			t.Fatalf("unexpected item: %+v", req.Item)
		}
		if req.Action != ActionSummon {
			t.Fatalf("unexpected action: %v", req.Action)
		}
		return nil
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	updated, _ = updated.Update(msg)
	m := updated.(Model)
	if !called {
		t.Fatal("expected open action to be called")
	}
	if m.status != "summon: qa" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestCursorMovesDownAndUp(t *testing.T) {
	model := New([]Item{{WorkspaceName: "one"}, {WorkspaceName: "two"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	m := updated.(Model)
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1, got %d", m.cursor)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("expected cursor 0, got %d", m.cursor)
	}
}

func TestShellKeyInvokesOpenWindowAction(t *testing.T) {
	var got ActionRequest
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		got = req
		return nil
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	updated, _ = updated.Update(msg)
	m := updated.(Model)

	if got.Action != ActionOpenWindow {
		t.Fatalf("unexpected action: %v", got.Action)
	}
	if got.Arg != "" {
		t.Fatalf("unexpected arg: %q", got.Arg)
	}
	if m.status != "open shell: qa" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestCIKeyInvokesCIAction(t *testing.T) {
	var got ActionRequest
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		got = req
		return nil
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd == nil {
		t.Fatal("expected command")
	}
	msg := cmd()
	updated, _ = updated.Update(msg)
	m := updated.(Model)

	if got.Action != ActionCI {
		t.Fatalf("unexpected action: %v", got.Action)
	}
	if m.status != "ci: qa" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestScopedModelStartsInAllProjectsViewWhenAvailable(t *testing.T) {
	model := NewScoped(
		[]Item{{ProjectName: "repo-a", WorkspaceName: "one"}},
		[]Item{{ProjectName: "repo-a", WorkspaceName: "one"}, {ProjectName: "repo-b", WorkspaceName: "two", Current: true}},
		"repo-a",
		nil,
	)
	items := model.items()
	if len(items) != 2 || items[1].WorkspaceName != "two" {
		t.Fatalf("expected all-project items first, got %#v", items)
	}
	if model.cursor != 1 {
		t.Fatalf("expected cursor on current workspace, got %d", model.cursor)
	}
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	called := false
	refreshed := false
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		called = true
		if req.Action != ActionDelete {
			t.Fatalf("unexpected action: %v", req.Action)
		}
		return nil
	}).WithRefresher(func() tea.Cmd {
		return func() tea.Msg {
			refreshed = true
			return RefreshDoneMsg([]Item{{ProjectName: "agent-deck", WorkspaceName: "qb"}}, nil, nil)
		}
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if cmd != nil {
		t.Fatal("expected no command before confirmation")
	}
	m := updated.(Model)
	if !m.confirmDelete {
		t.Fatal("expected delete confirmation mode")
	}
	if called {
		t.Fatal("delete should not run before confirmation")
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected command after confirmation")
	}
	msg := cmd()
	updated, cmd = updated.Update(msg)
	if cmd == nil {
		t.Fatal("expected refresh command after successful delete")
	}
	msg = cmd()
	updated, _ = updated.Update(msg)
	m = updated.(Model)
	if !called {
		t.Fatal("expected delete action to be called")
	}
	if !refreshed {
		t.Fatal("expected refresh to run")
	}
	if m.status != "delete: qa" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	if len(m.items()) != 1 || m.items()[0].WorkspaceName != "qb" {
		t.Fatalf("expected refreshed items, got %#v", m.items())
	}
}

func TestDeleteCanBeCancelled(t *testing.T) {
	called := false
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		called = true
		return nil
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	m := updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatal("expected no command when cancelling")
	}
	m = updated.(Model)
	if m.confirmDelete {
		t.Fatal("expected confirmation mode to end")
	}
	if called {
		t.Fatal("expected delete not to be called")
	}
	if m.status != "delete: cancelled" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestNewWorkspaceErrorStaysOpenAndShowsStatus(t *testing.T) {
	model := New(nil, nil)
	updated, cmd := model.Update(NewWorkspaceDoneMsg{Err: tea.ErrProgramKilled})
	if cmd != nil {
		t.Fatal("expected no quit/clear command on new-workspace error")
	}
	m := updated.(Model)
	if m.status == "" || m.status == "new: " {
		t.Fatalf("expected error status, got %q", m.status)
	}
}

func TestFindTwoLevelJumpMovesCursor(t *testing.T) {
	model := New([]Item{
		{ProjectName: "alpha", WorkspaceName: "main"},
		{ProjectName: "beta", WorkspaceName: "dev"},
		{ProjectName: "beta", WorkspaceName: "stg"},
	}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m := updated.(Model)
	if !m.findMode || m.findStage != findStageProject {
		t.Fatalf("expected find mode in project stage, got findMode=%v stage=%v", m.findMode, m.findStage)
	}
	if got := m.findProjectHints["beta"]; got != "b" {
		t.Fatalf("expected unique-first-letter hint b for beta, got %q (map=%+v)", got, m.findProjectHints)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(Model)
	if !m.findMode || m.findStage != findStageWorkspace || m.findProject != "beta" {
		t.Fatalf("expected workspace stage for beta, got findMode=%v stage=%v project=%q", m.findMode, m.findStage, m.findProject)
	}
	if got := m.findRowHints[1]; got != "d" {
		t.Fatalf("expected row hint d for dev, got %q (map=%+v)", got, m.findRowHints)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.findMode {
		t.Fatal("expected find mode to exit after row selection")
	}
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1, got %d", m.cursor)
	}
}

func TestFindAutoSelectsWhenProjectHasSingleWorkspace(t *testing.T) {
	model := New([]Item{
		{ProjectName: "alpha", WorkspaceName: "main"},
		{ProjectName: "beta", WorkspaceName: "dev"},
	}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m := updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(Model)
	if m.findMode {
		t.Fatal("expected find mode to exit when project has single workspace")
	}
	if m.cursor != 1 {
		t.Fatalf("expected cursor 1, got %d", m.cursor)
	}
	if m.status != "find: dev" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestFindHintsHideProjectLevelOnceInWorkspaceStage(t *testing.T) {
	model := New([]Item{
		{ProjectName: "alpha", WorkspaceName: "main"},
		{ProjectName: "beta", WorkspaceName: "dev"},
		{ProjectName: "beta", WorkspaceName: "stg"},
	}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m := updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(Model)

	projectHints, rowHints := m.findHints()
	if len(projectHints) != 0 {
		t.Fatalf("expected no project hints in workspace stage, got %+v", projectHints)
	}
	if len(rowHints) == 0 {
		t.Fatal("expected row hints in workspace stage")
	}
}

func TestFindPromotesPrefixCollisionsToTwoKey(t *testing.T) {
	model := New([]Item{
		{ProjectName: "repo-a", WorkspaceName: "one"},
		{ProjectName: "repo-a", WorkspaceName: "alt"},
		{ProjectName: "repo-b", WorkspaceName: "two"},
		{ProjectName: "repo-b", WorkspaceName: "tmp"},
	}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m := updated.(Model)

	hintA := m.findProjectHints["repo-a"]
	hintB := m.findProjectHints["repo-b"]
	if len([]rune(hintA)) != 2 || len([]rune(hintB)) != 2 {
		t.Fatalf("expected two-key hints for colliding projects, got %q / %q", hintA, hintB)
	}
	if hintA[0] != 'r' || hintB[0] != 'r' {
		t.Fatalf("expected both hints to share prefix r, got %q / %q", hintA, hintB)
	}
	if hintA == hintB {
		t.Fatalf("expected distinct hints, got %q twice", hintA)
	}
	if !m.findProjectPrefix['r'] {
		t.Fatalf("expected r registered as reserved prefix, got %+v", m.findProjectPrefix)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if m.findPendingPrefix != 'r' {
		t.Fatalf("expected pending prefix r, got %q", m.findPendingPrefix)
	}
	if m.findStage != findStageProject {
		t.Fatalf("expected to stay in project stage while pending, got %v", m.findStage)
	}

	second := rune(hintB[1])
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{second}})
	m = updated.(Model)
	if m.findStage != findStageWorkspace || m.findProject != "repo-b" {
		t.Fatalf("expected workspace stage for repo-b, got stage=%v project=%q", m.findStage, m.findProject)
	}
	if m.findPendingPrefix != 0 {
		t.Fatalf("expected pending prefix cleared, got %q", m.findPendingPrefix)
	}
}

func TestFindEscClearsPendingPrefixWithoutExitingFind(t *testing.T) {
	model := New([]Item{
		{ProjectName: "repo-a", WorkspaceName: "one"},
		{ProjectName: "repo-b", WorkspaceName: "two"},
	}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m := updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(Model)
	if m.findPendingPrefix != 'r' {
		t.Fatalf("expected pending prefix set, got %q", m.findPendingPrefix)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if !m.findMode {
		t.Fatal("expected to remain in find mode after esc on pending prefix")
	}
	if m.findPendingPrefix != 0 {
		t.Fatalf("expected pending prefix cleared, got %q", m.findPendingPrefix)
	}
}

func TestFindCancelWithQ(t *testing.T) {
	model := New([]Item{{ProjectName: "repo-a", WorkspaceName: "one"}}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m := updated.(Model)
	if !m.findMode {
		t.Fatal("expected find mode")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if m.findMode {
		t.Fatal("expected find mode cancelled")
	}
	if m.status != "find: cancelled" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestFindKeyIgnoredWhileFiltering(t *testing.T) {
	model := New([]Item{{ProjectName: "repo-a", WorkspaceName: "one"}}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m := updated.(Model)
	if !m.filtering {
		t.Fatal("expected filtering mode")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	if m.findMode {
		t.Fatal("did not expect find mode while filtering")
	}
	if !m.filtering {
		t.Fatal("expected filtering mode to remain active")
	}
	if m.filter != "f" {
		t.Fatalf("expected filter input to receive rune, got %q", m.filter)
	}
}

func TestAssignHintsUniqueFirstLettersStaySingle(t *testing.T) {
	got := assignHints([]string{"alpha", "beta", "gamma"})
	want := map[string]string{"alpha": "a", "beta": "b", "gamma": "g"}
	for name, expected := range want {
		if got[name] != expected {
			t.Fatalf("expected hint %q for %q, got %q (full=%+v)", expected, name, got[name], got)
		}
	}
}

func TestAssignHintsPromotesCollisions(t *testing.T) {
	got := assignHints([]string{"auth", "api", "billing"})
	if got["billing"] != "b" {
		t.Fatalf("expected billing single hint b, got %q", got["billing"])
	}
	for _, name := range []string{"auth", "api"} {
		hint := got[name]
		if len(hint) != 2 || hint[0] != 'a' {
			t.Fatalf("expected two-key hint starting with a for %q, got %q", name, hint)
		}
	}
	if got["auth"] == got["api"] {
		t.Fatalf("expected distinct hints, got %q twice", got["auth"])
	}
}

func TestAssignHintsSecondCharAvoidsReservedSingles(t *testing.T) {
	got := assignHints([]string{"alpha", "alt", "beta"})
	if got["beta"] != "b" {
		t.Fatalf("expected beta single hint b, got %q", got["beta"])
	}
	for _, name := range []string{"alpha", "alt"} {
		hint := got[name]
		if len(hint) != 2 || hint[0] != 'a' {
			t.Fatalf("expected two-key hint starting with a for %q, got %q", name, hint)
		}
		if hint[1] == 'b' {
			t.Fatalf("second char must not reuse reserved single %q for %q", "b", name)
		}
	}
}

func TestAssignHintsNonLetterFirstCharFallsBack(t *testing.T) {
	got := assignHints([]string{"1project", "alpha"})
	if got["alpha"] != "a" {
		t.Fatalf("expected alpha single hint a, got %q", got["alpha"])
	}
	hint := got["1project"]
	if len(hint) != 2 {
		t.Fatalf("expected two-key fallback hint for %q, got %q", "1project", hint)
	}
	if hint[0] < 'a' || hint[0] > 'z' {
		t.Fatalf("expected fallback first char from pool, got %q", hint)
	}
}

func TestViewShowsEmptyState(t *testing.T) {
	model := New(nil, nil)
	view := model.View()
	if view == "" {
		t.Fatal("expected non-empty view")
	}
}
