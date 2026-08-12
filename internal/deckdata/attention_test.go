package deckdata

import (
	"strings"
	"testing"
	"time"

	"github.com/andrewcohen/awp/internal/prstatus"
	"github.com/andrewcohen/awp/internal/workspace"
)

// The agent-state rule, unchanged from the bool it replaced (deckui's
// MiniIncluded + the freshness check) — this unit moves the rule and gives its
// answer a name, so these cases are the guard that it moved intact.
func TestAgentWantsReadsTheStatusAndTheUnreadFlag(t *testing.T) {
	cases := []struct {
		name   string
		status string
		unread bool
		active bool
		want   Reason
	}{
		{"working, and the unread flag is irrelevant", "working", false, true, ReasonWorking},
		{"in_progress is a spelling of working", "in_progress", false, true, ReasonWorking},
		// A crashed agent leaves "working" behind — Claude has no exit hook — so
		// without the freshness check it would sit in the scope forever.
		{"working with a dead session is not working", "working", true, false, ReasonNone},
		{"waiting needs unread, else it is a prompt you already saw", "WAITING", false, true, ReasonNone},
		{"waiting and unread is you being the blocker", "waiting", true, true, ReasonWaiting},
		{"idle and read is a quiet workspace", "idle", false, true, ReasonNone},
		{"idle and unread is a finished turn", "idle", true, true, ReasonNotified},
		{"no status, read", "", false, true, ReasonNone},
		{"no status, unread", "", true, true, ReasonNotified},
		{"exited never surfaces, unread or not", "exited", true, true, ReasonNone},
		{"exited, read", "exited", false, true, ReasonNone},
		// Nothing writes "error" — report-status takes a closed set — so this is a
		// status only a stale state file can hold. It used to be listed with
		// "exited" here and dropped from the scope, while Classify counted it in the
		// badge. It follows Classify now: an errored agent is not gone, and "the
		// agent is gone, so there is nothing to respond to" is the whole argument
		// for the exited case not applying to it.
		{"an unrecognised status follows the unread flag, like any other", "error", true, true, ReasonNotified},
		{"and stays quiet when it has been seen", "error", false, true, ReasonNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AgentWants(c.status, c.unread, c.active); got != c.want {
				t.Errorf("AgentWants(%q, unread=%v, active=%v) = %v, want %v",
					c.status, c.unread, c.active, got, c.want)
			}
		})
	}
}

// Scope membership and the tmux badge's count are two readings of one question,
// so they must not be able to answer it differently.
//
// They could, and had: this rule listed "error" beside "exited" and dropped such
// a workspace, while Classify — which the badge counts through — called it
// notified. Nothing writes "error" today so the badge never actually said 3 over
// a scope of 2, but a stale state file was all it would have taken, and the shape
// of the mistake outlives the one word it happened to be about.
//
// Liveness is the single admitted difference: the badge reads state files and
// cannot know whether a session is still up.
func TestAgentWantsAgreesWithClassify(t *testing.T) {
	// Every status the two rules have ever named between them, plus the shapes a
	// state file can be in. One spelling of working and no more: both rules reach
	// it through workspace.IsWorking, which is the only place allowed to know the
	// others (see TestOnlyOnePlaceSpellsOutTheWorkingStatuses), so listing them
	// here would prove nothing and re-open what that guard closed.
	for _, status := range []string{"working", "waiting", "WAITING", "idle", "done", "exited", "error", "starting", ""} {
		for _, unread := range []bool{true, false} {
			want := map[workspace.Attention]Reason{
				workspace.AttentionWorking:  ReasonWorking,
				workspace.AttentionWaiting:  ReasonWaiting,
				workspace.AttentionNotified: ReasonNotified,
				workspace.AttentionNone:     ReasonNone,
			}[workspace.Classify(status, unread)]
			// active=true, so the one difference is out of play and any disagreement
			// left is a real one.
			if got := AgentWants(status, unread, true); got != want {
				t.Errorf("AgentWants(%q, unread=%v) = %v but the badge counts it as %v",
					status, unread, got, workspace.Classify(status, unread))
			}
		}
	}
	// The admitted difference, stated rather than left as a gap: a stored
	// "working" whose session has died is in no scope, though the badge — which
	// cannot see tmux — still counts it.
	if got := AgentWants("working", false, false); got != ReasonNone {
		t.Fatalf("expected a dead session out of the scope, got %v", got)
	}
	if got := workspace.Classify("working", false); got != workspace.AttentionWorking {
		t.Fatalf("fixture is wrong: the badge should still count it, got %v", got)
	}
}

// Every reason has words. The scope is a flat list with no headers, so the
// reason a row renders is the only thing telling the reader why it is there —
// an arm added without a phrase would put a blank in that column and the row
// would read as noise.
//
// Loops to the last declared reason rather than naming them, so a new arm is
// covered by having been declared. ReasonNone is excluded deliberately: a row
// that is not in the scope has nothing to say, and a phrase there would invite
// a caller to render one.
func TestEveryReasonHasWords(t *testing.T) {
	for r := ReasonNone + 1; r <= lastReason; r++ {
		if r.String() == "" {
			t.Errorf("Reason(%d) renders as nothing", int(r))
		}
	}
	if ReasonNone.String() != "" {
		t.Errorf("ReasonNone says %q; a row not in the scope has nothing to say", ReasonNone)
	}
}

// The zero value is "not in the scope". Worth pinning: it is what a Reason
// read off an unset field means, and the scope filter is a comparison against
// it rather than a list of the reasons that count.
func TestTheZeroReasonIsNotInTheScope(t *testing.T) {
	var r Reason
	if r != ReasonNone {
		t.Errorf("the zero Reason is %v, want ReasonNone", r)
	}
}

// The scope filter and the reason are the same answer. A row in the list must
// be able to say why, and a row that can say why must be in the list —
// otherwise the rendered reason and the membership rule are two rules.
func TestTheScopeIsExactlyTheRowsWithAReason(t *testing.T) {
	all := []Item{
		{WorkspaceName: "blocked", Status: "waiting", Unread: true, Active: true},
		{WorkspaceName: "busy", Status: "working", Active: true},
		{WorkspaceName: "quiet", Status: "idle"},
		{WorkspaceName: "gone", Status: "exited", Unread: true},
	}
	v := View{All: all, Scope: ScopeAttention}
	got := v.Items()
	if len(got) != 2 {
		t.Fatalf("scope has %d rows, want 2: %+v", len(got), got)
	}
	for _, it := range got {
		if v.Wants(it) == ReasonNone {
			t.Errorf("%s is in the scope with no reason to be", it.WorkspaceName)
		}
	}
}

// The current workspace stays in the scope whatever it is doing: the deck was
// opened from it, and dropping it means the cursor cannot land on it and the
// selection jitters elsewhere once the first tmux refresh settles.
//
// It is the one row in the list with no reason, which is why the check above is
// on membership rather than on every row having words.
func TestTheCurrentWorkspaceStaysEvenWithNothingToSay(t *testing.T) {
	v := View{Scope: ScopeAttention, All: []Item{
		{WorkspaceName: "here", Status: "idle", Current: true},
	}}
	if got := v.Items(); len(got) != 1 {
		t.Fatalf("the current workspace was filtered out: %+v", got)
	}
}

// openPR is a View holding one repo's PR cache, for the reason arms that read it.
func openPR(st prstatus.PRStatus) View {
	st.State = prstatus.PRStateOpen
	if st.Number == 0 {
		st.Number = 7
	}
	return View{PRStatusByRepo: map[string]map[string]prstatus.PRStatus{
		"/repo": {"branch": st},
	}}
}

// prRow is the workspace those PR fixtures resolve against.
func prRow() Item {
	return Item{WorkspaceName: "ws", RepoRoot: "/repo", Bookmark: "branch", Status: "idle"}
}

// A PR you have checked out that still wants your review. GitHub clears
// ReviewRequested the moment you submit one and sets ReviewRerequested only if
// the author asks again — so "I reviewed it and nobody asked twice" leaves the
// scope on its own, with no rule saying so.
func TestAPRStillWantingYourReview(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   prstatus.PRStatus
		want Reason
	}{
		{"requested", prstatus.PRStatus{ReviewRequested: true}, ReasonReviewRequested},
		{"asked again after you looked", prstatus.PRStatus{ReviewRerequested: true}, ReasonReReviewRequested},
		{"you reviewed it and nobody asked again", prstatus.PRStatus{}, ReasonNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := openPR(tc.st).Wants(prRow()); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Your own PR, by what it needs from you. Read off the inbox's own bucket
// classification rather than re-deriving "is CI red", so the two scopes cannot
// come to disagree about the same PR.
func TestYourOwnPRByWhatItNeeds(t *testing.T) {
	mine := func(f func(*prstatus.PRStatus)) prstatus.PRStatus {
		st := prstatus.PRStatus{Mine: true}
		f(&st)
		return st
	}
	for _, tc := range []struct {
		name string
		st   prstatus.PRStatus
		want Reason
	}{
		{"CI red", mine(func(s *prstatus.PRStatus) { s.CIState = prstatus.PRCIFailing }), ReasonPRNeedsAction},
		{"changes requested", mine(func(s *prstatus.PRStatus) {
			s.ReviewDecision = prstatus.PRReviewChangesRequested
		}), ReasonPRNeedsAction},
		{"will not merge as it stands", mine(func(s *prstatus.PRStatus) {
			s.MergeStateStatus = prstatus.PRMergeStateDirty
		}), ReasonPRNeedsAction},
		{"approved and green", mine(func(s *prstatus.PRStatus) {
			s.ReviewDecision = prstatus.PRReviewApproved
			s.CIState = prstatus.PRCIPassing
			s.MergeStateStatus = prstatus.PRMergeStateClean
		}), ReasonPRReadyToMerge},
		// Open and waiting on somebody who is not you. The inbox's business,
		// not attention's — pulling these in would make the flat list a second
		// copy of a scope that already sections them properly.
		{"waiting on reviewers", mine(func(s *prstatus.PRStatus) {
			s.ReviewDecision = prstatus.PRReviewRequired
		}), ReasonNone},
		{"a draft", mine(func(s *prstatus.PRStatus) {
			s.IsDraft = true
			s.CIState = prstatus.PRCIFailing
		}), ReasonNone},
		{"somebody else's", prstatus.PRStatus{}, ReasonNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := openPR(tc.st).Wants(prRow()); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// A closed or merged PR asks nothing, whatever its last CI run said.
func TestAClosedPRAsksNothing(t *testing.T) {
	v := openPR(prstatus.PRStatus{Mine: true, CIState: prstatus.PRCIFailing})
	st := v.PRStatusByRepo["/repo"]["branch"]
	st.State = prstatus.PRStateMerged
	v.PRStatusByRepo["/repo"]["branch"] = st
	if got := v.Wants(prRow()); got != ReasonNone {
		t.Errorf("a merged PR gave %v", got)
	}
}

// "Checked out" is the whole difference between this and the inbox's "Needs
// your review" bucket, which deliberately includes PRs you have not pulled
// down. A synthetic row is a PR with no local workspace, which is the opposite
// of one you are working on.
func TestAPRYouHaveNotCheckedOutIsNotYoursToBeIn(t *testing.T) {
	it := prRow()
	it.Virtual = true
	if got := openPR(prstatus.PRStatus{ReviewRequested: true}).Wants(it); got != ReasonNone {
		t.Errorf("a virtual row gave %v", got)
	}
}

// A workspace you were just in stays listed after you have read it, which is
// the whole point: before this, looking at a row deleted the only evidence it
// was recent.
func TestRecentlyActiveSurvivesBeingRead(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	v := View{Now: func() time.Time { return now }}
	fresh := Item{Status: "idle", LastActiveAt: now.Add(-30 * time.Minute)}
	if got := v.Wants(fresh); got != ReasonRecent {
		t.Errorf("a workspace from 30m ago gave %v, want ReasonRecent", got)
	}
	if got := v.WantsText(fresh); got != "30m ago" {
		t.Errorf("WantsText = %q, want %q", got, "30m ago")
	}
	stale := Item{Status: "idle", LastActiveAt: now.Add(-RecentWindow - time.Minute)}
	if got := v.Wants(stale); got != ReasonNone {
		t.Errorf("a workspace past the window gave %v", got)
	}
}

// Unknown is no opinion. Every workspace that existed before LastActiveAt did
// has a zero time, and reading that as "last active in 1970" would be an answer
// rather than the absence of one.
func TestAnUndatedWorkspaceIsNeitherRecentNorStale(t *testing.T) {
	v := View{Now: func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) }}
	if got := v.Wants(Item{Status: "idle"}); got != ReasonNone {
		t.Errorf("an undated workspace gave %v", got)
	}
}

// A row can be several things at once, and the one it reports has to be the one
// it sorted under — otherwise the list reads as unordered.
func TestTheMostUrgentReasonWins(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	v := openPR(prstatus.PRStatus{Mine: true, CIState: prstatus.PRCIFailing})
	v.Now = func() time.Time { return now }
	it := prRow()
	it.LastActiveAt = now.Add(-time.Minute) // also recent
	it.Status = "waiting"                   // also waiting
	it.Unread = true
	if got := v.Wants(it); got != ReasonWaiting {
		t.Errorf("got %v, want ReasonWaiting — the halted agent outranks the rest", got)
	}
}

// A working agent outranks whatever else is true of its workspace, and that is
// the right way round: something is already on it, so reporting the red PR would
// send you to a row that is being dealt with.
func TestAWorkingAgentReportsWorking(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	v := openPR(prstatus.PRStatus{Mine: true, CIState: prstatus.PRCIFailing})
	v.Now = func() time.Time { return now }
	it := prRow()
	it.LastActiveAt = now.Add(-time.Minute) // also recent
	it.Status = "working"
	it.Active = true
	if got := v.Wants(it); got != ReasonWorking {
		t.Errorf("got %v, want ReasonWorking", got)
	}
}

// The flat list's order is its hierarchy: there are no headers doing that job.
func TestTheScopeReadsDownThePriority(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	v := View{
		Scope: ScopeAttention,
		Now:   func() time.Time { return now },
		PRStatusByRepo: map[string]map[string]prstatus.PRStatus{"/repo": {
			"needs-action": {Number: 1, State: prstatus.PRStateOpen, Mine: true, CIState: prstatus.PRCIFailing},
			"wants-review": {Number: 2, State: prstatus.PRStateOpen, ReviewRequested: true},
		}},
		All: []Item{
			{WorkspaceName: "recent", ProjectName: "p", Status: "idle", LastActiveAt: now.Add(-time.Hour)},
			{WorkspaceName: "broken", ProjectName: "p", RepoRoot: "/repo", Bookmark: "needs-action", Status: "idle"},
			{WorkspaceName: "blocked", ProjectName: "p", Status: "waiting", Unread: true, Active: true},
			{WorkspaceName: "reviewing", ProjectName: "p", RepoRoot: "/repo", Bookmark: "wants-review", Status: "idle"},
			{WorkspaceName: "running", ProjectName: "p", Status: "working", Active: true},
		},
	}
	var got []string
	for _, it := range v.Items() {
		got = append(got, it.WorkspaceName)
	}
	// The agent rows lead, which is not where urgency would put them — there is
	// nothing to do about a working agent. The deck is watched as much as it is
	// acted on, and scattered below the rows that want you the moving rows were
	// the hardest thing in the list to keep an eye on.
	//
	// blocked before running is the band's own order — project, then label — and
	// not a claim that a stopped agent outranks a running one. Inside a band the
	// order is alphabetical precisely so that the agent's state, which changes
	// every few seconds, is not what decides where its row sits.
	want := []string{"blocked", "running", "reviewing", "broken", "recent"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestAnAgentsLifecycleDoesNotMoveItsRow is the whole point of the bands. An
// agent working, stopping to ask, and finishing a turn is one agent, and under a
// Reason-ranked sort each of those steps walked its row several places and
// displaced everything in between — on every 5s refresh, for every agent running.
func TestAnAgentsLifecycleDoesNotMoveItsRow(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	rows := func(mid Item) []string {
		v := View{Scope: ScopeAttention, Now: func() time.Time { return now }, All: []Item{
			{WorkspaceName: "aaa", ProjectName: "p", Status: "working", Active: true},
			mid,
			{WorkspaceName: "zzz", ProjectName: "p", Status: "working", Active: true},
		}}
		var out []string
		for _, it := range v.Items() {
			out = append(out, it.WorkspaceName)
		}
		return out
	}
	want := "aaa,mmm,zzz"
	for _, tc := range []struct {
		what string
		it   Item
	}{
		{"working", Item{WorkspaceName: "mmm", ProjectName: "p", Status: "working", Active: true}},
		{"stopped to ask", Item{WorkspaceName: "mmm", ProjectName: "p", Status: "waiting", Unread: true}},
		{"finished a turn", Item{WorkspaceName: "mmm", ProjectName: "p", Status: "idle", Unread: true}},
	} {
		if got := strings.Join(rows(tc.it), ","); got != want {
			t.Errorf("with the middle row %s the order is %s, want %s", tc.what, got, want)
		}
	}
}

// TestAPRsCheckRunDoesNotMoveItsRow, for the same reason: a red PR going green
// and back is your own PR either way, and which of the two it is belongs on the
// row rather than in its position.
func TestAPRsCheckRunDoesNotMoveItsRow(t *testing.T) {
	order := func(st prstatus.PRStatus) []string {
		v := View{Scope: ScopeAttention, PRStatusByRepo: map[string]map[string]prstatus.PRStatus{
			"/repo": {
				"aaa": {Number: 1, State: prstatus.PRStateOpen, Mine: true, CIState: prstatus.PRCIFailing, HeadRefName: "aaa"},
				"mmm": st,
				"zzz": {Number: 3, State: prstatus.PRStateOpen, Mine: true, CIState: prstatus.PRCIFailing, HeadRefName: "zzz"},
			},
		}, All: []Item{
			{WorkspaceName: "aaa", ProjectName: "p", RepoRoot: "/repo", Bookmark: "aaa", Status: "idle"},
			{WorkspaceName: "mmm", ProjectName: "p", RepoRoot: "/repo", Bookmark: "mmm", Status: "idle"},
			{WorkspaceName: "zzz", ProjectName: "p", RepoRoot: "/repo", Bookmark: "zzz", Status: "idle"},
		}}
		var out []string
		for _, it := range v.Items() {
			out = append(out, it.WorkspaceName)
		}
		return out
	}
	red := prstatus.PRStatus{Number: 2, State: prstatus.PRStateOpen, Mine: true, CIState: prstatus.PRCIFailing, HeadRefName: "mmm"}
	green := prstatus.PRStatus{
		Number: 2, State: prstatus.PRStateOpen, Mine: true, HeadRefName: "mmm",
		CIState: prstatus.PRCIPassing, ReviewDecision: prstatus.PRReviewApproved,
		MergeStateStatus: prstatus.PRMergeStateClean,
	}
	want := "aaa,mmm,zzz"
	if got := strings.Join(order(red), ","); got != want {
		t.Fatalf("with a failing check the order is %s, want %s", got, want)
	}
	if got := strings.Join(order(green), ","); got != want {
		t.Errorf("once the PR is approved and green the order is %s, want %s", got, want)
	}
}

// TestABandsRowsStillSayWhichReasonTheyAre. Sorting coarser than the reason is
// only safe while every reason in a band is reported as itself: the position says
// which band, and the row says which reason within it. A row that sorted with the
// agents and said "recently active" would be the list lying about its own order.
func TestABandsRowsStillSayWhichReasonTheyAre(t *testing.T) {
	v := View{Scope: ScopeAttention, All: []Item{
		{WorkspaceName: "running", ProjectName: "p", Status: "working", Active: true},
		{WorkspaceName: "blocked", ProjectName: "p", Status: "waiting", Unread: true},
		{WorkspaceName: "read-me", ProjectName: "p", Status: "idle", Unread: true},
	}}
	want := map[string]Reason{
		"running": ReasonWorking,
		"blocked": ReasonWaiting,
		"read-me": ReasonNotified,
	}
	for _, it := range v.Items() {
		if got := v.Wants(it); got != want[it.WorkspaceName] {
			t.Errorf("%s reports %v, want %v", it.WorkspaceName, got, want[it.WorkspaceName])
		}
	}
}

// TestEveryReasonIsInSomeBand, walked from the constants rather than listed here.
// bandOf's default is bandNone — last — so a reason added without a band would
// silently sort below "recently active" instead of failing.
func TestEveryReasonIsInSomeBand(t *testing.T) {
	if bandOf(ReasonNone) != bandNone {
		t.Errorf("ReasonNone is in band %v, want bandNone", bandOf(ReasonNone))
	}
	for r := ReasonNone + 1; r <= lastReason; r++ {
		if r == ReasonRecent {
			// The one reason whose band is bandRecent, checked separately so the
			// loop's rule is "everything that wants you sorts above recent".
			if bandOf(r) != bandRecent {
				t.Errorf("%v is in band %v, want bandRecent", r, bandOf(r))
			}
			continue
		}
		if b := bandOf(r); b >= bandRecent {
			t.Errorf("%v is in band %v, which is at or below the band for rows that ask nothing", r, b)
		}
	}
}

// A pin outranks every computed reason: it is somewhere you asked for, and when
// the deck's guess disagrees with what you said, the deck is not the one to
// trust.
func TestAPinnedRowStaysOnTop(t *testing.T) {
	v := View{Scope: ScopeAttention, All: []Item{
		{WorkspaceName: "blocked", ProjectName: "p", Status: "waiting", Unread: true, Active: true},
		{WorkspaceName: "pinned", ProjectName: "p", Status: "working", Active: true, PinGroup: PinGroupDefault},
	}}
	if got := v.Items(); got[0].WorkspaceName != "pinned" {
		t.Errorf("first row is %q, want the pinned one", got[0].WorkspaceName)
	}
}

// The current workspace is kept whatever it is doing, so the cursor has
// somewhere to land — but it is the one row with nothing to say, and sorting it
// by its raw zero value would open the deck with it at the top of a list about
// what wants you.
func TestTheCurrentWorkspaceSortsLastWhenItWantsNothing(t *testing.T) {
	v := View{Scope: ScopeAttention, All: []Item{
		{WorkspaceName: "here", ProjectName: "p", Status: "idle", Current: true},
		{WorkspaceName: "working", ProjectName: "p", Status: "working", Active: true},
	}}
	got := v.Items()
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[len(got)-1].WorkspaceName != "here" {
		t.Errorf("last row is %q, want the reasonless current workspace", got[len(got)-1].WorkspaceName)
	}
}

// One unit, never two — the chip answers "roughly how long ago", and under a
// minute it says so in words rather than as "0m ago".
func TestAgoReadsAsOneUnit(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{90 * time.Second, "1m ago"},
		{2*time.Hour + 44*time.Minute, "2h ago"},
		{50 * time.Hour, "2d ago"},
	} {
		if got := ago(tc.d); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
