package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/deckui"
)

// PR-status cache lives next to other awp state in ~/.awp. The cache survives
// deck restarts so the per-repo 60s refresh throttle has a non-empty cooldown
// window across opens — a deck closed and reopened within a minute reuses the
// cached glyphs without re-running `gh`.
const prStatusCacheName = "pr-status-cache.json"

type prStatusCacheRepo struct {
	FetchedAt time.Time                    `json:"fetched_at"`
	PRs       map[string]deckui.PRStatus   `json:"prs"`
}

type prStatusCacheFile struct {
	Version int                          `json:"version"`
	Repos   map[string]prStatusCacheRepo `json:"repos"`
}

func prStatusCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".awp", prStatusCacheName), nil
}

// loadPRStatusCache returns the persisted cache. A missing file is not an
// error — both maps come back empty. Any other error is returned so callers
// can log it; the deck always degrades to a cold fetch on failure.
func loadPRStatusCache() (map[string]map[string]deckui.PRStatus, map[string]time.Time, error) {
	path, err := prStatusCachePath()
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]map[string]deckui.PRStatus{}, map[string]time.Time{}, nil
		}
		return nil, nil, err
	}
	var cache prStatusCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, nil, err
	}
	byRepo := make(map[string]map[string]deckui.PRStatus, len(cache.Repos))
	fetchedAt := make(map[string]time.Time, len(cache.Repos))
	for repo, entry := range cache.Repos {
		if entry.PRs != nil {
			byRepo[repo] = entry.PRs
		}
		if !entry.FetchedAt.IsZero() {
			fetchedAt[repo] = entry.FetchedAt
		}
	}
	return byRepo, fetchedAt, nil
}

// invalidatePRStatusCacheRepo drops one repo's entry from the persisted cache.
// Used after a workspace-create or review-open to ensure the next deck open
// fetches fresh PR status for that repo instead of reusing the previous
// fetch's data within the 60s throttle window. Best-effort: caller logs but
// does not fail on disk error.
func invalidatePRStatusCacheRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil
	}
	byRepo, fetchedAt, err := loadPRStatusCache()
	if err != nil {
		return err
	}
	if _, hadEntry := byRepo[repo]; !hadEntry {
		if _, hadTs := fetchedAt[repo]; !hadTs {
			return nil
		}
	}
	delete(byRepo, repo)
	delete(fetchedAt, repo)
	return savePRStatusCache(byRepo, fetchedAt)
}

// savePRStatusCache writes the cache to disk atomically (write to a temp file
// in the same directory, then rename). Best-effort: callers should log but
// never fail a fetch on save error.
func savePRStatusCache(byRepo map[string]map[string]deckui.PRStatus, fetchedAt map[string]time.Time) error {
	path, err := prStatusCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cache := prStatusCacheFile{
		Version: 1,
		Repos:   make(map[string]prStatusCacheRepo, len(byRepo)),
	}
	// Union of keys across both maps — a repo with prs but no timestamp (or
	// vice versa) shouldn't get dropped silently.
	repos := make(map[string]struct{}, len(byRepo)+len(fetchedAt))
	for r := range byRepo {
		repos[r] = struct{}{}
	}
	for r := range fetchedAt {
		repos[r] = struct{}{}
	}
	for repo := range repos {
		cache.Repos[repo] = prStatusCacheRepo{
			FetchedAt: fetchedAt[repo],
			PRs:       byRepo[repo],
		}
	}
	body, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
