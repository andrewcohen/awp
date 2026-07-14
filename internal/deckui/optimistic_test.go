package deckui

import (
	"strings"
	"testing"
)

func TestAddOptimisticCreateMergesIntoItems(t *testing.T) {
	m := New([]Item{{ProjectName: "alpha", WorkspaceName: "default", RepoRoot: "/a"}}, func(ActionRequest) error { return nil })
	// User typed a display name; the stored row will be normalized.
	m.addOptimisticCreate(Item{WorkspaceName: "Feat X", RepoRoot: "/a", Status: "starting"})

	found := false
	for _, it := range m.items() {
		if it.WorkspaceName == "feat-x" {
			found = true
			if !it.Optimistic {
				t.Error("merged optimistic row should carry Optimistic=true")
			}
			if it.ProjectName != "alpha" {
				t.Errorf("optimistic row project = %q, want alpha (copied from same repo)", it.ProjectName)
			}
		}
	}
	if !found {
		t.Fatal("optimistic create row should appear in items() before the real row lands")
	}
}

func TestMergedItemsDropsUnmanagedRowMidDelete(t *testing.T) {
	// After a delete job finishes, the state row is gone but the tmux session
	// lingers (kill deferred to popup exit), so loadDeckItems surfaces it as
	// an "unmanaged" adoptable row. A delete job for that workspace should
	// suppress it so it doesn't flash back as "(live tmux session, not in
	// store)".
	m := New(nil, func(ActionRequest) error { return nil })
	m.itemsAll = []Item{
		{ProjectName: "alpha", WorkspaceName: "feat-x", Status: "unmanaged", SessionName: "[awp]alpha__feat-x"},
	}
	m.jobs = []Job{
		{Action: "delete", Status: JobDone, RepoRoot: "/repos/alpha", WorkspaceName: "feat-x"},
	}
	for _, it := range m.mergedItemsAll() {
		if it.WorkspaceName == "feat-x" {
			t.Fatalf("unmanaged row for a deleted workspace should be suppressed; got %+v", it)
		}
	}

	// Without the delete job, the unmanaged row is a genuine orphan and stays.
	m.jobs = nil
	found := false
	for _, it := range m.mergedItemsAll() {
		if it.WorkspaceName == "feat-x" {
			found = true
		}
	}
	if !found {
		t.Fatal("a genuine unmanaged row (no delete job) should still appear")
	}
}

func TestAddOptimisticCreateSkipsEmptyName(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "   ", RepoRoot: "/a"})
	if len(m.optimisticCreates) != 0 {
		t.Fatal("a blank workspace name should not produce an optimistic row")
	}
}

func TestMergedItemsDropsOptimisticWhenRealRowExists(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "feat-x", RepoRoot: "/a"})
	// The real row lands.
	m.itemsAll = []Item{{ProjectName: "alpha", WorkspaceName: "feat-x", RepoRoot: "/a"}}

	count := 0
	for _, it := range m.mergedItemsAll() {
		if it.WorkspaceName == "feat-x" {
			count++
			if it.Optimistic {
				t.Error("the real row should win over the optimistic one")
			}
		}
	}
	if count != 1 {
		t.Fatalf("feat-x should appear exactly once, got %d", count)
	}
}

func TestPruneOptimisticCreatesRealRowLanded(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "feat-x", RepoRoot: "/a"})
	m.itemsAll = []Item{{WorkspaceName: "feat-x", RepoRoot: "/a"}}
	m.pruneOptimisticCreates()
	if len(m.optimisticCreates) != 0 {
		t.Fatal("optimistic row should be retired once the real row lands")
	}
}

func TestPruneOptimisticCreatesKeepsWhilePending(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "feat-x", RepoRoot: "/a"})
	// A running create job, no real row yet.
	m.jobs = []Job{{Action: "create-workspace", Status: JobRunning, RepoRoot: "/a", WorkspaceName: "feat-x"}}
	m.pruneOptimisticCreates()
	if len(m.optimisticCreates) != 1 {
		t.Fatal("optimistic row should survive while its create is still in flight")
	}
}

func TestPruneOptimisticCreatesDropsOnFailedJob(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "feat-x", RepoRoot: "/a"})
	m.jobs = []Job{{Action: "create-workspace", Status: JobError, RepoRoot: "/a", WorkspaceName: "feat-x"}}
	m.pruneOptimisticCreates()
	if len(m.optimisticCreates) != 0 {
		t.Fatal("optimistic row should be retired once its create job fails")
	}
}

func TestPruneOptimisticCreatesKeepsOnDoneWithoutRealRow(t *testing.T) {
	// A done create always wrote a real row; the real-row check (not the
	// terminal-job check) reconciles it. A done job with no real row yet
	// must not drop the optimistic row, or it would flicker away and back.
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "feat-x", RepoRoot: "/a"})
	m.jobs = []Job{{Action: "create-workspace", Status: JobDone, RepoRoot: "/a", WorkspaceName: "feat-x"}}
	m.pruneOptimisticCreates()
	if len(m.optimisticCreates) != 1 {
		t.Fatal("a done job with no real row yet should not drop the optimistic row")
	}
}

func TestBlockActionOnOptimisticRow(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	m.addOptimisticCreate(Item{WorkspaceName: "feat-x", RepoRoot: "/a"})
	m.cursor = 0
	item := m.items()[0]
	if !item.Optimistic {
		t.Fatal("expected the optimistic row to be selected")
	}
	got, blocked := m.blockIfSettingUp(item)
	if !blocked {
		t.Fatal("actions on an optimistic row should be blocked")
	}
	if !strings.Contains(got.status, "being created") {
		t.Errorf("status = %q, want it to mention being created", got.status)
	}
}

func TestWorkspaceDeletingMatchesRunningDeleteJob(t *testing.T) {
	item := Item{ProjectName: "alpha", WorkspaceName: "feat-x", RepoRoot: "/a"}
	m := New([]Item{item}, func(ActionRequest) error { return nil })
	m.jobs = []Job{{Action: "delete", Status: JobRunning, RepoRoot: "/a", WorkspaceName: "feat-x"}}
	if !m.workspaceDeleting(item) {
		t.Fatal("row with a running delete job should read as deleting")
	}

	// Terminal delete job: no longer deleting.
	m.jobs[0].Status = JobDone
	if m.workspaceDeleting(item) {
		t.Error("a done delete job should not count as deleting")
	}

	// Different repo: no match.
	m.jobs = []Job{{Action: "delete", Status: JobRunning, RepoRoot: "/other", WorkspaceName: "feat-x"}}
	if m.workspaceDeleting(item) {
		t.Error("a delete job in a different repo should not match")
	}

	// Non-delete action: no match.
	m.jobs = []Job{{Action: "create-workspace", Status: JobRunning, RepoRoot: "/a", WorkspaceName: "feat-x"}}
	if m.workspaceDeleting(item) {
		t.Error("a non-delete job should not count as deleting")
	}
}
