package github

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPostReviewCommentSendsAnchorAndSide(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"id":123,"node_id":"PRRC_abc"}`}}
	id, err := New(r).PostReviewComment(9, NewComment{
		Path: "a.go", Line: 42, Side: "RIGHT", Body: ":robot: leaks", CommitID: "deadbeef",
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	// The node id is what makes a retry able to recognise this comment as done.
	if id != "PRRC_abc" {
		t.Fatalf("expected the node id back, got %q", id)
	}
	joined := strings.Join(r.calls[1], " ")
	for _, want := range []string{"api", "--method", "POST", "pulls/9/comments", "path=a.go", "line=42", "side=RIGHT", "commit_id=deadbeef"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("post call missing %q, got %q", want, joined)
		}
	}
}

func TestPostReviewCommentFallsBackToNumericID(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"id":123}`}}
	id, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x", CommitID: "deadbeef"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if id != "123" {
		t.Fatalf("expected the numeric id when no node id is returned, got %q", id)
	}
}

// An unreadable response means the comment posted but its id is unknown.
// Reporting an error would invite a retry that duplicates it, so this is success.
func TestPostReviewCommentTreatsUnparseableResponseAsPosted(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, "not json"}}
	id, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x", CommitID: "deadbeef"})
	if err != nil {
		t.Fatalf("expected an unparseable response to count as posted, got %v", err)
	}
	if id != "" {
		t.Fatalf("expected an unknown id, got %q", id)
	}
}

func TestPostReviewCommentRequiresAnAnchor(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON}}
	if _, err := New(r).PostReviewComment(9, NewComment{Body: "no anchor"}); err == nil {
		t.Fatal("expected a comment with no path or line to be rejected")
	}
}

// A reply joins an existing thread, so it must not also send a line anchor.
func TestPostReviewCommentReplyUsesInReplyTo(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"id":5}`}}
	if _, err := New(r).PostReviewComment(9, NewComment{InReplyTo: "77", Body: "ack"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	joined := strings.Join(r.calls[1], " ")
	if !strings.Contains(joined, "in_reply_to=77") {
		t.Fatalf("expected in_reply_to, got %q", joined)
	}
	if strings.Contains(joined, "path=") {
		t.Fatalf("a reply should not carry a path anchor, got %q", joined)
	}
}

func TestPostReviewCommentSurfacesFailures(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, "422 Unprocessable"}, errs: []error{nil, errors.New("exit 1")}}
	if _, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x", CommitID: "deadbeef"}); err == nil {
		t.Fatal("expected a post failure to surface so it can be retried")
	}
}

func TestPostPRCommentRejectsEmptyBody(t *testing.T) {
	r := &threadRunner{}
	if _, err := New(r).PostPRComment(9, "  "); err == nil {
		t.Fatal("expected an empty comment to be rejected")
	}
	if len(r.calls) != 0 {
		t.Fatal("expected no gh call for an empty comment")
	}
}

// A PR-level comment posts to the issues endpoint — that is what GitHub calls a
// comment on the PR itself — and returns its id, so a re-publish can tell it
// already landed.
func TestPostPRCommentPostsAndReturnsAnID(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"id":88,"node_id":"IC_kwDO88"}`}}
	id, err := New(r).PostPRComment(9, "reviewed internal/cli")
	if err != nil {
		t.Fatalf("pr comment: %v", err)
	}
	if id != "IC_kwDO88" {
		t.Fatalf("expected the node id recorded, got %q", id)
	}
	joined := strings.Join(r.calls[1], " ")
	for _, want := range []string{"api", "POST", "issues/9/comments", "reviewed internal/cli"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("pr comment call missing %q, got %q", want, joined)
		}
	}
}

// An unreadable response body means the comment posted but its id is unknown.
// Reporting that as an error would invite a retry that double-posts.
func TestPostPRCommentTreatsAnUnreadableIDAsSuccess(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, "not json"}}
	id, err := New(r).PostPRComment(9, "body")
	if err != nil {
		t.Fatalf("expected success with an unknown id, got %v", err)
	}
	if id != "" {
		t.Fatalf("expected no id, got %q", id)
	}
}

// A ranged comment goes up as GitHub describes one: `line` is its last line, with
// `start_line` above it. start_side has to be sent too — GitHub defaults it to the
// PR's side rather than to the side already given for the end.
func TestPostReviewCommentSendsARange(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"node_id":"PRRC_abc"}`}}
	if _, err := New(r).PostReviewComment(9, NewComment{
		Path: "a.go", Line: 18, StartLine: 12, Side: "LEFT", Body: "this block", CommitID: "deadbeef",
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	joined := strings.Join(r.calls[1], " ")
	for _, want := range []string{"line=18", "start_line=12", "side=LEFT", "start_side=LEFT"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("post call missing %q, got %q", want, joined)
		}
	}
}

// A single-line comment sends no start_line: GitHub reads a start equal to the end
// as a malformed range rather than as one line.
func TestPostReviewCommentOmitsStartLineForOneLine(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"node_id":"PRRC_abc"}`}}
	if _, err := New(r).PostReviewComment(9, NewComment{
		Path: "a.go", Line: 12, StartLine: 12, Body: "one line", CommitID: "deadbeef",
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if joined := strings.Join(r.calls[1], " "); strings.Contains(joined, "start_line") {
		t.Fatalf("expected no start_line for a single-line comment, got %q", joined)
	}
}

// The verdict goes up as its own review submission, after the comments.
func TestSubmitReviewSendsTheEventAndBody(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"id":77,"node_id":"PRR_abc"}`}}
	id, err := New(r).SubmitReview(9, EventRequestChanges, "two things to fix")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if id != "PRR_abc" {
		t.Fatalf("expected the review's node id back, got %q", id)
	}
	joined := strings.Join(r.calls[1], " ")
	for _, want := range []string{"pulls/9/reviews", "event=REQUEST_CHANGES", "body=two things to fix"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("submit call missing %q, got %q", want, joined)
		}
	}
}

// An approval with nothing to say sends no body at all: an empty one is the
// difference between "approved" and "approved, with an empty comment attached".
func TestSubmitReviewOmitsAnEmptyBody(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, `{"node_id":"PRR_abc"}`}}
	if _, err := New(r).SubmitReview(9, EventApprove, "  "); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if joined := strings.Join(r.calls[1], " "); strings.Contains(joined, "body=") {
		t.Fatalf("expected no body on an empty approval, got %q", joined)
	}
}

// GitHub rejects these without a summary, so we do too — before spending a round
// trip to be told.
func TestSubmitReviewRequiresABodyWhereGitHubDoes(t *testing.T) {
	for _, event := range []string{EventComment, EventRequestChanges} {
		if !EventNeedsBody(event) {
			t.Fatalf("%s should need a body", event)
		}
		r := &threadRunner{outs: []string{repoViewJSON, "{}"}}
		if _, err := New(r).SubmitReview(9, event, ""); err == nil {
			t.Fatalf("%s: expected a bodyless review refused", event)
		}
	}
	if EventNeedsBody(EventApprove) {
		t.Fatal("an approval should not need a body")
	}
}

// An unknown event never reaches GitHub.
func TestSubmitReviewRejectsAnUnknownEvent(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, "{}"}}
	if _, err := New(r).SubmitReview(9, "LGTM", "x"); err == nil {
		t.Fatal("expected an unknown event refused")
	}
	if len(r.calls) != 0 {
		t.Fatalf("expected no API call, got %v", r.calls)
	}
}

// Which repository a call is about comes from where gh runs, so a client told to
// work In a directory has to use it for every call — the repo lookup included.
//
// This is the bug that made publishing 404: the deck is a tmux popup launched from
// wherever you happen to be, so resolving the repo from the process's own directory
// addressed whatever repo *that* belonged to. A 404 was the lucky outcome; had the
// launch directory's repo had a PR with the same number, the comments would have
// posted to it.
func TestClientInRunsEveryCallInThatDirectory(t *testing.T) {
	r := &dirRunner{outs: []string{repoViewJSON, `{"node_id":"PRRC_abc"}`}}
	if _, err := New(r).In("/repos/theirs").PostReviewComment(54, NewComment{
		Path: "a.go", Line: 1, Body: "x", CommitID: "deadbeef",
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if len(r.dirs) != 2 {
		t.Fatalf("expected two calls (repo lookup, then the post), got %v", r.dirs)
	}
	for i, dir := range r.dirs {
		if dir != "/repos/theirs" {
			t.Fatalf("call %d ran in %q, not the directory it was given", i, dir)
		}
	}
	// And without In, the dir is empty — the process's own, which is right only for a
	// command the user typed in the repo they meant.
	plain := &dirRunner{outs: []string{repoViewJSON, `{"node_id":"x"}`}}
	if _, err := New(plain).PostReviewComment(1, NewComment{Path: "a.go", Line: 1, Body: "x", CommitID: "deadbeef"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	for i, dir := range plain.dirs {
		if dir != "" {
			t.Fatalf("call %d ran in %q, expected the process's own directory", i, dir)
		}
	}
}

// dirRunner records the directory each call was made in.
type dirRunner struct {
	outs []string
	dirs []string
	n    int
}

func (r *dirRunner) Run(_ context.Context, dir string, _ string, _ ...string) (string, error) {
	r.dirs = append(r.dirs, dir)
	out := ""
	if r.n < len(r.outs) {
		out = r.outs[r.n]
	}
	r.n++
	return out, nil
}

// GitHub requires commit_id on a new review comment and rejects the whole request
// without it — with an error listing every alternative shape that also did not
// match, which is not a readable way to learn you omitted one field. Refused here
// so a run says it once instead of once per comment.
func TestPostReviewCommentRequiresTheCommit(t *testing.T) {
	r := &threadRunner{outs: []string{repoViewJSON, "{}"}}
	_, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("expected a comment with no commit refused, got %v", err)
	}
	// And it is always sent, since it is always required.
	ok := &threadRunner{outs: []string{repoViewJSON, `{"node_id":"x"}`}}
	if _, err := New(ok).PostReviewComment(9, NewComment{
		Path: "a.go", Line: 1, Body: "x", CommitID: "cafe1234",
	}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if joined := strings.Join(ok.calls[1], " "); !strings.Contains(joined, "commit_id=cafe1234") {
		t.Fatalf("expected commit_id sent, got %q", joined)
	}
	// A reply carries none: it joins a thread that is already anchored.
	reply := &threadRunner{outs: []string{repoViewJSON, `{"node_id":"x"}`}}
	if _, err := New(reply).PostReviewComment(9, NewComment{InReplyTo: "PRRC_1", Body: "answering"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
}
