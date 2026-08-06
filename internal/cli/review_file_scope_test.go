package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

// `awp review add --file a.go` with no --line has to reach review resolution,
// which is where every other well-formed add ends up in these tests. It used to
// be refused at validation with "requires --line with --file", and that refusal
// was the only reason an agent could not say anything about a file as a whole.
func TestReviewAddAcceptsAFileWithNoLine(t *testing.T) {
	// From a directory that is not a jj repo, so a command that gets past flag
	// validation fails at review resolution instead. That is what distinguishes
	// "the flags were rejected" from "the flags were fine".
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var out bytes.Buffer
	err = runReviewSubcommand(failingRunner{}, nil, []string{
		"add", "--file", "internal/cli/deck.go", "--body", "this file is doing too much",
	}, &out)
	if err == nil {
		t.Fatal("expected the command to fail outside a repo")
	}
	for _, refusal := range []string{"--line", "requires"} {
		if strings.Contains(err.Error(), refusal) {
			t.Fatalf("--file with no --line was refused at validation: %v", err)
		}
	}
}

// A comment on a whole file rides in the same staged review as a comment on a
// line. They are one mutation apart only in whether a line is sent, so they share
// a bucket.
//
// The history matters to the assertion below. A file comment used to be routed to
// a bucket of its own, because a staged thread was believed to need a line: as an
// inline thread the preflight called it "line 0 is not in the diff", and one
// blocked anchor refuses the whole run, so a single file-level remark stopped a
// reviewer publishing every line comment beside it. The separate bucket avoided
// that by not sending it at all. Now the preflight has an arm for the scope and
// GraphQL takes the thread, so neither half of the workaround is needed — but the
// property it protected still has to hold.
func TestAFileCommentShipsBesideALineComment(t *testing.T) {
	comments := []review.Comment{
		{ID: "line", Author: review.AuthorHuman, Body: "off by one",
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 2}},
		{ID: "file", Author: review.AuthorHuman, Body: "this belongs in internal/review",
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew}},
	}
	b := partitionForPublish(comments)

	if len(b.Threads) != 2 {
		t.Fatalf("expected both comments staged as threads, got %+v", b.Threads)
	}

	// The check the file comment used to fail, run over both of them.
	commentable := parseCommentable([]github.PRFile{{Filename: "a.go", Patch: "@@ -1,2 +1,3 @@\n ctx\n+added\n"}})
	if blocked := blockedAnchors(b.Threads, preflight(b.Threads, commentable)); len(blocked) > 0 {
		t.Fatalf("the run would be refused: %v", blocked)
	}
}

// The preview names it as covering the whole file. This list now mixes the two
// scopes, and "deck.go" beside "deck.go:12" is a difference the eye skips at the
// moment the reviewer is agreeing to send both.
func TestThePreviewSaysAFileCommentCoversTheWholeFile(t *testing.T) {
	c := review.Comment{ID: "file", Author: review.AuthorHuman, Body: "wrong package",
		Anchor: review.Anchor{Path: "internal/cli/deck.go", Side: review.SideNew}}
	b := partitionForPublish([]review.Comment{c})

	plan := strings.Join(publishPlan(publishRequest{PR: 54}, b, "", nil), "\n")
	for _, want := range []string{"all of internal/cli/deck.go", "wrong package"} {
		if !strings.Contains(plan, want) {
			t.Errorf("the preview does not name %q:\n%s", want, plan)
		}
	}
	// It is staged, not omitted — so it is one of the threads the review carries.
	// (The plan is two calls, stage then submit, as it is for any staged review.)
	if !strings.Contains(plan, "1 thread(s), staged") {
		t.Errorf("the file comment is not staged into the review:\n%s", plan)
	}
	// And the old wording must not survive: it would say the opposite of the truth.
	if strings.Contains(plan, "not sent") {
		t.Errorf("the preview still says the file comment is not sent:\n%s", plan)
	}
}
