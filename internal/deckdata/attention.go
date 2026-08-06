package deckdata

import (
	"strings"

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
	// ReasonWaiting is an agent that asked and stopped — a question, a
	// permission prompt, an elicitation. The only reason where you are the
	// thing standing in the way.
	ReasonWaiting
	// ReasonWorking is an agent running right now. Nothing to do about it, but
	// it is why the deck stays lit while work is in flight rather than going
	// quiet the moment you stop being needed.
	ReasonWorking
	// ReasonNotified is a workspace that finished a turn since you last looked.
	ReasonNotified
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
func (r Reason) String() string {
	switch r {
	case ReasonWaiting:
		return "waiting on you"
	case ReasonWorking:
		return "working"
	case ReasonNotified:
		return "finished a turn"
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
func (v View) Wants(it Item) Reason {
	return AgentWants(it.Status, it.Unread, it.Active)
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
	if workspace.IsWorking(status) {
		if !active {
			return ReasonNone
		}
		return ReasonWorking
	}
	// Not routed through workspace.Classify, and the divergence is deliberate
	// enough to be worth naming: Classify calls an errored-and-unread workspace
	// Notified, and this calls it nothing at all. Classify decides the badge
	// count and the dot's colour; this decides scope membership, and the two
	// have disagreed about "error" since before either was written down.
	// Preserved rather than quietly fixed here — see the note on ReasonNone in
	// the attention scope's task.
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "waiting":
		if unread {
			return ReasonWaiting
		}
		return ReasonNone
	case "exited", "error":
		return ReasonNone
	default:
		// idle, empty, and anything a reporter invents: the unread flag is the
		// whole signal. A quiet workspace you have already seen is asking nothing.
		if unread {
			return ReasonNotified
		}
		return ReasonNone
	}
}
