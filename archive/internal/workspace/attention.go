package workspace

import "strings"

// What a workspace wants from you, from its status and unread flag.
//
// One function because the answer is claimed on three surfaces that have to
// agree: the tmux status badge (`awp internal unread-summary`), the deck's
// status dot, and the deck's title-row summary. Before this it was written out
// once per surface, and internal/cli/unread.go's own comment admitted as much
// — "Mirrors deckui.alwaysShownStatus so the badge's green count matches" — a
// note that only exists because nothing enforced it. Two of those copies
// disagreeing is not a crash, it is a badge that says 2 while the deck shows 3,
// which reads as a bug in whichever one you happened to look at second.

// Attention is what a workspace is asking of you.
type Attention int

const (
	// AttentionNone is a workspace with nothing to act on: idle and already
	// read, or exited.
	AttentionNone Attention = iota
	// AttentionWorking is an agent running right now. Informational rather
	// than actionable, but it is why the deck stays lit while work is in
	// flight instead of going quiet the moment you stop being needed.
	AttentionWorking
	// AttentionWaiting is an agent that asked a question and stopped. The
	// only bucket where you are the blocker.
	AttentionWaiting
	// AttentionNotified is a workspace that finished a turn since you last
	// looked at it.
	AttentionNotified
)

// Classify sorts a workspace into exactly one bucket.
//
// The ordering is the part worth stating, because each step of it was a bug at
// some point:
//
//   - Working wins outright, and is not gated on the unread flag. A workspace
//     that resumed work still carrying a stale unread flag from an earlier
//     waiting turn is working, not also notified — otherwise it is counted
//     twice and the totals exceed the number of workspaces.
//   - Exited never counts. The agent is gone, so there is nothing to respond
//     to, and old state files may still carry an unread flag from the turn
//     before the process died.
//   - Everything else needs the unread flag. An idle workspace you have
//     already looked at is asking nothing.
//   - Only then does waiting split from notified, so "waiting" means the agent
//     is actually blocked on you rather than merely idle-and-unseen.
func Classify(status string, unread bool) Attention {
	switch {
	case IsWorking(status):
		return AttentionWorking
	case IsExited(status):
		return AttentionNone
	case !unread:
		return AttentionNone
	case strings.EqualFold(strings.TrimSpace(status), "waiting"):
		return AttentionWaiting
	default:
		return AttentionNotified
	}
}

// IsWorking reports whether a status means the agent is actively doing work.
//
// The vocabulary is wider than "working" because the status is written by
// whatever reports it — agent hooks, tmux enrichment, the report-status
// command — and those have never agreed on a single spelling.
func IsWorking(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "in progress", "in_progress", "running":
		return true
	default:
		return false
	}
}
