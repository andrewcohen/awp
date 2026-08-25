package github

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// gqlRunner captures the request body gh was handed and replies with a canned payload.
// The body goes through a temp file, so the assertion has to read it back the way gh
// would.
type gqlRunner struct {
	reply string
	err   error
	// bodies is each request's decoded JSON, in order.
	bodies []map[string]any
}

func (r *gqlRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	for i, a := range args {
		if a == "--input" && i+1 < len(args) {
			raw, err := os.ReadFile(args[i+1])
			if err != nil {
				return "", err
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				return "", err
			}
			r.bodies = append(r.bodies, body)
		}
	}
	return r.reply, r.err
}

// vars is the variables map of the nth request.
func (r *gqlRunner) vars(n int) map[string]any {
	v, _ := r.bodies[n]["variables"].(map[string]any)
	return v
}

const stagedReply = `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_1",
 "comments":{"nodes":[{"id":"PRRC_a","path":"a.go","line":12},{"id":"PRRC_b","path":"b.go","line":3}]}}}}}`

// The whole point: every comment goes up in one review. The REST comment endpoint
// creates a single-comment review per call, so N comments became N empty review
// entries on the PR — and GitHub does not allow deleting a submitted review.
func TestCreatePendingReviewSendsEveryThreadInOneCall(t *testing.T) {
	r := &gqlRunner{reply: stagedReply}
	got, err := New(r, "").CreatePendingReview("PR_1", "deadbeef", []DraftThread{
		{Path: "a.go", Line: 12, StartLine: 8, Side: "RIGHT", Body: "a range"},
		{Path: "b.go", Line: 3, Side: "LEFT", Body: "a removed line"},
	})
	if err != nil {
		t.Fatalf("CreatePendingReview: %v", err)
	}
	if len(r.bodies) != 1 {
		t.Fatalf("expected one request for two threads, got %d", len(r.bodies))
	}
	if got.ID != "PRR_1" {
		t.Fatalf("expected the review id back, got %q", got.ID)
	}
	// The ids come back per comment, so a local record can name the conversation it
	// produced rather than pointing everything at the review.
	if id := got.ThreadID("a.go", 12); id != "PRRC_a" {
		t.Fatalf("expected the thread id for a.go:12, got %q", id)
	}
	if id := got.ThreadID("b.go", 3); id != "PRRC_b" {
		t.Fatalf("expected the thread id for b.go:3, got %q", id)
	}
	if id := got.ThreadID("c.go", 1); id != "" {
		t.Fatalf("expected no id for a thread that was not created, got %q", id)
	}

	threads, ok := r.vars(0)["threads"].([]any)
	if !ok || len(threads) != 2 {
		t.Fatalf("expected two threads in the variables, got %#v", r.vars(0)["threads"])
	}
	first, _ := threads[0].(map[string]any)
	if first["path"] != "a.go" || first["line"] != float64(12) {
		t.Fatalf("first thread is wrong: %#v", first)
	}
	// start_side rides with start_line: GitHub defaults it to the side of the pull
	// request rather than to the side given for the end, so a range on the old side
	// would otherwise lose its start.
	if first["startLine"] != float64(8) || first["startSide"] != "RIGHT" {
		t.Fatalf("expected the range's start and side, got %#v", first)
	}
	second, _ := threads[1].(map[string]any)
	if _, has := second["startLine"]; has {
		t.Fatalf("a single-line thread must not claim a range: %#v", second)
	}
	if second["side"] != "LEFT" {
		t.Fatalf("expected the old side preserved, got %#v", second["side"])
	}
}

// No event, so the review is left PENDING — staged, and visible to nobody but the
// author. That is what makes the submit safe to fail.
func TestCreatePendingReviewSendsNoEvent(t *testing.T) {
	r := &gqlRunner{reply: stagedReply}
	if _, err := New(r, "").CreatePendingReview("PR_1", "deadbeef", []DraftThread{{Path: "a.go", Line: 1, Body: "x"}}); err != nil {
		t.Fatalf("CreatePendingReview: %v", err)
	}
	query, _ := r.bodies[0]["query"].(string)
	if strings.Contains(query, "event") {
		t.Fatalf("the staging mutation must not submit:\n%s", query)
	}
	if _, has := r.vars(0)["event"]; has {
		t.Fatal("the staging mutation must not carry an event")
	}
}

// An unset side must not silently become the old one — that would attach a comment to
// code the reviewer was not looking at.
func TestCreatePendingReviewDefaultsToTheNewSide(t *testing.T) {
	r := &gqlRunner{reply: stagedReply}
	if _, err := New(r, "").CreatePendingReview("PR_1", "", []DraftThread{{Path: "a.go", Line: 1, Body: "x"}}); err != nil {
		t.Fatalf("CreatePendingReview: %v", err)
	}
	threads, _ := r.vars(0)["threads"].([]any)
	first, _ := threads[0].(map[string]any)
	if first["side"] != "RIGHT" {
		t.Fatalf("expected RIGHT by default, got %#v", first["side"])
	}
	// An empty commit is omitted rather than sent: GitHub defaults it to the newest
	// commit, while an empty GitObjectID is a type error.
	if _, has := r.vars(0)["oid"]; has {
		t.Fatal("an empty commit must be omitted, not sent")
	}
}

// GraphQL reports failures in the body, so the errors array is what has to be read —
// gh's own message is only that the request failed.
func TestGraphQLErrorsAreReported(t *testing.T) {
	r := &gqlRunner{reply: `{"errors":[{"message":"line must be part of the diff"}]}`}
	_, err := New(r, "").CreatePendingReview("PR_1", "deadbeef", []DraftThread{{Path: "a.go", Line: 999, Body: "x"}})
	if err == nil {
		t.Fatal("expected the GraphQL error surfaced")
	}
	if !strings.Contains(err.Error(), "line must be part of the diff") {
		t.Fatalf("expected GitHub's own message, got %v", err)
	}
}

// A non-zero exit with a readable errors array still reports the array: the message
// there says what to fix.
func TestGraphQLErrorsWinOverTheExitCode(t *testing.T) {
	r := &gqlRunner{reply: `{"errors":[{"message":"Could not resolve to a node"}]}`, err: errors.New("exit status 1")}
	err := New(r, "").SubmitStagedReview("PRR_1", EventApprove, "")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve to a node") {
		t.Fatalf("expected GitHub's message, got %v", err)
	}
}

func TestSubmitStagedReviewRejectsAnUnknownEvent(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{}}`}
	if err := New(r, "").SubmitStagedReview("PRR_1", "LGTM", "body"); err == nil {
		t.Fatal("expected an unknown event rejected")
	}
	if len(r.bodies) != 0 {
		t.Fatal("expected nothing sent for an unknown event")
	}
}

// GitHub's own rule, and its UI's: a verdict that asks for something has to say what.
// Caught before the call so the run says so rather than relaying a validation error.
func TestSubmitStagedReviewNeedsABodyForTheTwoThatAskForOne(t *testing.T) {
	for _, event := range []string{EventComment, EventRequestChanges} {
		r := &gqlRunner{reply: `{"data":{}}`}
		if err := New(r, "").SubmitStagedReview("PRR_1", event, "  "); err == nil {
			t.Fatalf("%s: expected an empty summary rejected", event)
		}
		if len(r.bodies) != 0 {
			t.Fatalf("%s: expected nothing sent", event)
		}
	}
	// An approval needs none, and must not send an empty one — that is the difference
	// between "approved" and "approved, with an empty comment attached".
	r := &gqlRunner{reply: `{"data":{"submitPullRequestReview":{"pullRequestReview":{"state":"APPROVED"}}}}`}
	if err := New(r, "").SubmitStagedReview("PRR_1", EventApprove, ""); err != nil {
		t.Fatalf("approve with no body: %v", err)
	}
	if _, has := r.vars(0)["body"]; has {
		t.Fatal("an empty body must be omitted rather than sent")
	}
}

// Discarding is only ever called on a review that was never submitted — the only kind
// GitHub allows deleting — so that a failed submit leaves nothing staged for a retry to
// duplicate.
func TestDeleteStagedReview(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{"deletePullRequestReview":{"clientMutationId":null}}}`}
	if err := New(r, "").DeleteStagedReview("PRR_1"); err != nil {
		t.Fatalf("DeleteStagedReview: %v", err)
	}
	if r.vars(0)["id"] != "PRR_1" {
		t.Fatalf("expected the review id sent, got %#v", r.vars(0)["id"])
	}
	// Nothing staged is not an error: the caller does not have to check first.
	empty := &gqlRunner{reply: `{"data":{}}`}
	if err := New(empty, "").DeleteStagedReview(" "); err != nil {
		t.Fatalf("expected a quiet no-op, got %v", err)
	}
	if len(empty.bodies) != 0 {
		t.Fatal("expected no call for an empty id")
	}
}

// A comment GitHub already considers outdated comes back with a null line; where it
// was written is what we asked for and what a record can be matched on.
func TestCreatedThreadsFallBackToTheOriginalLine(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{"addPullRequestReview":{"pullRequestReview":{"id":"PRR_1",
	 "comments":{"nodes":[{"id":"PRRC_a","path":"a.go","line":null,"originalLine":42}]}}}}}`}
	got, err := New(r, "").CreatePendingReview("PR_1", "deadbeef", []DraftThread{{Path: "a.go", Line: 42, Body: "x"}})
	if err != nil {
		t.Fatalf("CreatePendingReview: %v", err)
	}
	if id := got.ThreadID("a.go", 42); id != "PRRC_a" {
		t.Fatalf("expected the thread matched on its original line, got %q", id)
	}
}

// A review with no id back is a failure, not a success with an unknown id: submitting
// needs the id, so pretending otherwise would strand the comments as pending.
func TestCreatePendingReviewRequiresAReviewBack(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{"addPullRequestReview":{"pullRequestReview":null}}}`}
	if _, err := New(r, "").CreatePendingReview("PR_1", "deadbeef", []DraftThread{{Path: "a.go", Line: 1, Body: "x"}}); err == nil {
		t.Fatal("expected a missing review to be an error")
	}
}

func TestCreatePendingReviewNeedsAPullRequest(t *testing.T) {
	r := &gqlRunner{reply: `{"data":{}}`}
	if _, err := New(r, "").CreatePendingReview("  ", "deadbeef", nil); err == nil {
		t.Fatal("expected a missing pull request id rejected")
	}
	if len(r.bodies) != 0 {
		t.Fatal("expected nothing sent")
	}
}
