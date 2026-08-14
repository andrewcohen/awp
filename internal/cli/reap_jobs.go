package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/jobs"
)

// Jobs are the third substrate a workspace holds, and until now the only one a
// delete did not sweep.
//
// A user action with `"background": true` is a detached `awp run-job` subprocess
// (Setsid, internal/jobs/spawn_unix.go) running `sh -c <command>` with its cwd set
// to the workspace's working copy. Nothing reaped it. So deleting the workspace
// left the command running in a directory that no longer exists — the same failure
// #246 fixed for agents in tmux and zmx sessions, one substrate over.
//
// SIGTERM rather than SIGKILL, and through the store: the run-job process traps it
// and flushes a `cancelled` record before exiting, so the jobs overlay says the job
// was cancelled instead of showing it running against a workspace that is gone.

// jobLedger is the part of jobs.Store this needs. An interface so the sweep can be
// driven in a test without a store on disk — and a small one, because a sweep that
// could do more than read and signal would be a sweep that could write a job record
// from inside a delete.
type jobLedger interface {
	List() ([]jobs.Job, error)
	SignalCancel(jobs.JobID) error
}

// The real store satisfies it, checked here so a signature change in internal/jobs
// fails at the seam rather than at the one call site.
var _ jobLedger = (*jobs.Store)(nil)

// cancelWorkspaceJobs asks every job still running for this workspace to stop.
//
// Matched on the repo *and* the workspace name, never the name alone: two projects
// can each hold a `feat`, and cancelling the wrong one is worse than the leak this
// closes.
//
// Two jobs are deliberately left alone:
//
//   - this process's own, which is how the sweep avoids killing the delete that is
//     running it. Recognised by pid rather than by an id threaded down through
//     handleDeckAction, because the pid is a fact the record already carries and an
//     id passed through four call sites is a thing one of them can forget.
//   - another delete of the same workspace. Interrupting one halfway leaves the
//     tear-down half done, which is the state this path exists to prevent.
//
// A review job is matched by the row the user was on when they pressed `r`, which is
// what Spec.WorkspaceName means — not the `pr-N-<branch>` workspace the job goes on
// to build (see jobs.Job.ErrorWorkspace). Deleting that row therefore cancels the
// review it launched, which is the intended reading: the job runs in the row's
// working copy, and the working copy is what is being removed.
func cancelWorkspaceJobs(ledger jobLedger, repoRoot, workspaceName string, reporter deckui.Reporter) error {
	if ledger == nil || workspaceName == "" {
		return nil
	}
	all, err := ledger.List()
	if err != nil {
		return fmt.Errorf("find the running jobs of workspace %q: %w", workspaceName, err)
	}
	self := os.Getpid()
	var failed []string
	for _, j := range all {
		if !j.IsActive() || j.Spec.WorkspaceName != workspaceName {
			continue
		}
		if j.Spec.RepoRoot != "" && repoRoot != "" && j.Spec.RepoRoot != repoRoot {
			continue
		}
		if j.PID == self {
			continue
		}
		if j.Spec.Action == jobs.ActionDelete || j.Spec.Action == jobs.ActionDeleteProject {
			continue
		}
		if reporter != nil {
			reporter.Step(fmt.Sprintf("Stop job %s (%s)", j.ID, j.Title))
		}
		if err := ledger.SignalCancel(j.ID); err != nil {
			failed = append(failed, string(j.ID))
		}
	}
	if len(failed) > 0 {
		// Not a reason to fail the delete — the workspace is already gone by the
		// time this runs — but a command still running in a removed working copy is
		// exactly what this exists to prevent, so name it and say how to finish the
		// job by hand.
		return fmt.Errorf("workspace %q was deleted but job(s) %s would not stop — kill them by pid, from `awp jobs`",
			workspaceName, strings.Join(failed, ", "))
	}
	return nil
}

// cancelWorkspaceJobsOnDisk is the sweep against the real store, opened here rather
// than threaded in: this runs inside a detached job as often as in the deck, and the
// two would otherwise have to agree about passing a store down a path whose other
// legs need none.
func cancelWorkspaceJobsOnDisk(repoRoot, workspaceName string, reporter deckui.Reporter) error {
	store, err := jobs.NewStore()
	if err != nil {
		return fmt.Errorf("open the job store to stop workspace %q's jobs: %w", workspaceName, err)
	}
	return cancelWorkspaceJobs(store, repoRoot, workspaceName, reporter)
}
