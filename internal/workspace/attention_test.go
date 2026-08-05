package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		status string
		unread bool
		want   Attention
	}{
		{"working", "working", false, AttentionWorking},
		{"working spelled in_progress", "in_progress", false, AttentionWorking},
		{"working spelled with a space", "in progress", false, AttentionWorking},
		{"working spelled running", "running", false, AttentionWorking},
		{"working with mixed case and padding", "  WoRkInG ", false, AttentionWorking},

		// The double-count bug: a workspace that resumed work still carrying
		// the unread flag from an earlier waiting turn is working, and only
		// working.
		{"working with a stale unread flag", "working", true, AttentionWorking},

		// The agent is gone; a leftover unread flag must not badge it.
		{"exited", "exited", false, AttentionNone},
		{"exited with a stale unread flag", "exited", true, AttentionNone},

		{"waiting and unread", "waiting", true, AttentionWaiting},
		{"waiting but already read", "waiting", false, AttentionNone},

		{"idle and unread", "idle", true, AttentionNotified},
		{"idle and read", "idle", false, AttentionNone},
		{"done and unread", "done", true, AttentionNotified},
		{"unknown status, unread", "whatever", true, AttentionNotified},
		{"unknown status, read", "whatever", false, AttentionNone},
		{"empty status, unread", "", true, AttentionNotified},
		{"empty status, read", "", false, AttentionNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.status, tc.unread); got != tc.want {
				t.Errorf("Classify(%q, %v) = %v, want %v", tc.status, tc.unread, got, tc.want)
			}
		})
	}
}

// Every workspace lands in exactly one bucket, which is what makes the counts
// addable: a summary that says "1 working · 1 notified" for one workspace is
// worse than no summary.
func TestClassifyIsExhaustiveAndExclusive(t *testing.T) {
	statuses := []string{"", "working", "in_progress", "in progress", "running", "waiting", "idle", "done", "error", "starting", "exited", "unmanaged"}
	for _, status := range statuses {
		for _, unread := range []bool{false, true} {
			got := Classify(status, unread)
			switch got {
			case AttentionNone, AttentionWorking, AttentionWaiting, AttentionNotified:
			default:
				t.Errorf("Classify(%q, %v) returned %d, which is not a named bucket", status, unread, got)
			}
		}
	}
}

// The list of working-status spellings is only correct in one place, and it got
// to four copies without anyone deciding to write it four times — each one
// arrived as "the status check I need right here". Three of them agreed by
// luck; one carried a comment saying it was mirroring another, which is a note
// that only gets written when nothing enforces the mirroring.
//
// So the guard is on the literal set rather than on anyone's discipline: adding
// a spelling to IsWorking now reaches every surface, and re-spelling it locally
// fails here instead of at the moment two counts disagree in front of the user.
func TestOnlyOnePlaceSpellsOutTheWorkingStatuses(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	// This file's own home. Its test file names the spellings too, which is
	// fine — a test asserting the vocabulary is not a second implementation.
	allowed := filepath.Join(root, "internal", "workspace")

	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".jj", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasPrefix(path, allowed+string(filepath.Separator)) {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			// The signature of a re-spelling: the underscore and the bare
			// spellings sitting together in one case clause.
			if strings.Contains(line, `"in_progress"`) && strings.Contains(line, `"running"`) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d spells out the working statuses again — ask workspace.IsWorking instead:\n\t%s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repo: %v", err)
	}
	// A guard that read nothing would pass forever.
	if checked < 50 {
		t.Fatalf("expected to scan the repo's sources, only read %d files", checked)
	}
}

func TestIsWorkingAcceptsEverySpelling(t *testing.T) {
	// Whatever writes the status — agent hooks, tmux enrichment,
	// report-status — has to be recognised, including case and stray padding.
	for _, status := range []string{"working", "Working", " running ", "in progress", "in_progress", "IN_PROGRESS"} {
		if !IsWorking(status) {
			t.Errorf("IsWorking(%q) = false, want true", status)
		}
	}
}

func TestIsWorkingRejectsNeighbouringStatuses(t *testing.T) {
	// Statuses that read as active but are not the agent doing work.
	for _, status := range []string{"starting", "waiting", "idle", "done", "error", "exited", "unmanaged", ""} {
		if IsWorking(status) {
			t.Errorf("IsWorking(%q) = true, want false", status)
		}
	}
}
