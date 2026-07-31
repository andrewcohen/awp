package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/jobs"
	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/state"
	"github.com/andrewcohen/awp/internal/workspace"
)

// threadStubRunner answers the two calls FetchReviewThreads makes — `gh repo
// view` for owner/name, then the graphql query — and the calls the surrounding
// pr-status fetch makes.
//
// Dispatch is on the query text rather than call order: the job fetches
// concurrently, so an order-keyed fixture would be both racy and dependent on
// which goroutine happens to arrive first.
type threadStubRunner struct {
	threadsJSON string
	// threadsErr, when set, is returned in place of threadsJSON.
	threadsErr error

	mu    sync.Mutex
	calls int
}

func (r *threadStubRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch {
	case name != "gh":
		return "", nil
	case strings.HasPrefix(joined, "repo view"):
		return `{"owner":{"login":"o"},"name":"r"}`, nil
	case strings.Contains(joined, "reviewThreads"):
		r.mu.Lock()
		r.calls++
		r.mu.Unlock()
		if r.threadsErr != nil {
			return "", r.threadsErr
		}
		return r.threadsJSON, nil
	case strings.HasPrefix(joined, "api graphql"):
		// The merge-queue lookup, which shares the graphql endpoint.
		return `{"data":{"repository":{"pullRequests":{"nodes":[]}}}}`, nil
	case strings.HasPrefix(joined, "api user"):
		return "testuser", nil
	}
	// `gh pr list`: no open PRs, so the test is only exercising the mirror.
	return "[]", nil
}

func (r *threadStubRunner) threadCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// threadsPayload is one unresolved thread on a.go:12 with a single comment.
func threadsPayload(body string) string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{` +
		`"id":"T1","isResolved":false,"isOutdated":false,"path":"a.go",` +
		`"line":12,"startLine":null,"diffSide":"RIGHT",` +
		`"comments":{"nodes":[{"author":{"login":"reviewer"},"body":"` + body + `"}]}` +
		`}]}}}}}`
}

// seedPins writes workspace state for repo: name → pinned PR number.
func seedPins(t *testing.T, repo string, pins map[string]int) {
	t.Helper()
	entries := map[string]workspace.Entry{}
	for name, pr := range pins {
		entries[name] = workspace.Entry{Name: name, Path: repo, PRNumber: pr}
	}
	if err := state.NewJSONStore().Save(repo, entries); err != nil {
		t.Fatalf("seed state: %v", err)
	}
}

// mirroredThreads reads what the mirror holds for a workspace, without creating
// a store — an absent store and an empty one both read as no threads, and the
// difference matters to these tests.
func mirroredThreads(t *testing.T, repo, workspaceName string) []review.Thread {
	t.Helper()
	id := review.ID(review.Target{Kind: review.TargetWorking, Workspace: workspaceName})
	if _, err := os.Stat(config.ReviewStorePath(repo, id)); err != nil {
		return nil
	}
	store := review.Store{}
	r, err := store.Open(repo, review.Target{Kind: review.TargetWorking, Workspace: workspaceName})
	if err != nil {
		t.Fatalf("open review for %s: %v", workspaceName, err)
	}
	return store.Threads(r)
}

// The whole point of moving the fetch here: the reviewers' conversation lands in
// the mirror on the pr-status pass, so pressing `c` on a PR-backed workspace
// shows it without the deck making a network call of its own.
func TestPRStatusJobMirrorsThreadsForPinnedWorkspaces(t *testing.T) {
	withTempHome(t)
	repo := t.TempDir()
	// Two workspaces on the same PR — a review workspace beside the author's.
	// Both get the mirror, from one fetch.
	seedPins(t, repo, map[string]int{"ws-author": 7, "ws-review": 7, "ws-none": 0})

	runner := &threadStubRunner{threadsJSON: threadsPayload("please rename this")}
	job := jobs.Job{ID: "j", Spec: jobs.Spec{Action: jobs.ActionPRStatus, Repos: []string{repo}}}
	if err := runPRStatusFromSpec(runner, job, noopReporter{}); err != nil {
		t.Fatalf("runPRStatusFromSpec: %v", err)
	}

	for _, ws := range []string{"ws-author", "ws-review"} {
		threads := mirroredThreads(t, repo, ws)
		if len(threads) != 1 {
			t.Fatalf("%s: expected 1 mirrored thread, got %d", ws, len(threads))
		}
		if threads[0].ID != "T1" || threads[0].Path != "a.go" || threads[0].Line != 12 {
			t.Fatalf("%s: unexpected thread %+v", ws, threads[0])
		}
		if len(threads[0].Comments) != 1 || threads[0].Comments[0].Author != "reviewer" {
			t.Fatalf("%s: unexpected comments %+v", ws, threads[0].Comments)
		}
		if threads[0].Side != review.SideNew {
			t.Fatalf("%s: RIGHT should mirror as the new side, got %v", ws, threads[0].Side)
		}
	}
	if got := runner.threadCalls(); got != 1 {
		t.Fatalf("expected one fetch for the shared PR, got %d", got)
	}
	// A workspace with no PR has nothing to mirror, and must not have a review
	// store conjured for it.
	if threads := mirroredThreads(t, repo, "ws-none"); threads != nil {
		t.Fatalf("unpinned workspace got threads: %+v", threads)
	}
}

// A failed fetch must leave the previous conversation on screen. Blanking it
// would turn a transient gh failure into "the reviewers said nothing".
func TestThreadFetchFailureKeepsThePreviousMirror(t *testing.T) {
	withTempHome(t)
	repo := t.TempDir()
	pins := map[int][]string{7: {"ws-author"}}
	sem := make(chan struct{}, 2)

	ok := &threadStubRunner{threadsJSON: threadsPayload("first pass")}
	if got := mirrorPinnedReviewThreads(github.New(fixedDirRunner{base: ok, dir: repo}), repo, pins, sem); got != 1 {
		t.Fatalf("first pass mirrored %d threads, want 1", got)
	}

	broken := &threadStubRunner{threadsErr: errors.New("gh: network is unreachable")}
	if got := mirrorPinnedReviewThreads(github.New(fixedDirRunner{base: broken, dir: repo}), repo, pins, sem); got != 0 {
		t.Fatalf("failed pass reported %d threads mirrored", got)
	}
	threads := mirroredThreads(t, repo, "ws-author")
	if len(threads) != 1 || threads[0].Comments[0].Body != "first pass" {
		t.Fatalf("failed fetch disturbed the mirror: %+v", threads)
	}
}

// An empty fetch is nothing to say, not an instruction to clear: creating a
// store per pinned workspace per pass would litter ~/.awp with reviews nobody
// opened.
func TestEmptyThreadFetchDoesNotCreateAStore(t *testing.T) {
	withTempHome(t)
	repo := t.TempDir()
	empty := &threadStubRunner{
		threadsJSON: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`,
	}
	if got := mirrorPinnedReviewThreads(github.New(fixedDirRunner{base: empty, dir: repo}), repo, map[int][]string{7: {"ws-author"}}, make(chan struct{}, 1)); got != 0 {
		t.Fatalf("expected nothing mirrored, got %d", got)
	}
	id := review.ID(review.Target{Kind: review.TargetWorking, Workspace: "ws-author"})
	if _, err := os.Stat(config.ReviewStorePath(repo, id)); err == nil {
		t.Fatal("an empty fetch created a review store")
	}
}

// The pinned map is grouped by PR because that is how it is consumed — one fetch
// per PR, however many workspaces point at it.
func TestPinnedWorkspacesGroupByPR(t *testing.T) {
	withTempHome(t)
	repo := t.TempDir()
	seedPins(t, repo, map[string]int{"a": 7, "b": 7, "c": 9, "d": 0})

	byPR := pinnedWorkspacesByPR(state.NewJSONStore(), repo)
	if len(byPR) != 2 {
		t.Fatalf("expected 2 pinned PRs, got %v", byPR)
	}
	if got := strings.Join(byPR[7], ","); got != "a,b" {
		t.Fatalf("PR 7 workspaces = %q, want \"a,b\" in order", got)
	}
	if got := strings.Join(byPR[9], ","); got != "c" {
		t.Fatalf("PR 9 workspaces = %q", got)
	}
	nums := prNumbersOf(byPR)
	if !nums[7] || !nums[9] || len(nums) != 2 {
		t.Fatalf("prNumbersOf = %v", nums)
	}
	// nil in, nil out: a state-load failure means "no pins", and the consumers
	// distinguish that from an empty set only by nil-ness.
	if prNumbersOf(nil) != nil {
		t.Fatal("prNumbersOf(nil) should stay nil")
	}
}
