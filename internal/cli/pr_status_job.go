package cli

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jobs"
)

// pr-status job: a detached subprocess that fetches PR status across one
// or more repos and writes results to the cache as each repo finishes.
// Hosted by the same jobs subsystem as the workspace-lifecycle jobs:
//
//   - Spawned via jobs.Store.Spawn — gets a record at
//     ~/.awp/jobs/<id>.json and shows up in the deck's J overlay.
//   - The subprocess re-enters via `awp run-job <id>`, which dispatches
//     to runPRStatusFromSpec below.
//   - Per-repo progress is recorded as Steps on the job record, so the
//     J overlay's per-job view shows which repos have been fetched.
//   - Cache writes (~/.awp/pr-status-cache.json) are atomic per repo,
//     so the deck's poll loop can read incremental results.
//
// Decoupling the fetch from the deck process means closing the deck
// mid-fetch no longer drops in-flight work — the next deck open either
// reuses the still-running job (the J overlay shows it) or, if it
// already finished, just reads the cache.

// runPRStatusFromSpec is the action handler the run-job subprocess
// invokes when Spec.Action == ActionPRStatus. Iterates Spec.Repos
// sequentially, calling gh per repo and merging each result into the
// persisted cache before reporting a per-repo Step to the job store.
// reporter accepts the deckui.Reporter interface so unit tests can pass
// a no-op.
func runPRStatusFromSpec(runner Runner, job jobs.Job, reporter deckui.Reporter) error {
	repos := job.Spec.Repos
	if len(repos) == 0 {
		return errors.New("pr-status: spec carries no repos")
	}
	for _, repo := range repos {
		started := time.Now()
		gh := github.New(fixedDirRunner{base: runner, dir: repo})
		statuses, err := gh.ListPRStatus(repo)
		if err != nil {
			reporter.Step(fmt.Sprintf("%s — error: %v", repo, err))
			continue
		}
		byHead := convertGithubStatusesToDeckui(statuses)
		persistPRStatusMerge(map[string]map[string]deckui.PRStatus{repo: byHead}, time.Now())
		reporter.Step(fmt.Sprintf("%s — %d PRs (%s)", repo, len(statuses), time.Since(started).Round(time.Millisecond)))
	}
	return nil
}

// convertGithubStatusesToDeckui translates the github.PRStatus list into
// the deckui.PRStatus map keyed by headRefName.
func convertGithubStatusesToDeckui(statuses []github.PRStatus) map[string]deckui.PRStatus {
	byHead := make(map[string]deckui.PRStatus, len(statuses))
	for _, s := range statuses {
		byHead[s.HeadRefName] = deckui.PRStatus{
			Number:           s.Number,
			HeadRefName:      s.HeadRefName,
			URL:              s.URL,
			State:            deckui.PRState(s.State),
			IsDraft:          s.IsDraft,
			ReviewDecision:   deckui.PRReviewDecision(s.ReviewDecision),
			CIState:          deckui.PRCIState(s.CIState),
			MergeStateStatus: deckui.PRMergeStateStatus(s.MergeStateStatus),
		}
	}
	return byHead
}

// spawnPRStatusJob spawns a detached subprocess via the jobs subsystem.
// The Spawn helper writes the pending record, forks `awp run-job <id>`
// with Setsid + dev/null stdin, and returns immediately. The deck-side
// caller continues without waiting; the subprocess writes its progress
// to the job record at ~/.awp/jobs/<id>.json and per-repo PR results
// into ~/.awp/pr-status-cache.json as it goes.
func spawnPRStatusJob(repos []string) (jobs.Job, error) {
	if len(repos) == 0 {
		return jobs.Job{}, errors.New("spawnPRStatusJob: no repos")
	}
	store, err := jobs.NewStore()
	if err != nil {
		return jobs.Job{}, fmt.Errorf("open job store: %w", err)
	}
	spec := jobs.Spec{
		Action: jobs.ActionPRStatus,
		Repos:  append([]string(nil), repos...),
	}
	title := fmt.Sprintf("pr-status · %d repo", len(repos))
	if len(repos) != 1 {
		title += "s"
	}
	return store.Spawn(spec, title, jobs.SpawnOptions{})
}

// findActivePRStatusJob scans the jobs store for an in-flight
// ActionPRStatus job. Returns the job (and true) if one is running or
// pending; returns the zero value (and false) otherwise. The deck-side
// fetcher uses this to reuse an existing job rather than spawning a
// duplicate.
func findActivePRStatusJob() (jobs.Job, bool) {
	store, err := jobs.NewStore()
	if err != nil {
		return jobs.Job{}, false
	}
	all, err := store.List()
	if err != nil {
		return jobs.Job{}, false
	}
	for _, j := range all {
		if j.Spec.Action != jobs.ActionPRStatus {
			continue
		}
		if !j.IsActive() {
			continue
		}
		if !pidAlive(j.PID) {
			continue
		}
		return j, true
	}
	return jobs.Job{}, false
}

// pidAlive reports whether the given pid is still running. Uses
// signal-0 probing — works on Unix; on Windows os.FindProcess always
// succeeds, so callers should treat this as best-effort.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// prStatusPollInterval is how often the deck checks the detached job's
// progress. Short enough that per-repo glyphs feel responsive, long
// enough that the file-system overhead is negligible.
const prStatusPollInterval = 250 * time.Millisecond

// prStatusPollMaxWait caps how long pollPRStatusJob will tail a job
// that never finishes (subprocess wedged, FS unreachable, etc.) so a
// pathological case can't keep the deck's activity bar spinning
// forever. The closing PRStatusDoneMsg fires when this elapses even if
// the job still appears active.
const prStatusPollMaxWait = 2 * time.Minute

// pollPRStatusJob returns a tea.Msg that batches:
//   - one PRStatusRepoDoneMsg for each repo whose entry has newly
//     landed in the cache since the previous poll
//   - either a closing PRStatusDoneMsg (job complete or absent) or a
//     tea.Tick command that re-invokes pollPRStatusJob 250ms later
//
// "seen" tracks which repos this fetcher has already reported. The
// caller threads it through each tick.
func pollPRStatusJob(watching []string, seen map[string]bool) tea.Msg {
	return pollPRStatusJobAt(watching, seen, time.Now())
}

// pollPRStatusJobAt is the tick-time variant — pollPRStatusJob calls
// this with time.Now(); the recursive retick calls it with the tick's
// timestamp so the elapsed-time guard is stable.
func pollPRStatusJobAt(watching []string, seen map[string]bool, firstSeenAt time.Time) tea.Msg {
	byRepo, _, _ := loadPRStatusCache()

	cmds := make([]tea.Cmd, 0, len(watching)+1)
	for _, repo := range watching {
		if seen[repo] {
			continue
		}
		entry, ok := byRepo[repo]
		if !ok {
			continue
		}
		seen[repo] = true
		repoCopy := repo
		entryCopy := entry
		cmds = append(cmds, func() tea.Msg {
			return deckui.PRStatusRepoDoneMsg{Repo: repoCopy, ByHead: entryCopy}
		})
	}

	allSeen := true
	for _, repo := range watching {
		if !seen[repo] {
			allSeen = false
			break
		}
	}
	_, jobAlive := findActivePRStatusJob()
	timedOut := time.Since(firstSeenAt) >= prStatusPollMaxWait
	if allSeen || !jobAlive || timedOut {
		fetchedAt := time.Now()
		if timedOut {
			deckDebugLogf("prStatus poll timed out after %s watching=%d seen=%d", prStatusPollMaxWait, len(watching), len(seen))
		}
		cmds = append(cmds, func() tea.Msg {
			return deckui.PRStatusDoneMsg{FetchedAt: fetchedAt}
		})
		return tea.BatchMsg(cmds)
	}

	watchingCopy := append([]string(nil), watching...)
	cmds = append(cmds, tea.Tick(prStatusPollInterval, func(_ time.Time) tea.Msg {
		return pollPRStatusJobAt(watchingCopy, seen, firstSeenAt)
	}))
	return tea.BatchMsg(cmds)
}
