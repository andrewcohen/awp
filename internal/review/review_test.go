package review

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) Store {
	t.Helper()
	n := 0
	return Store{Root: t.TempDir(), Now: func() time.Time {
		n++
		return time.Unix(1700000000, 0).Add(time.Duration(n) * time.Second)
	}}
}

func TestOpenCreatesThenReloadsTheSameReview(t *testing.T) {
	s := testStore(t)
	target := Target{Kind: TargetWorking, Workspace: "ws-1"}
	first, err := s.Open("/repo/proj", target)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected an id")
	}
	second, err := s.Open("/repo/proj", target)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("expected the same review back, got %+v vs %+v", second, first)
	}
}

// Identity must not include a head SHA — that is what stranded drafts in tuicr
// on every force-push.
func TestIDIsStableAcrossHeads(t *testing.T) {
	a := ID(Target{Kind: TargetPR, Value: "123"})
	b := ID(Target{Kind: TargetPR, Value: "123"})
	if a != b {
		t.Fatalf("expected a stable id, got %q and %q", a, b)
	}
	if got := ID(Target{Kind: TargetPR, Value: "124"}); got == a {
		t.Fatal("expected different PRs to differ")
	}
	if got := ID(Target{Kind: TargetWorking, Workspace: "ws"}); got == a {
		t.Fatal("expected a working review to differ from a PR review")
	}
}

func TestAddAndListComments(t *testing.T) {
	s := testStore(t)
	r, err := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, body := range []string{"first", "second"} {
		if _, err := s.AddComment(r, Comment{Body: body, Anchor: Anchor{Path: "a.go", LineHint: 3, Text: "x"}}); err != nil {
			t.Fatalf("add %q: %v", body, err)
		}
	}
	got, err := s.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(got))
	}
	if got[0].Body != "first" || got[1].Body != "second" {
		t.Fatalf("expected oldest first, got %q then %q", got[0].Body, got[1].Body)
	}
	if got[0].Author != AuthorHuman || got[0].State != Open || got[0].Anchor.Side != SideNew {
		t.Fatalf("expected defaults filled in, got %+v", got[0])
	}
}

func TestAddCommentRejectsEmptyBody(t *testing.T) {
	s := testStore(t)
	r, _ := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if _, err := s.AddComment(r, Comment{Body: "   "}); err == nil {
		t.Fatal("expected an empty body to be rejected")
	}
}

// One file per comment exists so concurrent agent writes cannot lose each other.
func TestConcurrentAddsAllSurvive(t *testing.T) {
	s := Store{Root: t.TempDir()}
	r, err := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	const n = 24
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.AddComment(r, Comment{
				ID:     "c" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
				Author: "agent",
				Body:   "finding",
				Anchor: Anchor{Path: "a.go", LineHint: i + 1, Text: "line"},
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	got, err := s.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != n {
		t.Fatalf("expected all %d comments to survive, got %d", n, len(got))
	}
}

// A corrupt record must not make the whole review unopenable.
func TestCommentsSkipsUnreadableRecords(t *testing.T) {
	s := testStore(t)
	r, _ := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if _, err := s.AddComment(r, Comment{Body: "good", Anchor: Anchor{Path: "a.go"}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	bad := filepath.Join(s.dir("/repo/proj", r.ID), "comments", "broken.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad file: %v", err)
	}
	got, err := s.Comments(r)
	if err != nil {
		t.Fatalf("expected the listing to survive, got %v", err)
	}
	if len(got) != 1 || got[0].Body != "good" {
		t.Fatalf("expected the good comment only, got %+v", got)
	}
}

func TestUpdateAndDeleteComment(t *testing.T) {
	s := testStore(t)
	r, _ := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	c, err := s.AddComment(r, Comment{Body: "original", Anchor: Anchor{Path: "a.go"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	c.State = Sent
	c.Body = "edited"
	if err := s.UpdateComment(r, c); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.Comments(r)
	if len(got) != 1 || got[0].State != Sent || got[0].Body != "edited" {
		t.Fatalf("expected the update to stick, got %+v", got)
	}
	if err := s.DeleteComment(r, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ = s.Comments(r); len(got) != 0 {
		t.Fatalf("expected no comments after delete, got %d", len(got))
	}
	// Deleting again is not an error — cleanup paths shouldn't have to check.
	if err := s.DeleteComment(r, c.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestOpenCountCountsOnlyOpen(t *testing.T) {
	comments := []Comment{
		{State: Open}, {State: Open}, {State: Sent}, {State: Addressed}, {State: Published},
	}
	if got := OpenCount(comments); got != 2 {
		t.Fatalf("expected 2 open, got %d", got)
	}
}

func TestCountsRoundTrip(t *testing.T) {
	s := testStore(t)
	want := Counts{ByWorkspace: map[string]int{"ws-1": 3, "ws-2": 0}}
	if err := s.WriteCounts("/repo/proj", want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := s.ReadCounts("/repo/proj")
	if got.ByWorkspace["ws-1"] != 3 {
		t.Fatalf("expected 3 for ws-1, got %+v", got)
	}
	// A missing index is zero counts, not an error — the badge is a nicety.
	if other := s.ReadCounts("/repo/absent"); len(other.ByWorkspace) != 0 {
		t.Fatalf("expected empty counts for an unknown repo, got %+v", other)
	}
}

// Cleanup may delete a review, but never one holding work that exists only here.
func TestDeleteWorkspaceReviewKeepsUnpublishedWork(t *testing.T) {
	s := testStore(t)
	r, _ := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	c, err := s.AddComment(r, Comment{Body: "unpublished", Anchor: Anchor{Path: "a.go"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	removed, err := s.DeleteWorkspaceReview("/repo/proj", "ws")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if removed {
		t.Fatal("expected an unpublished draft to block deletion")
	}

	c.State = Published
	if err := s.UpdateComment(r, c); err != nil {
		t.Fatalf("update: %v", err)
	}
	removed, err = s.DeleteWorkspaceReview("/repo/proj", "ws")
	if err != nil {
		t.Fatalf("delete after publish: %v", err)
	}
	if !removed {
		t.Fatal("expected a fully published review to be removable")
	}
	if _, err := os.Stat(s.dir("/repo/proj", r.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected the review directory to be gone, got %v", err)
	}
}

func TestDeleteWorkspaceReviewIsQuietWhenAbsent(t *testing.T) {
	s := testStore(t)
	removed, err := s.DeleteWorkspaceReview("/repo/proj", "never-existed")
	if err != nil || removed {
		t.Fatalf("expected a quiet no-op, got removed=%v err=%v", removed, err)
	}
}

func TestHasUnpublished(t *testing.T) {
	if HasUnpublished([]Comment{{State: Published}}) {
		t.Fatal("published-only should not count as unpublished")
	}
	if !HasUnpublished([]Comment{{State: Published}, {State: Open}}) {
		t.Fatal("an open comment is unpublished work")
	}
	if HasUnpublished(nil) {
		t.Fatal("no comments is nothing to lose")
	}
}
