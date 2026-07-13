package deckdata

import (
	"reflect"
	"testing"

	"github.com/andrewcohen/awp/internal/prstatus"
)

// alwaysAttention is the injected attention predicate for tests that want
// every row to count as needing attention.
func alwaysAttention(string, bool, bool) bool { return true }

func TestInboxStackLayoutGroupsAndOrders(t *testing.T) {
	v := View{
		Scope: ScopeInbox,
		All: []Item{
			{WorkspaceName: "base", ProjectName: "p", RepoRoot: "/r", PRNumber: 1},
			{WorkspaceName: "top", ProjectName: "p", RepoRoot: "/r", PRNumber: 2},
			{WorkspaceName: "solo", ProjectName: "p", RepoRoot: "/r", PRNumber: 3},
		},
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{"/r": {
			// base ← top is a stack. base alone is "Mine" (draft); top is
			// "Needs action" (CI failing). The stack sections under the most
			// actionable member: Needs action.
			"base": {Number: 1, HeadRefName: "base", BaseRefName: "main", State: prstatus.PRStateOpen, Mine: true, IsDraft: true},
			"top":  {Number: 2, HeadRefName: "top", BaseRefName: "base", State: prstatus.PRStateOpen, Mine: true, CIState: prstatus.PRCIFailing},
			// solo is a standalone review request → Needs your review, sorts
			// ahead of the stack's bucket.
			"solo": {Number: 3, HeadRefName: "solo", BaseRefName: "main", State: prstatus.PRStateOpen, ReviewRequested: true},
		}},
	}
	got := v.Items()
	var names []string
	for _, it := range got {
		names = append(names, it.WorkspaceName)
	}
	// solo (NeedsYourReview) first; then the stack root "base" then its
	// child "top", contiguous and root-first.
	if want := []string{"solo", "base", "top"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	byName := map[string]Item{}
	for _, it := range got {
		byName[it.WorkspaceName] = it
	}
	if d := byName["base"].StackDepth; d != 0 {
		t.Errorf("base StackDepth = %d, want 0 (stack root)", d)
	}
	if d := byName["top"].StackDepth; d != 1 {
		t.Errorf("top StackDepth = %d, want 1 (stacked on base)", d)
	}
	if b := byName["base"].SectionBucket; b != InboxNeedsAction {
		t.Errorf("base SectionBucket = %s, want Needs action", InboxBucketLabel(b))
	}
	if b := byName["top"].SectionBucket; b != InboxNeedsAction {
		t.Errorf("top SectionBucket = %s, want Needs action", InboxBucketLabel(b))
	}
	// top is stacked on base, which is a draft (not ready to merge), so
	// top is blocked; base is the root and blocks on nothing.
	if !byName["top"].StackBlocked {
		t.Errorf("top should be blocked by its unready base")
	}
	if byName["base"].StackBlocked {
		t.Errorf("base (stack root) should not be blocked")
	}
}

func TestAllScopeStackLayoutGroupsWithinProject(t *testing.T) {
	v := View{
		Scope: ScopeAll,
		All: []Item{
			{WorkspaceName: "solo", ProjectName: "beta", RepoRoot: "/b", PRNumber: 5},
			{WorkspaceName: "child", ProjectName: "alpha", RepoRoot: "/a", PRNumber: 2},
			{WorkspaceName: "root", ProjectName: "alpha", RepoRoot: "/a", PRNumber: 1},
		},
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{
			"/a": {
				"root":  {Number: 1, HeadRefName: "root", BaseRefName: "main", State: prstatus.PRStateOpen},
				"child": {Number: 2, HeadRefName: "child", BaseRefName: "root", State: prstatus.PRStateOpen},
			},
			"/b": {"solo": {Number: 5, HeadRefName: "solo", BaseRefName: "main", State: prstatus.PRStateOpen}},
		},
	}
	got := v.Items()
	var names []string
	for _, it := range got {
		names = append(names, it.WorkspaceName)
	}
	// Project alpha (with its stack root → child contiguous) before beta.
	if want := []string{"root", "child", "solo"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("all-scope order = %v, want %v", names, want)
	}
	byName := map[string]Item{}
	for _, it := range got {
		byName[it.WorkspaceName] = it
	}
	if d := byName["child"].StackDepth; d != 1 {
		t.Errorf("child StackDepth = %d, want 1 (stacked on root) in all scope", d)
	}
	if d := byName["root"].StackDepth; d != 0 {
		t.Errorf("root StackDepth = %d, want 0", d)
	}
}

func TestInboxStackCompletionPullsInNonOwnedLink(t *testing.T) {
	// A stack base ← mid ← tip where base and tip are yours (surfaced) but
	// mid is a teammate's PR (neither yours nor review-requested). Without
	// completion the chain would render base … tip with a hole; completion
	// pulls mid in as a virtual row so the stack is whole.
	v := View{
		Scope: ScopeInbox,
		All: []Item{
			{WorkspaceName: "base-ws", ProjectName: "p", RepoRoot: "/r", PRNumber: 10},
		},
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{"/r": {
			"base": {Number: 10, HeadRefName: "base", BaseRefName: "main", State: prstatus.PRStateOpen, Mine: true},
			"mid":  {Number: 11, HeadRefName: "mid", BaseRefName: "base", State: prstatus.PRStateOpen},
			"tip":  {Number: 12, HeadRefName: "tip", BaseRefName: "mid", State: prstatus.PRStateOpen, Mine: true},
		}},
	}
	got := v.Items()
	byNum := map[int]Item{}
	var order []int
	for _, it := range got {
		byNum[it.PRNumber] = it
		order = append(order, it.PRNumber)
	}
	mid, ok := byNum[11]
	if !ok {
		t.Fatalf("expected #11 (non-owned middle link) pulled in for completion; got rows %v", order)
	}
	if !mid.Virtual {
		t.Errorf("#11 completion row should be virtual")
	}
	if want := []int{10, 11, 12}; !reflect.DeepEqual(order, want) {
		t.Fatalf("stack order = %v, want %v (base, mid, tip contiguous)", order, want)
	}
	if d := byNum[12].StackDepth; d != 2 {
		t.Errorf("tip StackDepth = %d, want 2", d)
	}
}

func TestAllScopePinDragsWholeStack(t *testing.T) {
	// tip (#2137) is pinned; its base (#2264) is not. A pin drags the whole
	// stack up, so both land in the pinned prefix, contiguous root → tip,
	// with the base dragged into the tip's register. An unrelated unpinned
	// PR stays below.
	seed := map[string]map[string]prstatus.PRStatus{"/r": {
		"base":  {Number: 2264, HeadRefName: "base", BaseRefName: "main", State: prstatus.PRStateOpen},
		"tip":   {Number: 2137, HeadRefName: "tip", BaseRefName: "base", State: prstatus.PRStateOpen},
		"other": {Number: 99, HeadRefName: "other", BaseRefName: "main", State: prstatus.PRStateOpen},
	}}
	items := []Item{
		{WorkspaceName: "other", ProjectName: "redwood", RepoRoot: "/r", PRNumber: 99, Bookmark: "other"},
		{WorkspaceName: "base", ProjectName: "redwood", RepoRoot: "/r", PRNumber: 2264, Bookmark: "base"},
		{WorkspaceName: "tip", ProjectName: "redwood", RepoRoot: "/r", PRNumber: 2137, Bookmark: "tip", PinGroup: "default"},
	}
	got := View{Scope: ScopeAll, All: items, PRStatusByRepo: seed}.Items()
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3", len(got))
	}
	if got[0].WorkspaceName != "base" || got[1].WorkspaceName != "tip" {
		t.Fatalf("expected dragged stack [base, tip] first; got %q, %q", got[0].WorkspaceName, got[1].WorkspaceName)
	}
	if got[0].PinGroup != "default" {
		t.Errorf("base should be dragged into the 'default' register; got PinGroup %q", got[0].PinGroup)
	}
	if got[0].StackDepth != 0 || got[1].StackDepth != 1 {
		t.Errorf("stack depths = %d,%d want 0,1", got[0].StackDepth, got[1].StackDepth)
	}
	if PinnedCount(got) != 2 {
		t.Errorf("PinnedCount = %d, want 2 (base dragged in + pinned tip)", PinnedCount(got))
	}
	if got[2].WorkspaceName != "other" || got[2].PinGroup != "" {
		t.Errorf("unrelated unpinned PR should stay below, unpinned; got %q pin=%q", got[2].WorkspaceName, got[2].PinGroup)
	}
}

func TestInboxStackLayoutIgnoresNonOpenParent(t *testing.T) {
	v := View{
		Scope: ScopeInbox,
		All: []Item{
			{WorkspaceName: "child", ProjectName: "p", RepoRoot: "/r", PRNumber: 1},
		},
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{"/r": {
			// child's base is a merged PR's head — merged PRs are filtered
			// out of the inbox, so there's no visible parent to stack on.
			"child":  {Number: 1, HeadRefName: "child", BaseRefName: "merged", State: prstatus.PRStateOpen, ReviewRequested: true},
			"merged": {Number: 9, HeadRefName: "merged", State: prstatus.PRStateMerged},
		}},
	}
	got := v.Items()
	if len(got) != 1 {
		t.Fatalf("items = %d, want 1", len(got))
	}
	if d := got[0].StackDepth; d != 0 {
		t.Errorf("StackDepth = %d, want 0 (merged parent is not a visible stack edge)", d)
	}
}

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
