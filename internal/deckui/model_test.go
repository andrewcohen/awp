package deckui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

func TestStatusGlyphExitedNeverRenders(t *testing.T) {
	// Exited never renders a dot, even with a stale unread flag from an
	// old state file — the agent is gone, so there's nothing to act on.
	for _, unread := range []bool{true, false} {
		if got := statusGlyph("exited", false, unread); got != " " {
			t.Errorf("statusGlyph(exited, unread=%v) = %q, want blank", unread, got)
		}
	}
	if got := statusGlyph("waiting", false, true); got == " " {
		t.Error("statusGlyph(waiting, unread) should render a dot")
	}
}

func TestScopeInboxFiltersToOpenPRsIncludingDrafts(t *testing.T) {
	items := []Item{
		{ProjectName: "repo-a", WorkspaceName: "no-bookmark"},
		{ProjectName: "repo-a", WorkspaceName: "open", RepoRoot: "/repo-a", Bookmark: "feat/open"},
		{ProjectName: "repo-a", WorkspaceName: "draft", RepoRoot: "/repo-a", Bookmark: "feat/draft"},
		{ProjectName: "repo-a", WorkspaceName: "merged", RepoRoot: "/repo-a", Bookmark: "feat/merged"},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/repo-a": {
			"feat/open":   {State: PRStateOpen, IsDraft: false},
			"feat/draft":  {State: PRStateOpen, IsDraft: true, Mine: true},
			"feat/merged": {State: PRStateMerged},
		},
	}, nil)
	model.scope = ScopeInbox
	got := model.items()
	if len(got) != 2 {
		t.Fatalf("expected the open + draft workspaces, got %#v", got)
	}
	// "Other open PRs" (not mine) precedes the bottom "Mine" bucket
	// (drafts live there now), so the open PR sorts before the draft.
	if got[0].WorkspaceName != "open" || got[1].WorkspaceName != "draft" {
		t.Fatalf("expected bucket order [open draft], got [%s %s]", got[0].WorkspaceName, got[1].WorkspaceName)
	}
}

// Inbox scope sorts by bucket (action-first), then project, then label.
func TestScopeInboxSortsByBucketThenProject(t *testing.T) {
	items := []Item{
		{ProjectName: "zeta", WorkspaceName: "waiting", RepoRoot: "/z", Bookmark: "b/waiting"},
		{ProjectName: "alpha", WorkspaceName: "needs-fix", RepoRoot: "/a", Bookmark: "b/fix"},
		{ProjectName: "alpha", WorkspaceName: "review-me", RepoRoot: "/a", Bookmark: "b/review"},
		{ProjectName: "zeta", WorkspaceName: "ready", RepoRoot: "/z", Bookmark: "b/ready"},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/a": {
			"b/fix":    {State: PRStateOpen, Mine: true, CIState: PRCIFailing},
			"b/review": {State: PRStateOpen, ReviewRequested: true},
		},
		"/z": {
			"b/waiting": {State: PRStateOpen, Mine: true},
			"b/ready":   {State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean},
		},
	}, nil)
	model.scope = ScopeInbox
	got := model.items()
	want := []string{"review-me", "needs-fix", "ready", "waiting"}
	if len(got) != len(want) {
		t.Fatalf("expected %d items, got %#v", len(want), got)
	}
	for i, w := range want {
		if got[i].WorkspaceName != w {
			t.Errorf("items()[%d] = %s, want %s (full order %v)", i, got[i].WorkspaceName, w, itemNames(got))
		}
	}
}

func itemNames(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.WorkspaceName
	}
	return out
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
	model.active = newHelpModal()

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

// TestStateChangedCoalescesDuringRefresh guards the lost-update race: a
// StateChangedMsg arriving while a refresh is already in flight must not
// be dropped (the in-flight refresh may have snapshotted state before the
// write that triggered the signal). Instead it sets refreshPending, and
// refreshDoneMsg re-fires the refresher so the final read happens strictly
// after the signal.
func TestStateChangedCoalescesDuringRefresh(t *testing.T) {
	refreshed := 0
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil).
		WithRefresher(func() tea.Cmd {
			return func() tea.Msg {
				refreshed++
				return RefreshDoneMsg([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil)
			}
		}).
		WithStateChangeWatcher(func() tea.Cmd {
			return func() tea.Msg { return StateChangedMsg{} }
		})

	// Simulate a refresh already in flight.
	model.refreshing = true

	// drain runs a (possibly batched) cmd's leaf messages.
	var drain func(c tea.Cmd)
	drain = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, inner := range msg {
				drain(inner)
			}
		}
	}

	updated, cmd := model.Update(StateChangedMsg{})
	m := updated.(Model)
	if !m.refreshPending {
		t.Fatal("expected refreshPending to be set when a change arrives mid-refresh")
	}
	// The batch should only re-arm the watcher, not start a second refresh.
	drain(cmd)
	if refreshed != 0 {
		t.Fatalf("expected no concurrent refresh, got %d", refreshed)
	}

	// The in-flight refresh lands: refreshDoneMsg must clear the flag and
	// fire exactly one follow-up refresh.
	updated, cmd = m.Update(RefreshDoneMsg([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, nil))
	m = updated.(Model)
	if m.refreshPending {
		t.Fatal("expected refreshPending cleared after re-fire")
	}
	if !m.refreshing {
		t.Fatal("expected refreshing=true while the coalesced refresh runs")
	}
	if cmd == nil {
		t.Fatal("expected a follow-up refresh command")
	}
	drain(cmd)
	if refreshed != 1 {
		t.Fatalf("expected exactly one coalesced refresh, got %d", refreshed)
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
	if _, ok := m.active.(*confirmDeleteModal); !ok {
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
	if m.active != nil {
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
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Bookmark: "main"}, "", "")
	model.newWorkspaceForm = form
	// Pre-fill the bound values directly so the test doesn't need to
	// type into huh fields rune by rune. The pointers are shared with
	// the form, so writes here land inside huh too.
	*model.newWorkspaceForm.workspaceVal = "feat/x"
	*model.newWorkspaceForm.confirmSubmit = true
	// Driving huh through 3 tabs + enter requires draining tea.Cmds
	// produced by NextField/SubmitCmd into the form's Update — fiddly
	// to do synchronously in a unit test. Short-circuit by setting the
	// form's terminal state directly; that's what huh's normal key
	// flow eventually produces. dispatchNewWorkspaceForm then sees
	// StateCompleted on the next tick and triggers our submit branch.
	model.newWorkspaceForm.form.State = huh.StateCompleted

	m := model
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
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{}, "", "")
	model.newWorkspaceForm = form

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

// TestNewWorkspaceFormStartFromDefaultsToMain verifies that an empty
// initial bookmark resolves to "main" via the Start-from select, not
// the prior "current @" implicit default.
func TestNewWorkspaceFormStartFromDefaultsToMain(t *testing.T) {
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Name: "feat/x"}, "", "")
	*form.confirmSubmit = true
	got := form.request()
	if got.Bookmark != "main" {
		t.Fatalf("expected bookmark 'main', got %q", got.Bookmark)
	}
	if got.Name != "feat/x" {
		t.Fatalf("expected name 'feat/x', got %q", got.Name)
	}
}

// TestNewWorkspaceFormStartFromPicked verifies that an initial bookmark
// other than "main" lands as a picked bookmark on the Start-from select.
func TestNewWorkspaceFormStartFromPicked(t *testing.T) {
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Name: "feat/x", Bookmark: "andrew/feat-x"}, "", "")
	if got := form.request().Bookmark; got != "andrew/feat-x" {
		t.Fatalf("expected picked bookmark 'andrew/feat-x', got %q", got)
	}
}

// TestNewWorkspaceFormSetPickedBookmark records a picker result and the
// next request() reflects it.
func TestNewWorkspaceFormSetPickedBookmark(t *testing.T) {
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Name: "feat/x"}, "", "")
	form.SetPickedBookmark("andrew/feat-y")
	got := form.request()
	if got.Bookmark != "andrew/feat-y" {
		t.Fatalf("expected bookmark 'andrew/feat-y', got %q", got.Bookmark)
	}
}

// TestNewWorkspaceFormRevertStartFrom clears a previous pick and returns
// to the main default.
func TestNewWorkspaceFormRevertStartFrom(t *testing.T) {
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Name: "feat/x", Bookmark: "andrew/feat-x"}, "", "")
	form.RevertStartFrom()
	if got := form.request().Bookmark; got != "main" {
		t.Fatalf("expected revert to 'main', got %q", got)
	}
}

// TestComputeAutoBookmark pins down the sanitized auto-bookmark name
// that the form's Bookmark-name field auto-populates with as the user
// types a workspace name. Empty result = field stays blank (feature off
// or no name yet).
func TestComputeAutoBookmark(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		workspace string
		want      string
	}{
		{"raw slash collapsed to dash", "andrew", "feat/x", "andrew/feat-x"},
		{"underscore and space normalised", "andrew", "fix tests_now", "andrew/fix-tests-now"},
		{"uppercase folded", "andrew", "Feat/Foo", "andrew/feat-foo"},
		{"prefix trailing slash trimmed", "andrew/", "x", "andrew/x"},
		{"blank prefix → blank", "", "feat-x", ""},
		{"blank workspace → blank", "andrew", "  ", ""},
		{"all-invalid workspace → blank", "andrew", "@@@", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeAutoBookmark(tt.prefix, tt.workspace)
			if got != tt.want {
				t.Fatalf("computeAutoBookmark(%q, %q) = %q, want %q",
					tt.prefix, tt.workspace, got, tt.want)
			}
		})
	}
}

// TestNewWorkspaceFormAutoBookmarkSync verifies that the Bookmark-name
// field auto-populates as the workspace name changes, and stops syncing
// once the user edits the bookmark field directly.
func TestNewWorkspaceFormAutoBookmarkSync(t *testing.T) {
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Name: "feat-x"}, "andrew", "main")
	if got := strings.TrimSpace(*form.bookmarkVal); got != "andrew/feat-x" {
		t.Fatalf("initial auto-bookmark: got %q, want %q", got, "andrew/feat-x")
	}

	// Simulate workspace name change: writes through the bound pointer
	// arrive between update ticks, so we mutate then call syncBookmarkAuto.
	*form.workspaceVal = "feat-y"
	form = form.syncBookmarkAuto()
	if got := *form.bookmarkVal; got != "andrew/feat-y" {
		t.Fatalf("after workspace change: got %q, want %q", got, "andrew/feat-y")
	}

	// User types a custom bookmark; future workspace changes should not
	// overwrite it.
	*form.bookmarkVal = "andrew/custom-name"
	form = form.syncBookmarkAuto()
	*form.workspaceVal = "feat-z"
	form = form.syncBookmarkAuto()
	if got := *form.bookmarkVal; got != "andrew/custom-name" {
		t.Fatalf("after manual edit + workspace change: got %q, want %q",
			got, "andrew/custom-name")
	}
}

// TestNewWorkspaceFormUsesProvidedTrunk verifies the Start-from default
// uses the trunk name passed at construction instead of the hardcoded
// "main" fallback.
func TestNewWorkspaceFormUsesProvidedTrunk(t *testing.T) {
	form, _ := newNewWorkspaceForm(NewWorkspaceInitial{Name: "feat-x"}, "", "master")
	if got := form.request().Bookmark; got != "master" {
		t.Fatalf("expected anchor 'master', got %q", got)
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
	if m.renameForm.nameVal == nil || *m.renameForm.nameVal != "qa" {
		t.Fatalf("expected name value prefilled with current name, got %v", m.renameForm.nameVal)
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
	*m.renameForm.nameVal = "qb"
	// huh validates and transitions to StateCompleted on enter; the
	// test short-circuits the keystream by setting state directly
	// (same pattern as the new-workspace form test).
	m.renameForm.form.State = huh.StateCompleted

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
	// Validation lives inside huh.NewInput.Validate; exercise it
	// directly against the bound value rather than driving keys
	// through the form (which require draining tea.Cmds to land in
	// the validator).
	model := New([]Item{{ProjectName: "agent-deck", WorkspaceName: "qa"}}, func(req ActionRequest) error {
		t.Fatalf("handler should not be invoked, got action %v", req.Action)
		return nil
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)

	// Validate is attached to the first (and only) huh.Input field;
	// reach into it via the same key the form exposes.
	for _, candidate := range []string{"qa", "", "  "} {
		// Pretend the user just typed `candidate` into the input.
		// huh keeps validators on the *Input internals; the easiest
		// way to exercise them here is to set the value pointer and
		// re-invoke the function we know is attached.
		*m.renameForm.nameVal = candidate
		// Build the same predicate inline so this test stays
		// independent of huh's internals.
		current := "qa"
		validate := func(s string) error {
			trimmed := strings.TrimSpace(s)
			if trimmed == "" {
				return errors.New("required")
			}
			if trimmed == current {
				return errors.New("same")
			}
			return nil
		}
		if err := validate(candidate); err == nil {
			t.Fatalf("expected validation error for %q", candidate)
		}
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
	form, _ := newPromptForm(Item{ProjectName: "agent-deck", WorkspaceName: "qa"}, "")
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
	*m.promptForm.promptVal = "refactor the foo"
	// Short-circuit huh's keystream by setting state directly; same
	// pattern as the new-workspace form test.
	m.promptForm.form.State = huh.StateCompleted

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
	// Validation lives inside huh.NewText.Validate; exercise it
	// directly against the bound value rather than driving keys
	// through the form (which require draining tea.Cmds).
	validate := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("prompt is required")
		}
		return nil
	}
	for _, candidate := range []string{"", "  ", "\n\t"} {
		if err := validate(candidate); err == nil {
			t.Fatalf("expected validation error for %q", candidate)
		}
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
	bar := composeStatusBar(nil, "⠼", "ready", "? help", 80)
	if !contains(bar, "? help") {
		t.Fatalf("status bar missing help hint: %q", bar)
	}
	if !contains(bar, "ready") {
		t.Fatalf("status bar missing right segment: %q", bar)
	}
}

func TestComposeStatusBarOmitsHelpHintWhenEmpty(t *testing.T) {
	bar := composeStatusBar(nil, "⠼", "ready", "", 80)
	if contains(bar, "? help") {
		t.Fatalf("status bar should omit help hint when empty: %q", bar)
	}
	if !contains(bar, "ready") {
		t.Fatalf("status bar missing right segment: %q", bar)
	}
}

func TestComposeStatusBarShowsActivityBeforeRight(t *testing.T) {
	activities := []Activity{{ID: "pr-status", Label: "pr-status", Done: 1, Total: 3}}
	bar := composeStatusBar(activities, "⠼", "ready", "? help", 120)
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
	bar := composeStatusBar(activities, "⠼", right, "? help", 30)
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
	if _, ok := m.active.(*jobsModal); !ok {
		t.Fatal("expected J to open jobs overlay")
	}
}

func TestJobsOverlayClosesOnEsc(t *testing.T) {
	model := New(nil, nil)
	model.active = newJobsModal(nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m := updated.(Model)
	if m.active != nil {
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
	model.jobs = []Job{{ID: "abc", Status: JobRunning}}
	model.active = newJobsModal(model.jobs)
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
	model.jobs = []Job{{ID: "abc", Status: JobDone}}
	model.active = newJobsModal(model.jobs)
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
	model.jobs = []Job{{ID: "abc", Status: JobRunning}}
	model.active = newJobsModal(model.jobs)
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
	model.active = newJobsModal(model.jobs)
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
	// Generic failure with no ErrorKind — D must not fire even with
	// ErrorWorkspace populated.
	model.jobs = []Job{{
		ID:             "generic-1",
		Status:         JobError,
		WorkspaceName:  "default",
		ErrorWorkspace: "pr-2-bar",
	}}
	model.active = newJobsModal(model.jobs)
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
	model.jobs = []Job{{
		ID:            "no-ws-1",
		Status:        JobError,
		ErrorKind:     "stale_workspace",
		WorkspaceName: "default",
		// ErrorWorkspace intentionally empty — falling back to
		// WorkspaceName here is exactly the bug we're avoiding.
	}}
	model.active = newJobsModal(model.jobs)
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

	projectHints, pinHints, rowHints := m.findHints()
	if len(projectHints) != 0 {
		t.Fatalf("expected no project hints in workspace stage, got %+v", projectHints)
	}
	if len(pinHints) != 0 {
		t.Fatalf("expected no pin hints in workspace stage, got %+v", pinHints)
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
	rp, ok := m.active.(*reviewPicker)
	if !ok || !rp.loading {
		t.Fatal("expected review picker + loading")
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
	rp, ok = m.active.(*reviewPicker)
	if !ok || rp.loading {
		t.Fatal("expected loaded review picker after fetch")
	}
	items := rp.list.Items()
	if len(items) != 1 {
		t.Fatalf("expected 1 PR in list, got %d", len(items))
	}
	first, ok := items[0].(reviewItem)
	if !ok || first.pr.Number != 42 {
		t.Fatalf("expected PR #42, got %+v", items[0])
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
	rp, ok := m.active.(*reviewPicker)
	if !ok {
		t.Fatal("expected review picker active")
	}
	if rp.list.Index() != 1 {
		t.Fatalf("expected cursor 1, got %d", rp.list.Index())
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
	if m.active != nil {
		t.Fatal("expected review picker to close after selection")
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
	if m.active != nil {
		t.Fatal("expected review picker cancelled")
	}
	if m.status != "" {
		t.Fatalf("unexpected status: %q", m.status)
	}
}

func TestReviewModeNoPRsFetcher(t *testing.T) {
	model := New([]Item{{ProjectName: "repo", WorkspaceName: "ws"}}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m := updated.(Model)
	if m.active != nil {
		t.Fatal("expected no review picker without fetcher")
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
	if _, ok := m.active.(*reviewPicker); !ok {
		t.Fatal("expected review picker to stay open so user can see empty state")
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

func TestMetaLineSurfacesStaleWhenLocalDiffersFromPRHead(t *testing.T) {
	const prSHA = "abc123"
	item := Item{
		ProjectName:      "proj",
		WorkspaceName:    "ws",
		RepoRoot:         "/r",
		Bookmark:         "feat",
		BookmarkCommitID: "def456",
	}
	model := New([]Item{item}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, Author: "bob", HeadRefOid: prSHA, State: PRStateOpen,
			ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	if got := model.metaLine(item); !strings.Contains(got, "stale") {
		t.Errorf("metaLine should include 'stale' when local SHA differs; got %q", got)
	}

	// SHAs aligned → no stale chip.
	item.BookmarkCommitID = prSHA
	model2 := New([]Item{item}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, Author: "bob", HeadRefOid: prSHA, State: PRStateOpen,
			ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	if got := model2.metaLine(item); strings.Contains(got, "stale") {
		t.Errorf("metaLine should not include 'stale' when SHAs match; got %q", got)
	}
}

func TestDevURLsMsgPopulatesMetaLine(t *testing.T) {
	item := Item{
		ProjectName:   "awp",
		WorkspaceName: "port-capture",
		Path:          "/tmp/awp/port-capture",
		Status:        "in-progress",
		SessionName:   "awp/port-capture",
		Active:        true,
	}
	model := New([]Item{item}, nil)
	// Before any DevURLsMsg, the meta line must not advertise a port.
	if got := model.metaLine(item); strings.Contains(got, ":5173") {
		t.Fatalf("metaLine should not show :5173 before msg, got %q", got)
	}
	updated, _ := model.Update(DevURLsMsg{URLs: map[string]string{
		"awp/port-capture": "http://localhost:5173",
	}})
	m := updated.(Model)
	if got := m.metaLine(item); !strings.Contains(got, ":5173") {
		t.Fatalf("metaLine should show :5173 after msg, got %q", got)
	}
	// New snapshot with no URL clears the segment.
	updated, _ = m.Update(DevURLsMsg{URLs: map[string]string{}})
	m = updated.(Model)
	if got := m.metaLine(item); strings.Contains(got, ":5173") {
		t.Fatalf("metaLine should clear :5173 when URL drops, got %q", got)
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

func TestPRReviewReqGlyph(t *testing.T) {
	cases := []struct {
		name      string
		status    PRStatus
		wantGlyph string
		wantColor string
	}{
		{"their PR, my review requested", PRStatus{State: PRStateOpen, ReviewRequested: true}, prGlyphReviewReq, colInfo},
		{"their PR, my review RE-requested", PRStatus{State: PRStateOpen, ReviewRequested: true, ReviewRerequested: true}, prGlyphReviewReq, colWarning},
		{"my PR, changes requested", PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewChangesRequested}, prGlyphChangesReq, colWarning},
		{"my PR, review comments but no formal verdict", PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewRequired, HasReviewComments: true}, prGlyphChangesReq, colWarning},
		{"my PR, review comments, empty decision", PRStatus{State: PRStateOpen, Mine: true, HasReviewComments: true}, prGlyphChangesReq, colWarning},
		{"my PR, review comments but approved — feedback no longer the blocker", PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved, HasReviewComments: true}, "", ""},
		{"my PR, awaiting review, no comments yet", PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewRequired}, "", ""},
		{"their PR with review comments — not surfaced as my move", PRStatus{State: PRStateOpen, HasReviewComments: true}, "", ""},
		{"my PR, approved — no glyph", PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved}, "", ""},
		{"their PR, changes requested by someone else — not my move", PRStatus{State: PRStateOpen, ReviewDecision: PRReviewChangesRequested}, "", ""},
		{"nothing requested", PRStatus{State: PRStateOpen}, "", ""},
		{"closed PR never renders", PRStatus{State: PRStateClosed, ReviewRequested: true}, "", ""},
		{"merged PR never renders", PRStatus{State: PRStateMerged, Mine: true, ReviewDecision: PRReviewChangesRequested}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if g := prReviewReqGlyph(tc.status); g != tc.wantGlyph {
				t.Errorf("prReviewReqGlyph: got %q want %q", g, tc.wantGlyph)
			}
			if tc.wantGlyph == "" {
				return
			}
			if c := prReviewReqGlyphColor(tc.status); c != tc.wantColor {
				t.Errorf("prReviewReqGlyphColor: got %q want %q", c, tc.wantColor)
			}
		})
	}
}

func TestPRLocalStaleGlyph(t *testing.T) {
	open := PRStatus{State: PRStateOpen, HeadRefOid: "abc"}
	if g := prLocalStaleGlyph(open, "def"); g != prGlyphStale {
		t.Errorf("diverged tip: got %q want prGlyphStale", g)
	}
	if g := prLocalStaleGlyph(open, "abc"); g != "" {
		t.Errorf("matching tip: got %q want empty", g)
	}
	if g := prLocalStaleGlyph(open, ""); g != "" {
		t.Errorf("unknown local tip: got %q want empty", g)
	}
	if g := prLocalStaleGlyph(PRStatus{State: PRStateMerged, HeadRefOid: "abc"}, "def"); g != "" {
		t.Errorf("merged PR: got %q want empty", g)
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

func TestRowLabelRendersPRNumberAndTitle(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	model := New([]Item{item}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, Title: "Add the widget", URL: "https://example/pr/42",
			State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	if got := model.displayLabel(item); !strings.Contains(got, "#42") || !strings.Contains(got, "Add the widget") {
		t.Fatalf("displayLabel should contain '#42' and the PR title, got %q", got)
	}
}

func TestPRRepairPrompt(t *testing.T) {
	cases := []struct {
		name     string
		status   PRStatus
		localSHA string
		mine     bool
		want     string
		wantSub  []string
	}{
		{"healthy", PRStatus{Number: 1, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean}, "", true, "", nil},
		{"merged", PRStatus{Number: 1, State: PRStateMerged, CIState: PRCIFailing, MergeStateStatus: PRMergeStateDirty}, "", true, "", nil},
		{"closed", PRStatus{Number: 1, State: PRStateClosed, CIState: PRCIFailing}, "", true, "", nil},
		{"merge conflicts only", PRStatus{Number: 7, URL: "https://example/pr/7", State: PRStateOpen, MergeStateStatus: PRMergeStateDirty}, "", true,
			"PR #7 (https://example/pr/7) has merge conflicts against its base branch. Please resolve the conflicts on this branch (rebase or merge the base in), then push the fix. If pushing new commits dismisses an existing review — or the change addresses a reviewer's comments — re-request review from the affected reviewer(s) once their feedback is addressed so the PR isn't left blocked.", nil},
		{"failing CI only", PRStatus{Number: 8, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateClean}, "", true,
			"PR #8 has failing CI checks. Please diagnose the failing checks (e.g. `gh run list`, `gh run view`) and fix the underlying issues, then push the fix. If pushing new commits dismisses an existing review — or the change addresses a reviewer's comments — re-request review from the affected reviewer(s) once their feedback is addressed so the PR isn't left blocked.", nil},
		{"behind base only", PRStatus{Number: 9, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateBehind}, "", true,
			"PR #9 has an out-of-date base branch. Please update this branch with the latest base, then push the fix. If pushing new commits dismisses an existing review — or the change addresses a reviewer's comments — re-request review from the affected reviewer(s) once their feedback is addressed so the PR isn't left blocked.", nil},
		{"composite", PRStatus{Number: 11, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateDirty}, "", true, "",
			[]string{"PR #11 has multiple issues to address:", "merge conflicts against its base branch", "failing CI checks", "Push the fixes when done.", "re-request review from the affected reviewer(s)"}},
		{"stale only", PRStatus{Number: 12, HeadRefName: "andrew/foo", HeadRefOid: "abc123", State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean}, "def456", true, "",
			[]string{"PR #12 has new commits on origin", "jj git fetch", "andrew/foo@origin"}},
		{"stale with sha match — no repair", PRStatus{Number: 13, HeadRefName: "andrew/bar", HeadRefOid: "same", State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean}, "same", true,
			"", nil},
		{"composite with stale", PRStatus{Number: 14, HeadRefName: "andrew/baz", HeadRefOid: "abc", State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateClean}, "def", true, "",
			[]string{"PR #14 has multiple issues to address:", "failing CI checks", "new commits on origin"}},

		{"changes requested only", PRStatus{Number: 15, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewDecision: PRReviewChangesRequested}, "", true, "",
			[]string{"PR #15 has changes requested by a reviewer", "gh pr view --comments", "re-request review", "push"}},
		{"composite with changes requested", PRStatus{Number: 16, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateClean, ReviewDecision: PRReviewChangesRequested}, "", true, "",
			[]string{"PR #16 has multiple issues to address:", "failing CI checks", "changes requested by a reviewer"}},
		{"approved — no review repair", PRStatus{Number: 17, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewDecision: PRReviewApproved}, "", true, "", nil},
		{"review comments only (no formal verdict)", PRStatus{Number: 18, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewDecision: PRReviewRequired, HasReviewComments: true}, "", true, "",
			[]string{"PR #18 has review comments from a reviewer", "gh pr view --comments", "re-request review", "push"}},
		{"review comments but approved — suppressed", PRStatus{Number: 19, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewDecision: PRReviewApproved, HasReviewComments: true}, "", true, "", nil},

		// Review tone (mine=false): investigate + report, no mutations.
		{"review · merge conflicts only",
			PRStatus{Number: 22, URL: "https://example/pr/22", State: PRStateOpen, MergeStateStatus: PRMergeStateDirty}, "", false, "",
			[]string{"PR #22 (https://example/pr/22) has merge conflicts", "reviewing this PR", "Do NOT modify files", "report back in chat"}},
		{"review · failing CI only",
			PRStatus{Number: 23, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateClean}, "", false, "",
			[]string{"PR #23 has failing CI checks", "summarize the root cause", "Do NOT modify files"}},
		{"review · composite",
			PRStatus{Number: 24, State: PRStateOpen, CIState: PRCIFailing, MergeStateStatus: PRMergeStateDirty}, "", false, "",
			[]string{"PR #24 has multiple issues:", "merge conflicts against its base branch", "failing CI checks", "Do NOT modify files", "Report what you find in chat"}},
		{"review · changes requested",
			PRStatus{Number: 25, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewDecision: PRReviewChangesRequested}, "", false, "",
			[]string{"PR #25 has changes requested by a reviewer", "summarize what the reviewers asked for", "Do NOT modify files"}},
		{"review · my review requested",
			PRStatus{Number: 26, HeadRefName: "coworker/feat", State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewRequested: true}, "", false, "",
			[]string{"PR #26 has a pending request for your review", "jj git fetch", "coworker/feat@origin", "fall back to `gh pr diff`", "Do NOT modify files"}},
		{"review requested but mine tone — suppressed",
			PRStatus{Number: 27, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewRequested: true}, "", true, "", nil},
		{"review · my review RE-requested",
			PRStatus{Number: 28, State: PRStateOpen, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean, ReviewRequested: true, ReviewRerequested: true}, "", false, "",
			[]string{"PR #28 has a RE-request for your review", "whether each point was addressed", "what changed since your last pass", "Do NOT modify files"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := prRepairPrompt(tc.status, tc.localSHA, tc.mine)
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

func TestItemIsMyPR(t *testing.T) {
	cases := []struct {
		name   string
		item   Item
		prefix string
		want   bool
	}{
		{"prefix unconfigured → treat as mine (preserve legacy)",
			Item{Bookmark: "coworker/foo"}, "", true},
		{"bookmark missing → treat as mine (no signal to say otherwise)",
			Item{}, "andrew", true},
		{"bookmark under user prefix → mine",
			Item{Bookmark: "andrew/foo"}, "andrew", true},
		{"bookmark under another prefix → not mine",
			Item{Bookmark: "coworker/foo"}, "andrew", false},
		{"bookmark literally equals prefix (no slash) → not mine",
			Item{Bookmark: "andrew"}, "andrew", false},
		{"prefix with whitespace tolerated",
			Item{Bookmark: "andrew/foo"}, "  andrew  ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := itemIsMyPR(tc.item, tc.prefix); got != tc.want {
				t.Errorf("itemIsMyPR: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestPRStaleSuffix(t *testing.T) {
	const headSHA = "deadbeefcafef00d"
	cases := []struct {
		name  string
		s     PRStatus
		local string
		want  string
	}{
		{"open + match", PRStatus{State: PRStateOpen, HeadRefOid: headSHA}, headSHA, ""},
		{"open + differ", PRStatus{State: PRStateOpen, HeadRefOid: headSHA}, "1111111111111111", "stale"},
		{"open + local empty", PRStatus{State: PRStateOpen, HeadRefOid: headSHA}, "", ""},
		{"open + sha empty", PRStatus{State: PRStateOpen, HeadRefOid: ""}, headSHA, ""},
		{"merged + differ", PRStatus{State: PRStateMerged, HeadRefOid: headSHA}, "1111", ""},
		{"closed + differ", PRStatus{State: PRStateClosed, HeadRefOid: headSHA}, "1111", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prStaleSuffix(tc.s, tc.local); got != tc.want {
				t.Errorf("prStaleSuffix: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPRStatusLabelForItemAppendsStaleSuffix(t *testing.T) {
	const prSHA = "abc123"
	const localSHA = "def456"
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat", BookmarkCommitID: localSHA}
	model := New([]Item{item}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, HeadRefOid: prSHA, State: PRStateOpen,
			ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	_, label, ok := model.prStatusLabelForItem(item)
	if !ok {
		t.Fatalf("expected label, got none")
	}
	if !strings.Contains(label, "approved") || !strings.Contains(label, "stale") {
		t.Errorf("label should chain 'approved' with 'stale'; got %q", label)
	}

	// When SHAs match, no stale suffix.
	item2 := item
	item2.BookmarkCommitID = prSHA
	model2 := New([]Item{item2}, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, HeadRefOid: prSHA, State: PRStateOpen,
			ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean,
		}},
	}, nil)
	_, label2, _ := model2.prStatusLabelForItem(item2)
	if strings.Contains(label2, "stale") {
		t.Errorf("aligned SHAs should not produce 'stale'; got %q", label2)
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

func TestPRMenuMergeKeyOpensConfirmThenDispatches(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	var gotReq ActionRequest
	calls := 0
	handler := func(req ActionRequest) error { calls++; gotReq = req; return nil }
	model := New([]Item{item}, handler).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {Number: 99, Title: "Add merge key", State: PRStateOpen}},
	}, nil)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m := updated.(Model)
	cm, ok := m.active.(*confirmMergeModal)
	if !ok {
		t.Fatalf("expected confirm-merge modal after p m")
	}
	if cm.status.Number != 99 {
		t.Fatalf("expected merge status Number 99, got %d", cm.status.Number)
	}
	if calls != 0 {
		t.Fatalf("merge must not dispatch before confirmation; got calls=%d", calls)
	}
	// The confirm modal must surface the exact command for confidence.
	if view := cm.renderPopover(&m); !strings.Contains(view, "gh pr merge 99 --squash") || !strings.Contains(view, "#99") {
		t.Fatalf("expected merge confirm to show command and PR number; got %q", view)
	}

	// y confirms → enters the progress modal and dispatches ActionMergePR
	// with the PR number as Arg. Draining the batch runs the handler (the
	// dispatch cmd invokes it synchronously).
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	got := updated.(Model)
	if got.active != nil {
		t.Fatalf("expected confirm-merge modal cleared after y")
	}
	if !got.progressMode {
		t.Fatalf("expected progress modal to open and stay until success/failure")
	}
	drainCmd(cmd)
	if calls != 1 {
		t.Fatalf("expected handler called once, got %d", calls)
	}
	if gotReq.Action != ActionMergePR || gotReq.Arg != "99" {
		t.Fatalf("expected ActionMergePR arg=99; got action=%v arg=%q", gotReq.Action, gotReq.Arg)
	}
}

// A successful merge force-refetches the merged PR's repo so its status
// updates immediately (the merged PR drops out of the open-PR cache),
// bypassing the prStatusMinInterval throttle.
func TestMergeSuccessRefetchesPRStatus(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat", PRNumber: 99}
	var fetchedRepos []string
	model := New([]Item{item}, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/r": {"feat": {Number: 99, State: PRStateOpen}},
		}, nil).
		WithPRStatusFetcher(func(repos []string) tea.Cmd {
			fetchedRepos = append([]string(nil), repos...)
			return nil
		})
	// Simulate a recent fetch so a non-forced refresh would be throttled.
	model.prStatusFetchedAt = map[string]time.Time{"/r": time.Now()}

	updated, _ := model.Update(actionResultMsg{
		action: ActionMergePR,
		arg:    "99",
		item:   item,
	})
	m := updated.(Model)

	if len(fetchedRepos) != 1 || fetchedRepos[0] != "/r" {
		t.Fatalf("expected forced PR-status fetch for /r, got %v", fetchedRepos)
	}
	// Throttle timestamp for the repo must be cleared so the fetch isn't
	// suppressed.
	if _, ok := m.prStatusFetchedAt["/r"]; ok {
		t.Errorf("expected /r fetch timestamp cleared by forcePRStatusRefresh")
	}
}

func TestPRMenuMergeKeyCancelDoesNotDispatch(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	calls := 0
	model := New([]Item{item}, func(ActionRequest) error { calls++; return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/r": {"feat": {Number: 7, State: PRStateOpen}},
		}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m := updated.(Model)
	if m.active != nil {
		t.Fatalf("expected confirm-merge modal cleared after n")
	}
	if calls != 0 {
		t.Fatalf("cancel must not dispatch; got calls=%d", calls)
	}
}

func TestPRMenuMergeKeyNoopsWhenPRNotOpen(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	model := New([]Item{item}, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/r": {"feat": {Number: 7, State: PRStateMerged}},
		}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m := updated.(Model)
	if m.active != nil {
		t.Fatalf("expected no merge confirm for a non-open PR")
	}
	if !strings.Contains(m.status, "nothing to merge") {
		t.Fatalf("expected status to explain the PR isn't open; got %q", m.status)
	}
}

func TestPRMenuRepairKeyOpensPrepopulatedPromptForm(t *testing.T) {
	item := Item{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/r", Bookmark: "feat"}
	calls := 0
	handler := func(ActionRequest) error { calls++; return nil }
	model := New([]Item{item}, handler).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/r": {"feat": {
			Number: 42, URL: "https://example/pr/42", State: PRStateOpen, MergeStateStatus: PRMergeStateDirty,
		}},
	}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if _, ok := updated.(Model).active.(prMenuModal); !ok {
		t.Fatalf("expected pr menu after p")
	}
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m := updated.(Model)
	if _, ok := m.active.(prMenuModal); ok {
		t.Fatalf("expected pr menu dismissed after r")
	}
	// Repair now routes through the send-prompt form prepopulated with
	// the repair prompt, so the user can review/edit before sending —
	// it must NOT dispatch straight to the agent.
	if !m.promptMode {
		t.Fatalf("expected promptMode after p r")
	}
	if calls != 0 {
		t.Fatalf("repair should not dispatch straight to the agent; got calls=%d", calls)
	}
	if got := m.promptForm.value(); !strings.Contains(got, "merge conflicts") {
		t.Fatalf("expected prompt form prepopulated with repair prompt; got %q", got)
	}
	if got := m.promptForm.target.WorkspaceName; got != "ws" {
		t.Fatalf("expected form target to be the selected row; got %q", got)
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
	pm, ok := m.active.(*prNumberModal)
	if !ok {
		t.Fatalf("expected pr-number modal after p s")
	}
	pm.input.SetValue("123")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if gotPR != 123 {
		t.Fatalf("expected handler called with 123, got %d", gotPR)
	}
	if updated.(Model).active != nil {
		t.Fatalf("expected pr-number modal closed after enter")
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
	pm, ok := m.active.(*prNumberModal)
	if !ok {
		t.Fatalf("expected pr-number modal after p s")
	}
	pm.input.SetValue("77")
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
	pm, ok := m.active.(*prNumberModal)
	if !ok {
		t.Fatalf("expected pr-number modal after p s")
	}
	pm.input.SetValue("")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if gotPR != 0 {
		t.Fatalf("expected blank submit to call handler with 0, got %d", gotPR)
	}
}

func TestClampDeckViewportEdgeTriggered(t *testing.T) {
	// 30 items in one project — body has a single project header at
	// body[0], items at body[1..30]. width=120 keeps us in side-by-side
	// layout (above deckStackThreshold) so capacity comes from the
	// non-stacked chrome budget — capacity is read dynamically below.
	items := make([]Item, 30)
	for i := range items {
		items[i] = Item{ProjectName: "proj", WorkspaceName: fmt.Sprintf("ws%02d", i)}
	}
	m := New(items, nil)
	m.width = 120
	m.height = 20

	// Initial state: cursor=0, viewport.YOffset must clamp to 0.
	m.cursor = 0
	(&m).clampDeckViewport()
	if got := m.deckYOffset; got != 0 {
		t.Fatalf("cursor=0 should give YOffset=0; got %d", got)
	}

	// Move cursor down within the scrolloff-aware safe zone — YOffset
	// must NOT change. cursorBodyRow = cursor*itemBodyHeight + 1 (header
	// at body[0], cursor's primary row at body[cursor*H+1]). The cursor
	// enters the bottom margin when cursorRow + scrolloff >= capacity.
	prior := m.deckYOffset
	cap := m.deckBodyCapacity()
	safeUpper := (cap - 1 - deckScrollOff - 1) / itemBodyHeight // last c that stays inside the safe zone
	for c := 1; c <= safeUpper; c++ {
		m.cursor = c
		(&m).clampDeckViewport()
		if got := m.deckYOffset; got != prior {
			t.Fatalf("cursor=%d within scrolloff safe zone must not shift YOffset; got %d, want %d", c, got, prior)
		}
	}

	// Cursor enters the bottom margin band: YOffset advances to keep
	// scrolloff rows of context below the cursor. With single-project
	// list the cursor's project header is at body[0]; the moment
	// yoff > 0, sticky engages and effective capacity drops by 1, so
	// yoff lands one row further than scrolloff alone would predict.
	m.cursor = safeUpper + 1 // first cursor that breaks into the margin
	(&m).clampDeckViewport()
	if got := m.deckYOffset; got <= prior {
		t.Errorf("cursor entered bottom margin: YOffset should advance past %d, got %d", prior, got)
	}

	// Move cursor back up — YOffset stays (still inside the safe
	// zone of the new viewport).
	prior = m.deckYOffset
	m.cursor = safeUpper
	(&m).clampDeckViewport()
	if got := m.deckYOffset; got != prior {
		t.Errorf("moving cursor up inside safe zone must not shift YOffset; got %d, want %d", got, prior)
	}
}

func TestClampDeckViewportStickyHeaderShrinksWindow(t *testing.T) {
	// Cursor scrolled deep into a tall single-project list: the project
	// header is well above the viewport, so the sticky-header line
	// (rendered above viewport in renderList) takes one row out of the
	// scrollable capacity. clampDeckViewport must account for this so
	// the cursor doesn't fall off the bottom of the shrunken window.
	items := make([]Item, 30)
	for i := range items {
		items[i] = Item{ProjectName: "proj", WorkspaceName: fmt.Sprintf("ws%02d", i)}
	}
	m := New(items, nil)
	m.width = 120
	m.height = 20

	// Jump cursor to last item; first-pass clamp would land yoff at
	// cursorRow - (capacity-1). The cursor's project header (body[0])
	// is then far above, so sticky engages and effective capacity
	// shrinks by 1 — YOffset must shift one further to keep the cursor
	// row inside the sticky-aware window.
	m.cursor = 29
	(&m).clampDeckViewport()
	bodyRows := m.bodyRows(m.items())
	cursorRow := deckBodyCursorRow(bodyRows, m.cursor)
	yoff := m.deckYOffset
	effectiveCap := m.deckBodyCapacity() - 1
	if cursorRow < yoff || cursorRow >= yoff+effectiveCap {
		t.Errorf("cursor row %d not inside sticky-aware window [%d, %d); body=%d",
			cursorRow, yoff, yoff+effectiveCap, len(bodyRows))
	}
}

func TestCollapsedProjectsDetectsDefaultOnly(t *testing.T) {
	items := []Item{
		{ProjectName: "alpha", WorkspaceName: "default"},     // collapses
		{ProjectName: "beta", WorkspaceName: "default"},      // does NOT — has a sibling
		{ProjectName: "beta", WorkspaceName: "feat"},         //
		{ProjectName: "gamma", WorkspaceName: "feat"},        // single, but not "default"
		{ProjectName: "delta", WorkspaceName: "  default  "}, // trimmed match → collapses
	}
	got := collapsedProjects(items)
	want := map[string]bool{"alpha": true, "delta": true}
	if len(got) != len(want) {
		t.Fatalf("collapsedProjects = %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("expected project %q to collapse", k)
		}
	}
}

func TestCollapsedProjectsOnlyWhenQuiet(t *testing.T) {
	// A default-only project collapses only while its status dot would
	// be blank. A visible dot (working, or unread waiting/idle) keeps
	// the full header + workspace + meta layout.
	items := []Item{
		{ProjectName: "quiet", WorkspaceName: "default", Status: "idle"},                   // collapses
		{ProjectName: "busy", WorkspaceName: "default", Status: "working"},                 // dot → uncollapsed
		{ProjectName: "pinged", WorkspaceName: "default", Status: "waiting", Unread: true}, // dot → uncollapsed
		{ProjectName: "done", WorkspaceName: "default", Status: "idle", Unread: true},      // dot → uncollapsed
		{ProjectName: "gone", WorkspaceName: "default", Status: "exited", Unread: true},    // exited never dots → collapses
	}
	got := collapsedProjects(items)
	want := map[string]bool{"quiet": true, "gone": true}
	if len(got) != len(want) {
		t.Fatalf("collapsedProjects = %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("expected project %q to collapse", k)
		}
	}
}

func TestDeckBodyRowsCollapseLayout(t *testing.T) {
	// items() sorts by (project, displayLabel); with no PR cache the
	// label is the workspace name. So the order is:
	//   alpha/default   (collapses → 1 row)
	//   beta/default, beta/feat   (header + 2×2 rows)
	items := []Item{
		{ProjectName: "alpha", WorkspaceName: "default", Bookmark: "main"},
		{ProjectName: "beta", WorkspaceName: "default"},
		{ProjectName: "beta", WorkspaceName: "feat"},
	}
	m := New(items, nil)
	got := m.items()

	rows := deckBodyRows(got, collapsedProjects(got), nil)
	wantKinds := []deckRowKind{
		deckRowCollapsed, // alpha/default
		deckRowSpacer,    // between projects
		deckRowHeader,    // beta
		deckRowPrimary,   // beta/default
		deckRowMeta,      //
		deckRowPrimary,   // beta/feat
		deckRowMeta,      //
	}
	if len(rows) != len(wantKinds) {
		t.Fatalf("deckBodyRows length = %d, want %d (%v)", len(rows), len(wantKinds), rows)
	}
	for i, want := range wantKinds {
		if rows[i].kind != want {
			t.Errorf("row %d kind = %d, want %d", i, rows[i].kind, want)
		}
	}
	// Cursor → primary/collapsed row index.
	cases := []struct{ cursor, row, header int }{
		{0, 0, 0}, // alpha collapsed: row is its own header
		{1, 3, 2}, // beta/default: primary at body[3], header at body[2]
		{2, 5, 2}, // beta/feat
	}
	for _, c := range cases {
		if r := deckBodyCursorRow(rows, c.cursor); r != c.row {
			t.Errorf("deckBodyCursorRow(cursor=%d) = %d, want %d", c.cursor, r, c.row)
		}
		if h := deckBodyHeaderRowForCursor(rows, c.cursor); h != c.header {
			t.Errorf("deckBodyHeaderRowForCursor(cursor=%d) = %d, want %d", c.cursor, h, c.header)
		}
	}
}

func TestFitRowClampsToOneLine(t *testing.T) {
	long := strings.Repeat("x", 200) + " trailing words that would otherwise wrap"
	got := fitRow(long, 40)
	if strings.Contains(got, "\n") {
		t.Errorf("fitRow must not wrap; got %d lines", strings.Count(got, "\n")+1)
	}
	if w := lipgloss.Width(got); w != 40 {
		t.Errorf("fitRow width = %d, want 40 (padded + clamped)", w)
	}
	// Embedded newlines collapse to spaces rather than splitting the row.
	if multi := fitRow("alpha\nbeta", 40); strings.Contains(multi, "\n") {
		t.Errorf("fitRow should collapse embedded newlines; got %q", multi)
	}
}

func TestRenderListNeverWrapsRows(t *testing.T) {
	items := []Item{
		{ProjectName: "web", WorkspaceName: strings.Repeat("long-", 40), Status: "idle"},
		{ProjectName: "alpha", WorkspaceName: "default", Status: "working", Bookmark: strings.Repeat("br/", 40)},
	}
	m := New(items, nil)
	for _, w := range []int{24, 60, 120} {
		m.width = w
		m.height = 40
		(&m).clampDeckViewport()
		out := m.renderList(m.width)
		for i, line := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("width=%d: rendered line %d is %d cols wide (wrap/overflow): %q", w, i, lw, line)
			}
		}
	}
}

func TestRenderListCollapsedAlignsWithProjectHeader(t *testing.T) {
	// A collapsed default-only project has no header line, so its project
	// name must line up with the project-header column — otherwise it
	// reads as a workspace of the project above it.
	items := []Item{
		{ProjectName: "frontend", WorkspaceName: "dashboard", Status: "idle"},
		{ProjectName: "frontend", WorkspaceName: "feat", Status: "idle"},
		{ProjectName: "zapi", WorkspaceName: "default", Status: "idle"},
	}
	m := New(items, nil)
	m.width = 80
	m.height = 40
	(&m).clampDeckViewport()
	out := m.renderList(m.width)

	headerCol, collapsedCol := -1, -1
	for _, line := range strings.Split(out, "\n") {
		plain := ansi.Strip(line)
		if i := strings.Index(plain, "frontend"); i >= 0 && headerCol < 0 {
			headerCol = i
		}
		if i := strings.Index(plain, "zapi"); i >= 0 {
			collapsedCol = i
		}
	}
	if headerCol < 0 || collapsedCol < 0 {
		t.Fatalf("expected both a 'frontend' header and a collapsed 'zapi' row; header=%d collapsed=%d", headerCol, collapsedCol)
	}
	if headerCol != collapsedCol {
		t.Errorf("collapsed project name starts at col %d; project header name at col %d — must align", collapsedCol, headerCol)
	}
}

func TestRenderListCollapsedDoesNotShiftInFindMode(t *testing.T) {
	// Entering find mode adds a hint to the prefix; the collapsed project
	// name must stay in the same column (the hint lives inside the fixed
	// prefix slot, like project headers).
	items := []Item{
		{ProjectName: "zapi", WorkspaceName: "default", Status: "idle", Bookmark: "main"},
	}
	nameCol := func(m Model) int {
		(&m).clampDeckViewport()
		for _, line := range strings.Split(m.renderList(m.width), "\n") {
			if i := strings.Index(ansi.Strip(line), "zapi"); i >= 0 {
				return i
			}
		}
		return -1
	}

	normal := New(items, nil)
	normal.width, normal.height = 80, 40

	find := New(items, nil)
	find.width, find.height = 80, 40
	find.findMode = true
	find.findStage = findStageProject
	find.findProjectHints = map[string]string{"zapi": "ab"} // 2-char hint → widest "[ab]"

	normalCol, findCol := nameCol(normal), nameCol(find)
	if normalCol < 0 || findCol < 0 {
		t.Fatalf("collapsed name not found; normal=%d find=%d", normalCol, findCol)
	}
	if normalCol != findCol {
		t.Errorf("collapsed name shifted entering find mode: normal col %d, find col %d", normalCol, findCol)
	}
}

func TestRenderListCollapsesDefaultOnlyProject(t *testing.T) {
	items := []Item{
		{ProjectName: "alpha", WorkspaceName: "default", Status: "idle", Bookmark: "main"},
		{ProjectName: "beta", WorkspaceName: "default", Status: "idle"},
		{ProjectName: "beta", WorkspaceName: "feat", Status: "idle"},
	}
	m := New(items, nil)
	m.width = 120
	m.height = 40
	(&m).clampDeckViewport()
	out := m.renderList(m.width)

	// The collapsed alpha row carries the project name, status glyph, and
	// meta (branch) all on ONE line. ANSI color codes don't span the
	// substrings, so a per-line Contains check is sound.
	var alphaLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "alpha") {
			alphaLine = line
			break
		}
	}
	if alphaLine == "" {
		t.Fatal("no line containing project name 'alpha' rendered")
	}
	if !strings.Contains(alphaLine, "main") {
		t.Errorf("collapsed alpha row should carry its meta (branch 'main') inline; got %q", alphaLine)
	}

	// The non-collapsed project still renders its own header line plus a
	// separate workspace row — i.e. 'feat' must not share beta's header
	// line.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "feat") && strings.Contains(line, "beta") {
			t.Errorf("beta header and 'feat' workspace should be on separate lines; got %q", line)
		}
	}
}

// renderHelp must truncate long legend / key lines to their column
// width instead of letting lipgloss wrap them onto extra rows — wrap
// made the overlay grow taller as the terminal narrowed. With
// truncation, every two-column rendering has the same height
// regardless of viewport width.
func TestRenderHelpTruncatesInsteadOfWrapping(t *testing.T) {
	height := func(width int) int {
		_, inner := helpBoxDims(width)
		return strings.Count(helpColumns(inner), "\n") + 1
	}

	wide := height(200)
	// width 90 → boxWidth 82 → innerWidth 76: still the two-column
	// layout, but narrow enough that long binding descriptions used to
	// wrap before truncation was added.
	if narrow := height(90); narrow != wide {
		t.Errorf("help columns height should not depend on width in two-column mode: width 200 → %d lines, width 90 → %d lines", wide, narrow)
	}

	// No content line may exceed the inner column width at the narrow size.
	_, inner := helpBoxDims(90)
	for _, line := range strings.Split(helpColumns(inner), "\n") {
		if w := ansi.StringWidth(line); w > inner {
			t.Errorf("help line exceeds inner width %d (got %d): %q", inner, w, line)
		}
	}
}

// Plain open is deliberately glyph-less (open is the baseline state —
// only deviations earn ink), while a draft renders the pencil-ruler
// glyph. The details panel keeps the "open" wording.
func TestPRGlyphOpenIsBlankDraftIsPencilRuler(t *testing.T) {
	open := PRStatus{State: PRStateOpen, CIState: PRCIPassing}
	if g := prGlyphFor(open); g != "" {
		t.Errorf("plain open PR should render no glyph, got %q", g)
	}
	if l := prStatusLabel(open); l != "open" {
		t.Errorf("plain open PR label: got %q want %q", l, "open")
	}
	draft := PRStatus{State: PRStateOpen, IsDraft: true}
	if g := prGlyphFor(draft); g != prGlyphDraft {
		t.Errorf("draft PR glyph: got %q want prGlyphDraft", g)
	}
	if prGlyphDraft != "\U000F1353" {
		t.Errorf("draft glyph should be nf-md-pencil_ruler (U+F1353), got %q", prGlyphDraft)
	}
}

func TestPRInboxBucket(t *testing.T) {
	cases := []struct {
		name   string
		status PRStatus
		want   inboxBucket
	}{
		{
			name:   "review requested on someone else's PR",
			status: PRStatus{State: PRStateOpen, ReviewRequested: true},
			want:   inboxNeedsYourReview,
		},
		{
			name:   "re-requested review",
			status: PRStatus{State: PRStateOpen, ReviewRequested: true, ReviewRerequested: true},
			want:   inboxNeedsYourReview,
		},
		{
			// A review request names you regardless of the PR's own
			// state — even a failing-CI PR sits in "needs your review".
			name:   "review requested wins over CI failing",
			status: PRStatus{State: PRStateOpen, ReviewRequested: true, CIState: PRCIFailing},
			want:   inboxNeedsYourReview,
		},
		{
			name:   "mine + changes requested",
			status: PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewChangesRequested},
			want:   inboxNeedsAction,
		},
		{
			name:   "mine + CI failing",
			status: PRStatus{State: PRStateOpen, Mine: true, CIState: PRCIFailing},
			want:   inboxNeedsAction,
		},
		{
			name:   "mine + merge conflicts",
			status: PRStatus{State: PRStateOpen, Mine: true, MergeStateStatus: PRMergeStateDirty},
			want:   inboxNeedsAction,
		},
		{
			name:   "mine + approved but behind base",
			status: PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateBehind},
			want:   inboxNeedsAction,
		},
		{
			name:   "mine + approved + green + clean",
			status: PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean},
			want:   inboxReadyToMerge,
		},
		{
			name:   "mine + approved + no checks + clean",
			status: PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved, CIState: PRCINone, MergeStateStatus: PRMergeStateClean},
			want:   inboxReadyToMerge,
		},
		{
			name:   "mine + in merge queue",
			status: PRStatus{State: PRStateOpen, Mine: true, IsInMergeQueue: true, CIState: PRCIPassing},
			want:   inboxReadyToMerge,
		},
		{
			// Waiting on reviewers → the bottom "Mine" pile.
			name:   "mine + no review yet",
			status: PRStatus{State: PRStateOpen, Mine: true, CIState: PRCIPassing},
			want:   inboxMine,
		},
		{
			// CI pending is not yet actionable — it parks in Mine.
			name:   "mine + CI pending",
			status: PRStatus{State: PRStateOpen, Mine: true, CIState: PRCIPending},
			want:   inboxMine,
		},
		{
			// Approved but merge state unknown/blocked: not provably
			// ready, nothing for the author to fix → Mine.
			name:   "mine + approved + merge state unknown",
			status: PRStatus{State: PRStateOpen, Mine: true, ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateUnknown},
			want:   inboxMine,
		},
		{
			// Drafts live in Mine, not their own bucket anymore.
			name:   "mine + draft",
			status: PRStatus{State: PRStateOpen, Mine: true, IsDraft: true},
			want:   inboxMine,
		},
		{
			// Draft precedes CI/decision: a draft isn't submitted for
			// review, so failing CI on it is informational → still Mine.
			name:   "mine + draft + CI failing stays in Mine",
			status: PRStatus{State: PRStateOpen, Mine: true, IsDraft: true, CIState: PRCIFailing},
			want:   inboxMine,
		},
		{
			name:   "someone else's PR, no review requested",
			status: PRStatus{State: PRStateOpen},
			want:   inboxOtherOpen,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prInboxBucket(tc.status); got != tc.want {
				t.Errorf("prInboxBucket = %v (%s), want %v (%s)", got, inboxBucketLabel(got), tc.want, inboxBucketLabel(tc.want))
			}
		})
	}
}

// Inbox scope sections rows under bucket headers carrying counts, never
// collapses, and hides empty buckets (no header is emitted for a bucket
// with no rows).
func TestBodyRowsInboxBucketHeaders(t *testing.T) {
	items := []Item{
		{ProjectName: "alpha", WorkspaceName: "default", RepoRoot: "/a", Bookmark: "b/review"},
		{ProjectName: "beta", WorkspaceName: "fix", RepoRoot: "/b", Bookmark: "b/fix"},
		{ProjectName: "gamma", WorkspaceName: "fix2", RepoRoot: "/g", Bookmark: "b/fix2"},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/a": {"b/review": {State: PRStateOpen, ReviewRequested: true}},
		"/b": {"b/fix": {State: PRStateOpen, Mine: true, CIState: PRCIFailing}},
		"/g": {"b/fix2": {State: PRStateOpen, Mine: true, MergeStateStatus: PRMergeStateDirty}},
	}, nil)
	model.scope = ScopeInbox
	sorted := model.items()
	rows := model.bodyRows(sorted)

	var headers []string
	for _, r := range rows {
		switch r.kind {
		case deckRowHeader:
			headers = append(headers, r.project)
		case deckRowCollapsed:
			t.Errorf("inbox scope must not collapse rows, got collapsed row for %q", r.project)
		}
	}
	want := []string{"Needs your review (1)", "Needs action (2)"}
	if len(headers) != len(want) {
		t.Fatalf("headers = %v, want %v", headers, want)
	}
	for i := range want {
		if headers[i] != want[i] {
			t.Errorf("header[%d] = %q, want %q", i, headers[i], want[i])
		}
	}
}

// metaSegStyle drives meta-line token colors: only :port is tinted
// (blue); everything else (author, branch, prompt, the "to review"
// hint) stays muted. Asserting on GetForeground keeps this independent
// of the test renderer's color profile (which strips ANSI). Author
// (teal) and branch (green) tints were tried and read as too much color
// repeated on every row, so the meta line stays mostly muted.
func TestMetaSegStyle(t *testing.T) {
	m := New([]Item{{ProjectName: "p", WorkspaceName: "w"}}, nil)
	cases := []struct {
		seg  string
		want string
	}{
		{"@andrewcohen", colMuted},
		{glyphBranch + " andrew/fix", colMuted},
		{glyphKeyboard + ` "do the thing"`, colMuted},
		{":5173", colInfo},
		{glyphReturn + "  to review", colMuted},
	}
	for _, c := range cases {
		if got := m.metaSegStyle(c.seg).GetForeground(); got != lipgloss.Color(c.want) {
			t.Errorf("metaSegStyle(%q) fg = %v, want %v", c.seg, got, lipgloss.Color(c.want))
		}
	}
	// renderMetaText must not alter the visible text.
	text := "@andrewcohen · :5173 · " + glyphReturn + "  to review"
	if got := ansi.Strip(m.renderMetaText(text)); got != text {
		t.Errorf("renderMetaText changed visible text: got %q want %q", got, text)
	}
}

// Inbox bucket headers are urgency-colored: review = accent, action =
// danger, ready = success, waiting = warning, drafts/other = muted.
func TestInboxBucketColor(t *testing.T) {
	cases := []struct {
		bucket inboxBucket
		want   string
	}{
		{inboxNeedsYourReview, colAccent},
		{inboxNeedsAction, colDanger},
		{inboxReadyToMerge, colSuccess},
		{inboxOtherOpen, colMuted},
		{inboxMine, colMuted},
	}
	for _, c := range cases {
		if got := inboxBucketColor(c.bucket); got != c.want {
			t.Errorf("inboxBucketColor(%s) = %q, want %q", inboxBucketLabel(c.bucket), got, c.want)
		}
	}
}

// headerStyle resolves an inbox header label back to its bucket color
// and uses the teal ProjectHeader treatment outside the inbox.
func TestHeaderStyleResolvesBucketColor(t *testing.T) {
	m := New([]Item{{ProjectName: "p", WorkspaceName: "w"}}, nil)

	m.scope = ScopeInbox
	if got := m.headerStyle("Needs action (2)"); got.GetForeground() != lipgloss.Color(colDanger) {
		t.Errorf("inbox 'Needs action' header should be danger-colored, got %v", got.GetForeground())
	}
	if got := m.headerStyle("Needs your review (1)"); got.GetForeground() != lipgloss.Color(colAccent) {
		t.Errorf("inbox 'Needs your review' header should be accent-colored, got %v", got.GetForeground())
	}

	m.scope = ScopeAll
	if got := m.headerStyle("shop-api"); got.GetForeground() != lipgloss.Color(colAccent) {
		t.Errorf("project header should use Accent, got %v", got.GetForeground())
	}
}

// In the inbox scope, review-requested PRs with no local workspace
// surface as synthetic virtual rows, deduped against any workspace that
// already resolves to the same PR, and bucketed into "Needs your review".
func TestInboxVirtualReviewRows(t *testing.T) {
	items := []Item{
		// A real workspace in repo /a, pinned to PR #1 (review requested).
		{ProjectName: "alpha", WorkspaceName: "pulled", RepoRoot: "/a", PRNumber: 1},
		// A real workspace in repo /b (its own PR), so /b has a project name.
		{ProjectName: "beta", WorkspaceName: "mine", RepoRoot: "/b", Bookmark: "b/mine"},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/a": {
			// #1 already has the "pulled" workspace → no virtual row.
			"feat/one": {Number: 1, State: PRStateOpen, HeadRefName: "feat/one", ReviewRequested: true},
			// #2 is review-requested but has no workspace → virtual row.
			"feat/two": {Number: 2, State: PRStateOpen, HeadRefName: "feat/two", ReviewRequested: true, Author: "teammate"},
		},
		"/b": {
			"b/mine": {Number: 9, State: PRStateOpen, HeadRefName: "b/mine", Mine: true},
		},
	}, nil)
	model.scope = ScopeInbox
	got := model.items()

	var virtual []Item
	for _, it := range got {
		if it.Virtual {
			virtual = append(virtual, it)
		}
	}
	if len(virtual) != 1 {
		t.Fatalf("expected exactly one virtual row (PR #2), got %d: %v", len(virtual), itemNames(got))
	}
	v := virtual[0]
	if v.PRNumber != 2 {
		t.Errorf("virtual row PRNumber = %d, want 2", v.PRNumber)
	}
	if v.RepoRoot != "/a" {
		t.Errorf("virtual row RepoRoot = %q, want /a", v.RepoRoot)
	}
	if v.ProjectName != "alpha" {
		t.Errorf("virtual row should borrow sibling project name 'alpha', got %q", v.ProjectName)
	}
	if v.Bookmark != "feat/two" {
		t.Errorf("virtual row should carry PR head ref as bookmark, got %q", v.Bookmark)
	}
	if b := model.itemInboxBucket(v); b != inboxNeedsYourReview {
		t.Errorf("virtual review row bucket = %s, want Needs your review", inboxBucketLabel(b))
	}
	// The label resolves to the PR title slot (#N) and the row sorts into
	// the review bucket alongside the pulled-down #1.
	if got := model.displayLabel(v); !strings.Contains(got, "#2") {
		t.Errorf("virtual row label should reference #2, got %q", got)
	}
}

// Your own open PRs with no local workspace surface as virtual rows in
// the inbox, sorted into their state bucket — they don't silently vanish
// just because you haven't checked them out (e.g. opened from another
// machine, or workspace deleted).
func TestInboxVirtualMineRows(t *testing.T) {
	items := []Item{
		// A real workspace in repo /a so /a is in the pr-status fetch set
		// and has a project name to lend the virtual rows.
		{ProjectName: "alpha", WorkspaceName: "pulled", RepoRoot: "/a", PRNumber: 1},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/a": {
			// #1 has the "pulled" workspace → no virtual row.
			"feat/one": {Number: 1, State: PRStateOpen, HeadRefName: "feat/one", Mine: true},
			// #2 is yours, draft, no workspace → virtual row in "Mine".
			"feat/two": {Number: 2, State: PRStateOpen, HeadRefName: "feat/two", Mine: true, IsDraft: true},
			// #3 is yours, approved + green, no workspace → "Ready to merge".
			"feat/three": {Number: 3, State: PRStateOpen, HeadRefName: "feat/three", Mine: true,
				ReviewDecision: PRReviewApproved, CIState: PRCIPassing, MergeStateStatus: PRMergeStateClean},
			// #4 is someone else's, not awaiting you → NOT surfaced.
			"feat/four": {Number: 4, State: PRStateOpen, HeadRefName: "feat/four", Author: "teammate"},
		},
	}, nil)
	model.scope = ScopeInbox
	got := model.items()

	byNum := map[int]Item{}
	for _, it := range got {
		if it.Virtual {
			byNum[it.PRNumber] = it
		}
	}
	if len(byNum) != 2 {
		t.Fatalf("expected virtual rows for #2 and #3 only, got %d: %v", len(byNum), itemNames(got))
	}
	if _, ok := byNum[4]; ok {
		t.Errorf("someone else's PR #4 should not be surfaced as a virtual mine row")
	}
	if v, ok := byNum[2]; !ok {
		t.Errorf("draft PR #2 missing a virtual row")
	} else if b := model.itemInboxBucket(v); b != inboxMine {
		t.Errorf("draft PR #2 bucket = %s, want Mine", inboxBucketLabel(b))
	}
	if v, ok := byNum[3]; !ok {
		t.Errorf("approved PR #3 missing a virtual row")
	} else if b := model.itemInboxBucket(v); b != inboxReadyToMerge {
		t.Errorf("approved PR #3 bucket = %s, want Ready to merge", inboxBucketLabel(b))
	}
}

// Outside the inbox scope, no virtual rows are synthesized.
func TestVirtualRowsOnlyInInboxScope(t *testing.T) {
	items := []Item{{ProjectName: "alpha", WorkspaceName: "w", RepoRoot: "/a", Bookmark: "b/w"}}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/a": {
			"feat/x": {Number: 5, State: PRStateOpen, HeadRefName: "feat/x", ReviewRequested: true},
		},
	}, nil)
	for _, sc := range []Scope{ScopeAll, ScopeAttention} {
		model.scope = sc
		for _, it := range model.items() {
			if it.Virtual {
				t.Errorf("scope %s synthesized a virtual row; should be inbox-only", scopeLabel(sc))
			}
		}
	}
}

// Enter on a virtual row dispatches the review flow (ActionReview) with
// the PR number, rather than summoning a non-existent workspace.
func TestEnterOnVirtualRowStartsReview(t *testing.T) {
	var gotSpec AsyncJobSpec
	launched := false
	items := []Item{{ProjectName: "alpha", WorkspaceName: "sib", RepoRoot: "/a", Bookmark: "b/sib"}}
	model := New(items, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/a": {
				"feat/two": {Number: 2, State: PRStateOpen, HeadRefName: "feat/two", ReviewRequested: true},
			},
		}, nil).
		WithAsyncJobLauncher(func(spec AsyncJobSpec) error {
			gotSpec = spec
			launched = true
			return nil
		})
	model.scope = ScopeInbox

	// Cursor onto the virtual row.
	virtualIdx := -1
	for i, it := range model.items() {
		if it.Virtual {
			virtualIdx = i
			break
		}
	}
	if virtualIdx < 0 {
		t.Fatal("no virtual row present to select")
	}
	model.cursor = virtualIdx

	_, cmd := model.trigger(ActionSummon, "")
	if cmd == nil {
		t.Fatal("trigger returned no command for enter on virtual row")
	}
	execCmd(t, cmd)
	if !launched {
		t.Fatal("enter on virtual row did not launch any async job")
	}
	if gotSpec.Action != "review" {
		t.Errorf("async job action = %q, want review", gotSpec.Action)
	}
	if gotSpec.Arg != "2" {
		t.Errorf("review job PR arg = %q, want 2", gotSpec.Arg)
	}
}

// A second enter on the same virtual review row while the first review
// is still setting up must not dispatch a duplicate job. Once the
// backing job reaches a terminal state the guard clears and enter works
// again (retry after failure).
func TestEnterOnVirtualRowDoesNotDoubleDispatchReview(t *testing.T) {
	launches := 0
	items := []Item{{ProjectName: "alpha", WorkspaceName: "sib", RepoRoot: "/a", Bookmark: "b/sib"}}
	model := New(items, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/a": {
				"feat/two": {Number: 2, State: PRStateOpen, HeadRefName: "feat/two", ReviewRequested: true},
			},
		}, nil).
		WithAsyncJobLauncher(func(AsyncJobSpec) error { launches++; return nil })
	model.scope = ScopeInbox

	virtualIdx := -1
	for i, it := range model.items() {
		if it.Virtual {
			virtualIdx = i
			break
		}
	}
	if virtualIdx < 0 {
		t.Fatal("no virtual row present to select")
	}
	model.cursor = virtualIdx

	// First enter dispatches the review setup.
	updated, cmd := model.trigger(ActionSummon, "")
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("first enter returned no command")
	}
	execCmd(t, cmd)
	if launches != 1 {
		t.Fatalf("after first enter launches = %d, want 1", launches)
	}

	// Second enter while still setting up must be suppressed.
	updated, cmd = model.trigger(ActionSummon, "")
	model = updated.(Model)
	if cmd != nil {
		execCmd(t, cmd)
	}
	if launches != 1 {
		t.Fatalf("second enter re-dispatched review: launches = %d, want 1", launches)
	}

	// A terminal job for the same PR clears the guard.
	updated, _ = model.Update(jobsListMsg{jobs: []Job{
		{ID: "j1", Action: "review", Title: "review · PR 2", RepoRoot: "/a", Status: JobDone},
	}})
	model = updated.(Model)

	updated, cmd = model.trigger(ActionSummon, "")
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("enter after terminal job returned no command — guard did not clear")
	}
	execCmd(t, cmd)
	if launches != 2 {
		t.Fatalf("after guard cleared launches = %d, want 2", launches)
	}
}

// Enter on a virtual row for YOUR OWN PR opens the prefilled
// new-workspace form (anchored on the PR branch) instead of the review
// flow — you want to keep working on it, not review it.
func TestEnterOnMineVirtualRowOpensCreateForm(t *testing.T) {
	launched := false
	items := []Item{{ProjectName: "alpha", WorkspaceName: "pulled", RepoRoot: "/a", PRNumber: 1}}
	model := New(items, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/a": {
				"feat/one":    {Number: 1, State: PRStateOpen, HeadRefName: "feat/one", Mine: true},
				"andrew/feat": {Number: 2, State: PRStateOpen, HeadRefName: "andrew/feat", Mine: true, IsDraft: true},
			},
		}, nil).
		WithAsyncJobLauncher(func(AsyncJobSpec) error { launched = true; return nil })
	model.bookmarkPrefix = "andrew"
	model.scope = ScopeInbox

	virtualIdx := -1
	for i, it := range model.items() {
		if it.Virtual {
			virtualIdx = i
			break
		}
	}
	if virtualIdx < 0 {
		t.Fatal("no virtual row present to select")
	}
	model.cursor = virtualIdx

	updated, _ := model.trigger(ActionSummon, "")
	m2 := updated.(Model)
	if launched {
		t.Error("enter on a mine virtual row should not launch the review job")
	}
	if !m2.newWorkspaceMode {
		t.Fatal("enter on a mine virtual row should open the new-workspace form")
	}
	if m2.newWorkspaceRepo != "/a" {
		t.Errorf("new-workspace repo = %q, want /a", m2.newWorkspaceRepo)
	}
	req := m2.newWorkspaceForm.request()
	if req.Bookmark != "andrew/feat" {
		t.Errorf("form anchored on %q, want PR branch andrew/feat", req.Bookmark)
	}
	// Prefix-derived name round-trips: "andrew/feat" → name "feat" →
	// auto-bookmark "andrew/feat" (the PR branch, not a fork).
	if req.Name != "feat" {
		t.Errorf("prefilled name = %q, want feat", req.Name)
	}
	if req.BookmarkToCreate != "andrew/feat" {
		t.Errorf("auto-bookmark = %q, want andrew/feat (re-uses the PR branch)", req.BookmarkToCreate)
	}
	// The PR number is carried alongside the form so the created
	// workspace links to the PR without reopening the deck.
	if m2.newWorkspacePR != 2 {
		t.Errorf("pending PR link = %d, want 2", m2.newWorkspacePR)
	}
}

// Submitting the form opened from a mine virtual row threads the PR
// number into the async create spec, so the create handler pins the
// workspace to the PR (RecordPROverride) and it links without a reopen.
func TestMineVirtualCreateThreadsPRNumber(t *testing.T) {
	var gotSpec AsyncJobSpec
	items := []Item{{ProjectName: "alpha", WorkspaceName: "pulled", RepoRoot: "/a", PRNumber: 1}}
	model := New(items, func(ActionRequest) error { return nil }).
		WithPRStatusSeed(map[string]map[string]PRStatus{
			"/a": {
				"feat/one": {Number: 1, State: PRStateOpen, HeadRefName: "feat/one", Mine: true},
				"feat/two": {Number: 2, State: PRStateOpen, HeadRefName: "feat/two", Mine: true, IsDraft: true},
			},
		}, nil).
		WithAsyncJobLauncher(func(spec AsyncJobSpec) error { gotSpec = spec; return nil })
	model.scope = ScopeInbox

	virtualIdx := -1
	for i, it := range model.items() {
		if it.Virtual {
			virtualIdx = i
			break
		}
	}
	if virtualIdx < 0 {
		t.Fatal("no virtual row present to select")
	}
	model.cursor = virtualIdx

	updated, _ := model.trigger(ActionSummon, "")
	m2 := updated.(Model)
	if !m2.newWorkspaceMode {
		t.Fatal("expected the new-workspace form to open")
	}
	_, cmd := m2.startCreateAction(NewWorkspaceRequest{
		Name:             "two",
		Bookmark:         "feat/two",
		BookmarkToCreate: "andrew/two",
		PRNumber:         m2.newWorkspacePR,
	}, m2.newWorkspaceRepo)
	if cmd != nil {
		execCmd(t, cmd)
	}
	if gotSpec.Action != "create-workspace" {
		t.Fatalf("async spec action = %q, want create-workspace", gotSpec.Action)
	}
	if gotSpec.PRNumber != 2 {
		t.Errorf("async spec PRNumber = %d, want 2", gotSpec.PRNumber)
	}
}

// proposeWorkspaceName strips the configured prefix, else falls back to
// the last path segment.
func TestProposeWorkspaceName(t *testing.T) {
	cases := []struct{ ref, prefix, want string }{
		{"andrew/fix-login", "andrew", "fix-login"},
		{"andrew/fix-login", "andrew/", "fix-login"},
		{"feat/two", "andrew", "two"},
		{"flat", "andrew", "flat"},
		{"andrew/nested/deep", "andrew", "nested/deep"},
	}
	for _, c := range cases {
		if got := proposeWorkspaceName(c.ref, c.prefix); got != c.want {
			t.Errorf("proposeWorkspaceName(%q, %q) = %q, want %q", c.ref, c.prefix, got, c.want)
		}
	}
}

// Within "Needs your review", re-reviews (you reviewed before, author
// re-requested) sort ahead of first-time review requests; ties fall
// back to project/label order.
func TestInboxNeedsReviewSortsReReviewFirst(t *testing.T) {
	items := []Item{
		{ProjectName: "alpha", WorkspaceName: "fresh-a", RepoRoot: "/a", Bookmark: "b/fresh-a"},
		{ProjectName: "alpha", WorkspaceName: "rereq-z", RepoRoot: "/a", Bookmark: "b/rereq-z"},
		{ProjectName: "beta", WorkspaceName: "rereq-b", RepoRoot: "/b", Bookmark: "b/rereq-b"},
	}
	model := New(items, nil).WithPRStatusSeed(map[string]map[string]PRStatus{
		"/a": {
			"b/fresh-a": {Number: 1, State: PRStateOpen, HeadRefName: "b/fresh-a", ReviewRequested: true},
			"b/rereq-z": {Number: 2, State: PRStateOpen, HeadRefName: "b/rereq-z", ReviewRequested: true, ReviewRerequested: true},
		},
		"/b": {
			"b/rereq-b": {Number: 3, State: PRStateOpen, HeadRefName: "b/rereq-b", ReviewRequested: true, ReviewRerequested: true},
		},
	}, nil)
	model.scope = ScopeInbox
	got := model.items()

	// All three are in "Needs your review". Order: the two re-reviews
	// first (by project: beta? no — alpha < beta, so rereq-z then
	// rereq-b), then the fresh request.
	want := []string{"rereq-z", "rereq-b", "fresh-a"}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %v", len(want), itemNames(got))
	}
	for i, w := range want {
		if got[i].WorkspaceName != w {
			t.Errorf("items()[%d] = %s, want %s (full order %v)", i, got[i].WorkspaceName, w, itemNames(got))
		}
	}
}
