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
