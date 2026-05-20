package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrewcohen/awp/internal/jobs"
)

// withTempHome redirects HOME (and the cache files under ~/.awp) to a
// temp dir so the test never touches the user's real ~/.awp.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestRunPRStatusFromSpecWritesCachePerRepo(t *testing.T) {
	home := withTempHome(t)
	repoA := t.TempDir()
	repoB := t.TempDir()

	prJSONA := `[{"number":1,"headRefName":"andrew/a","url":"https://example/a/1","state":"OPEN","isDraft":false,"reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}],"mergeStateStatus":"CLEAN"}]`
	prJSONB := `[{"number":2,"headRefName":"andrew/b","url":"https://example/b/2","state":"OPEN","isDraft":true,"reviewDecision":"","statusCheckRollup":[],"mergeStateStatus":"BEHIND"}]`
	var counter int
	wrapped := &sequencedRunner{seq: []string{prJSONA, prJSONB}, counter: &counter}

	job := jobs.Job{
		ID: "test-job",
		Spec: jobs.Spec{
			Action: jobs.ActionPRStatus,
			Repos:  []string{repoA, repoB},
		},
	}
	if err := runPRStatusFromSpec(wrapped, job, noopReporter{}); err != nil {
		t.Fatalf("runPRStatusFromSpec: %v", err)
	}

	// Cache should now hold both repos' PR data, written atomically.
	cachePath := filepath.Join(home, ".awp", prStatusCacheName)
	body, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache prStatusCacheFile
	if err := json.Unmarshal(body, &cache); err != nil {
		t.Fatalf("parse cache: %v", err)
	}
	if cache.Repos[repoA].PRs["andrew/a"].Number != 1 {
		t.Errorf("missing repoA entry; got %+v", cache.Repos[repoA])
	}
	if cache.Repos[repoB].PRs["andrew/b"].Number != 2 {
		t.Errorf("missing repoB entry; got %+v", cache.Repos[repoB])
	}
	if cache.Repos[repoB].PRs["andrew/b"].URL != "https://example/b/2" {
		t.Errorf("repoB URL not propagated; got %q", cache.Repos[repoB].PRs["andrew/b"].URL)
	}
}

func TestRunPRStatusFromSpecContinuesPastRepoFailure(t *testing.T) {
	home := withTempHome(t)
	repoBad := t.TempDir()
	repoGood := t.TempDir()

	prJSONGood := `[{"number":7,"headRefName":"andrew/x","url":"https://example/x/7","state":"OPEN","isDraft":false,"reviewDecision":"","statusCheckRollup":[],"mergeStateStatus":"CLEAN"}]`
	var counter int
	// First call returns junk so ListPRStatus fails; second returns valid JSON.
	wrapped := &sequencedRunner{seq: []string{"not json", prJSONGood}, counter: &counter}

	job := jobs.Job{
		ID: "test-job",
		Spec: jobs.Spec{
			Action: jobs.ActionPRStatus,
			Repos:  []string{repoBad, repoGood},
		},
	}
	if err := runPRStatusFromSpec(wrapped, job, noopReporter{}); err != nil {
		t.Fatalf("runPRStatusFromSpec: %v", err)
	}

	// The good repo's data should still be in the cache.
	cachePath := filepath.Join(home, ".awp", prStatusCacheName)
	body, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache prStatusCacheFile
	if err := json.Unmarshal(body, &cache); err != nil {
		t.Fatalf("parse cache: %v", err)
	}
	if cache.Repos[repoGood].PRs["andrew/x"].Number != 7 {
		t.Errorf("missing repoGood entry; cache=%+v", cache.Repos)
	}
	if _, present := cache.Repos[repoBad]; present {
		t.Errorf("repoBad should not have been cached: %+v", cache.Repos[repoBad])
	}
}

// sequencedRunner returns each successive output from seq in order for
// `gh pr list` calls (one per repo). The merge-queue lookup that the
// pr-status job runs after each ListPRStatus (`gh repo view` then
// `gh api graphql`) is answered with a benign "nothing queued" payload
// so the test fixture only needs to declare the bulk-status outputs.
type sequencedRunner struct {
	seq     []string
	counter *int
}

func (r *sequencedRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	if name == "gh" && len(args) >= 2 && args[0] == "repo" && args[1] == "view" {
		return `{"owner":{"login":"o"},"name":"r"}`, nil
	}
	if name == "gh" && len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
		return `{"data":{"repository":{"pullRequests":{"nodes":[]}}}}`, nil
	}
	i := *r.counter
	if i >= len(r.seq) {
		return "", nil
	}
	*r.counter = i + 1
	return r.seq[i], nil
}
