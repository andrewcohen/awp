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
	got, err := New(r, "").FetchReviewThreads(7)
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
	if _, err := New(r, "").FetchReviewThreads(7); err != nil {
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
	if _, err := New(r, "").FetchReviewThreads(7); err == nil {
		t.Fatal("expected the repo-view failure to surface")
	}
	r = &threadRunner{outs: []string{repoViewJSON, "{not json"}}
	if _, err := New(r, "").FetchReviewThreads(7); err == nil {
		t.Fatal("expected a parse failure to surface")
	}
}

// Resolving is a GraphQL mutation; REST has no equivalent.
func TestResolveAndUnresolveUseMutations(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{"resolveReviewThread":{"thread":{"id":"T1","isResolved":true}}}}`}
	if err := New(r, "").ResolveReviewThread("T1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(r.bodies) != 1 {
		t.Fatalf("expected one request, got %d", len(r.bodies))
	}
	if q, _ := r.bodies[0]["query"].(string); !strings.Contains(q, "resolveReviewThread") {
		t.Fatalf("expected the resolve mutation, got %q", q)
	}
	if got := r.vars(0)["id"]; got != "T1" {
		t.Fatalf("expected the thread id in the variables, got %v", got)
	}

	r = &gqlRunner{reply: `{"data":{"unresolveReviewThread":{"thread":{"id":"T1","isResolved":false}}}}`}
	if err := New(r, "").UnresolveReviewThread("T1"); err != nil {
		t.Fatalf("unresolve: %v", err)
	}
	if q, _ := r.bodies[0]["query"].(string); !strings.Contains(q, "unresolveReviewThread") {
		t.Fatalf("expected the unresolve mutation, got %q", q)
	}
}

// The caller mirrors "resolved" locally on success, and the diff hides resolved
// threads — so a resolve reported as done when GitHub refused it makes a whole
// conversation disappear while the mirror insists GitHub resolved it. Every way
// GitHub can decline has to come back as an error.
func TestResolveReportsWhatGitHubActuallySaid(t *testing.T) {
	for _, tc := range []struct {
		name, reply, wantIn string
	}{
		{
			// GraphQL reports refusals in the body, and gh does not always exit non-zero
			// for them. This used to be read as success.
			name:   "errors array",
			reply:  `{"errors":[{"message":"Could not resolve to a node with the global id"}]}`,
			wantIn: "global id",
		},
		{
			name:   "no thread came back",
			reply:  `{"data":{"resolveReviewThread":{"thread":null}}}`,
			wantIn: "no thread",
		},
		{
			// Accepted the call and did not do it.
			name:   "state did not change",
			reply:  `{"data":{"resolveReviewThread":{"thread":{"id":"T1","isResolved":false}}}}`,
			wantIn: "still reports it as unresolved",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &gqlRunner{reply: tc.reply}
			err := New(r, "").ResolveReviewThread("T1")
			if err == nil {
				t.Fatal("expected the refusal to surface")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("expected %q in the error, got %v", tc.wantIn, err)
			}
		})
	}
	// And the same for reopening, which fails the same way in the other direction.
	r := &gqlRunner{reply: `{"data":{"unresolveReviewThread":{"thread":{"id":"T1","isResolved":true}}}}`}
	err := New(r, "").UnresolveReviewThread("T1")
	if err == nil || !strings.Contains(err.Error(), "still reports it as resolved") {
		t.Fatalf("expected an unresolve that did nothing to be reported, got %v", err)
	}
}

func TestResolveRejectsEmptyThreadID(t *testing.T) {
	r := &threadRunner{}
	if err := New(r, "").ResolveReviewThread("  "); err == nil {
		t.Fatal("expected an empty thread id to be rejected")
	}
	if len(r.calls) != 0 {
		t.Fatal("expected no gh call for an empty thread id")
	}
}

// A reply goes into the thread it answers, and its body never touches argv.
func TestReplyToReviewThreadPostsIntoTheThread(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"PRRC_new"}}}}`}
	// A body with the shell metacharacters that broke agent-filed comments when
	// they went through gh's -f flags.
	const body = "answered: `git log` $(whoami) \"quoted\""
	id, err := New(r, "").ReplyToReviewThread("T1", body)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if id != "PRRC_new" {
		t.Fatalf("expected the new comment's id back, got %q", id)
	}
	if len(r.bodies) != 1 {
		t.Fatalf("expected one request, got %d", len(r.bodies))
	}
	query, _ := r.bodies[0]["query"].(string)
	if !strings.Contains(query, "addPullRequestReviewThreadReply") {
		t.Fatalf("expected the reply mutation, got %q", query)
	}
	// Staging it into a pending review is what the input's pullRequestReviewId
	// would do, and a reply must not need a verdict to go out.
	if strings.Contains(query, "pullRequestReviewId") {
		t.Fatalf("a reply is posted on its own, not staged into a review: %q", query)
	}
	vars := r.vars(0)
	if vars["threadId"] != "T1" {
		t.Fatalf("expected threadId T1, got %v", vars["threadId"])
	}
	if vars["body"] != body {
		t.Fatalf("the body was altered on the way out: %q", vars["body"])
	}
}

func TestReplyToReviewThreadRejectsEmptyInput(t *testing.T) {
	for _, tc := range []struct{ name, threadID, body string }{
		{"no thread", "  ", "something"},
		{"no body", "T1", "  \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &gqlRunner{}
			if _, err := New(r, "").ReplyToReviewThread(tc.threadID, tc.body); err == nil {
				t.Fatal("expected the empty input to be rejected")
			}
			if len(r.bodies) != 0 {
				t.Fatal("expected no gh call")
			}
		})
	}
}

// A reply GitHub did not confirm must not be reported as posted: the caller marks
// the draft published on success, and a false success loses what was typed.
func TestReplyToReviewThreadFailsWhenNoCommentComesBack(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{"addPullRequestReviewThreadReply":{"comment":null}}}`}
	if _, err := New(r, "").ReplyToReviewThread("T1", "hello"); err == nil {
		t.Fatal("expected a missing comment to be an error")
	}
	r = &gqlRunner{reply: `{"errors":[{"message":"thread is gone"}]}`}
	_, err := New(r, "").ReplyToReviewThread("T1", "hello")
	if err == nil || !strings.Contains(err.Error(), "thread is gone") {
		t.Fatalf("expected GitHub's own message, got %v", err)
	}
}
