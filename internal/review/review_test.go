package review

import (
	"os"
	"path/filepath"
	"strings"
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

// Identity must not include a head SHA — that is what stranded drafts on every
// force-push in the external tool this store replaced.
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

// ---- reply threads ----

func TestReplyThreadsUnderItsParentAndReopensIt(t *testing.T) {
	s := testStore(t)
	r, _ := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	parent, err := s.AddComment(r, Comment{
		Body:   "this drops the error",
		Anchor: Anchor{Path: "a.go", LineHint: 12, Text: "_ = do()"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// Handing it to the agent moves it out of open.
	parent.State = Sent
	if err := s.UpdateComment(r, parent); err != nil {
		t.Fatalf("update: %v", err)
	}

	reply, err := s.Reply(r, parent.ID, Comment{Author: "agent", Body: "agreed, wrapping it"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if reply.ReplyTo != parent.ID {
		t.Fatalf("expected the reply to point at its parent, got %q", reply.ReplyTo)
	}
	// A reply inherits the parent's anchor so the thread stays together.
	if reply.Anchor.Path != "a.go" || reply.Anchor.LineHint != 12 {
		t.Fatalf("expected the parent's anchor inherited, got %+v", reply.Anchor)
	}

	got, _ := s.Comments(r)
	var reloaded Comment
	for _, c := range got {
		if c.ID == parent.ID {
			reloaded = c
		}
	}
	// The exchange needs the reviewer again, so the parent reopens.
	if reloaded.State != Open {
		t.Fatalf("expected the reply to reopen its parent, got %q", reloaded.State)
	}
}

// One exchange is one thing awaiting triage, not one per message.
func TestOpenCountCountsThreadsNotMessages(t *testing.T) {
	comments := []Comment{
		{ID: "p1", State: Open},
		{ID: "r1", State: Open, ReplyTo: "p1"},
		{ID: "r2", State: Open, ReplyTo: "p1"},
		{ID: "p2", State: Sent},
	}
	if got := OpenCount(comments); got != 1 {
		t.Fatalf("expected 1 open thread, got %d", got)
	}
}

func TestReplyRejectsAMissingParent(t *testing.T) {
	s := testStore(t)
	r, _ := s.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if _, err := s.Reply(r, "nope", Comment{Body: "hi"}); err == nil {
		t.Fatal("expected a reply to an unknown comment to be rejected")
	}
	if _, err := s.Reply(r, "  ", Comment{Body: "hi"}); err == nil {
		t.Fatal("expected an empty parent id to be rejected")
	}
}

func TestThreadsGroupsRepliesInOrder(t *testing.T) {
	threads := Threads([]Comment{
		{ID: "p1", Body: "first"},
		{ID: "p2", Body: "second"},
		{ID: "r1", Body: "reply a", ReplyTo: "p1"},
		{ID: "r2", Body: "reply b", ReplyTo: "p1"},
	})
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	if threads[0].Parent.ID != "p1" || len(threads[0].Replies) != 2 {
		t.Fatalf("unexpected first thread: %+v", threads[0])
	}
	if threads[0].Replies[0].ID != "r1" || threads[0].Replies[1].ID != "r2" {
		t.Fatalf("expected replies in order, got %+v", threads[0].Replies)
	}
	if len(threads[1].Replies) != 0 {
		t.Fatalf("expected the second thread childless, got %+v", threads[1])
	}
}

// A reply whose parent is gone is promoted rather than dropped: showing it out of
// place beats losing it.
func TestThreadsPromotesAnOrphanedReply(t *testing.T) {
	threads := Threads([]Comment{{ID: "r1", Body: "dangling", ReplyTo: "vanished"}})
	if len(threads) != 1 || threads[0].Parent.ID != "r1" {
		t.Fatalf("expected the orphaned reply promoted, got %+v", threads)
	}
}

// An unset kind reads as a plain comment. Records written before kinds existed
// have an empty one, and every reader has to resolve it the same way — the
// default is the one that claims the least about what the reader should do.
func TestKindDefaultsToComment(t *testing.T) {
	if got := Kind("").OrDefault(); got != KindComment {
		t.Fatalf("expected an unset kind to be a comment, got %q", got)
	}
	if got := ParseKind(""); got != KindComment {
		t.Fatalf("expected empty input to parse as a comment, got %q", got)
	}
	// A label we do not recognise is not worth rejecting the comment over.
	if got := ParseKind("nitpick"); got != KindComment {
		t.Fatalf("expected an unknown kind to fall back to comment, got %q", got)
	}
	if got := ParseKind("  SUGGESTION "); got != KindSuggestion {
		t.Fatalf("expected case and space tolerance, got %q", got)
	}
}

// tab's cycle has to visit every kind and return to the start, or a kind becomes
// unreachable from the compose box.
func TestKindCycleIsComplete(t *testing.T) {
	seen := map[Kind]bool{}
	k := KindComment
	for range Kinds() {
		seen[k] = true
		k = k.Next()
	}
	if k != KindComment {
		t.Fatalf("expected the cycle to close, ended on %q", k)
	}
	for _, want := range Kinds() {
		if !seen[want] {
			t.Fatalf("%q is unreachable by cycling", want)
		}
	}
	// An unset kind still advances rather than sticking.
	if got := Kind("").Next(); got == KindComment {
		t.Fatal("expected an unset kind to advance off the default")
	}
}

// The kind survives a write and read back, and an agent's comment is recognised
// as a robot's wherever it renders or publishes.
func TestKindPersistsAndRobotAuthorshipIsDetectable(t *testing.T) {
	store := Store{Root: t.TempDir()}
	r, err := store.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.AddComment(r, Comment{
		Author: "agent", Body: "this leaks", Kind: KindSuggestion,
		Anchor: Anchor{Path: "a.go", LineHint: 3},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one comment, got %d", len(got))
	}
	if got[0].Kind != KindSuggestion {
		t.Fatalf("expected the kind persisted, got %q", got[0].Kind)
	}
	if !got[0].ByRobot() {
		t.Fatal("expected an agent's comment to count as a robot's")
	}
	if (Comment{Author: AuthorHuman}).ByRobot() {
		t.Fatal("expected your own comment not to count as a robot's")
	}
}

// The kind and the robot marker are composed at publish time, never stored. The
// stored body is what the author typed; baking prefixes in would double them on a
// re-publish and would show them to the reviewer mid-edit.
func TestPublishBodyPrefixesKindAndRobot(t *testing.T) {
	human := Comment{Author: AuthorHuman, Body: "this drops the error", Kind: KindSuggestion}
	if got := human.PublishBody(); got != "suggestion: this drops the error" {
		t.Fatalf("got %q", got)
	}
	// A robot's comment is marked, and the marker leads: who wrote a remark frames
	// everything after it, the kind included. On GitHub it posts under the
	// authenticated user's account, so nothing else distinguishes it from a person's.
	robot := Comment{Author: "agent", Body: "nil deref here", Kind: KindSuggestion}
	want := RobotMarker + " suggestion: nil deref here"
	if got := robot.PublishBody(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// An unset kind still gets a label, since GitHub has no colour to fall back on.
	plain := Comment{Author: AuthorHuman, Body: "reads fine"}
	// A plain comment carries no label at all: it is the default, so labelling it
	// labels every remark that had nothing special to say.
	if got := plain.PublishBody(); got != "reads fine" {
		t.Fatalf("got %q", got)
	}
}

// A reply joins a thread whose first comment already carries the kind, so
// repeating it on every message is noise. The robot marker still applies —
// authorship changes per message, the kind does not.
func TestPublishBodyOmitsKindOnReplies(t *testing.T) {
	reply := Comment{Author: "agent", Body: "fixed", Kind: KindSuggestion, ReplyTo: "parent-1"}
	if got := reply.PublishBody(); got != RobotMarker+" fixed" {
		t.Fatalf("got %q", got)
	}
	humanReply := Comment{Author: AuthorHuman, Body: "thanks", ReplyTo: "parent-1"}
	if got := humanReply.PublishBody(); got != "thanks" {
		t.Fatalf("got %q", got)
	}
}

// Publishing twice must produce the same body. It would not if the prefixes were
// written back into the stored record.
func TestPublishBodyIsIdempotent(t *testing.T) {
	c := Comment{Author: "agent", Body: "leaks", Kind: KindQuestion}
	first := c.PublishBody()
	if second := c.PublishBody(); second != first {
		t.Fatalf("composing twice differed: %q then %q", first, second)
	}
	if strings.Contains(c.Body, RobotMarker) || strings.Contains(c.Body, "(") {
		t.Fatalf("expected the stored body untouched, got %q", c.Body)
	}
}

// Deleting a comment takes its replies with it. Left behind, Threads promotes each
// orphaned reply to a conversation of its own, so the answers to a deleted remark
// would scatter through the diff as independent findings.
func TestDeleteCommentCascadesToReplies(t *testing.T) {
	store := Store{Root: t.TempDir()}
	r, err := store.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	parent, err := store.AddComment(r, Comment{
		Author: AuthorHuman, Body: "needs a guard",
		Anchor: Anchor{Path: "a.go", LineHint: 3},
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := store.Reply(r, parent.ID, Comment{Author: "agent", Body: "fixed"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	// An unrelated conversation must survive untouched.
	other, err := store.AddComment(r, Comment{
		Author: AuthorHuman, Body: "separate point",
		Anchor: Anchor{Path: "b.go", LineHint: 9},
	})
	if err != nil {
		t.Fatalf("add other: %v", err)
	}

	if err := store.DeleteComment(r, parent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 || left[0].ID != other.ID {
		t.Fatalf("expected only the unrelated comment left, got %+v", left)
	}
	// And nothing is left that Threads would promote to a top-level comment.
	for _, th := range Threads(left) {
		if th.Parent.ReplyTo != "" {
			t.Fatalf("an orphaned reply survived as its own conversation: %+v", th.Parent)
		}
	}
}

// `awp review reply --to` accepts any comment's id, so a chain can be deeper than
// one level even though the deck normalises its own replies onto the thread's top.
// The cascade has to reach the whole chain.
func TestDeleteCommentCascadesTransitively(t *testing.T) {
	store := Store{Root: t.TempDir()}
	r, err := store.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	parent, err := store.AddComment(r, Comment{
		Author: AuthorHuman, Body: "top", Anchor: Anchor{Path: "a.go", LineHint: 1},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	mid, err := store.Reply(r, parent.ID, Comment{Author: "agent", Body: "middle"})
	if err != nil {
		t.Fatalf("reply 1: %v", err)
	}
	if _, err := store.Reply(r, mid.ID, Comment{Author: AuthorHuman, Body: "deep"}); err != nil {
		t.Fatalf("reply 2: %v", err)
	}
	if err := store.DeleteComment(r, parent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left, err := store.Comments(r)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("expected the whole chain gone, got %+v", left)
	}
}

// Deleting a reply takes only that reply — the conversation it was part of stays.
func TestDeletingAReplyLeavesItsParent(t *testing.T) {
	store := Store{Root: t.TempDir()}
	r, _ := store.Open("/repo/proj", Target{Kind: TargetWorking, Workspace: "ws"})
	parent, err := store.AddComment(r, Comment{
		Author: AuthorHuman, Body: "top", Anchor: Anchor{Path: "a.go", LineHint: 1},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	reply, err := store.Reply(r, parent.ID, Comment{Author: "agent", Body: "answer"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := store.DeleteComment(r, reply.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left, _ := store.Comments(r)
	if len(left) != 1 || left[0].ID != parent.ID {
		t.Fatalf("expected the parent to survive, got %+v", left)
	}
}

// The closure is order-independent: a reply may be listed before the reply it
// answers, so one pass over the set could miss the tail of a chain.
func TestCommentAndRepliesIgnoresOrder(t *testing.T) {
	comments := []Comment{
		{ID: "deep", ReplyTo: "mid"},
		{ID: "mid", ReplyTo: "top"},
		{ID: "top"},
		{ID: "unrelated"},
	}
	got := CommentAndReplies(comments, "top")
	want := []string{"deep", "mid", "top"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (sorted)", got, want)
		}
	}
	// A comment with no replies is just itself.
	if got := CommentAndReplies(comments, "unrelated"); len(got) != 1 || got[0] != "unrelated" {
		t.Fatalf("expected only the comment itself, got %v", got)
	}
}

// LineRange is the one spelling of a location every surface shares, so the
// compose box, the index, the agent prompt and the publish log cannot disagree.
func TestAnchorLineRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a     Anchor
		want  string
		multi bool
	}{
		{"one line", Anchor{LineHint: 12}, "12", false},
		{"a range", Anchor{LineHint: 12, EndLineHint: 18}, "12-18", true},
		// An end at the start is one line, and an end above it is a record written
		// wrongly; both read as "one line" rather than as a range of zero or -6.
		{"end at the start", Anchor{LineHint: 12, EndLineHint: 12}, "12", false},
		{"end before the start", Anchor{LineHint: 12, EndLineHint: 6}, "12", false},
		// No line at all: a remark about the change as a whole.
		{"no line", Anchor{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.LineRange(); got != tc.want {
				t.Fatalf("LineRange: got %q, want %q", got, tc.want)
			}
			if got := tc.a.Multiline(); got != tc.multi {
				t.Fatalf("Multiline: got %v, want %v", got, tc.multi)
			}
		})
	}
}

// A range anchor survives the round trip through the store, which is what makes
// it worth recording at all.
func TestRangeAnchorRoundTrips(t *testing.T) {
	store := testStore(t)
	r, err := store.Open(t.TempDir(), Target{Kind: TargetWorking, Workspace: "ws"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	in := Comment{Author: AuthorHuman, Body: "this block", Anchor: Anchor{
		Path: "a.go", Side: SideNew,
		LineHint: 12, Text: "first",
		EndLineHint: 18, EndText: "last",
	}}
	if _, err := store.AddComment(r, in); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := store.Comments(r)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one comment, got %d", len(got))
	}
	if a := got[0].Anchor; a.EndLineHint != 18 || a.EndText != "last" {
		t.Fatalf("expected the range preserved, got %+v", a)
	}
}
