package deckui

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func execCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			m := c()
			if m == nil {
				continue
			}
			if _, ok := m.(spinner.TickMsg); ok {
				continue
			}
			return m
		}
		t.Fatal("batch contained no non-spinner cmd")
	}
	return msg
}

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
	msg := execCmd(t, cmd)
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
	msg := execCmd(t, cmd)
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
	msg := execCmd(t, cmd)
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
	msg := execCmd(t, cmd)
	updated, _ = updated.Update(msg)
	m = updated.(Model)
	if !called {
		t.Fatal("expected delete action to be called")
	}
	if !m.progressDone || m.progressDoneAction != ActionDelete {
		t.Fatalf("expected progress done after delete, got done=%v action=%v", m.progressDone, m.progressDoneAction)
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected refresh command after esc dismisses delete progress")
	}
	msg = cmd()
	updated, _ = updated.Update(msg)
	m = updated.(Model)
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

func TestReviewModeEntersOnR(t *testing.T) {
	fetchCalled := false
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws", RepoRoot: "/tmp"}}, nil).WithPRFetcher(func(string) tea.Cmd {
		return func() tea.Msg {
			fetchCalled = true
			return PRFetchDoneMsg{PRs: []PRItem{{Number: 42, Title: "Fix bug", HeadRef: "fix", Author: "dev"}}}
		}
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m := updated.(Model)
	if !m.reviewMode || !m.reviewLoading {
		t.Fatal("expected review mode + loading")
	}
	if cmd == nil {
		t.Fatal("expected fetch command")
	}
	msg := execCmd(t, cmd)
	updated, _ = updated.Update(msg)
	m = updated.(Model)
	if !fetchCalled {
		t.Fatal("expected fetch to be called")
	}
	if m.reviewLoading {
		t.Fatal("expected loading to be false after fetch")
	}
	if len(m.reviewPRs) != 1 || m.reviewPRs[0].Number != 42 {
		t.Fatalf("expected 1 PR, got %+v", m.reviewPRs)
	}
}

func TestReviewModeSelectDispatchesAction(t *testing.T) {
	var got ActionRequest
	handler := func(req ActionRequest) error {
		got = req
		return nil
	}
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws", RepoRoot: "/tmp"}}, handler).WithPRFetcher(func(string) tea.Cmd {
		return func() tea.Msg {
			return PRFetchDoneMsg{PRs: []PRItem{
				{Number: 10, Title: "First"},
				{Number: 20, Title: "Second"},
			}}
		}
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	msg := execCmd(t, cmd)
	updated, _ = updated.Update(msg)

	// move down to second PR
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	m := updated.(Model)
	if m.reviewCursor != 1 {
		t.Fatalf("expected cursor 1, got %d", m.reviewCursor)
	}

	// press enter
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command on enter")
	}
	msg = execCmd(t, cmd)
	updated, _ = updated.Update(msg)
	m = updated.(Model)

	if got.Action != ActionReview {
		t.Fatalf("expected ActionReview, got %v", got.Action)
	}
	if got.Arg != "20" {
		t.Fatalf("expected arg '20', got %q", got.Arg)
	}
	if m.reviewMode {
		t.Fatal("expected review mode to exit after selection")
	}
}

func TestReviewModeCancelWithEsc(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws", RepoRoot: "/tmp"}}, nil).WithPRFetcher(func(string) tea.Cmd {
		return func() tea.Msg {
			return PRFetchDoneMsg{PRs: []PRItem{{Number: 1, Title: "PR"}}}
		}
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	msg := execCmd(t, cmd)
	updated, _ = updated.Update(msg)

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m := updated.(Model)
	if m.reviewMode {
		t.Fatal("expected review mode cancelled")
	}
	if m.status != "review: cancelled" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestReviewModeNoPRsFetcher(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m := updated.(Model)
	if m.reviewMode {
		t.Fatal("expected no review mode without fetcher")
	}
	if m.status != "review: not configured" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestReviewModeEmptyPRs(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws", RepoRoot: "/tmp"}}, nil).WithPRFetcher(func(string) tea.Cmd {
		return func() tea.Msg {
			return PRFetchDoneMsg{PRs: nil}
		}
	})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	msg := execCmd(t, cmd)
	updated, _ = updated.Update(msg)
	m := updated.(Model)
	if m.reviewMode {
		t.Fatal("expected review mode to exit on empty PRs")
	}
	if m.status != "review: no open PRs" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestActionModeDispatchesOnAlias(t *testing.T) {
	var got ActionRequest
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, func(req ActionRequest) error {
		got = req
		return nil
	}).WithUserActions([]UserAction{
		{Name: "dev", Command: "pnpm dev", Alias: "d"},
		{Name: "lint", Command: "pnpm lint", Alias: "l"},
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m := updated.(Model)
	if !m.actionMode {
		t.Fatal("expected action mode")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("expected command on alias press")
	}
	msg := execCmd(t, cmd)
	updated, _ = updated.Update(msg)
	m = updated.(Model)
	if m.actionMode {
		t.Fatal("expected action mode to exit")
	}
	if got.Action != ActionCustom {
		t.Fatalf("expected ActionCustom, got %v", got.Action)
	}
	if got.Arg != "dev" {
		t.Fatalf("expected arg 'dev', got %q", got.Arg)
	}
}

func TestActionModeCancelWithEsc(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, nil).WithUserActions([]UserAction{
		{Name: "dev", Command: "pnpm dev", Alias: "d"},
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m := updated.(Model)
	if !m.actionMode {
		t.Fatal("expected action mode")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.actionMode {
		t.Fatal("expected action mode cancelled")
	}
	if m.status != "action: cancelled" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestActionModeUnknownAlias(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, nil).WithUserActions([]UserAction{
		{Name: "dev", Command: "pnpm dev", Alias: "d"},
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m := updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if cmd != nil {
		t.Fatal("expected no command for unknown alias")
	}
	m = updated.(Model)
	if m.actionMode {
		t.Fatal("expected action mode to exit on unknown alias")
	}
	if m.status != `action: unknown alias "z"` {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestActionModeNoActionsConfigured(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m := updated.(Model)
	if m.actionMode {
		t.Fatal("expected no action mode without actions")
	}
	if m.status != "no user actions configured" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestProgressEventStepAndDoneAdvancesState(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, func(ActionRequest) error { return nil })
	model.progressMode = true
	model.progressChan = make(chan progressEvent, 8)
	var updated tea.Model = model

	feed := func(ev progressEvent) {
		updated, _ = updated.Update(progressEventMsg{ev: ev, ok: true})
	}
	feed(progressEvent{kind: progressEventStep, label: "first step"})
	feed(progressEvent{kind: progressEventLog, line: "hello"})
	feed(progressEvent{kind: progressEventStep, label: "second step"})
	feed(progressEvent{kind: progressEventDone, action: ActionSummon, item: Item{WorkspaceName: "ws"}})

	m := updated.(Model)
	if len(m.progressSteps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(m.progressSteps))
	}
	if m.progressSteps[0].Label != "first step" || m.progressSteps[0].State != StepDone {
		t.Fatalf("step 0 wrong: %+v", m.progressSteps[0])
	}
	if m.progressSteps[1].Label != "second step" || m.progressSteps[1].State != StepDone {
		t.Fatalf("step 1 wrong: %+v", m.progressSteps[1])
	}
	if len(m.progressLog) != 1 || m.progressLog[0] != "hello" {
		t.Fatalf("log wrong: %+v", m.progressLog)
	}
	if !m.progressDone {
		t.Fatal("expected done")
	}
}

func TestProgressEventDoneWithErrorMarksRunningStepError(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, func(ActionRequest) error { return nil })
	model.progressMode = true
	model.progressChan = make(chan progressEvent, 8)
	var updated tea.Model = model

	feed := func(ev progressEvent) {
		updated, _ = updated.Update(progressEventMsg{ev: ev, ok: true})
	}
	feed(progressEvent{kind: progressEventStep, label: "doing thing"})
	feed(progressEvent{kind: progressEventDone, err: errTest, action: ActionReview, item: Item{WorkspaceName: "ws"}})

	m := updated.(Model)
	if !m.progressMode || !m.progressDone || m.progressErr == nil {
		t.Fatalf("expected progress mode + done + err, got mode=%v done=%v err=%v", m.progressMode, m.progressDone, m.progressErr)
	}
	if m.progressSteps[0].State != StepError {
		t.Fatalf("expected error state on running step, got %v", m.progressSteps[0].State)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.progressMode {
		t.Fatal("expected progress mode dismissed after esc")
	}
}

func TestStartActionEntersProgressMode(t *testing.T) {
	// Summon is a quick action: no progress UI, just busy.
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, func(ActionRequest) error { return nil })
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}
	m := updated.(Model)
	if m.progressMode {
		t.Fatal("did not expect progress mode for summon")
	}
	if !m.busy {
		t.Fatal("expected busy spinner")
	}
}

func TestDeleteEntersProgressMode(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, func(ActionRequest) error { return nil })
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m := updated.(Model)
	if !m.progressMode {
		t.Fatal("expected progress mode for delete")
	}
}

var errTest = fmtErr("boom")

type fmtErr string

func (e fmtErr) Error() string { return string(e) }
