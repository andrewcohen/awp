package cli

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/deckui"
)

func TestPersistPRStatusMergePreservesEntriesNotInFreshFetch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"

	// Seed cache with two PRs that won't appear in the next fresh fetch
	// (simulates: bulk window pushed them out).
	initial := map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/old":   {Number: 100, HeadRefName: "andrew/old", State: deckui.PRStateOpen},
			"andrew/older": {Number: 50, HeadRefName: "andrew/older", State: deckui.PRStateOpen},
		},
	}
	persistPRStatusMerge(initial, time.Now())

	// Fresh fetch returns one new PR and updates the state of nothing
	// in the prior set (the older two fell out of the bulk window).
	fresh := map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/new": {Number: 200, HeadRefName: "andrew/new", State: deckui.PRStateOpen},
		},
	}
	persistPRStatusMerge(fresh, time.Now())

	got, _, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("loadPRStatusCache: %v", err)
	}
	byHead := got[repo]
	for _, want := range []string{"andrew/old", "andrew/older", "andrew/new"} {
		if _, ok := byHead[want]; !ok {
			t.Errorf("expected %q in cache after merge, got %v", want, byHead)
		}
	}
}

func TestPollPRStatusJobSkipsStaleCacheEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"
	// Seed cache so the entry's fetched_at is well in the past.
	pastFetchedAt := time.Now().Add(-1 * time.Hour)
	persistByRepo := map[string]map[string]deckui.PRStatus{
		repo: {"andrew/x": {Number: 1, HeadRefName: "andrew/x", State: deckui.PRStateOpen}},
	}
	if err := savePRStatusCache(persistByRepo, map[string]time.Time{repo: pastFetchedAt}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Poll with pollStartedAt = now. The cache's fetched_at < now, so
	// this entry must be treated as stale (the poll should NOT emit a
	// PRStatusRepoDoneMsg for it).
	got := pollPRStatusJobAt([]string{repo}, map[string]bool{}, time.Now())
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", got)
	}
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		msg := cmd()
		if rd, ok := msg.(deckui.PRStatusRepoDoneMsg); ok {
			t.Errorf("did not expect PRStatusRepoDoneMsg for stale cache; got %+v", rd)
		}
	}
}

func TestPollPRStatusJobConsumesFreshCacheEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"
	pollStart := time.Now()
	freshFetchedAt := pollStart.Add(1 * time.Second)
	persistByRepo := map[string]map[string]deckui.PRStatus{
		repo: {"andrew/x": {Number: 1, HeadRefName: "andrew/x", State: deckui.PRStateOpen}},
	}
	if err := savePRStatusCache(persistByRepo, map[string]time.Time{repo: freshFetchedAt}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := pollPRStatusJobAt([]string{repo}, map[string]bool{}, pollStart)
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", got)
	}
	sawRepoDone := false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		msg := cmd()
		if _, ok := msg.(deckui.PRStatusRepoDoneMsg); ok {
			sawRepoDone = true
		}
	}
	if !sawRepoDone {
		t.Errorf("expected PRStatusRepoDoneMsg for fresh cache, got none")
	}
}

func TestInvalidatePRStatusCacheRepoPreservesPRData(t *testing.T) {
	// Regression: invalidatePRStatusCacheRepo used to delete the repo's
	// PR data along with the throttle stamp, which wiped every cached
	// PR for the repo on every `awp w open`. Repos whose only
	// workspaces had `Bookmark` (no PRNumber) then got stuck — the
	// eligibility check would skip them, leaving the cache permanently
	// empty.
	t.Setenv("HOME", t.TempDir())
	repo := "/r"
	prs := map[string]deckui.PRStatus{
		"andrew/x": {Number: 1, HeadRefName: "andrew/x", State: deckui.PRStateOpen},
		"andrew/y": {Number: 2, HeadRefName: "andrew/y", State: deckui.PRStateOpen},
	}
	if err := savePRStatusCache(
		map[string]map[string]deckui.PRStatus{repo: prs},
		map[string]time.Time{repo: time.Now()},
	); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := invalidatePRStatusCacheRepo(repo); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	byRepo, fetchedAt, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := fetchedAt[repo]; ok {
		t.Errorf("expected fetched_at to be cleared so the throttle expires")
	}
	if len(byRepo[repo]) != 2 {
		t.Errorf("expected PR data to survive invalidation, got %v", byRepo[repo])
	}
	if _, ok := byRepo[repo]["andrew/x"]; !ok {
		t.Errorf("expected andrew/x to survive invalidation")
	}
}

func TestPersistPRStatusBulkMergePrunesTerminalNotInFresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"

	// Seed: one merged + one closed PR that won't be in the next bulk
	// fetch (they aged out of the gh window), one open PR likewise
	// missing, and one current open PR.
	persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/old-merged": {Number: 10, HeadRefName: "andrew/old-merged", State: deckui.PRStateMerged},
			"andrew/old-closed": {Number: 11, HeadRefName: "andrew/old-closed", State: deckui.PRStateClosed},
			"andrew/old-open":   {Number: 12, HeadRefName: "andrew/old-open", State: deckui.PRStateOpen},
			"andrew/current":    {Number: 13, HeadRefName: "andrew/current", State: deckui.PRStateOpen},
		},
	}, time.Now())

	// Bulk fetch returns only the current PR.
	fresh := map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/current": {Number: 13, HeadRefName: "andrew/current", State: deckui.PRStateOpen},
		},
	}
	// nil completeByRepo → treated as a possibly-truncated fetch, so the
	// conservative terminal-only prune applies and the absent OPEN PR is
	// kept.
	persistPRStatusBulkMerge(fresh, nil, nil, time.Now())

	got, _, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	byHead := got[repo]
	if _, ok := byHead["andrew/current"]; !ok {
		t.Errorf("current PR should remain")
	}
	if _, ok := byHead["andrew/old-open"]; !ok {
		t.Errorf("OPEN PR missing from fresh should be kept (non-terminal, fetch not known-complete)")
	}
	if _, ok := byHead["andrew/old-merged"]; ok {
		t.Errorf("MERGED PR missing from fresh should be pruned")
	}
	if _, ok := byHead["andrew/old-closed"]; ok {
		t.Errorf("CLOSED PR missing from fresh should be pruned")
	}
}

// TestPersistPRStatusBulkMergePrunesStaleOpenOnCompleteFetch pins the
// fix for the phantom-inbox-row bug: a PR cached as OPEN (with
// ReviewRequested / IsInMergeQueue set) that has since merged out of the
// `--state open` list never comes back as MERGED to overwrite the stale
// entry. When the fetch is COMPLETE (not truncated), its absence is
// authoritative, so the stale OPEN entry must be pruned — otherwise it
// lingers forever as a bogus "needs your review" / in-merge-queue row.
func TestPersistPRStatusBulkMergePrunesStaleOpenOnCompleteFetch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"

	persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
		repo: {
			// Merged in reality, but cached as OPEN + review-requested +
			// queued — exactly the redwood #2183 shape.
			"andrew/brand-config": {Number: 2183, HeadRefName: "andrew/brand-config", State: deckui.PRStateOpen, ReviewRequested: true, IsInMergeQueue: true},
			"andrew/current":      {Number: 13, HeadRefName: "andrew/current", State: deckui.PRStateOpen},
		},
	}, time.Now())

	// Complete fetch (not truncated) returns only the still-open PR.
	fresh := map[string]map[string]deckui.PRStatus{
		repo: {"andrew/current": {Number: 13, HeadRefName: "andrew/current", State: deckui.PRStateOpen}},
	}
	persistPRStatusBulkMerge(fresh, nil, map[string]bool{repo: true}, time.Now())

	got, _, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	byHead := got[repo]
	if _, ok := byHead["andrew/brand-config"]; ok {
		t.Errorf("stale OPEN PR absent from a complete fetch must be pruned (phantom inbox row)")
	}
	if _, ok := byHead["andrew/current"]; !ok {
		t.Errorf("still-open PR present in the fresh set must remain")
	}
}

func TestPersistPRStatusBulkMergeKeepsPinnedTerminalPRs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"

	persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/pinned-merged": {Number: 100, HeadRefName: "andrew/pinned-merged", State: deckui.PRStateMerged},
		},
	}, time.Now())

	// Bulk fetch doesn't include the pinned PR (typical: it's older
	// than the gh window). Pinned set covers it → it must survive.
	fresh := map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/other": {Number: 200, HeadRefName: "andrew/other", State: deckui.PRStateOpen},
		},
	}
	pinned := map[string]map[int]bool{repo: {100: true}}
	// Even a complete fetch must keep pinned PRs (their terminal state
	// arrives via the top-up), so assert retention under complete=true.
	persistPRStatusBulkMerge(fresh, pinned, map[string]bool{repo: true}, time.Now())

	got, _, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := got[repo]["andrew/pinned-merged"]; !ok {
		t.Errorf("pinned MERGED PR must be retained across bulk merge")
	}
}

func TestPersistPRStatusBulkMergeSkipsPruneOnEmptyFresh(t *testing.T) {
	// Defensive: an upstream gh failure returning []  must not be
	// treated as "every cached PR is gone" and trigger a wipe.
	t.Setenv("HOME", t.TempDir())
	repo := "/r"

	persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
		repo: {
			"andrew/keep": {Number: 1, HeadRefName: "andrew/keep", State: deckui.PRStateMerged},
		},
	}, time.Now())

	persistPRStatusBulkMerge(
		map[string]map[string]deckui.PRStatus{repo: {}},
		nil,
		map[string]bool{repo: true},
		time.Now(),
	)

	got, _, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := got[repo]["andrew/keep"]; !ok {
		t.Errorf("empty fresh set must not prune the cache (likely upstream error)")
	}
}

func TestPersistPRStatusMergeOverwritesEntriesByHeadRefName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := "/r"

	// Seed: PR is open.
	persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
		repo: {"andrew/x": {Number: 9, HeadRefName: "andrew/x", State: deckui.PRStateOpen}},
	}, time.Now())

	// Fresh fetch: same head ref, now merged.
	persistPRStatusMerge(map[string]map[string]deckui.PRStatus{
		repo: {"andrew/x": {Number: 9, HeadRefName: "andrew/x", State: deckui.PRStateMerged}},
	}, time.Now())

	got, _, err := loadPRStatusCache()
	if err != nil {
		t.Fatalf("loadPRStatusCache: %v", err)
	}
	if got[repo]["andrew/x"].State != deckui.PRStateMerged {
		t.Errorf("expected merged transition to overwrite, got state=%s", got[repo]["andrew/x"].State)
	}
}
