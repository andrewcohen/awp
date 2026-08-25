package cli

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/jj"
)

// countingRunner is a jj.Runner that answers plausibly and counts how many
// commands it was asked to run. The count is the whole point of these tests:
// what the deck's idle refresh costs is the number of jj subprocesses it
// starts, not how long any one of them takes.
type countingRunner struct {
	mu    sync.Mutex
	calls int
	out   string
}

func (r *countingRunner) Run(_ context.Context, _ string, _ string, _ ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.out, nil
}

func (r *countingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// fakeWorkspace writes the .jj layout jj.OpHead reads and returns the
// workspace path, plus a func that records a new operation in it.
func fakeWorkspace(t *testing.T, head string) (string, func(string)) {
	t.Helper()
	dir := t.TempDir()
	heads := filepath.Join(dir, ".jj", "repo", "op_heads", "heads")
	if err := os.MkdirAll(heads, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(h string) {
		entries, err := os.ReadDir(heads)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if err := os.Remove(filepath.Join(heads, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(heads, h), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(head)
	return dir, write
}

const enrichTimeout = 5 * time.Second

// The baseline case: a deck refreshing over and over with nothing happening in
// any repo. The first pass pays for the reads; every pass after it must run no
// jj at all, which is the difference between an idle deck costing most of a
// core and costing nothing.
func TestIdleRefreshesRunNoJJ(t *testing.T) {
	ws, _ := fakeWorkspace(t, "op1")
	runner := &countingRunner{out: "abcd1234\twip: something"}
	h := newHeadEnricher()

	first := h.heads(context.Background(), jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)
	if first[ws].desc != "wip: something" {
		t.Fatalf("first read = %+v, want the runner's description", first[ws])
	}
	after := runner.count()
	if after == 0 {
		t.Fatal("first read ran no jj at all")
	}

	for i := 0; i < 10; i++ {
		got := h.heads(context.Background(), jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)
		if got[ws] != first[ws] {
			t.Fatalf("refresh %d = %+v, want the cached %+v", i, got[ws], first[ws])
		}
	}
	if runner.count() != after {
		t.Fatalf("idle refreshes ran %d jj commands, want 0", runner.count()-after)
	}
}

// The cache is only allowed to be quiet while nothing has happened. An
// operation in the repo — a commit, a describe, a fetch — has to bring the
// reads back.
func TestAnOperationBringsTheReadBack(t *testing.T) {
	ws, recordOp := fakeWorkspace(t, "op1")
	runner := &countingRunner{out: "abcd1234\twip: before"}
	h := newHeadEnricher()

	h.heads(context.Background(), jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)
	before := runner.count()

	recordOp("op2")
	runner.out = "abcd1234\twip: after"
	got := h.heads(context.Background(), jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)

	if runner.count() == before {
		t.Fatal("a new operation ran no jj — the deck would show stale rows forever")
	}
	if got[ws].desc != "wip: after" {
		t.Fatalf("after the operation = %q, want the fresh description", got[ws].desc)
	}
}

// The bookmark is read against the same operation, so changing which bookmark a
// row tracks has to re-read even though the repo has not moved.
func TestChangingTheBookmarkReReads(t *testing.T) {
	ws, _ := fakeWorkspace(t, "op1")
	runner := &countingRunner{out: "abcd1234\twip: x"}
	h := newHeadEnricher()

	h.heads(context.Background(), jj.New(runner), []headSpec{{path: ws, bookmark: "andrew/one"}}, enrichTimeout)
	before := runner.count()

	h.heads(context.Background(), jj.New(runner), []headSpec{{path: ws, bookmark: "andrew/two"}}, enrichTimeout)
	if runner.count() == before {
		t.Fatal("a different bookmark reused the cached commit-id")
	}
}

// The state file outlives the directories it names. An entry whose workspace
// has been deleted has nothing to read, and asking jj to tell us so once per
// refresh is exactly the cost this avoids.
func TestADeletedWorkspaceRunsNoJJ(t *testing.T) {
	runner := &countingRunner{}
	h := newHeadEnricher()

	got := h.heads(context.Background(), jj.New(runner), []headSpec{{path: filepath.Join(t.TempDir(), "gone")}}, enrichTimeout)

	if runner.count() != 0 {
		t.Fatalf("a path with no repo ran %d jj commands, want 0", runner.count())
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no entry for a workspace that is not there", got)
	}
}

// A repo whose head cannot be read is the one case that must not be cached:
// there is no signal to invalidate against, so the honest answer is to keep
// asking jj.
func TestAnUnreadableHeadKeepsAsking(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".jj", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &countingRunner{out: "abcd1234\twip: x"}
	h := newHeadEnricher()

	h.heads(context.Background(), jj.New(runner), []headSpec{{path: dir}}, enrichTimeout)
	before := runner.count()
	if before == 0 {
		t.Fatal("an unreadable head ran no jj")
	}
	h.heads(context.Background(), jj.New(runner), []headSpec{{path: dir}}, enrichTimeout)
	if runner.count() == before {
		t.Fatal("an unreadable head was cached; it has nothing to invalidate against")
	}
}

// blockingRunner is a jj.Runner that hangs until its ctx is cancelled, which is
// what a jj waiting on the repo's operation-log lock looks like from here. It
// records how many of its calls are still in flight so a test can ask the
// question that matters: when heads returned, was anything still running?
type blockingRunner struct {
	mu      sync.Mutex
	running int
	started chan struct{}
}

func newBlockingRunner(specs int) *blockingRunner {
	return &blockingRunner{started: make(chan struct{}, specs)}
}

func (r *blockingRunner) Run(ctx context.Context, _ string, _ string, _ ...string) (string, error) {
	r.mu.Lock()
	r.running++
	r.mu.Unlock()
	r.started <- struct{}{}
	// The real runner is exec.CommandContext, so a cancel is a kill and the call
	// returns. This models that, and nothing else about it.
	<-ctx.Done()
	r.mu.Lock()
	r.running--
	r.mu.Unlock()
	return "", ctx.Err()
}

func (r *blockingRunner) inFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// TestSlowFanOutLeavesNothingRunning is the guarantee that makes the timeout
// mean "stop" rather than "stop waiting".
//
// heads used to select between wg.Wait() and time.After, so a batch that ran
// long returned while every goroutine and every jj subprocess it started kept
// going. The deck asks again every refreshInterval, so those batches stacked:
// the one nobody was waiting for still held a core when the next one started.
// Post-wake, with a cold page cache, they all take the slow path at once.
func TestSlowFanOutLeavesNothingRunning(t *testing.T) {
	const workspaces = 4
	specs := make([]headSpec, 0, workspaces)
	for i := 0; i < workspaces; i++ {
		ws, _ := fakeWorkspace(t, "op1")
		specs = append(specs, headSpec{path: ws})
	}

	runner := newBlockingRunner(workspaces)
	h := newHeadEnricher()

	const timeout = 150 * time.Millisecond
	start := time.Now()
	got := h.heads(context.Background(), jj.New(runner), specs, timeout)
	elapsed := time.Since(start)

	// Every read hung, so there is nothing to report — and, importantly, nothing
	// cached either: a cancelled read is not an answer, and caching the blank it
	// returned would answer later refreshes with it until the repo moved.
	if len(got) != 0 {
		t.Fatalf("heads returned %d entries from reads that never completed: %+v", len(got), got)
	}

	// The call is bounded by the timeout rather than by the reads finishing.
	if elapsed > timeout*4 {
		t.Fatalf("heads took %s with a %s timeout — the deadline is not bounding the batch", elapsed, timeout)
	}

	// The point of the test, and checked with no grace period on purpose. For a
	// heads that waits on its own WaitGroup this is deterministic: every Run has
	// returned before Wait does, so zero is guaranteed rather than likely. Polling
	// with a sleep here instead would pass for a heads that abandoned the batch and
	// merely let the deferred cancel catch up with it afterwards, which is the
	// weaker property and not the one the doc comment claims.
	if n := runner.inFlight(); n != 0 {
		t.Fatalf("%d jj calls still running the instant heads returned — the batch outlived its caller", n)
	}
}
