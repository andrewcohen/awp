package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
	wrapped := &repoStubRunner{prListByDir: map[string]string{repoA: prJSONA, repoB: prJSONB}}

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
	// repoBad returns junk so its ListPRStatus fails; repoGood returns valid JSON.
	wrapped := &repoStubRunner{prListByDir: map[string]string{repoBad: "not json", repoGood: prJSONGood}}

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

// repoStubRunner answers `gh pr list` per repo dir via prListByDir. The
// pr-status job fetches repos concurrently, so the fixture is keyed by
// dir rather than call order — that keeps it both race-free (the map is
// read-only during the run) and independent of the nondeterministic
// order in which the concurrent fetches arrive. The merge-queue lookup
// (`gh repo view` then `gh api graphql`) and the viewer-login lookup
// (`gh api user`) get benign fixed payloads so a test only has to
// declare each repo's bulk-status output.
type repoStubRunner struct {
	prListByDir map[string]string
}

func (r *repoStubRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	// Where the repo is hosted is asked before anything is asked of gh, and a repo
	// that is not on GitHub is skipped without a single gh call — see
	// fetchRepoPRStatus. A stub that did not answer this would have every test in
	// here exercising the skip.
	if name == "git" {
		return "git@github.com:o/r.git", nil
	}
	if name == "gh" && len(args) >= 2 && args[0] == "repo" && args[1] == "view" {
		return `{"owner":{"login":"o"},"name":"r"}`, nil
	}
	if name == "gh" && len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
		return `{"data":{"repository":{"pullRequests":{"nodes":[]}}}}`, nil
	}
	if name == "gh" && len(args) >= 2 && args[0] == "api" && args[1] == "user" {
		return "testuser", nil
	}
	return r.prListByDir[dir], nil
}

// forgeStubRunner answers the origin-host probe per repo dir and records every
// gh call, so a test can say both where a repo is hosted and whether anything was
// asked of gh about it.
type forgeStubRunner struct {
	originByDir map[string]string
	mu          sync.Mutex
	ghDirs      []string
}

func (r *forgeStubRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	if name == "git" {
		return r.originByDir[dir], nil
	}
	r.mu.Lock()
	r.ghDirs = append(r.ghDirs, dir)
	r.mu.Unlock()
	if len(args) >= 2 && args[0] == "repo" && args[1] == "view" {
		return `{"owner":{"login":"o"},"name":"r"}`, nil
	}
	if len(args) >= 2 && args[0] == "api" && args[1] == "graphql" {
		return `{"data":{"repository":{"pullRequests":{"nodes":[]}}}}`, nil
	}
	if len(args) >= 2 && args[0] == "api" && args[1] == "user" {
		return "testuser", nil
	}
	return `[{"number":3,"headRefName":"andrew/x","url":"https://example/x/3","state":"OPEN","isDraft":false,"reviewDecision":"","statusCheckRollup":[],"mergeStateStatus":"CLEAN"}]`, nil
}

func (r *forgeStubRunner) ghCallsFor(dir string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, d := range r.ghDirs {
		if d == dir {
			n++
		}
	}
	return n
}

// TestARepoThatIsNotOnGitHubIsSkippedWithoutAskingGH.
//
// awp's projects are not all on GitHub, and gh fails the same way on every call
// against a GitLab remote. Four failed subprocesses per repo per minute is the
// cost of asking anyway — and because nothing landed in the cache, the deck never
// started the repo's cooldown and asked again on the next refresh.
//
// The empty cache entry is the other half: it is what says the question was asked
// and answered.
func TestARepoThatIsNotOnGitHubIsSkippedWithoutAskingGH(t *testing.T) {
	home := withTempHome(t)
	gitlab := t.TempDir()
	hub := t.TempDir()

	runner := &forgeStubRunner{originByDir: map[string]string{
		gitlab: "git@gitlab.com:fastgrowingtrees/harbor-works.git",
		hub:    "git@github.com:andrewcohen/awp.git",
	}}
	job := jobs.Job{ID: "test-job", Spec: jobs.Spec{Action: jobs.ActionPRStatus, Repos: []string{gitlab, hub}}}
	if err := runPRStatusFromSpec(runner, job, noopReporter{}); err != nil {
		t.Fatalf("runPRStatusFromSpec: %v", err)
	}

	if n := runner.ghCallsFor(gitlab); n != 0 {
		t.Errorf("the GitLab repo cost %d gh calls, want none", n)
	}
	if n := runner.ghCallsFor(hub); n == 0 {
		t.Error("the GitHub repo was not fetched at all, so the skip is too broad")
	}

	body, err := os.ReadFile(filepath.Join(home, ".awp", prStatusCacheName))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var cache prStatusCacheFile
	if err := json.Unmarshal(body, &cache); err != nil {
		t.Fatalf("parse cache: %v", err)
	}
	entry, present := cache.Repos[gitlab]
	if !present {
		t.Fatal("the skipped repo landed nothing in the cache, so the deck will ask again next refresh")
	}
	if len(entry.PRs) != 0 {
		t.Errorf("the skipped repo cached %d PRs", len(entry.PRs))
	}
}
