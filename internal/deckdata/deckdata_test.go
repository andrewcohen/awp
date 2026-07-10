package deckdata

import (
	"reflect"
	"testing"

	"github.com/andrewcohen/awp/internal/prstatus"
)

// alwaysAttention is the injected attention predicate for tests that want
// every row to count as needing attention.
func alwaysAttention(string, bool, bool) bool { return true }

func TestPRInboxBucketClassification(t *testing.T) {
	cases := []struct {
		name   string
		status prstatus.PRStatus
		want   InboxBucket
	}{
		{"review requested wins over CI failing",
			prstatus.PRStatus{State: prstatus.PRStateOpen, ReviewRequested: true, CIState: prstatus.PRCIFailing},
			InboxNeedsYourReview},
		{"mine + changes requested",
			prstatus.PRStatus{State: prstatus.PRStateOpen, Mine: true, ReviewDecision: prstatus.PRReviewChangesRequested},
			InboxNeedsAction},
		{"mine + approved + green + clean",
			prstatus.PRStatus{State: prstatus.PRStateOpen, Mine: true, ReviewDecision: prstatus.PRReviewApproved, CIState: prstatus.PRCIPassing, MergeStateStatus: prstatus.PRMergeStateClean},
			InboxReadyToMerge},
		{"mine + draft + failing CI stays in Mine",
			prstatus.PRStatus{State: prstatus.PRStateOpen, Mine: true, IsDraft: true, CIState: prstatus.PRCIFailing},
			InboxMine},
		{"someone else's PR, no review requested",
			prstatus.PRStatus{State: prstatus.PRStateOpen},
			InboxOtherOpen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PRInboxBucket(tc.status); got != tc.want {
				t.Errorf("PRInboxBucket = %s, want %s", InboxBucketLabel(got), InboxBucketLabel(tc.want))
			}
		})
	}
}

func TestResolvePRStatusByNumberAndBookmark(t *testing.T) {
	v := View{PRStatusByRepo: map[string]map[string]prstatus.PRStatus{
		"/repo": {
			"feat/x": {Number: 12, HeadRefName: "feat/x", State: prstatus.PRStateOpen},
		},
	}}

	// By pinned PRNumber (bookmark irrelevant).
	if st, ok := v.ResolvePRStatus(Item{RepoRoot: "/repo", PRNumber: 12}); !ok || st.Number != 12 {
		t.Fatalf("resolve by PRNumber: got %+v ok=%v", st, ok)
	}
	// By bookmark → headRefName (compat path when PRNumber unset).
	if st, ok := v.ResolvePRStatus(Item{RepoRoot: "/repo", Bookmark: "feat/x"}); !ok || st.Number != 12 {
		t.Fatalf("resolve by bookmark: got %+v ok=%v", st, ok)
	}
	// Unknown repo / no match.
	if _, ok := v.ResolvePRStatus(Item{RepoRoot: "/other", PRNumber: 12}); ok {
		t.Fatal("resolve in unknown repo should miss")
	}
	if _, ok := v.ResolvePRStatus(Item{RepoRoot: "/repo", PRNumber: 999}); ok {
		t.Fatal("resolve with unknown PRNumber should miss")
	}
}

func TestItemsInboxFiltersToOpenPRsAndSortsByBucket(t *testing.T) {
	pr := func(n int, head string, mut func(*prstatus.PRStatus)) prstatus.PRStatus {
		s := prstatus.PRStatus{Number: n, HeadRefName: head, State: prstatus.PRStateOpen}
		mut(&s)
		return s
	}
	v := View{
		Scope: ScopeInbox,
		All: []Item{
			{WorkspaceName: "ready", ProjectName: "p", RepoRoot: "/r", PRNumber: 1},
			{WorkspaceName: "review", ProjectName: "p", RepoRoot: "/r", PRNumber: 2},
			{WorkspaceName: "closed", ProjectName: "p", RepoRoot: "/r", PRNumber: 3},
			{WorkspaceName: "noPR", ProjectName: "p", RepoRoot: "/r"},
		},
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{"/r": {
			"a": pr(1, "a", func(s *prstatus.PRStatus) {
				s.Mine = true
				s.ReviewDecision = prstatus.PRReviewApproved
				s.CIState = prstatus.PRCIPassing
				s.MergeStateStatus = prstatus.PRMergeStateClean
			}),
			"b": pr(2, "b", func(s *prstatus.PRStatus) { s.ReviewRequested = true }),
			"c": {Number: 3, HeadRefName: "c", State: prstatus.PRStateMerged},
		}},
	}
	got := v.Items()
	var names []string
	for _, it := range got {
		names = append(names, it.WorkspaceName)
	}
	// "closed" (merged) and "noPR" are filtered out; "review"
	// (NeedsYourReview) sorts ahead of "ready" (ReadyToMerge).
	want := []string{"review", "ready"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("inbox items = %v, want %v", names, want)
	}
}

func TestItemsAttentionUsesInjectedPredicate(t *testing.T) {
	all := []Item{
		{WorkspaceName: "a", ProjectName: "p", Status: "working", Active: true},
		{WorkspaceName: "b", ProjectName: "p", Status: "idle"},
	}
	// nil predicate → attention scope shows nothing.
	if got := (View{Scope: ScopeAttention, All: all}).Items(); len(got) != 0 {
		t.Fatalf("nil predicate should yield no rows, got %d", len(got))
	}
	// Predicate selecting only "a".
	only := func(status string, _, _ bool) bool { return status == "working" }
	got := (View{Scope: ScopeAttention, All: all, Attention: only}).Items()
	if len(got) != 1 || got[0].WorkspaceName != "a" {
		t.Fatalf("attention filter = %+v, want [a]", got)
	}
}

func TestItemsTextFilterMatchesLabelAndProject(t *testing.T) {
	v := View{
		Scope:     ScopeAll,
		Filter:    "widget",
		Attention: alwaysAttention,
		All: []Item{
			{WorkspaceName: "widget-fix", ProjectName: "p"},
			{WorkspaceName: "other", ProjectName: "widgets"},
			{WorkspaceName: "unrelated", ProjectName: "q"},
		},
	}
	got := v.Items()
	if len(got) != 2 {
		t.Fatalf("filter matched %d rows, want 2 (%v)", len(got), got)
	}
}

func TestItemsAllScopeFloatsPinnedFirstInRegisterOrder(t *testing.T) {
	v := View{
		Scope:      ScopeAll,
		PinAliases: map[string]string{"a": "zebra"}, // letter a aliased so it sorts after "default"
		All: []Item{
			{WorkspaceName: "plain", ProjectName: "p"},
			{WorkspaceName: "lettered", ProjectName: "p", PinGroup: "a"},
			{WorkspaceName: "def", ProjectName: "p", PinGroup: PinGroupDefault},
		},
	}
	got := v.Items()
	var names []string
	for _, it := range got {
		names = append(names, it.WorkspaceName)
	}
	// default register first, then other registers by alias, then unpinned.
	want := []string{"def", "lettered", "plain"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("pinned order = %v, want %v", names, want)
	}
	if n := PinnedCount(got); n != 2 {
		t.Fatalf("PinnedCount = %d, want 2", n)
	}
}

func TestInboxVirtualRowsSynthesizedAndDeduped(t *testing.T) {
	v := View{
		Scope: ScopeInbox,
		// One checked-out workspace pinned to PR #1 (review requested).
		All: []Item{{WorkspaceName: "checked-out", ProjectName: "proj", RepoRoot: "/r", PRNumber: 1}},
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{"/r": {
			"h1": {Number: 1, HeadRefName: "h1", State: prstatus.PRStateOpen, ReviewRequested: true},
			"h2": {Number: 2, HeadRefName: "h2", State: prstatus.PRStateOpen, ReviewRequested: true}, // no workspace → virtual review
			"h3": {Number: 3, HeadRefName: "h3", State: prstatus.PRStateOpen, Mine: true},            // no workspace → virtual mine
			"h4": {Number: 4, HeadRefName: "h4", State: prstatus.PRStateMerged, Mine: true},          // merged → not surfaced
		}},
	}
	got := v.Items()
	byNum := map[int]Item{}
	for _, it := range got {
		byNum[it.PRNumber] = it
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows (1 real + 2 virtual), got %d: %+v", len(got), got)
	}
	// #1 stays the real row (not duplicated as a virtual).
	if byNum[1].Virtual {
		t.Error("PR #1 should be the real workspace row, not virtual")
	}
	// #2 and #3 are synthesized as virtual, borrowing the sibling project.
	if !byNum[2].Virtual || byNum[2].ProjectName != "proj" || byNum[2].Bookmark != "h2" {
		t.Errorf("virtual review row #2 = %+v", byNum[2])
	}
	if !byNum[3].Virtual || byNum[3].ProjectName != "proj" {
		t.Errorf("virtual mine row #3 = %+v", byNum[3])
	}
	// #4 (merged) is never surfaced.
	if _, ok := byNum[4]; ok {
		t.Error("merged PR #4 should not appear in the inbox")
	}
}
