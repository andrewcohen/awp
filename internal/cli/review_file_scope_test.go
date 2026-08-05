package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
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
