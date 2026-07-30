package github

import (
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
	id, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x"})
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
	id, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x"})
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
	if _, err := New(r).PostReviewComment(9, NewComment{Path: "a.go", Line: 1, Body: "x"}); err == nil {
		t.Fatal("expected a post failure to surface so it can be retried")
	}
}

func TestPostReviewSummaryRejectsEmptyBody(t *testing.T) {
	r := &threadRunner{}
	if err := New(r).PostReviewSummary(9, "  "); err == nil {
		t.Fatal("expected an empty summary to be rejected")
	}
	if len(r.calls) != 0 {
		t.Fatal("expected no gh call for an empty summary")
	}
}

func TestPostReviewSummaryPosts(t *testing.T) {
	r := &threadRunner{outs: []string{"ok"}}
	if err := New(r).PostReviewSummary(9, "reviewed internal/cli"); err != nil {
		t.Fatalf("summary: %v", err)
	}
	joined := strings.Join(r.calls[0], " ")
	for _, want := range []string{"pr", "comment", "9", "reviewed internal/cli"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("summary call missing %q, got %q", want, joined)
		}
	}
}
