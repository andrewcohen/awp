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
	"github.com/andrewcohen/awp/internal/state"
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
	store := state.NewJSONStore()
	for _, repo := range repos {
		started := time.Now()
		gh := github.New(fixedDirRunner{base: runner, dir: repo})
		statuses, err := gh.ListPRStatus(repo)
		if err != nil {
			reporter.Step(fmt.Sprintf("%s — error: %v", repo, err))
			deckDebugLogf("prStatus fetch err repo=%s err=%v", repo, err)
			continue
		}
		// Merge-queue membership is graphql-only — `gh pr list --json`
		// does not expose isInMergeQueue. Best-effort; a failure here
		// must not lose the bulk PR status we already fetched.
		queued, qErr := gh.ListMergeQueuedHeads(repo)
		if qErr != nil {
			reporter.Step(fmt.Sprintf("%s — merge-queue lookup failed: %v", repo, qErr))
		}
		byHead := prStatusMapFromGithub(statuses, queued)
		// Top up pinned-PR overrides that fell outside the bulk
		// window. Walk this repo's workspace entries for PROverride > 0,
		// see which numbers are absent from byHead, and fetch each
		// individually via `gh pr view`. Single-PR fetches are cheap
		// and rare (only workspaces actually pinned), so it's safe to
		// do on every refresh.
		topUps := topUpMissingOverrides(store, gh, repo, byHead)
		for head, status := range topUps {
			byHead[head] = status
		}
		persistPRStatusMerge(map[string]map[string]deckui.PRStatus{repo: byHead}, time.Now())
		reporter.Step(fmt.Sprintf("%s — %d PRs (+%d pinned) (%s)", repo, len(statuses), len(topUps), time.Since(started).Round(time.Millisecond)))
		// Diagnostic: log every PR # and head ref returned for this
		// repo. If a PR you expect to see isn't in `numbers=[...]`, gh
		// didn't return it — most likely the repo has more PRs than
		// the `gh pr list --limit 100` cap and the PR is older than
		// the cutoff. The `truncated` flag flags that condition
		// explicitly (count == limit).
		numbers := sortedPRNumbers(byHead)
		truncated := len(statuses) >= 100
		deckDebugLogf("prStatus fetched repo=%s count=%d topup=%d truncated=%t numbers=%v",
			repo, len(statuses), len(topUps), truncated, numbers)
	}
	return nil
}

// topUpMissingOverrides walks this repo's workspace state for entries
// with PRNumber > 0 whose PRs aren't already in byHead, and fetches each
// one individually via `gh pr view`. Returns a map of headRefName →
// PRStatus to merge into byHead.
//
// Pinned PRs are typically older than the bulk-list window — a busy
// repo can have hundreds of PRs more recent than the one a user is
// trying to surface, and `gh pr list --limit 100` cuts them off. This
// helper closes that gap.
func topUpMissingOverrides(store *state.JSONStore, gh *github.Client, repo string, byHead map[string]deckui.PRStatus) map[string]deckui.PRStatus {
	entries, err := store.Load(repo)
	if err != nil {
		deckDebugLogf("prStatus topUp load err repo=%s err=%v", repo, err)
		return nil
	}
	wantNumbers := map[int]bool{}
	for _, e := range entries {
		if e.PRNumber > 0 {
			wantNumbers[e.PRNumber] = true
		}
	}
	if len(wantNumbers) == 0 {
		return nil
	}
	have := map[int]bool{}
	for _, s := range byHead {
		if s.Number > 0 {
			have[s.Number] = true
		}
	}
	out := map[string]deckui.PRStatus{}
	for n := range wantNumbers {
		if have[n] {
			continue
		}
		s, err := gh.GetPRStatus(repo, n)
		if err != nil {
			deckDebugLogf("prStatus topUp gh pr view err repo=%s pr=%d err=%v", repo, n, err)
			continue
		}
		// Coerce empty headRefName (shouldn't happen for a real PR)
		// to a synthetic key so an over-eager gh response that elides
		// the field doesn't collide with another entry.
		key := s.HeadRefName
		if key == "" {
			key = fmt.Sprintf("__pin_%d", n)
		}
		out[key] = prStatusFromGithub(s, false)
		deckDebugLogf("prStatus topUp ok repo=%s pr=%d head=%s state=%s", repo, n, s.HeadRefName, s.State)
	}
	return out
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
//     landed in the cache since the poll started (entries written
//     before pollStartedAt are stale leftovers from a prior fetch and
//     get skipped)
//   - either a closing PRStatusDoneMsg (job complete or absent) or a
//     tea.Tick command that re-invokes pollPRStatusJob 250ms later
//
// "seen" tracks which repos this fetcher has already reported.
// pollStartedAt is when the deck-side fetcher closure was invoked; we
// use it both as the timeout baseline AND as the freshness boundary so
// a forced refresh (`p s` save, new workspace, etc.) doesn't consume
// the stale cache entry from a previous fetch and immediately declare
// itself done.
func pollPRStatusJob(watching []string, seen map[string]bool, pollStartedAt time.Time) tea.Msg {
	return pollPRStatusJobAt(watching, seen, pollStartedAt)
}

// pollPRStatusJobAt is the tick-time variant — pollPRStatusJob calls
// this with the fetcher's invocation timestamp; the recursive retick
// calls it with the same timestamp so the elapsed-time guard is stable
// and the freshness boundary doesn't drift.
func pollPRStatusJobAt(watching []string, seen map[string]bool, pollStartedAt time.Time) tea.Msg {
	byRepo, fetchedAt, _ := loadPRStatusCache()

	cmds := make([]tea.Cmd, 0, len(watching)+1)
	for _, repo := range watching {
		if seen[repo] {
			continue
		}
		entry, ok := byRepo[repo]
		if !ok {
			continue
		}
		// Only treat this repo's entry as a fresh result for the
		// current fetch if it was written after the poll started. The
		// cache file persists across deck sessions, so without this
		// check the very first poll tick would consume the prior
		// fetch's entry and mark the repo "done" before the new
		// subprocess has had a chance to write — defeating
		// forcePRStatusRefresh on flows like the `p s` chord.
		if ts, ok := fetchedAt[repo]; !ok || ts.Before(pollStartedAt) {
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
	timedOut := time.Since(pollStartedAt) >= prStatusPollMaxWait
	if allSeen || !jobAlive || timedOut {
		fetchedAtNow := time.Now()
		if timedOut {
			deckDebugLogf("prStatus poll timed out after %s watching=%d seen=%d", prStatusPollMaxWait, len(watching), len(seen))
		}
		cmds = append(cmds, func() tea.Msg {
			return deckui.PRStatusDoneMsg{FetchedAt: fetchedAtNow}
		})
		return tea.BatchMsg(cmds)
	}

	watchingCopy := append([]string(nil), watching...)
	cmds = append(cmds, tea.Tick(prStatusPollInterval, func(_ time.Time) tea.Msg {
		return pollPRStatusJobAt(watchingCopy, seen, pollStartedAt)
	}))
	return tea.BatchMsg(cmds)
}
