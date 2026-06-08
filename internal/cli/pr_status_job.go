package cli

import (
	"errors"
	"fmt"
	"os"
	"sync"
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

// prStatusMaxConcurrency bounds how many `gh` subprocesses the pr-status
// job runs at once across all repos and all per-PR top-ups. gh forks a
// process and does a fresh API round-trip per call, so the job is almost
// entirely latency-bound — fanning the independent calls out concurrently
// collapses the wall-clock from sum-of-calls down to roughly the slowest
// single call, while the cap keeps us clear of GitHub's secondary rate
// limits and avoids a fork storm on a many-repo deck.
const prStatusMaxConcurrency = 8

// runPRStatusFromSpec is the action handler the run-job subprocess
// invokes when Spec.Action == ActionPRStatus. It fetches every repo in
// Spec.Repos concurrently (bounded by prStatusMaxConcurrency), merging
// each result into the persisted cache and reporting a per-repo Step to
// the job store as it lands — so the deck's poll loop still sees
// incremental per-repo results. reporter accepts the deckui.Reporter
// interface so unit tests can pass a no-op.
func runPRStatusFromSpec(runner Runner, job jobs.Job, reporter deckui.Reporter) error {
	repos := job.Spec.Repos
	if len(repos) == 0 {
		return errors.New("pr-status: spec carries no repos")
	}
	store := state.NewJSONStore()

	// sem bounds the total number of concurrent `gh` invocations. A slot
	// is held only for the duration of a leaf gh exec — never while a
	// goroutine waits on its children — so the fan-out can't deadlock no
	// matter how it nests (repo → list/merge-queue → per-pin top-up).
	sem := make(chan struct{}, prStatusMaxConcurrency)

	// Resolve the viewer login concurrently with the per-repo fetches.
	// The login is account-global and only feeds the viewer-relative
	// review-requested signals, so its latency should overlap the bulk
	// list calls rather than serialize in front of them. Repo goroutines
	// block on getViewer() before projecting (the projection needs it),
	// by which point this single fast `gh api user` call is usually done.
	// A failure just disables those signals for this fetch; it must not
	// fail the job.
	var viewer string
	viewerReady := make(chan struct{})
	go func() {
		defer close(viewerReady)
		sem <- struct{}{}
		defer func() { <-sem }()
		if login, err := github.New(fixedDirRunner{base: runner, dir: repos[0]}).ViewerLogin(repos[0]); err == nil {
			viewer = login
		} else {
			deckDebugLogf("prStatus viewer-login err: %v", err)
		}
	}()
	getViewer := func() string {
		<-viewerReady
		return viewer
	}

	var wg sync.WaitGroup
	for _, repo := range repos {
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			fetchRepoPRStatus(runner, store, repo, sem, getViewer, reporter)
		}(repo)
	}
	wg.Wait()
	return nil
}

// fetchRepoPRStatus fetches one repo's PR status and merges it into the
// persisted cache. The two independent bulk calls — `gh pr list` and the
// graphql merge-queue lookup — run concurrently, as do the per-pinned-PR
// top-ups; every gh exec draws a slot from the shared sem so total
// concurrency across all repos stays bounded. Errors are reported as job
// Steps and logged but never abort sibling repos' fetches.
func fetchRepoPRStatus(runner Runner, store *state.JSONStore, repo string, sem chan struct{}, getViewer func() string, reporter deckui.Reporter) {
	started := time.Now()
	gh := github.New(fixedDirRunner{base: runner, dir: repo})

	var (
		statuses []github.PRStatus
		listErr  error
		queued   map[string]bool
		qErr     error
	)
	var fetchWG sync.WaitGroup
	fetchWG.Add(2)
	go func() {
		defer fetchWG.Done()
		sem <- struct{}{}
		defer func() { <-sem }()
		statuses, listErr = gh.ListPRStatus(repo)
	}()
	go func() {
		defer fetchWG.Done()
		sem <- struct{}{}
		defer func() { <-sem }()
		// Merge-queue membership is graphql-only — `gh pr list --json`
		// does not expose isInMergeQueue. Best-effort; a failure here
		// must not lose the bulk PR status we fetch alongside it.
		queued, qErr = gh.ListMergeQueuedHeads(repo)
	}()
	fetchWG.Wait()

	if listErr != nil {
		reporter.Step(fmt.Sprintf("%s — error: %v", repo, listErr))
		deckDebugLogf("prStatus fetch err repo=%s err=%v", repo, listErr)
		return
	}
	if qErr != nil {
		reporter.Step(fmt.Sprintf("%s — merge-queue lookup failed: %v", repo, qErr))
	}

	viewer := getViewer()
	byHead := prStatusMapFromGithub(statuses, queued, viewer)
	pinned := pinnedPRNumbersForRepo(store, repo)
	topUps := topUpMissingOverrides(gh, repo, byHead, pinned, viewer, sem)
	for head, status := range topUps {
		byHead[head] = status
	}
	persistPRStatusBulkMerge(
		map[string]map[string]deckui.PRStatus{repo: byHead},
		map[string]map[int]bool{repo: pinned},
		time.Now(),
	)
	reporter.Step(fmt.Sprintf("%s — %d PRs (+%d pinned) (%s)", repo, len(statuses), len(topUps), time.Since(started).Round(time.Millisecond)))
	// Diagnostic: log every PR # and head ref returned for this repo. If
	// a PR you expect to see isn't in `numbers=[...]`, gh didn't return
	// it — most likely the repo has more PRs than the `gh pr list
	// --limit 100` cap and the PR is older than the cutoff. The
	// `truncated` flag flags that condition explicitly (count == limit).
	numbers := sortedPRNumbers(byHead)
	truncated := len(statuses) >= 100
	deckDebugLogf("prStatus fetched repo=%s count=%d topup=%d truncated=%t numbers=%v",
		repo, len(statuses), len(topUps), truncated, numbers)
}

// pinnedPRNumbersForRepo walks this repo's workspace state and
// returns the set of PR numbers any entry has explicitly pinned via
// Entry.PRNumber. Returns nil on store-load error (callers treat that
// as "no pins"). Used twice per bulk-fetch pass: by topUpMissingOverrides
// (to fetch pins that fell outside the bulk window) and by
// persistPRStatusBulkMerge (to keep pinned terminal PRs from being
// pruned).
func pinnedPRNumbersForRepo(store *state.JSONStore, repo string) map[int]bool {
	entries, err := store.Load(repo)
	if err != nil {
		deckDebugLogf("prStatus pinned-numbers load err repo=%s err=%v", repo, err)
		return nil
	}
	pinned := map[int]bool{}
	for _, e := range entries {
		if e.PRNumber > 0 {
			pinned[e.PRNumber] = true
		}
	}
	return pinned
}

// topUpMissingOverrides fetches each pinned PR (pinned ⊆ a workspace's
// PRNumber for this repo) that isn't already in byHead, concurrently via
// `gh pr view` (each call bounded by the shared sem). Returns a map of
// headRefName → PRStatus to merge into byHead.
//
// Pinned PRs are typically older than the bulk-list window — a busy
// repo can have hundreds of PRs more recent than the one a user is
// trying to surface, and `gh pr list --limit 100` cuts them off. This
// helper closes that gap.
func topUpMissingOverrides(gh *github.Client, repo string, byHead map[string]deckui.PRStatus, pinned map[int]bool, viewer string, sem chan struct{}) map[string]deckui.PRStatus {
	if len(pinned) == 0 {
		return nil
	}
	have := map[int]bool{}
	for _, s := range byHead {
		if s.Number > 0 {
			have[s.Number] = true
		}
	}
	out := map[string]deckui.PRStatus{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for n := range pinned {
		if have[n] {
			continue
		}
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s, err := gh.GetPRStatus(repo, n)
			if err != nil {
				deckDebugLogf("prStatus topUp gh pr view err repo=%s pr=%d err=%v", repo, n, err)
				return
			}
			// Coerce empty headRefName (shouldn't happen for a real PR)
			// to a synthetic key so an over-eager gh response that elides
			// the field doesn't collide with another entry.
			key := s.HeadRefName
			if key == "" {
				key = fmt.Sprintf("__pin_%d", n)
			}
			status := prStatusFromGithub(s, false, viewer)
			mu.Lock()
			out[key] = status
			mu.Unlock()
			deckDebugLogf("prStatus topUp ok repo=%s pr=%d head=%s state=%s", repo, n, s.HeadRefName, s.State)
		}(n)
	}
	wg.Wait()
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
