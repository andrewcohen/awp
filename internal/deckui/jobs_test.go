package deckui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWorkspaceJobJustFinished(t *testing.T) {
	cases := []struct {
		name string
		prev []Job
		cur  []Job
		want bool
	}{
		{
			name: "create running -> done",
			prev: []Job{{ID: "1", Action: "create-workspace", Status: JobRunning}},
			cur:  []Job{{ID: "1", Action: "create-workspace", Status: JobDone}},
			want: true,
		},
		{
			name: "review pending -> done",
			prev: []Job{{ID: "1", Action: "review", Status: JobPending}},
			cur:  []Job{{ID: "1", Action: "review", Status: JobDone}},
			want: true,
		},
		{
			name: "done job appears with no prior record",
			prev: nil,
			cur:  []Job{{ID: "1", Action: "create-workspace", Status: JobDone}},
			want: true,
		},
		{
			name: "already done stays done",
			prev: []Job{{ID: "1", Action: "create-workspace", Status: JobDone}},
			cur:  []Job{{ID: "1", Action: "create-workspace", Status: JobDone}},
			want: false,
		},
		{
			name: "still running",
			prev: []Job{{ID: "1", Action: "create-workspace", Status: JobRunning}},
			cur:  []Job{{ID: "1", Action: "create-workspace", Status: JobRunning}},
			want: false,
		},
		{
			name: "error does not count",
			prev: []Job{{ID: "1", Action: "create-workspace", Status: JobRunning}},
			cur:  []Job{{ID: "1", Action: "create-workspace", Status: JobError}},
			want: false,
		},
		{
			name: "non-workspace job ignored",
			prev: []Job{{ID: "1", Action: "delete", Status: JobRunning}},
			cur:  []Job{{ID: "1", Action: "delete", Status: JobDone}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workspaceJobJustFinished(tc.prev, tc.cur); got != tc.want {
				t.Errorf("workspaceJobJustFinished = %v, want %v", got, tc.want)
			}
		})
	}
}

// A create job flipping to done must kick a row refresh immediately
// (registered as the "enrich" activity) rather than waiting for the
// periodic poll or the best-effort fsnotify watcher.
func TestJobsListRefreshesRowsWhenCreateFinishes(t *testing.T) {
	model := New(nil, nil).WithRefresher(func() tea.Cmd {
		return func() tea.Msg { return RefreshDoneMsg(nil, nil) }
	})
	// Prime with a running create job.
	updated, _ := model.Update(jobsListMsg{jobs: []Job{
		{ID: "1", Action: "create-workspace", Status: JobRunning},
	}})
	model = updated.(Model)
	if model.hasActivity("enrich") {
		t.Fatal("running create job should not trigger a row refresh yet")
	}

	// The job finishes.
	updated, _ = model.Update(jobsListMsg{jobs: []Job{
		{ID: "1", Action: "create-workspace", Status: JobDone},
	}})
	model = updated.(Model)
	if !model.hasActivity("enrich") {
		t.Fatal("finished create job should trigger a row refresh (enrich activity)")
	}
}
