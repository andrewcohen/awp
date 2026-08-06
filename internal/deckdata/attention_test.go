package deckdata

import "testing"

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
		{"error is treated like exited", "error", true, true, ReasonNone},
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
