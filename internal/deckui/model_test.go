package deckui

import (
	"strings"
	"testing"
	"time"

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

func TestModelStartsAllScopeWithCursorOnCurrentWorkspace(t *testing.T) {
	model := New(
		[]Item{{ProjectName: "repo-a", WorkspaceName: "one"}, {ProjectName: "repo-b", WorkspaceName: "two", Current: true}},
		nil,
	)
	if model.Scope() != ScopeAll {
		t.Fatalf("expected ScopeAll on launch, got %v", model.Scope())
	}
	items := model.items()
	if len(items) != 2 || items[1].WorkspaceName != "two" {
		t.Fatalf("expected both workspaces, got %#v", items)
	}
	if model.cursor != 1 {
		t.Fatalf("expected cursor on current workspace, got %d", model.cursor)
	}
}

func TestScopeAttentionMatchesMiniDeckCriteria(t *testing.T) {
	items := []Item{
		// Active=true: live tmux session with a real agent. Surfaces.
		{ProjectName: "a", WorkspaceName: "working", Status: "working", Active: true},
		// Active=false: stale "working" from a crashed agent — drops out
		// via the AttentionIncluded freshness check so we don't keep
		// showing dead rows.
		{ProjectName: "a", WorkspaceName: "working-stale", Status: "working", Active: false},
		{ProjectName: "a", WorkspaceName: "waiting-read", Status: "waiting", Unread: false},
		{ProjectName: "a", WorkspaceName: "waiting-unread", Status: "waiting", Unread: true},
		{ProjectName: "a", WorkspaceName: "idle-read", Status: "idle"},
		{ProjectName: "a", WorkspaceName: "idle-unread", Status: "idle", Unread: true},
		{ProjectName: "a", WorkspaceName: "exited-unread", Status: "exited", Unread: true},
	}
	model := New(items, nil)
	model.scope = ScopeAttention
	got := model.items()
	gotNames := map[string]bool{}
	for _, it := range got {
		gotNames[it.WorkspaceName] = true
	}
	want := []string{"working", "waiting-unread", "idle-unread"}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d: %#v", len(want), len(got), got)
	}
	for _, w := range want {
		if !gotNames[w] {
			t.Fatalf("expected %q in attention scope, got %#v", w, got)
		}
	}
	if gotNames["working-stale"] {
		t.Fatal("expected stale working row (Active=false) to be dropped by freshness check")
	}
}

func TestScopeOpenPRFiltersToNonDraftOpenPRs(t *testing.T) {
	items := []Item{
		{ProjectName: "repo-a", WorkspaceName: "no-bookmark"},
		{ProjectName: "repo-a", WorkspaceName: "open", RepoRoot: "/repo-a", Bookmark: "feat/open"},
		{ProjectName: "repo-a", WorkspaceName: "draft", RepoRoot: "/repo-a", Bookmark: "feat/draft"},
		{ProjectName: "repo-a", WorkspaceName: "merged", RepoRoot: "/repo-a", Bookmark: "feat/merged"},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/repo-a": {
			"feat/open":   {State: PRStateOpen, IsDraft: false},
			"feat/draft":  {State: PRStateOpen, IsDraft: true},
			"feat/merged": {State: PRStateMerged},
		},
	}, nil)
	model.scope = ScopeOpenPR
	got := model.items()
	if len(got) != 1 || got[0].WorkspaceName != "open" {
		t.Fatalf("expected only the non-draft open workspace, got %#v", got)
	}
}

func TestStateChangedRefreshesAndResubscribes(t *testing.T) {
	refreshed := 0
	watched := 0
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil).
		WithRefresher(func() tea.Cmd {
			return func() tea.Msg {
				refreshed++
				return RefreshDoneMsg([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa", Status: "working"}}, nil)
			}
		}).
		WithStateChangeWatcher(func() tea.Cmd {
			watched++
			return func() tea.Msg { return StateChangedMsg{} }
		})

	_, cmd := model.Update(StateChangedMsg{})
	if cmd == nil {
		t.Fatal("expected watcher/refresh batch")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected batch msg, got %T", msg)
	}
	for _, c := range batch {
		if c != nil {
			_ = c()
		}
	}
	if refreshed != 1 {
		t.Fatalf("expected one refresh, got %d", refreshed)
	}
	if watched != 1 {
		t.Fatalf("expected watcher to resubscribe once, got %d", watched)
	}
}

func TestStateChangedDoesNotRefreshDuringOverlay(t *testing.T) {
	refreshed := 0
	watched := 0
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil).
		WithRefresher(func() tea.Cmd {
			return func() tea.Msg {
				refreshed++
				return RefreshDoneMsg(nil, nil)
			}
		}).
		WithStateChangeWatcher(func() tea.Cmd {
			watched++
			return func() tea.Msg { return StateChangedMsg{} }
		})
	model.helpMode = true

	_, cmd := model.Update(StateChangedMsg{})
	if cmd == nil {
		t.Fatal("expected watcher resubscribe command")
	}
	_ = cmd()
	if refreshed != 0 {
		t.Fatalf("expected no refresh during overlay, got %d", refreshed)
	}
	if watched != 1 {
		t.Fatalf("expected watcher to resubscribe once, got %d", watched)
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
			return RefreshDoneMsg([]Item{{ProjectName: "agent-deck", WorkspaceName: "qb"}}, nil)
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
	if m.status != "" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestNewWorkspaceErrorStaysOpenAndShowsStatus(t *testing.T) {
	model := New(nil, nil)
	updated, _ := model.Update(NewWorkspaceDoneMsg{Err: tea.ErrProgramKilled})
	// A tea.ClearScreen cmd is expected here so the deck repaints
	// after returning from the form's tea.Exec; we don't assert its
	// absence anymore. The important guarantee is that the deck
	// stays open and surfaces the error in status.
	m := updated.(Model)
	if m.status == "" || m.status == "new: " {
		t.Fatalf("expected error status, got %q", m.status)
	}
}

// TestAsyncCreateSkipsProgressMode verifies that when an async job
// launcher is configured, the create flow does NOT enter modal
// progressMode and the deck stays interactive.
func TestAsyncCreateSkipsProgressMode(t *testing.T) {
	var got AsyncJobSpec
	launcher := func(spec AsyncJobSpec) error {
		got = spec
		return nil
	}
	model := New(nil, nil).WithAsyncJobLauncher(launcher)
	req := NewWorkspaceRequest{Name: "feat/x", Bookmark: "main", Prompt: "go"}
	updated, cmd := model.Update(NewWorkspaceDoneMsg{Request: &req, RepoRoot: "/repo"})
	if cmd == nil {
		t.Fatal("expected dispatch cmd")
	}
	// Run the dispatch closure so the launcher is invoked.
	_ = cmd()
	m := updated.(Model)
	if m.progressMode {
		t.Fatal("async create should not enter progressMode")
	}
	if m.busy {
		t.Fatal("async create should not mark deck busy")
	}
	if got.Name != "feat/x" || got.Bookmark != "main" || got.RepoRoot != "/repo" {
		t.Fatalf("launcher received unexpected spec: %+v", got)
	}
	if got.Title != "create · feat/x" {
		t.Fatalf("title not derived from name: %q", got.Title)
	}
}

func TestAsyncCreateLauncherErrorSetsStatus(t *testing.T) {
	launcher := func(AsyncJobSpec) error { return tea.ErrProgramKilled }
	model := New(nil, nil).WithAsyncJobLauncher(launcher)
	req := NewWorkspaceRequest{Name: "feat/x"}
	updated, cmd := model.Update(NewWorkspaceDoneMsg{Request: &req, RepoRoot: "/repo"})
	// Trigger the dispatch and feed the resulting message back in.
	msg := cmd()
	updated, _ = updated.Update(msg)
	m := updated.(Model)
	if m.status == "" {
		t.Fatal("expected error status when launcher fails")
	}
}

// TestInlineNewWorkspaceFormSubmitDispatches verifies the deck's
// inline form path: enter form mode, populate the workspace name,
// move to the action row, and press Enter on Submit. The async
// launcher should receive a spec derived from the form values.
// This is the architectural replacement for the prior nested
// tea.Program flow that produced alt-screen bleed.
func TestInlineNewWorkspaceFormSubmitDispatches(t *testing.T) {
	var got AsyncJobSpec
	launcher := func(spec AsyncJobSpec) error {
		got = spec
		return nil
	}
	model := New(nil, nil).WithAsyncJobLauncher(launcher)
	model.newWorkspaceMode = true
	model.newWorkspaceRepo = "/repo"
	model.newWorkspaceForm = newNewWorkspaceForm(NewWorkspaceInitial{Bookmark: "main"})
	model.newWorkspaceForm.workspaceInput.SetValue("feat/x")

	updatedModel, _ := model.dispatchNewWorkspaceForm(tea.KeyMsg{Type: tea.KeyTab})
	m := updatedModel.(Model)
	updatedModel, _ = m.dispatchNewWorkspaceForm(tea.KeyMsg{Type: tea.KeyTab})
	m = updatedModel.(Model)
	updatedModel, _ = m.dispatchNewWorkspaceForm(tea.KeyMsg{Type: tea.KeyTab})
	m = updatedModel.(Model)
	if m.newWorkspaceForm.activeField != 3 {
		t.Fatalf("expected to land on action row, got field %d", m.newWorkspaceForm.activeField)
	}
	updatedModel, cmd := m.dispatchNewWorkspaceForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = updatedModel.(Model)
	if m.newWorkspaceMode {
		t.Fatal("submit should leave form mode")
	}
	if cmd == nil {
		t.Fatal("expected dispatch cmd from submit")
	}
	// Submit returns a Batch (dispatch + tea.ClearScreen). Drain the
	// batch by invoking it; tea.Batch returns a BatchMsg whose contents
	// we run individually so the dispatch closure actually fires.
	if msg := cmd(); msg != nil {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub != nil {
					_ = sub()
				}
			}
		}
	}
	if got.Name != "feat/x" || got.Bookmark != "main" || got.RepoRoot != "/repo" {
		t.Fatalf("launcher received unexpected spec: %+v", got)
	}
}

// TestInlineNewWorkspaceFormCancelClearsState verifies pressing esc
// in the form returns to the row list with no side effects.
func TestInlineNewWorkspaceFormCancelClearsState(t *testing.T) {
	model := New(nil, nil)
	model.newWorkspaceMode = true
	model.newWorkspaceRepo = "/repo"
	model.newWorkspaceForm = newNewWorkspaceForm(NewWorkspaceInitial{})

	updated, _ := model.dispatchNewWorkspaceForm(tea.KeyMsg{Type: tea.KeyEsc})
	m := updated.(Model)
	if m.newWorkspaceMode {
		t.Fatal("esc should leave form mode")
	}
	if m.newWorkspaceRepo != "" {
		t.Fatalf("repo should be cleared, got %q", m.newWorkspaceRepo)
	}
	if m.status != "" {
		t.Fatalf("expected empty status, got %q", m.status)
	}
}

func TestRenameKeyOpensModalOnWorkspaceRow(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)
	if !m.renameMode {
		t.Fatal("expected rename modal to open")
	}
	if got := m.renameForm.target.WorkspaceName; got != "qa" {
		t.Fatalf("expected form target to be selected row, got %q", got)
	}
	if got := m.renameForm.nameInput.Value(); got != "qa" {
		t.Fatalf("expected name input prefilled with current name, got %q", got)
	}
}

func TestRenameKeyRefusedOnDefaultWorkspace(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "default"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)
	if m.renameMode {
		t.Fatal("expected rename modal not to open on default workspace")
	}
	if m.status != "rename: cannot rename the default workspace" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestRenameFormSubmitInvokesHandler(t *testing.T) {
	var gotAction Action
	var gotArg string
	var gotItem Item
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		gotAction = req.Action
		gotArg = req.Arg
		gotItem = req.Item
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)
	m.renameForm.nameInput.SetValue("qb")

	updated, cmd := m.dispatchRenameForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.renameMode {
		t.Fatal("submit should leave rename mode")
	}
	if cmd == nil {
		t.Fatal("expected dispatch cmd from submit")
	}
	if msg := cmd(); msg != nil {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub != nil {
					_ = sub()
				}
			}
		}
	}
	if gotAction != ActionRename {
		t.Fatalf("expected ActionRename, got %v", gotAction)
	}
	if gotArg != "qb" {
		t.Fatalf("expected new name 'qb', got %q", gotArg)
	}
	if gotItem.WorkspaceName != "qa" {
		t.Fatalf("expected handler to receive original item, got %q", gotItem.WorkspaceName)
	}
}

func TestRenameFormCancelClearsState(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)

	updated, _ = m.dispatchRenameForm(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.renameMode {
		t.Fatal("esc should leave rename mode")
	}
	if m.status != "" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestRenameFormRejectsEmptyAndUnchangedNames(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		t.Fatalf("handler should not be invoked, got action %v", req.Action)
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)

	// Unchanged name → form stays open with error.
	updated, _ = m.dispatchRenameForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.renameMode {
		t.Fatal("expected rename mode to stay open after unchanged-name submit")
	}
	if m.renameForm.err == "" {
		t.Fatal("expected validation error for unchanged name")
	}

	// Empty name → form stays open with error.
	m.renameForm.nameInput.SetValue("")
	updated, _ = m.dispatchRenameForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.renameMode {
		t.Fatal("expected rename mode to stay open after empty-name submit")
	}
	if m.renameForm.err == "" {
		t.Fatal("expected validation error for empty name")
	}
}

func TestSendPromptKeyOpensModalOnWorkspaceRow(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m := updated.(Model)
	if !m.promptMode {
		t.Fatal("expected prompt modal to open")
	}
	if got := m.promptForm.target.WorkspaceName; got != "qa" {
		t.Fatalf("expected form target to be selected row, got %q", got)
	}
	if got := m.promptForm.target.ProjectName; got != "agent-deck" {
		t.Fatalf("expected form target project, got %q", got)
	}
}

func TestSendPromptViewIncludesTarget(t *testing.T) {
	form := newPromptForm(Item{ProjectName: "agent-deck", WorkspaceName: "qa"})
	out := form.view(120, 30)
	if !strings.Contains(out, "agent-deck") {
		t.Fatalf("view should show project name: %q", out)
	}
	if !strings.Contains(out, "qa") {
		t.Fatalf("view should show workspace name: %q", out)
	}
}

func TestSendPromptFormSubmitInvokesHandler(t *testing.T) {
	var gotAction Action
	var gotArg string
	var gotItem Item
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		gotAction = req.Action
		gotArg = req.Arg
		gotItem = req.Item
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m := updated.(Model)
	m.promptForm.promptInput.SetValue("refactor the foo")

	updated, cmd := m.dispatchPromptForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.promptMode {
		t.Fatal("submit should leave prompt mode")
	}
	if cmd == nil {
		t.Fatal("expected dispatch cmd from submit")
	}
	if msg := cmd(); msg != nil {
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				if sub != nil {
					_ = sub()
				}
			}
		}
	}
	if gotAction != ActionSendPrompt {
		t.Fatalf("expected ActionSendPrompt, got %v", gotAction)
	}
	if gotArg != "refactor the foo" {
		t.Fatalf("expected prompt arg, got %q", gotArg)
	}
	if gotItem.WorkspaceName != "qa" {
		t.Fatalf("expected handler to receive selected item, got %q", gotItem.WorkspaceName)
	}
}

func TestSendPromptFormRejectsEmpty(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		t.Fatalf("handler should not be invoked, got action %v", req.Action)
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m := updated.(Model)
	updated, _ = m.dispatchPromptForm(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.promptMode {
		t.Fatal("empty submit should keep prompt modal open")
	}
	if m.promptForm.err == "" {
		t.Fatal("expected validation error for empty prompt")
	}
}

func TestSendPromptFormCancelClearsState(t *testing.T) {
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m := updated.(Model)

	updated, _ = m.dispatchPromptForm(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.promptMode {
		t.Fatal("esc should leave prompt mode")
	}
	if m.status != "" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestComposeStatusBarIncludesHelpHint(t *testing.T) {
	bar := composeStatusBar(nil, "⠼", "ready", 80)
	if !contains(bar, "? help") {
		t.Fatalf("status bar missing help hint: %q", bar)
	}
	if !contains(bar, "ready") {
		t.Fatalf("status bar missing right segment: %q", bar)
	}
}

func TestComposeStatusBarShowsActivityBeforeRight(t *testing.T) {
	activities := []Activity{{ID: "pr-status", Label: "pr-status", Done: 1, Total: 3}}
	bar := composeStatusBar(activities, "⠼", "ready", 120)
	prIdx := strings.Index(bar, "pr-status")
	readyIdx := strings.Index(bar, "ready")
	if prIdx < 0 || readyIdx < 0 {
		t.Fatalf("bar missing expected segments: %q", bar)
	}
	if prIdx > readyIdx {
		t.Fatalf("expected activity before right segment, got bar=%q", bar)
	}
}

func TestComposeStatusBarDropsActivityBeforeRightUnderWidthPressure(t *testing.T) {
	activities := []Activity{{ID: "pr-status", Label: "pr-status fetching repos", Done: 1, Total: 9}}
	right := "filter: \"verylongfilterneedle\" · ready"
	bar := composeStatusBar(activities, "⠼", right, 30)
	if !strings.Contains(bar, "ready") {
		t.Fatalf("expected right segment to survive narrow width, got %q", bar)
	}
	if strings.Contains(bar, "pr-status") {
		t.Fatalf("expected activity segment to drop under width pressure, got %q", bar)
	}
}

func TestJobsOverlayOpensOnCapitalJ(t *testing.T) {
	model := New(nil, nil).WithJobsListRefresher(func() []Job {
		return []Job{{ID: "a", Title: "create · x", Status: JobRunning, StartedAt: time.Now()}}
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'J'}})
	m := updated.(Model)
	if !m.jobsOverlay {
		t.Fatal("expected J to open jobs overlay")
	}
}

func TestJobsOverlayClosesOnEsc(t *testing.T) {
	model := New(nil, nil)
	model.jobsOverlay = true
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m := updated.(Model)
	if m.jobsOverlay {
		t.Fatal("expected esc to close overlay")
	}
}

func TestJobsOverlayCancelInvokesHandler(t *testing.T) {
	called := ""
	model := New(nil, nil).
		WithJobCancelHandler(func(id string) error {
			called = id
			return nil
		})
	model.jobsOverlay = true
	model.jobs = []Job{{ID: "abc", Status: JobRunning}}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("expected cancel cmd")
	}
	_ = cmd()
	_ = updated
	if called != "abc" {
		t.Fatalf("cancel handler called with %q, want abc", called)
	}
}

func TestJobsOverlayCancelTerminalNoop(t *testing.T) {
	calls := 0
	model := New(nil, nil).
		WithJobCancelHandler(func(id string) error { calls++; return nil })
	model.jobsOverlay = true
	model.jobs = []Job{{ID: "abc", Status: JobDone}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m := updated.(Model)
	if calls != 0 {
		t.Fatal("cancel should be a no-op for terminal jobs")
	}
	if m.status == "" {
		t.Fatal("expected status hint when cancelling a finished job")
	}
}

func TestJobsOverlayDismissRequiresTerminal(t *testing.T) {
	calls := 0
	model := New(nil, nil).
		WithJobDismissHandler(func(id string) error { calls++; return nil })
	model.jobsOverlay = true
	model.jobs = []Job{{ID: "abc", Status: JobRunning}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m := updated.(Model)
	if calls != 0 {
		t.Fatal("dismiss should refuse running jobs")
	}
	if m.status == "" {
		t.Fatal("expected status hint when dismissing a running job")
	}
}

func TestJobsOverlayDeleteAndRetryInvokesHandlerOnStaleWorkspace(t *testing.T) {
	called := ""
	model := New(nil, nil).
		WithJobDeleteWorkspaceRetryHandler(func(id string) error {
			called = id
			return nil
		})
	model.jobsOverlay = true
	// CRITICAL: WorkspaceName is the row the user was on (often
	// `default`), but ErrorWorkspace is what the failure attached to
	// (the pr-N-* workspace). D must dispatch on the latter.
	model.jobs = []Job{{
		ID:             "stale-1",
		Status:         JobError,
		ErrorKind:      "stale_workspace",
		WorkspaceName:  "default",
		ErrorWorkspace: "pr-1-feat",
	}}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if cmd == nil {
		t.Fatal("expected D to dispatch a cmd for a stale-workspace job")
	}
	_ = cmd()
	_ = updated
	if called != "stale-1" {
		t.Fatalf("delete-and-retry called with %q, want %q", called, "stale-1")
	}
}

func TestJobsOverlayDeleteAndRetryRefusesNonStaleJob(t *testing.T) {
	calls := 0
	model := New(nil, nil).
		WithJobDeleteWorkspaceRetryHandler(func(id string) error { calls++; return nil })
	model.jobsOverlay = true
	// Generic failure with no ErrorKind — D must not fire even with
	// ErrorWorkspace populated.
	model.jobs = []Job{{
		ID:             "generic-1",
		Status:         JobError,
		WorkspaceName:  "default",
		ErrorWorkspace: "pr-2-bar",
	}}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if cmd != nil {
		_ = cmd()
	}
	m := updated.(Model)
	if calls != 0 {
		t.Fatal("D must not dispatch on a non-stale job")
	}
	if m.status == "" {
		t.Fatal("expected status hint explaining why D was rejected")
	}
}

func TestJobsOverlayDeleteAndRetryRefusesWithoutErrorWorkspace(t *testing.T) {
	// Regression: D used to read Spec.WorkspaceName as the deletion
	// target, which silently nuked the user's home row (often
	// `default`). The guard now keys on ErrorWorkspace, and refuses
	// to act when it's empty rather than guessing from the spec.
	calls := 0
	model := New(nil, nil).
		WithJobDeleteWorkspaceRetryHandler(func(id string) error { calls++; return nil })
	model.jobsOverlay = true
	model.jobs = []Job{{
		ID:            "no-ws-1",
		Status:        JobError,
		ErrorKind:     "stale_workspace",
		WorkspaceName: "default",
		// ErrorWorkspace intentionally empty — falling back to
		// WorkspaceName here is exactly the bug we're avoiding.
	}}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if cmd != nil {
		_ = cmd()
	}
	m := updated.(Model)
	if calls != 0 {
		t.Fatal("D must not dispatch when ErrorWorkspace is empty — falling back to WorkspaceName would delete the home row")
	}
	if m.status == "" {
		t.Fatal("expected status hint explaining the missing error workspace")
	}
}

func TestAsyncReviewSkipsProgressMode(t *testing.T) {
	var got AsyncJobSpec
	model := New([]Item{{ProjectName: "p", WorkspaceName: "w", RepoRoot: "/repo"}}, nil).
		WithAsyncJobLauncher(func(s AsyncJobSpec) error { got = s; return nil })
	item := Item{RepoRoot: "/repo", WorkspaceName: "w"}
	updated, cmd := model.startAction(ActionReview, item, "42")
	_ = cmd()
	m := updated.(Model)
	if m.progressMode {
		t.Fatal("review should not enter progressMode when async is configured")
	}
	if got.Action != "review" || got.Arg != "42" {
		t.Fatalf("unexpected spec: %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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
	if m.status != "" {
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
	if !m.reviewMode {
		t.Fatal("expected review mode to stay open so user can see empty state")
	}
	if m.status != "review: no open PRs (esc to cancel)" {
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
	if m.status != "" {
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

func TestDevURLsMsgPopulatesDetails(t *testing.T) {
	item := Item{
		ProjectName:   "awp",
		WorkspaceName: "port-capture",
		Path:          "/tmp/awp/port-capture",
		Status:        "in-progress",
		SessionName:   "awp/port-capture",
		Active:        true,
	}
	model := New([]Item{item}, nil)
	// Before any DevURLsMsg, the Dev line must not appear.
	if got := model.renderDetails(80); strings.Contains(got, "Dev:") {
		t.Fatalf("renderDetails should not show Dev line before msg:\n%s", got)
	}
	updated, _ := model.Update(DevURLsMsg{URLs: map[string]string{
		"awp/port-capture": "http://localhost:5173",
	}})
	m := updated.(Model)
	got := m.renderDetails(80)
	if !strings.Contains(got, "Dev:") {
		t.Fatalf("renderDetails should show Dev line after msg:\n%s", got)
	}
	if !strings.Contains(got, "http://localhost:5173") {
		t.Fatalf("renderDetails should contain the URL:\n%s", got)
	}
	// New snapshot with no URL clears the line.
	updated, _ = m.Update(DevURLsMsg{URLs: map[string]string{}})
	m = updated.(Model)
	if strings.Contains(m.renderDetails(80), "Dev:") {
		t.Fatal("renderDetails should clear Dev line when URL drops")
	}
}

func TestDKeyOpensURLWhenAvailable(t *testing.T) {
	item := Item{ProjectName: "awp", WorkspaceName: "x", SessionName: "awp/x"}
	model := New([]Item{item}, nil)
	// No URL discovered yet → status surfaces the empty case, no crash.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m := updated.(Model)
	if !strings.Contains(m.status, "no dev url") {
		t.Fatalf("expected 'no dev url' status, got %q", m.status)
	}
}

func TestDevURLTickMsgReschedulesAndDispatches(t *testing.T) {
	called := 0
	discoverer := func() tea.Cmd {
		return func() tea.Msg {
			called++
			return DevURLsMsg{URLs: map[string]string{"s": "http://localhost:5173"}}
		}
	}
	model := New(nil, nil).WithDevURLDiscoverer(discoverer)
	updated, cmd := model.Update(devURLTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected cmd from devURLTickMsg")
	}
	// Execute the batch: it must contain the discoverer's tea.Msg
	// (DevURLsMsg) and a rescheduled tick.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected BatchMsg, got %T", cmd())
	}
	sawDevURL := false
	for _, c := range batch {
		if c == nil {
			continue
		}
		if _, ok := c().(DevURLsMsg); ok {
			sawDevURL = true
		}
	}
	if !sawDevURL {
		t.Fatal("expected a DevURLsMsg in the batch")
	}
	if called != 1 {
		t.Fatalf("expected discoverer to be invoked once, got %d", called)
	}
	_ = updated
}

func TestDevURLTickNoopWithoutDiscoverer(t *testing.T) {
	model := New(nil, nil)
	_, cmd := model.Update(devURLTickMsg(time.Now()))
	if cmd != nil {
		t.Fatalf("expected nil cmd when discoverer is unset, got %T", cmd())
	}
}

func TestPRStaleGlyphAndLabel(t *testing.T) {
	cases := []struct {
		name      string
		status    PRStatus
		wantGlyph string
		wantLabel string
	}{
		{
			name:      "open + behind base",
			status:    PRStatus{State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateBehind},
			wantGlyph: prGlyphBehind,
			wantLabel: "open · behind base",
		},
		{
			name:      "open + dirty",
			status:    PRStatus{State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateDirty},
			wantGlyph: prGlyphDirty,
			wantLabel: "open · merge conflicts",
		},
		{
			name:      "open + clean — no stale glyph",
			status:    PRStatus{State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean},
			wantGlyph: "",
			wantLabel: "open",
		},
		{
			name:      "merged + behind — never stale (already merged)",
			status:    PRStatus{State: PRStateMerged, MergeStateStatus: PRMergeStateBehind},
			wantGlyph: "",
			wantLabel: "merged",
		},
		{
			name:      "open + approved + behind — base label kept, stale suffix appended",
			status:    PRStatus{State: PRStateOpen, ReviewDecision: PRReviewApproved, MergeStateStatus: PRMergeStateBehind},
			wantGlyph: prGlyphBehind,
			wantLabel: "approved · behind base",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if g := prStaleGlyph(tc.status); g != tc.wantGlyph {
				t.Errorf("prStaleGlyph: got %q want %q", g, tc.wantGlyph)
			}
			if l := prStatusLabel(tc.status); l != tc.wantLabel {
				t.Errorf("prStatusLabel: got %q want %q", l, tc.wantLabel)
			}
		})
	}
}

func TestPRInMergeQueueGlyphAndLabel(t *testing.T) {
	cases := []struct {
		name      string
		status    PRStatus
		wantGlyph string
		wantLabel string
		wantColor string
	}{
		{
			name:      "open + in queue — queue wins over plain open",
			status:    PRStatus{State: PRStateOpen, IsInMergeQueue: true},
			wantGlyph: prGlyphInQueue,
			wantLabel: "in merge queue",
			wantColor: colSuccess,
		},
		{
			name:      "approved + in queue — queue wins over approved",
			status:    PRStatus{State: PRStateOpen, IsInMergeQueue: true, ReviewDecision: PRReviewApproved, CIState: PRCIPassing},
			wantGlyph: prGlyphInQueue,
			wantLabel: "in merge queue",
			wantColor: colSuccess,
		},
		{
			name:      "in queue + CI failing — CI failing wins",
			status:    PRStatus{State: PRStateOpen, IsInMergeQueue: true, CIState: PRCIFailing},
			wantGlyph: prGlyphCIFail,
			wantLabel: "CI failing",
			wantColor: colDanger,
		},
		{
			name:      "in queue + CI pending — CI pending wins",
			status:    PRStatus{State: PRStateOpen, IsInMergeQueue: true, CIState: PRCIPending},
			wantGlyph: prGlyphCIPend,
			wantLabel: "CI pending",
			wantColor: colWarning,
		},
		{
			name:      "merged + stale in-queue flag — merged terminal state wins",
			status:    PRStatus{State: PRStateMerged, IsInMergeQueue: true},
			wantGlyph: prGlyphMerged,
			wantLabel: "merged",
			wantColor: colMuted,
		},
		{
			name:      "in queue + behind base — base label kept, stale suffix appended",
			status:    PRStatus{State: PRStateOpen, IsInMergeQueue: true, MergeStateStatus: PRMergeStateBehind},
			wantGlyph: prGlyphInQueue,
			wantLabel: "in merge queue · behind base",
			wantColor: colSuccess,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if g := prGlyphFor(tc.status); g != tc.wantGlyph {
				t.Errorf("prGlyphFor: got %q want %q", g, tc.wantGlyph)
			}
			if l := prStatusLabel(tc.status); l != tc.wantLabel {
				t.Errorf("prStatusLabel: got %q want %q", l, tc.wantLabel)
			}
			if c := prGlyphColor(tc.status); c != tc.wantColor {
				t.Errorf("prGlyphColor: got %q want %q", c, tc.wantColor)
			}
		})
	}
}

func TestDetailsRendersPRTitleAndStatus(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	model := New([]Item{item}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, Title: "Add the widget", URL: "https://example/pr/42",
			State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	model.width = 120
	model.height = 30
	out := model.renderDetails(60)
	for _, want := range []string{"#42", "Add the widget", "open"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in details, got:\n%s", want, out)
		}
	}
}

func TestPRRepairPrompt(t *testing.T) {
	cases := []struct {
		name    string
		status  PRStatus
		want    string
		wantSub []string
	}{
		{"healthy", PRStatus{Number: 1, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean}, "", nil},
		{"merged", PRStatus{Number: 1, State: PRStateMerged, CIState: PRCIFailing, MergeStateStatus: PRMergeStateDirty}, "", nil},
		{"closed", PRStatus{Number: 1, State: PRStateClosed, CIState: PRCIFailing}, "", nil},
		{"merge conflicts only", PRStatus{Number: 7, URL: "https://example/pr/7", State: PRStateOpen, MergeStateStatus: PRMergeStateDirty},
			"PR #7 (https://example/pr/7) has merge conflicts against its base branch. Please resolve the conflicts on this branch (rebase or merge the base in), then push the fix.", nil},
		{"failing CI only", PRStatus{Number: 8, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateClean},
			"PR #8 has failing CI checks. Please diagnose the failing checks (e.g. `gh run list`, `gh run view`) and fix the underlying issues, then push the fix.", nil},
		{"behind base only", PRStatus{Number: 9, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateBehind},
			"PR #9 has an out-of-date base branch. Please update this branch with the latest base, then push the fix.", nil},
		{"composite", PRStatus{Number: 11, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateDirty}, "",
			[]string{"PR #11 has multiple issues to address:", "merge conflicts against its base branch", "failing CI checks", "Push the fixes when done."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := prRepairPrompt(tc.status)
			if tc.wantSub != nil {
				for _, sub := range tc.wantSub {
					if !strings.Contains(got, sub) {
						t.Errorf("prRepairPrompt missing %q in:\n%s", sub, got)
					}
				}
				return
			}
			if got != tc.want {
				t.Errorf("prRepairPrompt:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestPRStatusLabelHonorsPROverride(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "missing", PRNumber: 99}
	model := New([]Item{item}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"someone-elses/branch": {
			Number: 99, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	pr, _, ok := model.prStatusLabelForItem(item)
	if !ok {
		t.Fatalf("expected PR # override to resolve PR even when bookmark doesn't match")
	}
	if pr.Number != 99 {
		t.Fatalf("expected #99, got #%d", pr.Number)
	}
}

func TestPRMenuRepairKeyDispatchesPrompt(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	var gotAction Action
	var gotArg string
	handler := func(req ActionRequest) error {
		gotAction = req.Action
		gotArg = req.Arg
		return nil
	}
	model := New([]Item{item}, handler).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, URL: "https://example/pr/42", State: PRStateOpen, MergeStateStatus: PRMergeStateDirty,
		}},
	}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !updated.(Model).prMenuMode {
		t.Fatalf("expected prMenuMode after p")
	}
	updated, cmd := updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if updated.(Model).prMenuMode {
		t.Fatalf("expected prMenuMode false after r")
	}
	if cmd == nil {
		t.Fatalf("expected dispatch cmd")
	}
	if msg := drainCmdForActionResult(t, cmd); msg.action != ActionSendPrompt {
		t.Fatalf("expected ActionSendPrompt, got %v", msg.action)
	}
	if gotAction != ActionSendPrompt || !strings.Contains(gotArg, "merge conflicts") {
		t.Fatalf("handler not invoked correctly; action=%v arg=%q", gotAction, gotArg)
	}
}

func TestPRMenuRepairKeyNoopsWhenNothingToRepair(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	calls := 0
	model := New([]Item{item}, func(ActionRequest) error { calls++; return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/r": {"feat": {Number: 42, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean}},
		}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if calls != 0 {
		t.Fatalf("handler should not run; got calls=%d", calls)
	}
	if !strings.Contains(updated.(Model).status, "nothing to repair") {
		t.Fatalf("expected status to mention 'nothing to repair', got %q", updated.(Model).status)
	}
}

func TestPRMenuSetKeyPersistsNumber(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	gotPR := -1
	model := New([]Item{item}, nil).WithPRNumberLinkHandler(func(_ Item, n int) error {
		gotPR = n
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m := updated.(Model)
	if !m.prNumberSetMode {
		t.Fatalf("expected prNumberSetMode after p s")
	}
	m.prNumberInput.SetValue("123")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if gotPR != 123 {
		t.Fatalf("expected handler called with 123, got %d", gotPR)
	}
	if updated.(Model).prNumberSetMode {
		t.Fatalf("expected prNumberSetMode false after enter")
	}
}

func TestPRMenuSetKeyForcesPRStatusRefetch(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Path: "/ws/path", Bookmark: "feat"}
	fetched := 0
	var fetchedRepos []string
	model := New([]Item{item}, nil).
		WithPRNumberLinkHandler(func(Item, int) error { return nil }).
		WithPRStatusFetcher(func(repos []string) tea.Cmd {
			fetched++
			fetchedRepos = append([]string(nil), repos...)
			return func() tea.Msg { return PRStatusDoneMsg{FetchedAt: time.Now()} }
		})
	// Pre-stamp the throttle so a cold-init refetch doesn't count: only
	// the forcePRStatusRefresh call after the override save should fire.
	model.prStatusFetchedAt = map[string]time.Time{"/r": time.Now()}
	fetched = 0
	fetchedRepos = nil

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m := updated.(Model)
	m.prNumberInput.SetValue("77")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = updated
	if cmd == nil {
		t.Fatalf("expected cmd from override save")
	}
	// Drain the batch — looking for the PRStatusDoneMsg the fake fetcher
	// returns to confirm it ran.
	drainCmd(cmd)
	if fetched != 1 {
		t.Fatalf("expected 1 PR-status fetcher call, got %d", fetched)
	}
	if len(fetchedRepos) != 1 || fetchedRepos[0] != "/r" {
		t.Fatalf("expected fetcher called for /r, got %v", fetchedRepos)
	}
}

func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
		}
	}
}

func TestPRMenuSetKeyBlankClearsOverride(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat", PRNumber: 42}
	gotPR := -1
	model := New([]Item{item}, nil).WithPRNumberLinkHandler(func(_ Item, n int) error {
		gotPR = n
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m := updated.(Model)
	m.prNumberInput.SetValue("")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if gotPR != 0 {
		t.Fatalf("expected blank submit to call handler with 0, got %d", gotPR)
	}
}

// drainCmdForActionResult walks a tea.Cmd batch tree and returns the first
// actionResultMsg it produces.
func drainCmdForActionResult(t *testing.T, cmd tea.Cmd) actionResultMsg {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch v := msg.(type) {
		case actionResultMsg:
			return v
		case tea.BatchMsg:
			queue = append(queue, v...)
		}
	}
	t.Fatal("no actionResultMsg observed")
	return actionResultMsg{}
}

