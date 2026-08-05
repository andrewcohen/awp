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

// A comment on a whole file has no line, by design. Routed as an inline thread it
// was reported by the anchor preflight as "line 0 is not in the diff", and one
// blocked anchor refuses the entire run before anything is sent — so a single
// file-level comment stopped a reviewer publishing every line comment beside it.
func TestAFileCommentDoesNotBlockThePublish(t *testing.T) {
	comments := []review.Comment{
		{ID: "line", Author: review.AuthorHuman, Body: "off by one",
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 2}},
		{ID: "file", Author: review.AuthorHuman, Body: "this belongs in internal/review",
			Anchor: review.Anchor{Path: "a.go", Side: review.SideNew}},
	}
	b := partitionForPublish(comments)

	if len(b.Inline) != 1 || b.Inline[0].ID != "line" {
		t.Fatalf("expected only the line comment inline, got %+v", b.Inline)
	}
	if len(b.FileLevel) != 1 || b.FileLevel[0].ID != "file" {
		t.Fatalf("expected the file comment in its own bucket, got %+v", b.FileLevel)
	}

	// The check the file comment used to fail. It never reaches it now, so the run
	// is not refused and the line comment still goes.
	commentable := parseCommentable([]github.PRFile{{Filename: "a.go", Patch: "@@ -1,2 +1,3 @@\n ctx\n+added\n"}})
	if blocked := blockedAnchors(b.Inline, preflight(b.Inline, commentable)); len(blocked) > 0 {
		t.Fatalf("the run would be refused: %v", blocked)
	}
}

// Not published is not the same as not there. A comment the reviewer wrote and
// believes went up is worse than one they know is still local, so both the preview
// and the run have to say so.
func TestAFileCommentIsReportedAsNotSent(t *testing.T) {
	c := review.Comment{ID: "file", Author: review.AuthorHuman, Body: "wrong package",
		Anchor: review.Anchor{Path: "internal/cli/deck.go", Side: review.SideNew}}
	b := partitionForPublish([]review.Comment{c})

	plan := strings.Join(publishPlan(publishRequest{PR: 54}, b, "", nil), "\n")
	if !strings.Contains(plan, "not sent") {
		t.Errorf("the preview does not say the file comment is not sent:\n%s", plan)
	}
	for _, want := range []string{"internal/cli/deck.go", "wrong package"} {
		if !strings.Contains(plan, want) {
			t.Errorf("the preview does not name %q:\n%s", want, plan)
		}
	}
	// And it must not be counted as a call the run is about to make.
	if strings.Contains(plan, "1 call(s)") {
		t.Errorf("a comment that is not sent is counted as a call:\n%s", plan)
	}
}
