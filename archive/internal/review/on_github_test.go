package review

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Either half saying yes means the words are up there. The two are supposed to
// move together and sometimes do not — which is the whole reason this is one
// function — so the union is the honest answer, and it errs towards "public",
// since telling someone their remark is unsent when it is public is the costly
// direction to be wrong in.
func TestOnGitHubTakesEitherHalf(t *testing.T) {
	rec := &PublishRecord{ThreadID: "PRRT_abc", At: time.Unix(1700000000, 0)}
	for _, tc := range []struct {
		name string
		c    Comment
		want bool
	}{
		{"neither", Comment{State: Open}, false},
		{"state only", Comment{State: Published}, true},
		{"record only", Comment{State: Open, Publish: rec}, true},
		{"both", Comment{State: Published, Publish: rec}, true},
		// The shape the "unsent" chip used to get wrong: a reply that reached GitHub
		// and had its state dropped on the way to disk went on saying it had not.
		{"a posted reply whose state was lost", Comment{State: Open, ReplyToThread: "PRRT_abc", Publish: rec}, true},
	} {
		if got := tc.c.OnGitHub(); got != tc.want {
			t.Errorf("%s: OnGitHub() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The id is what a published comment is matched to its mirrored echo by, so
// "we do not know it" and "here it is" must not look alike.
func TestPublishedThreadID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		c      Comment
		want   string
		wantOK bool
	}{
		{"never published", Comment{}, "", false},
		{"published with an id", Comment{Publish: &PublishRecord{ThreadID: "PRRT_abc"}}, "PRRT_abc", true},
		// A mirror written before the ids were carried says nothing, and must not be
		// read as a match — every unidentified comment would match every thread.
		{"published before ids were carried", Comment{Publish: &PublishRecord{}}, "", false},
		{"published with a blank id", Comment{Publish: &PublishRecord{ThreadID: "   "}}, "", false},
	} {
		got, ok := tc.c.PublishedThreadID()
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: PublishedThreadID() = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

// There were three spellings of this question and the two that checked one half
// each were both wrong in the same direction. A fourth must not appear.
//
// Comparisons only: assigning State = Published, or writing it into a literal, is
// how a record gets published in the first place. What this forbids is asking.
//
// Tests are exempt, and deliberately. A test that checks the state *and* the
// publish record were both written is what guarantees OnGitHub's inputs are
// right — the wiring tests for MarkPublished exist precisely because those two
// came apart once. Routing them through OnGitHub would make them assert less than
// they do now, and the drift this guards against is in the code that asks the
// question, not in the code that pins the answer.
func TestOnlyOnePlaceAsksWhetherACommentIsOnGitHub(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repo root: %v", err)
	}
	allowed := filepath.Join(root, "internal", "review")

	// The forbidden asks, and what to use instead.
	banned := map[string]string{
		"== review.Published": "Comment.OnGitHub",
		"!= review.Published": "Comment.OnGitHub",
		".Publish != nil":     "Comment.OnGitHub / Comment.PublishedThreadID",
		".Publish == nil":     "Comment.OnGitHub / Comment.PublishedThreadID",
	}

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
				continue // prose about the history of this is not a re-spelling of it
			}
			for spelling, use := range banned {
				if !strings.Contains(line, spelling) {
					continue
				}
				// The deck's CommentStore has a Publish *function field*; a nil check on
				// a seam is not a claim about a comment.
				if strings.Contains(line, "comments.Publish") || strings.Contains(line, "PublishReview") {
					continue
				}
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d asks whether a comment is on GitHub with %q — use %s:\n\t%s",
					rel, i+1, spelling, use, trimmed)
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
