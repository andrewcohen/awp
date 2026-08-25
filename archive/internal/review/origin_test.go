package review

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The four kinds of record, each in the shape a store actually holds it in.
func originCases() []struct {
	name string
	c    Comment
	want Origin
} {
	rec := &PublishRecord{ThreadID: "PRRT_abc", At: time.Unix(1700000000, 0)}
	return []struct {
		name string
		c    Comment
		want Origin
	}{
		{"a draft finding", Comment{ID: "1-human", State: Open}, OriginLocal},
		{"a reply to one of our own", Comment{ID: "2-agent", State: Open, ReplyTo: "1-human"}, OriginLocal},
		{"an agent's proposal", Comment{ID: "3-agent", State: Open, ReplyTo: "1-human", Proposal: ProposalPending}, OriginLocal},
		{"a finding the store marked sent", Comment{ID: "4-human", State: Sent}, OriginLocal},

		{"a reply into a thread, not posted", Comment{ID: "5-human", State: Open, ReplyToThread: "PRRT_abc"}, OriginReply},

		{"a published finding", Comment{ID: "6-human", State: Published, Publish: rec}, OriginPublished},
		{"a posted reply", Comment{ID: "7-human", State: Published, ReplyToThread: "PRRT_abc", Publish: rec}, OriginPublished},
		// The shape #83 and #106 were about: the post went through, the state did not
		// reach disk. It is on GitHub, and the record has to say so.
		{"a posted reply whose state was lost", Comment{ID: "8-human", State: Open, ReplyToThread: "PRRT_abc", Publish: rec}, OriginPublished},

		{"a mirrored thread's opening message", Comment{ID: RemoteThreadID("PRRT_xyz"), Author: "github"}, OriginMirrored},
		{"a later message of a mirrored thread", Comment{ID: RemoteMessageID("PRRT_xyz", 2), Author: "github"}, OriginMirrored},
	}
}

func TestOriginNamesWhatARecordIs(t *testing.T) {
	for _, tc := range originCases() {
		if got := tc.c.Origin(); got != tc.want {
			t.Errorf("%s: Origin() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Mirrored wins over everything. A record whose words we did not write is not ours
// to publish, edit or delete no matter what its other fields happen to say — and a
// mirror written with a stale publish record must not read as one of our findings.
func TestMirroredOutranksTheOtherFields(t *testing.T) {
	c := Comment{
		ID:            RemoteThreadID("PRRT_abc"),
		State:         Published,
		ReplyToThread: "PRRT_abc",
		Publish:       &PublishRecord{ThreadID: "PRRT_abc"},
	}
	if got := c.Origin(); got != OriginMirrored {
		t.Errorf("Origin() = %v for a mirrored record carrying every other marker, want %v", got, OriginMirrored)
	}
	if c.Mutable() {
		t.Error("a mirrored comment is other people's words and must not be mutable")
	}
}

// You may change your own words for as long as they have not left.
//
// Both keys, one rule. `i` and `D` used to answer separately and both let a
// published *finding* be changed while refusing a published *reply* — the same
// situation given opposite answers. Neither edit reaches GitHub, so allowing
// either leaves the local record disagreeing with what everyone else can read.
func TestOnlyWhatHasNotLeftIsMutable(t *testing.T) {
	for _, tc := range originCases() {
		want := tc.want == OriginLocal || tc.want == OriginReply
		if got := tc.c.Mutable(); got != want {
			t.Errorf("%s (%v): Mutable() = %v, want %v", tc.name, tc.want, got, want)
		}
	}
}

// Every origin says its own name. A new one defaulting to "local" would print a
// record as something it is not, in a status line or a `review list` column.
func TestEveryOriginIsNamed(t *testing.T) {
	seen := map[string]Origin{}
	for _, o := range []Origin{OriginLocal, OriginReply, OriginPublished, OriginMirrored} {
		s := o.String()
		if s == "" {
			t.Errorf("origin %d has no name", int(o))
			continue
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("origins %d and %d both call themselves %q", int(prev), int(o), s)
		}
		seen[s] = o
	}
	// A fifth constant added without a String case would land here as "local".
	if got := Origin(99).String(); got != "local" {
		t.Fatalf("the default arm changed: Origin(99).String() = %q", got)
	}
}

// Origin exists because every surface was deciding what a record is for itself,
// from whichever pair of fields was nearest — which is how the same reply came to
// read as unsent in the index and as published in the editor's refusal.
//
// The pieces it is built from are each fine alone: ThreadReply answers "is this
// inside a conversation", which the anchor rules and PublishBody legitimately ask.
// What is forbidden is combining them back into a hand-rolled classification.
func TestNothingOutsideReviewReclassifiesARecord(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	allowed := filepath.Join(root, "internal", "review")

	// A line asking two of these at once is re-deriving Origin. Tests are exempt for
	// the same reason as in TestOnlyOnePlaceAsksWhetherACommentIsOnGitHub: a test that
	// pins both halves of a record is what guarantees Origin's inputs are right.
	parts := []string{".ThreadReply()", ".OnGitHub()", ".Mirrored()"}

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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(path, allowed+string(filepath.Separator)) {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // prose about why this rule exists is not a breach of it
			}
			n := 0
			for _, p := range parts {
				if strings.Contains(line, p) {
					n++
				}
			}
			if n > 1 {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d classifies a record by hand — use Comment.Origin or Comment.Mutable:\n\t%s",
					rel, i+1, trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repo: %v", err)
	}
	if checked < 50 {
		t.Fatalf("expected to scan the repo's sources, only read %d files", checked)
	}
}
