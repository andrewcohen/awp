package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// reviewKindHarness runs the CLI's review subcommands against a temp store, from
// a temp cwd that looks like a jj workspace. Returns the store and review the
// commands resolve to, so a test can read back what was written.
//
// runReviewAdd resolves its review through openReviewForCwd, which shells out to
// jj — so these tests assert the flag plumbing at the level below that, on the
// same code path the command uses to build the record.
func TestReviewAddTypeFlagParsesEveryKind(t *testing.T) {
	for _, tc := range []struct {
		flag string
		want review.Kind
	}{
		{"comment", review.KindComment},
		{"suggestion", review.KindSuggestion},
		{"question", review.KindQuestion},
		// The default when --type is not passed.
		{"", review.KindComment},
		// A typo must not lose the finding.
		{"nitpick", review.KindComment},
		{"SUGGESTION", review.KindSuggestion},
	} {
		if got := review.ParseKind(tc.flag); got != tc.want {
			t.Fatalf("--type %q: got %q, want %q", tc.flag, got, tc.want)
		}
	}
}

// The flag has to exist on both add and reply: a reply that cannot carry a kind
// would silently downgrade an agent's answer to a plain comment.
func TestReviewSubcommandsAcceptTypeFlag(t *testing.T) {
	// Run from a directory that is not a jj repo, so the command fails at review
	// resolution rather than at flag parsing. An unknown-flag error looks
	// different from "not a jj repository", which is what this distinguishes.
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	for _, args := range [][]string{
		{"add", "--file", "a.go", "--line", "1", "--body", "x", "--type", "suggestion"},
		{"reply", "--to", "abc", "--body", "x", "--type", "question"},
	} {
		var out bytes.Buffer
		err := runReviewSubcommand(failingRunner{}, nil, args, &out)
		if err == nil {
			t.Fatalf("%v: expected the command to fail outside a repo", args)
		}
		if strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("%v: --type is not wired: %v", args, err)
		}
	}
}

// failingRunner stands in for the command runner so nothing shells out.
type failingRunner struct{}

func (failingRunner) Run(context.Context, string, string, ...string) (string, error) {
	return "", errors.New("not a jj repository")
}

// A review-level remark is filed with no --file: it is about the change as a
// whole, so demanding a line would force the author to invent one.
func TestReviewAddAcceptsARemarkWithNoFile(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// Outside a repo the command fails at review resolution, which is past the
	// validation this asserts: what matters is that it is not rejected for the
	// missing --file.
	var out bytes.Buffer
	err = runReviewSubcommand(failingRunner{}, nil, []string{"add", "--body", "overall this needs tests"}, &out)
	if err == nil {
		t.Fatal("expected the command to fail outside a repo")
	}
	if strings.Contains(err.Error(), "--file") || strings.Contains(err.Error(), "--line") {
		t.Fatalf("a review-level remark was rejected for its missing anchor: %v", err)
	}
}

// The two halves of an anchor go together: a line with no file has nothing to
// mean, and a file with no line cannot be placed.
func TestReviewAddRejectsAHalfAnchor(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "--line", "4", "--body", "x"}, "--line needs --file"},
		{[]string{"add", "--file", "a.go", "--body", "x"}, "requires --line with --file"},
		{[]string{"add", "--file", "a.go", "--line", "1"}, "requires --body"},
	} {
		var out bytes.Buffer
		err := runReviewSubcommand(failingRunner{}, nil, tc.args, &out)
		if err == nil {
			t.Fatalf("%v: expected an error", tc.args)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %q, want it to mention %q", tc.args, err, tc.want)
		}
	}
}

// An agent can file a finding about a block, not only about a line.
func TestReviewAddAcceptsALineRange(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// Outside a repo the command fails at review resolution, past the flag
	// parsing and validation this asserts.
	var out bytes.Buffer
	err = runReviewSubcommand(failingRunner{}, nil, []string{
		"add", "--file", "a.go", "--line", "12", "--end-line", "18",
		"--text", "for {", "--end-text", "}", "--body", "this loop",
	}, &out)
	if err == nil {
		t.Fatal("expected the command to fail outside a repo")
	}
	for _, bad := range []string{"not defined", "--end-line"} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("the range flags are not wired: %v", err)
		}
	}
}

// An end before the start describes no block, and the difference between "line
// 12" and "lines 12-18" is the whole content of the flag — so it is refused
// rather than quietly dropped.
func TestReviewAddRejectsABackwardsRange(t *testing.T) {
	var out bytes.Buffer
	err := runReviewSubcommand(failingRunner{}, nil, []string{
		"add", "--file", "a.go", "--line", "18", "--end-line", "12", "--body", "x",
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "--end-line") {
		t.Fatalf("expected a backwards range rejected by name, got %v", err)
	}
}

// rangeEnd is what decides whether a record says "range" at all: an end at or
// before the start is one line, and Multiline() has to agree with that.
func TestRangeEndOnlyRecordsARealRange(t *testing.T) {
	for _, tc := range []struct{ line, end, want int }{
		{12, 18, 18},
		{12, 12, 0},
		{12, 6, 0},
		{12, 0, 0},
	} {
		if got := rangeEnd(tc.line, tc.end); got != tc.want {
			t.Fatalf("rangeEnd(%d, %d) = %d, want %d", tc.line, tc.end, got, tc.want)
		}
	}
}

// The agent has to be told the ability exists, or it files five findings where
// one ranged finding was the honest shape.
func TestReviewPromptDocumentsRanges(t *testing.T) {
	for _, want := range []string{"--end-line", "--end-text", "one hunk"} {
		if !strings.Contains(reviewPromptTemplate, want) {
			t.Fatalf("the review prompt never mentions %q", want)
		}
	}
}

// The prompt must not invent a fourth kind in prose. It told the agent to file a
// `note` for pushing back on an existing comment, which `--type note` silently
// turns into a plain comment — the prompt contradicting its own Comment types
// section a few paragraphs later.
func TestReviewPromptNamesOnlyTheRealKinds(t *testing.T) {
	for _, kind := range review.Kinds() {
		if !strings.Contains(reviewPromptTemplate, "`"+string(kind)+"`") {
			t.Fatalf("the review prompt never names the %q kind", kind)
		}
	}
	// Backticked, i.e. named as a type the agent could pass to --type. The word
	// itself is fine in prose ("note that…"); it is the vocabulary that matters.
	for _, invented := range []string{"`note`", "`nit`", "`praise`", "`issue`"} {
		if strings.Contains(reviewPromptTemplate, invented) {
			t.Fatalf("the review prompt offers %s, which is not a kind awp has", invented)
		}
	}
}
