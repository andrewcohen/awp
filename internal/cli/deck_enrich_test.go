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

	first := h.heads(jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)
	if first[ws].desc != "wip: something" {
		t.Fatalf("first read = %+v, want the runner's description", first[ws])
	}
	after := runner.count()
	if after == 0 {
		t.Fatal("first read ran no jj at all")
	}

	for i := 0; i < 10; i++ {
		got := h.heads(jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)
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

	h.heads(jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)
	before := runner.count()

	recordOp("op2")
	runner.out = "abcd1234\twip: after"
	got := h.heads(jj.New(runner), []headSpec{{path: ws}}, enrichTimeout)

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

	h.heads(jj.New(runner), []headSpec{{path: ws, bookmark: "andrew/one"}}, enrichTimeout)
	before := runner.count()

	h.heads(jj.New(runner), []headSpec{{path: ws, bookmark: "andrew/two"}}, enrichTimeout)
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

	got := h.heads(jj.New(runner), []headSpec{{path: filepath.Join(t.TempDir(), "gone")}}, enrichTimeout)

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

	h.heads(jj.New(runner), []headSpec{{path: dir}}, enrichTimeout)
	before := runner.count()
	if before == 0 {
		t.Fatal("an unreadable head ran no jj")
	}
	h.heads(jj.New(runner), []headSpec{{path: dir}}, enrichTimeout)
	if runner.count() == before {
		t.Fatal("an unreadable head was cached; it has nothing to invalidate against")
	}
}
