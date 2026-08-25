package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// The non-interactive form of `awp review`. The review flow itself was already
// scriptable; what was not was the two things around it — a picker with nobody to
// ask, and a tmux switch with no client to switch. These cover the refusals, which
// are the part that has to be exactly right: the failure mode being avoided is a
// hang, and a hang reads as work in progress rather than as a mistake.

// reviewApp is an App whose review workflow records rather than runs, so the
// interactive path can be checked without a PR or a repo.
func reviewApp(t *testing.T) (*App, *int, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	app := NewApp(&fakeService{}, out)
	var got int
	app.review = func(_ Runner, _ workspace.Service, prNumber int, _ io.Reader, _ io.Writer) error {
		got = prNumber
		return nil
	}
	return app, &got, out
}

// The refusal that matters. A picker is a terminal UI over `gh pr list`; opening one
// for a caller that asked for the non-interactive form hangs it forever.
func TestReviewRefusesToPickWhenAskedNotToBeInteractive(t *testing.T) {
	for _, args := range [][]string{
		{"--no-attach"},
		{"--project", "proj"},
		{"--project", "proj", "--no-attach"},
	} {
		app := NewApp(&fakeService{}, &bytes.Buffer{})
		err := app.runReview(args)
		if err == nil {
			t.Errorf("%v: expected a refusal rather than a picker", args)
			continue
		}
		if !strings.Contains(err.Error(), "PR number") {
			t.Errorf("%v: the error should ask for a PR number, got %v", args, err)
		}
	}
}

// The flags are parsed before the arity check, so a number plus a flag is one PR and
// one flag rather than "at most one PR number" being violated.
func TestReviewTakesANumberAndAFlagTogether(t *testing.T) {
	app := NewApp(&fakeService{}, &bytes.Buffer{})
	err := app.runReview([]string{"123", "--no-attach", "--project", "nosuchproject"})
	if err == nil {
		t.Fatal("expected the unresolvable project to be the failure")
	}
	if strings.Contains(err.Error(), "at most one PR number") {
		t.Errorf("the flags were counted as arguments: %v", err)
	}
	if !strings.Contains(err.Error(), "nosuchproject") {
		t.Errorf("expected the project to be named, got %v", err)
	}
}

// Without the flags, nothing changes: a number still goes down the interactive path,
// which is the one that ends by putting you in the review session.
func TestReviewWithANumberIsUnchanged(t *testing.T) {
	app, got, _ := reviewApp(t)
	if err := app.runReview([]string{"123"}); err != nil {
		t.Fatalf("review 123: %v", err)
	}
	if *got != 123 {
		t.Errorf("the review workflow got PR #%d, want 123", *got)
	}
}

// A bad number says what it wanted, in the words of the flags a machine would use.
func TestReviewNamesTheFormsWhenTheNumberIsWrong(t *testing.T) {
	app := NewApp(&fakeService{}, &bytes.Buffer{})
	err := app.runReview([]string{"notanumber", "--no-attach"})
	if err == nil {
		t.Fatal("expected a non-numeric PR to be refused")
	}
	if !strings.Contains(err.Error(), "--no-attach") {
		t.Errorf("the error should show the non-interactive form, got %v", err)
	}
}

func TestReviewUsageDocumentsBothFlags(t *testing.T) {
	out := &bytes.Buffer{}
	app := NewApp(&fakeService{}, out)
	if err := app.runReview([]string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"--no-attach", projectFlag, "Requires a PR number"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the review usage does not mention %q:\n%s", want, out)
		}
	}
}

// The review subcommands (add / reply / list / publish) must not be caught by the
// flag parsing above — they have their own argument handling, and `--project` means
// nothing to them.
func TestReviewSubcommandsStillDispatch(t *testing.T) {
	for _, sub := range []string{"add", "reply", "list", "publish"} {
		if !isReviewSubcommand([]string{sub}) {
			t.Errorf("%q is no longer recognised as a review subcommand", sub)
		}
	}
}
