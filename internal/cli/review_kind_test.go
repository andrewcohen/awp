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
