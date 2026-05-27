package deckui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

func TestStartActivityAppendsEntry(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("pr-status", "pr-status", 3)
	if len(m.activities) != 1 {
		t.Fatalf("expected 1 activity, got %d", len(m.activities))
	}
	a := m.activities[0]
	if a.ID != "pr-status" || a.Label != "pr-status" || a.Total != 3 || a.Done != 0 {
		t.Fatalf("unexpected activity: %+v", a)
	}
}

func TestStartActivityIsIdempotentByID(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("enrich", "enrich", 0)
	m = m.tickActivity("enrich", 1) // sanity: arbitrary tick is allowed
	m = m.startActivity("enrich", "enrich", 0)
	if len(m.activities) != 1 {
		t.Fatalf("expected dedup by ID, got %d entries", len(m.activities))
	}
	if m.activities[0].Done != 0 {
		t.Fatalf("expected Done reset on restart, got %d", m.activities[0].Done)
	}
}

func TestStartActivityIgnoresEmptyID(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("", "noop", 0)
	if len(m.activities) != 0 {
		t.Fatalf("expected empty ID to be ignored, got %d entries", len(m.activities))
	}
}

func TestTickActivityIncrementsDoneClampedToTotal(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("pr-status", "pr-status", 2)
	m = m.tickActivity("pr-status", 1)
	if m.activities[0].Done != 1 {
		t.Fatalf("expected Done=1, got %d", m.activities[0].Done)
	}
	m = m.tickActivity("pr-status", 5) // over-tick is clamped
	if m.activities[0].Done != 2 {
		t.Fatalf("expected Done clamped to 2, got %d", m.activities[0].Done)
	}
}

func TestTickActivityUnknownIDIsNoop(t *testing.T) {
	m := New(nil, nil)
	m = m.tickActivity("missing", 1)
	if len(m.activities) != 0 {
		t.Fatalf("expected no activities, got %d", len(m.activities))
	}
}

func TestFinishActivityFlashesAndScheduledExpire(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("workspace:create:foo", "workspace:create:foo", 0)
	m, cmd := m.finishActivity("workspace:create:foo")
	if cmd == nil {
		t.Fatal("expected expire cmd")
	}
	if len(m.activities) != 1 {
		t.Fatalf("expected activity to stay during flash, got %d", len(m.activities))
	}
	if m.activities[0].FinishedAt.IsZero() {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestFinishActivityUnknownIDReturnsNilCmd(t *testing.T) {
	m := New(nil, nil)
	_, cmd := m.finishActivity("missing")
	if cmd != nil {
		t.Fatal("expected nil cmd for unknown id")
	}
}

func TestActivityExpireMsgDropsEntry(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("workspace:link:foo", "workspace:link:foo", 0)
	m, _ = m.finishActivity("workspace:link:foo")
	updated, _ := m.Update(activityExpireMsg{id: "workspace:link:foo"})
	if got := updated.(Model); len(got.activities) != 0 {
		t.Fatalf("expected activity to drop on expire, got %d", len(got.activities))
	}
}

func TestRenderActivitiesCompactEmpty(t *testing.T) {
	if got := renderActivitiesCompact(nil, "⠼"); got != "" {
		t.Fatalf("expected empty render, got %q", got)
	}
}

func TestRenderActivitiesCompactShowsProgressFraction(t *testing.T) {
	out := renderActivitiesCompact([]Activity{
		{ID: "pr-status", Label: "pr-status", Done: 2, Total: 5},
	}, "⠼")
	if !strings.Contains(out, "2/5") {
		t.Fatalf("expected progress fraction, got %q", out)
	}
}

func TestRenderActivitiesCompactSeparator(t *testing.T) {
	out := renderActivitiesCompact([]Activity{
		{ID: "pr-status", Label: "pr-status", Done: 1, Total: 5},
		{ID: "enrich", Label: "enrich"},
	}, "⠼")
	if !strings.Contains(out, "·") {
		t.Fatalf("expected separator between activities, got %q", out)
	}
}

func TestRenderActivitiesCompactFinishedFlashesCheck(t *testing.T) {
	out := renderActivitiesCompact([]Activity{
		{ID: "workspace:create:foo", Label: "workspace:create:foo", FinishedAt: time.Now()},
	}, "⠼")
	if !strings.Contains(out, "✓") {
		t.Fatalf("expected ✓ flash for finished activity, got %q", out)
	}
}

func TestRenderActivitiesCompactUsesGlyphOverride(t *testing.T) {
	out := renderActivitiesCompact([]Activity{
		{ID: jobActivityIDPrefix + "abc", Label: "create · x", Glyph: "⚠", Color: "203"},
	}, "⠼")
	if !strings.Contains(out, "⚠") {
		t.Fatalf("expected ⚠ glyph override, got %q", out)
	}
}

func TestSyncJobActivitiesAddsRunningJobs(t *testing.T) {
	m := New(nil, nil)
	jobs := []Job{
		{ID: "j1", Title: "create · foo", Status: JobRunning, StartedAt: time.Now()},
		{ID: "j2", Title: "delete · bar", Status: JobPending, StartedAt: time.Now()},
	}
	m, _ = m.syncJobActivities(jobs)
	if len(m.activities) != 2 {
		t.Fatalf("expected 2 job-derived activities, got %d", len(m.activities))
	}
}

func TestSyncJobActivitiesMarksTerminalCleanForFlash(t *testing.T) {
	m := New(nil, nil)
	first := []Job{{ID: "j1", Title: "create · foo", Status: JobRunning, StartedAt: time.Now()}}
	m, _ = m.syncJobActivities(first)
	final := []Job{{ID: "j1", Title: "create · foo", Status: JobDone, StartedAt: time.Now()}}
	m, cmd := m.syncJobActivities(final)
	if cmd == nil {
		t.Fatal("expected expire cmd for terminal-clean job")
	}
	if len(m.activities) != 1 {
		t.Fatalf("expected activity to remain during flash, got %d", len(m.activities))
	}
	if m.activities[0].FinishedAt.IsZero() {
		t.Fatal("expected FinishedAt set on terminal job")
	}
}

func TestSyncJobActivitiesKeepsErroredJobs(t *testing.T) {
	m := New(nil, nil)
	jobs := []Job{{ID: "j1", Title: "create · foo", Status: JobError, StartedAt: time.Now()}}
	m, cmd := m.syncJobActivities(jobs)
	if cmd != nil {
		t.Fatal("errored jobs should not auto-expire")
	}
	if len(m.activities) != 1 {
		t.Fatalf("expected error activity, got %d", len(m.activities))
	}
	if m.activities[0].Glyph != "⚠" {
		t.Fatalf("expected ⚠ glyph for error, got %q", m.activities[0].Glyph)
	}
}

func TestSyncJobActivitiesDropsDismissedJobs(t *testing.T) {
	m := New(nil, nil)
	m, _ = m.syncJobActivities([]Job{{ID: "j1", Title: "create · foo", Status: JobRunning, StartedAt: time.Now()}})
	m, _ = m.syncJobActivities(nil) // job dismissed
	if len(m.activities) != 0 {
		t.Fatalf("expected job activity dropped on dismiss, got %d", len(m.activities))
	}
}

func TestSyncJobActivitiesPreservesExplicitActivities(t *testing.T) {
	m := New(nil, nil)
	m = m.startActivity("pr-status", "pr-status", 3)
	m, _ = m.syncJobActivities([]Job{{ID: "j1", Title: "create · foo", Status: JobRunning, StartedAt: time.Now()}})
	if len(m.activities) != 2 {
		t.Fatalf("expected explicit + job activities, got %d", len(m.activities))
	}
	// pr-status survives a sync that has no matching job.
	m, _ = m.syncJobActivities(nil)
	if len(m.activities) != 1 || m.activities[0].ID != "pr-status" {
		t.Fatalf("expected pr-status to survive, got %+v", m.activities)
	}
}

// Compile-time guard that activityExpireMsg satisfies tea.Msg.
var _ tea.Msg = activityExpireMsg{}

func TestInitKickStartsPRStatusActivity(t *testing.T) {
	called := 0
	model := New([]Item{
		// PRNumber > 0 marks the workspace as PR-status eligible. The
		// "Path != RepoRoot" heuristic was removed in favor of explicit
		// PR association (see prStatusReposPolicy).
		{ProjectName: "repo-a", WorkspaceName: "ws", RepoRoot: "/r/a", Path: "/r/a/ws", PRNumber: 1},
	}, nil).WithPRStatusFetcher(func(repos []string) tea.Cmd {
		called++
		return func() tea.Msg { return PRStatusDoneMsg{FetchedAt: time.Now()} }
	})

	updated, _ := model.Update(initKickMsg{})
	m := updated.(Model)
	if called != 1 {
		t.Fatalf("expected fetcher to dispatch, got called=%d", called)
	}
	if !m.hasActivity("pr-status") {
		t.Fatalf("expected pr-status activity, got %+v", m.activities)
	}
	if m.activities[0].Total != 1 {
		t.Fatalf("expected Total=1, got %d", m.activities[0].Total)
	}
}

func TestRefreshDoneTriggersPRStatusForNewlyEligibleRepo(t *testing.T) {
	called := 0
	var fetchedRepos []string
	model := New(nil, nil).WithPRStatusFetcher(func(repos []string) tea.Cmd {
		called++
		fetchedRepos = append([]string(nil), repos...)
		return func() tea.Msg { return PRStatusDoneMsg{FetchedAt: time.Now()} }
	})
	// Simulate refreshDoneMsg landing with a new PR-bearing workspace
	// in a repo the deck never saw before (typical awp-review-from-
	// outside scenario). The refresh handler must dispatch a fetch
	// for that repo without waiting on a separate trigger.
	updated, _ := model.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "ws", RepoRoot: "/r", PRNumber: 7},
	}})
	_ = updated
	if called != 1 {
		t.Fatalf("expected fetcher to be invoked once on refreshDoneMsg, got %d", called)
	}
	if len(fetchedRepos) != 1 || fetchedRepos[0] != "/r" {
		t.Fatalf("expected fetcher called for /r, got %v", fetchedRepos)
	}
}

func TestRefreshDoneSkipsRepoWithNeitherPRNumberNorBookmark(t *testing.T) {
	called := 0
	model := New(nil, nil).WithPRStatusFetcher(func(repos []string) tea.Cmd {
		called++
		return func() tea.Msg { return PRStatusDoneMsg{FetchedAt: time.Now()} }
	})
	updated, _ := model.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "default", RepoRoot: "/r"},
	}})
	_ = updated
	if called != 0 {
		t.Fatalf("expected no fetch for workspace with neither PRNumber nor Bookmark, got %d", called)
	}
}

func TestRefreshDoneTriggersPRStatusForBookmarkOnlyWorkspace(t *testing.T) {
	// Regression: legacy entries with Bookmark but no PRNumber (created
	// before the rename) must keep their repo eligible — otherwise the
	// on-load Bookmark→PRNumber migration has no cache data to match
	// against, and the entries stay un-linked forever.
	called := 0
	var fetchedRepos []string
	model := New(nil, nil).WithPRStatusFetcher(func(repos []string) tea.Cmd {
		called++
		fetchedRepos = append([]string(nil), repos...)
		return func() tea.Msg { return PRStatusDoneMsg{FetchedAt: time.Now()} }
	})
	updated, _ := model.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "feat", RepoRoot: "/r", Bookmark: "andrew/feat"},
	}})
	_ = updated
	if called != 1 {
		t.Fatalf("expected fetcher to be invoked for bookmark-only workspace, got %d", called)
	}
	if len(fetchedRepos) != 1 || fetchedRepos[0] != "/r" {
		t.Fatalf("expected fetcher called for /r, got %v", fetchedRepos)
	}
}

func TestPRStatusRepoDoneTicksActivity(t *testing.T) {
	model := New(nil, nil)
	model = model.startActivity("pr-status", "pr-status", 3)
	updated, _ := model.Update(PRStatusRepoDoneMsg{Repo: "/r/a", ByHead: map[string]PRStatus{}})
	m := updated.(Model)
	if m.activities[0].Done != 1 {
		t.Fatalf("expected Done=1 after repo report, got %d", m.activities[0].Done)
	}
}

func TestPRStatusDoneFinishesActivity(t *testing.T) {
	model := New(nil, nil)
	model = model.startActivity("pr-status", "pr-status", 1)
	updated, cmd := model.Update(PRStatusDoneMsg{FetchedAt: time.Now()})
	if cmd == nil {
		t.Fatal("expected expire cmd from final PRStatusDoneMsg")
	}
	m := updated.(Model)
	if m.activities[0].FinishedAt.IsZero() {
		t.Fatal("expected pr-status to be marked finished")
	}
}

func TestRefreshTickDoesNotRegisterEnrichActivity(t *testing.T) {
	model := New(nil, nil).WithRefresher(func() tea.Cmd {
		return func() tea.Msg { return RefreshDoneMsg(nil, nil) }
	})
	updated, _ := model.Update(refreshTickMsg(time.Now()))
	m := updated.(Model)
	if m.hasActivity("enrich") {
		t.Fatalf("periodic refresh should not register enrich activity, got %+v", m.activities)
	}
}

func TestInitKickRegistersEnrichActivity(t *testing.T) {
	model := New([]Item{{WorkspaceName: "ws"}}, nil).WithRefresher(func() tea.Cmd {
		return func() tea.Msg { return RefreshDoneMsg(nil, nil) }
	})
	updated, _ := model.Update(initKickMsg{})
	m := updated.(Model)
	if !m.hasActivity("enrich") {
		t.Fatalf("expected enrich activity after initKickMsg, got %+v", m.activities)
	}
}

func TestRenameSubmitStartsAndFinishesActivity(t *testing.T) {
	target := Item{ProjectName: "repo", WorkspaceName: "old"}
	model := New([]Item{target}, func(req ActionRequest) error { return nil })
	model.renameMode = true
	form, _ := newRenameWorkspaceForm(target)
	model.renameForm = form
	*model.renameForm.nameVal = "new"
	// Short-circuit huh's keystream by setting state directly; same
	// pattern as the new-workspace form test.
	model.renameForm.form.State = huh.StateCompleted

	updated, _ := model.dispatchRenameForm(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(Model)
	if !m.hasActivity("workspace:rename:old") {
		t.Fatalf("expected workspace:rename:old activity, got %+v", m.activities)
	}

	// Simulate the handler completing — actionResultMsg should finish.
	updated, _ = m.Update(actionResultMsg{action: ActionRename, arg: "new", item: target})
	m = updated.(Model)
	a := findActivity(m, "workspace:rename:old")
	if a == nil {
		t.Fatalf("expected rename activity to still exist during flash, got %+v", m.activities)
	}
	if a.FinishedAt.IsZero() {
		t.Fatal("expected rename activity to be finished")
	}
}

func findActivity(m Model, id string) *Activity {
	for i := range m.activities {
		if m.activities[i].ID == id {
			return &m.activities[i]
		}
	}
	return nil
}
