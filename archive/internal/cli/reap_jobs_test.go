package cli

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/jobs"
)

// Deleting a workspace stops the jobs it was running.
//
// The leak was a background user action: a detached `sh -c <command>` with its cwd
// in the working copy, still running after the working copy had been removed. tmux
// sessions and zmx sessions were both swept (#246); jobs are the third substrate and
// were not (#265).

// fakeLedger is a job store's worth of records, and a note of what was signalled.
type fakeLedger struct {
	records   []jobs.Job
	signalled []jobs.JobID
	err       error
	refuse    map[jobs.JobID]bool
}

func (f *fakeLedger) List() ([]jobs.Job, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

func (f *fakeLedger) SignalCancel(id jobs.JobID) error {
	if f.refuse[id] {
		return errors.New("no such process")
	}
	f.signalled = append(f.signalled, id)
	return nil
}

func (f *fakeLedger) signalledContains(id jobs.JobID) bool {
	return slices.Contains(f.signalled, id)
}

// runningJob is an active record for a workspace.
func runningJob(id, repo, workspace string, action jobs.JobAction) jobs.Job {
	return jobs.Job{
		ID:     jobs.JobID(id),
		Title:  string(action) + " " + workspace,
		Status: jobs.StatusRunning,
		Spec:   jobs.Spec{Action: action, RepoRoot: repo, WorkspaceName: workspace},
	}
}

// TestDeletingAWorkspaceStopsItsBackgroundJob — the leak itself.
func TestDeletingAWorkspaceStopsItsBackgroundJob(t *testing.T) {
	ledger := &fakeLedger{records: []jobs.Job{
		runningJob("j1", "/repo", "feat", jobs.ActionCustom),
	}}
	if err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if !ledger.signalledContains("j1") {
		t.Errorf("the workspace's running job was left alone, signalled=%v", ledger.signalled)
	}
}

// TestAnotherWorkspacesJobIsNotTouched, and neither is the same-named workspace in
// another repo — which is the case that makes matching on the name alone unsafe.
func TestAnotherWorkspacesJobIsNotTouched(t *testing.T) {
	ledger := &fakeLedger{records: []jobs.Job{
		runningJob("mine", "/repo", "feat", jobs.ActionCustom),
		runningJob("sibling", "/repo", "other", jobs.ActionCustom),
		runningJob("namesake", "/elsewhere", "feat", jobs.ActionCustom),
	}}
	if err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if got := ledger.signalled; len(got) != 1 || got[0] != "mine" {
		t.Errorf("signalled %v, want only [mine]", got)
	}
}

// TestAFinishedJobIsNotSignalled. Its pid is gone or reused, and a record that has
// already ended has nothing to stop.
func TestAFinishedJobIsNotSignalled(t *testing.T) {
	done := runningJob("done", "/repo", "feat", jobs.ActionCustom)
	done.Status = jobs.StatusDone
	ledger := &fakeLedger{records: []jobs.Job{done}}
	if err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if len(ledger.signalled) != 0 {
		t.Errorf("a finished job was signalled: %v", ledger.signalled)
	}
}

// TestTheSweepDoesNotKillTheDeleteRunningIt. The sweep runs *inside* the delete job,
// so the delete's own record matches every other filter — and signalling it would
// SIGTERM the process doing the deleting, half way through.
func TestTheSweepDoesNotKillTheDeleteRunningIt(t *testing.T) {
	self := runningJob("self", "/repo", "feat", jobs.ActionDelete)
	self.PID = os.Getpid()
	ledger := &fakeLedger{records: []jobs.Job{self}}
	if err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if len(ledger.signalled) != 0 {
		t.Fatalf("the sweep signalled the delete running it: %v", ledger.signalled)
	}
}

// TestAnotherDeleteIsLeftToFinish. Interrupting a tear-down halfway leaves exactly
// the half-reaped state this path exists to prevent, so a second delete of the same
// workspace is not ours to stop even though it is not us.
func TestAnotherDeleteIsLeftToFinish(t *testing.T) {
	other := runningJob("other-delete", "/repo", "feat", jobs.ActionDelete)
	other.PID = os.Getpid() + 1
	ledger := &fakeLedger{records: []jobs.Job{other}}
	if err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil); err != nil {
		t.Fatalf("sweeping: %v", err)
	}
	if len(ledger.signalled) != 0 {
		t.Errorf("a concurrent delete was interrupted: %v", ledger.signalled)
	}
}

// TestAJobThatWillNotStopIsNamed. The workspace is already gone, so this cannot fail
// the delete — but a command still running in a removed working copy is the whole
// bug, and silence would leave no way to find it.
func TestAJobThatWillNotStopIsNamed(t *testing.T) {
	ledger := &fakeLedger{
		records: []jobs.Job{runningJob("stubborn", "/repo", "feat", jobs.ActionCustom)},
		refuse:  map[jobs.JobID]bool{"stubborn": true},
	}
	err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil)
	if err == nil {
		t.Fatal("a job that refused to stop was reported as swept")
	}
	if !strings.Contains(err.Error(), "stubborn") {
		t.Errorf("the error %q does not name the job", err)
	}
	if !strings.Contains(err.Error(), "feat") {
		t.Errorf("the error %q does not name the workspace", err)
	}
}

// TestAnUnreadableLedgerIsReported rather than treated as no jobs. "The store would
// not open" and "this workspace had nothing running" are the same silence otherwise,
// and only one of them is fine.
func TestAnUnreadableLedgerIsReported(t *testing.T) {
	ledger := &fakeLedger{err: errors.New("permission denied")}
	err := cancelWorkspaceJobs(ledger, "/repo", "feat", nil)
	if err == nil {
		t.Fatal("an unreadable job store swept cleanly")
	}
	if !strings.Contains(err.Error(), "feat") {
		t.Errorf("the error %q does not say which workspace was being deleted", err)
	}
}
