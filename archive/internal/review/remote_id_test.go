package review

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mirrored comment is recognisable as GitHub's, from any of its rows.
func TestAMirroredCommentKnowsItIsNotOurs(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		want bool
	}{
		{"a thread's opening message", RemoteThreadID("PRRT_abc"), true},
		{"a later message of the same thread", RemoteMessageID("PRRT_abc", 3), true},
		{"one of ours", "1786023108678241000-human", false},
		{"one of ours whose author slug looks nothing like a thread", "1700000000-agent", false},
		{"the empty id", "", false},
	} {
		if got := (Comment{ID: tc.id}).Mirrored(); got != tc.want {
			t.Errorf("%s: Mirrored() = %v for %q, want %v", tc.name, got, tc.id, tc.want)
		}
	}
}

// Every message answers for its whole conversation. Resolving, folding and
// replying all act on the thread, so the cursor may sit on any of its rows and
// the id has to give the thread back.
func TestAnyMessageNamesItsThread(t *testing.T) {
	const thread = "PRRT_kwDOAbc123"
	for _, id := range []string{RemoteThreadID(thread), RemoteMessageID(thread, 1), RemoteMessageID(thread, 42)} {
		got, ok := ThreadIDOf(id)
		if !ok {
			t.Errorf("%q is not recognised as a mirrored comment", id)
			continue
		}
		if got != thread {
			t.Errorf("%q names thread %q, want %q", id, got, thread)
		}
	}
	if _, ok := ThreadIDOf("1786023108678241000-human"); ok {
		t.Error("one of our own comments was read as a mirrored thread")
	}
}

// The separator must not appear in a GitHub node id, or splitting on it would cut
// a thread's id in half and the message would name a thread that does not exist.
func TestTheSeparatorCannotCutANodeID(t *testing.T) {
	// GitHub's node ids are base64-ish: letters, digits, underscore, hyphen.
	if strings.ContainsAny(threadMessageSep, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") {
		t.Fatalf("the message separator %q is a character a node id can contain", threadMessageSep)
	}
}

// The id scheme is the contract between Thread and Comment, and it holds only as
// long as nothing outside this package builds or reads one by hand. While it
// lived in the viewer, every caller answered "is this GitHub's record" by
// prefix-matching the id itself — which is the invariant this guard replaces.
func TestOnlyOnePlaceKnowsTheRemoteIDScheme(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	// This package's own home. Its tests name the prefix too, which is fine — a
	// test asserting the scheme is not a second implementation of it.
	allowed := filepath.Join(root, "internal", "review")

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
			// The literal prefix anywhere outside review is either a hand-built id or a
			// hand-rolled Mirrored(), and both go stale the moment the scheme changes.
			if strings.Contains(line, `"thread-"`) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d spells the mirrored-thread prefix — use review.RemoteThreadID / Comment.Mirrored:\n\t%s",
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
