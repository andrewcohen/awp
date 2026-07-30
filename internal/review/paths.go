package review

import "github.com/andrewcohen/awp/internal/config"

// The default store location lives in internal/config alongside every other
// ~/.awp path, so the write, read and cleanup sides cannot disagree. Indirected
// through these wrappers so tests can point a Store at a temp dir instead.

func configReviewStorePath(repoRoot, id string) string {
	return config.ReviewStorePath(repoRoot, id)
}

func configReviewStoreRepoDir(repoRoot string) string {
	return config.ReviewStoreRepoDir(repoRoot)
}
