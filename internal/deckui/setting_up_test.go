package deckui

import (
	"strings"
	"testing"
)

func settingUpModel(t *testing.T) Model {
	t.Helper()
	items := []Item{{ProjectName: "alpha", WorkspaceName: "feat-x", RepoRoot: "/a"}}
	m := New(items, func(ActionRequest) error { return nil })
	// A create-workspace job for this row is still running (bootstrap
	// hooks not finished). WorkspaceName carries the un-normalized name
	// the deck dispatched with; matching normalizes it.
	m.jobs = []Job{{
		ID:            "1",
		Action:        "create-workspace",
		Status:        JobRunning,
		RepoRoot:      "/a",
		WorkspaceName: "Feat X",
		Steps:         []JobStep{{Label: "pnpm i"}},
	}}
	m.cursor = 0
	return m
}

func TestWorkspaceSettingUpMatchesRunningCreateJob(t *testing.T) {
	m := settingUpModel(t)
	item := m.items()[0]
	if !m.workspaceSettingUp(item) {
		t.Fatal("row with a running create job should read as setting up")
	}
	job, ok := m.workspaceSetupJob(item)
	if !ok {
		t.Fatal("expected a matching setup job")
	}
	if lbl := workspaceSetupStepLabel(job); lbl != "pnpm i" {
		t.Errorf("setup step label = %q, want pnpm i", lbl)
	}
}

func TestWorkspaceSettingUpIgnoresTerminalAndMismatched(t *testing.T) {
	m := settingUpModel(t)
	item := m.items()[0]

	// Terminal job: no longer setting up.
	m.jobs[0].Status = JobDone
	if m.workspaceSettingUp(item) {
		t.Error("a done create job should not count as setting up")
	}

	// Different repo: no match.
	m.jobs = []Job{{Action: "create-workspace", Status: JobRunning, RepoRoot: "/other", WorkspaceName: "feat-x"}}
	if m.workspaceSettingUp(item) {
		t.Error("a create job in a different repo should not match")
	}

	// Different workspace name: no match.
	m.jobs = []Job{{Action: "create-workspace", Status: JobRunning, RepoRoot: "/a", WorkspaceName: "other"}}
	if m.workspaceSettingUp(item) {
		t.Error("a create job for another workspace should not match")
	}

	// Non-create action: no match.
	m.jobs = []Job{{Action: "review", Status: JobRunning, RepoRoot: "/a", WorkspaceName: "feat-x"}}
	if m.workspaceSettingUp(item) {
		t.Error("a non-create job should not count as setting up")
	}
}

// Summoning (enter) a row whose workspace is still being created must be
// held — the tmux session + agent don't exist yet.
func TestSummonBlockedWhileSettingUp(t *testing.T) {
	m := settingUpModel(t)
	updated, cmd := m.trigger(ActionSummon, "")
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("summon on a setting-up row should not dispatch an action")
	}
	if !strings.Contains(got.status, "setting up") {
		t.Errorf("status = %q, want it to mention setting up", got.status)
	}
	if !strings.Contains(got.status, "pnpm i") {
		t.Errorf("status = %q, want it to name the current step", got.status)
	}
}

// Once the create job finishes, summon works again.
func TestSummonAllowedAfterSetupFinishes(t *testing.T) {
	m := settingUpModel(t)
	m.jobs[0].Status = JobDone
	updated, cmd := m.trigger(ActionSummon, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("summon should dispatch once the workspace is ready")
	}
	if strings.Contains(got.status, "setting up") {
		t.Errorf("status = %q, should not mention setting up after done", got.status)
	}
}
