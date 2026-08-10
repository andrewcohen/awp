package deckdata

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andrewcohen/awp/internal/workspace"
)

// Why a row is in the attention scope.
//
// The scope is a flat list — no sections, no bucket headers, unlike the inbox.
// That is a deliberate choice and it has one consequence: with several kinds of
// claim sharing a single column, every row has to say why it is there. "the
// agent stopped and is waiting on you" and "CI went red" are different enough
// that a list mixing them silently is a list you have to open each row to read.
//
// So the filter answers with a reason rather than a bool. A bool cannot be
// rendered, and the predicate it came from could only grow by taking another
// parameter at every call site — which is what View.Attention was doing, a
// func(status, unread, active) bool injected from deckui, one scalar per signal
// it knew about.

// Reason is what a row is asking of you, or ReasonNone when it is asking
// nothing and so is not in the scope at all.
//
// Declaration order is precedence *and* render order, the same convention
// InboxBucket uses: most-your-problem first. A row matching several reasons
// reports the first, because the flat list has no headers to carry that
// hierarchy — the sort does, and the reason a row shows has to be the one it
// sorted under or the list reads as unordered.
type Reason int

const (
	// ReasonNone is a row with nothing to act on. Not in the scope.
	ReasonNone Reason = iota
	// ReasonWorking is an agent running right now. First, which is not where
	// urgency would put it — there is nothing to do about a working agent — but
	// the deck is watched as much as it is acted on, and the running agents are
	// the rows that are changing. Scattered below the rows that want you they
	// were the hardest thing in the list to keep an eye on; grouped at the top
	// they are one glance.
	//
	// Being first also decides what a row reports when it matches more than one
	// reason, and that lands the right way round: a workspace whose agent is
	// working and whose PR has gone red reads as working, because something is
	// already on it.
	ReasonWorking
	// ReasonWaiting is an agent that asked and stopped — a question, a
	// permission prompt, an elicitation. First of the reasons that want you,
	// because it is the only one where the work has actually halted and you are
	// the thing in its way.
	ReasonWaiting
	// ReasonReReviewRequested is a PR you reviewed once, that the author has
	// pushed to and asked you about again. Above a first request because
	// somebody acted on what you said and is now waiting a second time.
	ReasonReReviewRequested
	// ReasonReviewRequested is a PR you have checked out whose review is still
	// wanted from you.
	ReasonReviewRequested
	// ReasonNotified is a workspace that finished a turn since you last looked.
	// Above the PR reasons: an agent that stopped is waiting to be read, where
	// a PR of yours sits there either way.
	ReasonNotified
	// ReasonPRNeedsAction is your own PR with something to fix — changes
	// requested, CI red, or a branch that will not merge as it stands.
	ReasonPRNeedsAction
	// ReasonPRReadyToMerge is your own PR, approved and green. Nothing is
	// broken, so it ranks below what is — but it is one keypress from done.
	ReasonPRReadyToMerge
	// ReasonRecent is a workspace you were in recently and nothing else is true
	// of. Last, because it asks for nothing at all — it is the answer to "what
	// was I doing", which is worth a row only once the list has run out of
	// things that actually want you.
	ReasonRecent
	// lastReason is the highest declared reason, so a test can walk them all
	// without a list to keep in step with the constants above. Not a reason —
	// never returned, never rendered.
	lastReason = iota - 1
)

// String is what the row shows: the reason in the words a reader needs, not the
// constant's name.
//
// Phrased as what happened rather than as a state name — "finished a turn" over
// "notified" — because the reader is deciding what to do next, and a state name
// makes them translate it first. ReasonNone has no words on purpose: a row that
// is not in the scope has nothing to say, and giving it a phrase would invite a
// caller to render one.
//
// ReasonRecent's real words are a duration, which needs the row — see
// View.WantsText, the one place a reason is turned into text. What is here is
// its fallback, for a caller holding a reason and no row to date it against.
func (r Reason) String() string {
	switch r {
	case ReasonWaiting:
		return "waiting on you"
	case ReasonReReviewRequested:
		return "re-review requested"
	case ReasonReviewRequested:
		return "your review"
	case ReasonNotified:
		return "finished a turn"
	case ReasonPRNeedsAction:
		return "PR needs action"
	case ReasonPRReadyToMerge:
		return "approved, green"
	case ReasonWorking:
		return "working"
	case ReasonRecent:
		return "recently active"
	default:
		return ""
	}
}

// Wants is why a row is in the scope, given everything the read model knows
// about it.
//
// A method on View rather than a function taking the facts, because View is
// already "the raw rows plus the lookup tables the selection logic joins
// against" — the PR cache, and whatever else a later signal needs. A new signal
// is then a field on the view and an arm in here, not a parameter threaded
// through every call site, which is the failure the old injected predicate was
// heading for.
//
// Every source is asked and the most urgent answer wins, rather than each
// source returning early. A workspace can be several things at once — an agent
// mid-turn on a PR whose CI just went red — and which of those the row reports
// has to be the one it sorted under, or the list reads as unordered. Taking the
// minimum makes the constants' declaration order the precedence, so a reason
// inserted in the right place needs nothing else changed to rank correctly.
func (v View) Wants(it Item) Reason {
	best := ReasonNone
	consider := func(r Reason) {
		if r != ReasonNone && (best == ReasonNone || r < best) {
			best = r
		}
	}
	consider(AgentWants(it.Status, it.Unread, it.Active))
	consider(v.prWants(it))
	consider(v.recentlyActive(it))
	return best
}

// WantsText is the words a row shows for why it is in the scope, and the one
// place a reason becomes text.
//
// A method rather than Reason.String alone because one reason's words are a
// duration, and a duration needs the row to measure against. Everything else
// delegates, so this is a decorator over String rather than a second set of
// phrases that could drift from it.
func (v View) WantsText(it Item) string {
	r := v.Wants(it)
	if r == ReasonRecent {
		return ago(v.now().Sub(it.LastActiveAt))
	}
	return r.String()
}

// urgencyOrdered is the attention scope's row order: most-your-problem first.
//
// Pinned rows still float above everything, in register order, because a pin is
// something you said out loud and a reason is something the deck worked out —
// when the two disagree the deck is not the one to trust. Below the pins it is
// Reason order, which is the constants' declaration order, so the ranking lives
// in one place and a new reason slots into it by being declared in the right
// spot.
//
// Ties fall back to project then label, the order the scope had before it had
// urgency — so rows that are equally your problem stay in a stable, familiar
// arrangement rather than shuffling between refreshes.
func (v View) urgencyOrdered(items []Item) []Item {
	out := append([]Item(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ka, kb := v.pinKey(a), v.pinKey(b); ka != kb {
			return ka < kb
		}
		if ra, rb := urgency(v.Wants(a)), urgency(v.Wants(b)); ra != rb {
			return ra < rb
		}
		if a.ProjectName != b.ProjectName {
			return a.ProjectName < b.ProjectName
		}
		return v.DisplayLabel(a) < v.DisplayLabel(b)
	})
	return out
}

// pinKey sorts pinned rows ahead of unpinned ones, pinned by register order.
func (v View) pinKey(it Item) string {
	pg := strings.TrimSpace(it.PinGroup)
	if pg == "" {
		// After every register. PinGroupSortKey's own keys start at \x00, so
		// anything above them puts the unpinned pile last.
		return "\xff"
	}
	return PinGroupSortKey(v.PinAliases, pg)
}

// urgency ranks a reason for sorting, putting "no reason at all" last.
//
// Only one row can have no reason and still be in the scope: the workspace the
// deck was opened from, which is kept whatever it is doing so the cursor has
// somewhere to land. Sorting it by its raw value would put it first — the zero
// value being the smallest — which would open the deck with the one row that
// wants nothing at the top of a list about what wants you.
func urgency(r Reason) int {
	if r == ReasonNone {
		return int(lastReason) + 1
	}
	return int(r)
}

// prWants is what the row's pull request is asking of you, if anything.
//
// It reads the inbox's own classification rather than asking "is CI red" a
// second time. The two scopes answer the same question about the same PR, and a
// second copy of the rule is how they come to disagree — the inbox filing a PR
// under "Needs action" while the attention scope has nothing to say about it.
//
// Only three of the five buckets are reasons. A PR that is merely open, or
// somebody else's and not awaiting you, is the inbox's business: attention is
// "act on this now", and pulling those in would make the flat list a second
// copy of a scope that already exists and sections its rows properly.
func (v View) prWants(it Item) Reason {
	// A synthetic inbox row is a PR with no local workspace, which is the
	// opposite of one you are working on. The attention scope is built from
	// real rows only, so this is defensive — but it is the whole difference
	// between "your review" here and the inbox's "Needs your review" bucket,
	// which deliberately includes PRs you have not pulled down.
	if it.Virtual {
		return ReasonNone
	}
	st, ok := v.OpenPRStatus(it)
	if !ok {
		return ReasonNone
	}
	switch PRInboxBucket(st) {
	case InboxNeedsYourReview:
		// Still wanted from you, which is the whole test: GitHub clears
		// ReviewRequested the moment you submit a review and sets
		// ReviewRerequested only if the author asks again. So a review you have
		// already given, with no re-request, drops out of the scope on its own
		// rather than needing a rule that says so.
		if st.ReviewRerequested {
			return ReasonReReviewRequested
		}
		return ReasonReviewRequested
	case InboxNeedsAction:
		return ReasonPRNeedsAction
	case InboxReadyToMerge:
		return ReasonPRReadyToMerge
	case InboxOtherOpen, InboxMine:
		// Open, and waiting on somebody who is not you.
		return ReasonNone
	}
	return ReasonNone
}

// recentlyActive keeps a workspace you were just in from vanishing the moment
// you read it.
//
// Before this the scope was binary on the unread flag, so looking at a row
// deleted the only evidence it was recent — the deck could tell you what wanted
// you and never what you were in the middle of.
//
// An undated workspace is not recent and not stale: every entry written before
// LastActiveAt existed has a zero time, and reading that as "last active in
// 1970" would be an answer rather than the absence of one.
func (v View) recentlyActive(it Item) Reason {
	if it.LastActiveAt.IsZero() {
		return ReasonNone
	}
	if v.now().Sub(it.LastActiveAt) > RecentWindow {
		return ReasonNone
	}
	return ReasonRecent
}

// RecentWindow is how long a workspace goes on counting as one you are in the
// middle of.
//
// Short on purpose. The claim is "you were just here", not "you worked on this
// today" — a day-long window would put every workspace you touched since
// breakfast in a list whose whole job is to be shorter than the all scope.
const RecentWindow = 4 * time.Hour

// now is the view's clock, defaulting to the real one. Injectable so a test can
// say what "recent" means without sleeping.
func (v View) now() time.Time {
	if v.Now == nil {
		return time.Now()
	}
	return v.Now()
}

// ago is an elapsed time as a row shows it: "4m ago", "2h ago", "just now".
//
// One unit, never two. It is a chip on a meta line answering "roughly how long
// ago", and "2h14m ago" spends four more columns to answer a question nobody
// asked with that precision. Rounds down, so a row never claims to be older
// than it is — and under a minute it says "just now", because "0m ago" is a
// sentence about arithmetic rather than about the workspace.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	default:
		// Days are unreachable while RecentWindow is hours, but the function is
		// the answer to "how long ago" rather than to "how long ago, given the
		// window" — a wider window should not need this edited too.
		if d >= 24*time.Hour {
			return strconv.Itoa(int(d.Hours()/24)) + "d ago"
		}
		return strconv.Itoa(int(d.Hours())) + "h ago"
	}
}

// AgentWants is the agent-session half of the answer: what the workspace's own
// status and unread flag say, with a freshness check on top.
//
// Split out from View.Wants because the mini-deck is deliberately only this
// half. It is a jump list for live sessions — "take me to the thing that is
// happening" — and the signals View.Wants adds are not things you can jump to.
// A PR with red CI is worth surfacing on the deck and meaningless as a jump
// target, so widening this would put rows in the mini-deck that do nothing when
// you press enter on them.
//
// A stored "working" status counts only when the row is fresh: a live tmux
// session whose :agent pane is really an agent rather than a bare shell. Claude
// has no exit hook, so a crashed agent leaves "working" behind forever, and
// without the check it would sit at the top of the scope permanently.
//
// active should be true when the session exists and its :agent pane is running
// an agent, OR when tmux state is not yet known — on the fast first paint,
// trust the stored status and let the refresh correct it.
//
// active is consulted only for "working". For waiting and idle the unread flag
// is the durable signal, written after the turn finished, so those rows surface
// whether or not the process is still alive; the mini-deck recreates the
// session on jump if it has to.
func AgentWants(status string, unread, active bool) Reason {
	// The one thing Classify cannot answer, asked first: whether the session
	// behind a stored "working" is still alive. Everything else about an agent's
	// state is a question the badge asks too, so it is asked in one place.
	if workspace.IsWorking(status) && !active {
		return ReasonNone
	}
	// Routed through workspace.Classify rather than re-deciding here. The two used
	// to be separate switches over the same vocabulary, and they had drifted: this
	// one listed "error" alongside "exited" and dropped such a workspace from the
	// scope, while Classify — which decides the tmux badge count — called it
	// notified and counted it. Nothing writes "error" today (report-status takes a
	// closed set: working / idle / waiting / exited), so the badge never actually
	// said 3 over a scope of 2; a stale state file was all it would have taken.
	//
	// The general shape is the point. Membership of the attention scope and the
	// badge's count are two readings of one question, so a second switch here is a
	// second answer waiting to disagree with the first.
	switch workspace.Classify(status, unread) {
	case workspace.AttentionWorking:
		return ReasonWorking
	case workspace.AttentionWaiting:
		return ReasonWaiting
	case workspace.AttentionNotified:
		return ReasonNotified
	case workspace.AttentionNone:
	}
	return ReasonNone
}
