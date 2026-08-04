package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// threadRunner replays canned outputs in order and records the argv it was
// called with, so the gh invocations can be asserted without a network.
type threadRunner struct {
	outs  []string
	errs  []error
	calls [][]string
	n     int
}

func (r *threadRunner) Run(_ context.Context, dir string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	i := r.n
	r.n++
	var out string
	var err error
	if i < len(r.outs) {
		out = r.outs[i]
	}
	if i < len(r.errs) {
		err = r.errs[i]
	}
	_ = dir
	return out, err
}

const repoViewJSON = `{"owner":{"login":"acme"},"name":"widgets"}`

const threadsJSON = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
  {"id":"T1","isResolved":false,"isOutdated":false,"path":"a.go","line":12,"startLine":0,"diffSide":"RIGHT",
   "comments":{"nodes":[{"id":"PRRC_a","body":"this leaks","author":{"login":"alice"}},{"id":"PRRC_b","body":"agreed","author":{"login":"bob"}}]}},
  {"id":"T2","isResolved":true,"isOutdated":true,"path":"b.go","line":3,"startLine":1,"diffSide":"LEFT",
   "comments":{"nodes":[{"body":"settled","author":{"login":"carol"}}]}},
  {"id":"T3","isResolved":false,"isOutdated":false,"path":"c.go","line":9,"startLine":0,"diffSide":"RIGHT",
   "comments":{"nodes":[{"body":"   ","author":{"login":"dave"}}]}}
]}}}}}`

func TestFetchReviewThreadsParsesThreads(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, threadsJSON}}
	got, err := New(r).FetchReviewThreads(7)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// The whitespace-only thread carries no signal and is dropped.
	if len(got) != 2 {
		t.Fatalf("expected 2 threads, got %d: %+v", len(got), got)
	}
	first := got[0]
	if first.ID != "T1" || first.Path != "a.go" || first.Line != 12 || first.Side != "RIGHT" {
		t.Fatalf("unexpected first thread: %+v", first)
	}
	if len(first.Comments) != 2 || first.Comments[0].Author != "alice" {
		t.Fatalf("expected both comments with authors, got %+v", first.Comments)
	}
	// The per-comment node id comes back too. It is what lets a mirrored thread be
	// recognised as the echo of a comment published from here, instead of the diff
	// showing both copies of the same conversation.
	if first.Comments[0].ID != "PRRC_a" || first.Comments[1].ID != "PRRC_b" {
		t.Fatalf("expected the comment node ids, got %+v", first.Comments)
	}
	if first.Resolved || first.Outdated {
		t.Fatalf("expected the first thread unresolved and current: %+v", first)
	}
	if !got[1].Resolved || !got[1].Outdated || got[1].StartLine != 1 {
		t.Fatalf("expected the second thread resolved, outdated, multi-line: %+v", got[1])
	}
}

// Threads come from GraphQL because REST exposes neither thread grouping nor
// resolution state.
func TestFetchReviewThreadsUsesGraphQLWithResolvedRepo(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, threadsJSON}}
	if _, err := New(r).FetchReviewThreads(7); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected repo view then graphql, got %d calls: %v", len(r.calls), r.calls)
	}
	if got := strings.Join(r.calls[0], " "); !strings.Contains(got, "repo view") {
		t.Fatalf("first call should resolve the repo, got %q", got)
	}
	joined := strings.Join(r.calls[1], " ")
	for _, want := range []string{"api", "graphql", "owner=acme", "name=widgets", "number=7", "reviewThreads"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("graphql call missing %q, got %q", want, joined)
		}
	}
}

func TestFetchReviewThreadsSurfacesErrors(t *testing.T) {
	r := &threadRunner{outs: []string{""}, errs: []error{errors.New("no auth")}}
	if _, err := New(r).FetchReviewThreads(7); err == nil {
		t.Fatal("expected the repo-view failure to surface")
	}
	r = &threadRunner{outs: []string{repoViewJSON, "{not json"}}
	if _, err := New(r).FetchReviewThreads(7); err == nil {
		t.Fatal("expected a parse failure to surface")
	}
}

// Resolving is a GraphQL mutation; REST has no equivalent.
func TestResolveAndUnresolveUseMutations(t *testing.T) {
	r := &threadRunner{outs: []string{`{"data":{}}`}}
	if err := New(r).ResolveReviewThread("T1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	joined := strings.Join(r.calls[0], " ")
	for _, want := range []string{"api", "graphql", "resolveReviewThread", "id=T1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("resolve call missing %q, got %q", want, joined)
		}
	}

	r = &threadRunner{outs: []string{`{"data":{}}`}}
	if err := New(r).UnresolveReviewThread("T1"); err != nil {
		t.Fatalf("unresolve: %v", err)
	}
	if joined := strings.Join(r.calls[0], " "); !strings.Contains(joined, "unresolveReviewThread") {
		t.Fatalf("expected the unresolve mutation, got %q", joined)
	}
}

func TestResolveRejectsEmptyThreadID(t *testing.T) {
	r := &threadRunner{}
	if err := New(r).ResolveReviewThread("  "); err == nil {
		t.Fatal("expected an empty thread id to be rejected")
	}
	if len(r.calls) != 0 {
		t.Fatal("expected no gh call for an empty thread id")
	}
}
