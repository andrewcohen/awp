package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

// changedFilesRunner is rootRunner that also answers `jj diff --name-only` with a
// real file list, which is what warnAnchorOutsideDiff checks an anchor against.
//
// err makes that one call fail while every other probe still works — the
// "cannot tell" path, which has to stay silent.
type changedFilesRunner struct {
	root    string
	changed []string
	err     error
}

func (r changedFilesRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if len(args) >= 2 && args[0] == "diff" && args[1] == "--name-only" {
		if r.err != nil {
			return "", r.err
		}
		return strings.Join(r.changed, "\n") + "\n", nil
	}
	return rootRunner{root: r.root}.Run(ctx, dir, name, args...)
}

// addWith files a finding through the given runner and returns everything the
// command printed.
func addWith(t *testing.T, runner Runner, args ...string) string {
	t.Helper()
	root := tempRoot(t)
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "default", Path: root}}}
	chdir(t, root)
	if r, ok := runner.(changedFilesRunner); ok {
		r.root = root
		runner = r
	}
	var out bytes.Buffer
	if err := runReviewAdd(runner, svc, args, &out); err != nil {
		t.Fatalf("review add: %v", err)
	}
	return out.String()
}

// The signal this is for: an anchor naming a file the change does not touch
// usually means the wrong review was picked. #84 made that visible by naming the
// review on every write, but an agent filing a dozen findings is not reading
// twelve confirmation lines — so the odd one out has to say so itself.
func TestAddWarnsWhenTheFileIsNotInTheDiff(t *testing.T) {
	out := addWith(t,
		changedFilesRunner{changed: []string{"internal/ui/model.go", "README.md"}},
		"--file", "cmd/other/main.go", "--line", "12", "--body", "this drops the error")

	if !strings.Contains(out, "warning:") {
		t.Fatalf("no warning for a file outside the diff:\n%s", out)
	}
	if !strings.Contains(out, "cmd/other/main.go") {
		t.Errorf("the warning does not name the path:\n%s", out)
	}
	// Actionable per AGENTS.md: it has to say what to do, and the two things that
	// are actually wrong in this situation are the workspace and the path.
	if !strings.Contains(out, "--workspace") {
		t.Errorf("the warning does not say what to check:\n%s", out)
	}
}

// A warning, never a refusal — and the finding is on disk before it prints. The
// words are the valuable part of a finding and an anchor can be repaired; losing
// the text to a failed anchor check would be the worse trade by a distance.
func TestAWarnedAnchorStillFilesTheFinding(t *testing.T) {
	out := addWith(t,
		changedFilesRunner{changed: []string{"internal/ui/model.go"}},
		"--file", "cmd/other/main.go", "--line", "12", "--body", "this drops the error")

	added := strings.Index(out, "added ")
	warned := strings.Index(out, "warning:")
	if added < 0 {
		t.Fatalf("the finding was not filed:\n%s", out)
	}
	if warned < 0 {
		t.Fatalf("no warning:\n%s", out)
	}
	// Order is the assertion: the confirmation comes first because the write does.
	if warned < added {
		t.Errorf("the warning printed before the finding was filed:\n%s", out)
	}
}

// Nothing to say when the anchor is in the change. The whole value of the warning
// is that it is rare.
func TestAddIsQuietWhenTheFileIsInTheDiff(t *testing.T) {
	out := addWith(t,
		changedFilesRunner{changed: []string{"internal/ui/model.go", "README.md"}},
		"--file", "internal/ui/model.go", "--line", "12", "--body", "this drops the error")
	if strings.Contains(out, "warning") {
		t.Errorf("warned about a file that is in the diff:\n%s", out)
	}
}

// A remark about the change as a whole names no file, so there is nothing that
// could be outside the diff.
func TestAReviewLevelRemarkIsNeverWarnedAbout(t *testing.T) {
	out := addWith(t,
		changedFilesRunner{changed: []string{"internal/ui/model.go"}},
		"--body", "the error paths are inconsistent across this change")
	if strings.Contains(out, "warning") {
		t.Errorf("warned about a remark with no anchor:\n%s", out)
	}
}

// A remark about a whole file is checked the same way — it names a file, and the
// file can be the wrong one just as easily.
func TestAFileLevelRemarkIsCheckedToo(t *testing.T) {
	out := addWith(t,
		changedFilesRunner{changed: []string{"internal/ui/model.go"}},
		"--file", "cmd/other/main.go", "--body", "this file should not exist")
	if !strings.Contains(out, "warning:") {
		t.Errorf("no warning for a whole-file remark on a file outside the diff:\n%s", out)
	}
}

// Silent when it cannot tell. A check that does not know must not manufacture a
// complaint — a warning that fires on uncertainty is one that gets scrolled past,
// and then the real one does too.
func TestAddIsQuietWhenTheDiffCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner changedFilesRunner
	}{
		{"jj failed", changedFilesRunner{err: context.DeadlineExceeded}},
		// An empty list is read as "cannot tell" rather than "the change is empty":
		// both readings are available and the wrong one warns on every single call. A
		// change was resolving as its own base until recently, and that produced
		// exactly this — an empty diff that looked authoritative.
		{"nothing changed", changedFilesRunner{changed: nil}},
	} {
		out := addWith(t, tc.runner,
			"--file", "cmd/other/main.go", "--line", "12", "--body", "this drops the error")
		if strings.Contains(out, "warning") {
			t.Errorf("%s: warned without knowing:\n%s", tc.name, out)
		}
		if !strings.Contains(out, "added ") {
			t.Errorf("%s: the finding was not filed:\n%s", tc.name, out)
		}
	}
}
