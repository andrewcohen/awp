// Package deckdata is the deck's read model: the pure logic that turns
// raw workspace rows + PR status into the filtered, sorted, and bucketed
// list the deck renders. It has no Bubble Tea / lipgloss dependency and
// does not import internal/deckui or internal/cli, so the selection logic
// can be unit-tested without a TUI and reused outside the deck.
//
// deckui keeps type/const aliases (Item, Scope, inboxBucket, …) and
// one-line delegating methods on its Model so its existing call sites
// compile unchanged; the logic lives here.
package deckdata

import (
	"strings"

	"github.com/andrewcohen/awp/internal/prstatus"
)

// Item is one deck row: a real workspace, or a synthetic inbox row for an
// open PR with no local workspace (Virtual). It is pure data — rendering
// lives in deckui.
type Item struct {
	ProjectName      string
	WorkspaceName    string
	Path             string
	RepoRoot         string
	Bookmark         string // jj bookmark associated with this workspace (still used for the new-workspace form, not for PR lookup)
	PRNumber         int    // PR this workspace is associated with; when > 0, used to resolve PR status
	Status           string
	Unread           bool
	PromptPreview    string
	HeadDesc         string
	HeadChangeID     string // jj short change-id of the working-copy commit
	BookmarkCommitID string // full hex commit-id of the workspace's local bookmark; compared to PR head SHA on GitHub to detect "behind remote" / re-review
	TmuxWindow       string
	SessionName      string
	Active           bool
	Current          bool
	// Virtual marks a synthetic inbox row that has no local workspace —
	// an open PR you haven't pulled down yet, either awaiting your review
	// (inboxVirtualReviewItems) or your own (inboxVirtualMineItems). It
	// resolves PR status via PRNumber and renders read-only: enter starts
	// the review flow for a review-requested PR, or opens the prefilled
	// new-workspace form for your own; other workspace actions are no-ops.
	Virtual bool
	// Optimistic marks a synthetic row shown the instant a create is
	// submitted, before the detached subprocess writes the workspace into
	// workspace-state.json. It bridges the gap until a refresh surfaces the
	// real row (see deckui Model.optimisticCreates); the row renders with
	// the "creating…" spinner treatment and its lifecycle actions are
	// blocked. Pure data — deckui owns the rendering and reconciliation.
	Optimistic bool
	// PinGroup is the register this workspace is pinned to (from
	// Entry.PinGroup): "" unpinned, "default" the gg register, or a
	// single lowercase letter a–z. Pinned rows float to a section at the
	// top of the deck in the All/Attention scopes.
	PinGroup string
	// StackDepth is the row's depth in its PR stack, set only by
	// View.Items in the inbox scope: 0 for a standalone PR or the base
	// (root) of a stack, 1+ for a PR stacked on another open PR. Drives
	// the render indent. Zero in every other scope.
	StackDepth int
	// SectionBucket is the inbox bucket this row sections under. For a
	// standalone PR it's the row's own bucket; for a PR in a stack it's
	// the whole stack's bucket (its most-actionable member) so the stack
	// stays contiguous under one header. Set only by View.Items (inbox).
	SectionBucket InboxBucket
	// StackBlocked is true when this PR has an open ancestor in its stack
	// that isn't ready to merge — it can't land until that ancestor does.
	// Set alongside StackDepth wherever stacks are annotated.
	StackBlocked bool
	// DevLoop is a compact snapshot of the agent's dev-loop progress
	// (todos done, current phase, in-progress unit), derived from the
	// agent's transcript by internal/watch. It is populated only for rows
	// whose agent is actively working and whose transcript yields real
	// progress; nil otherwise. When set, it replaces the port/branch meta
	// line — see deckui.Model.metaLine.
	DevLoop *DevLoopSummary
}

// DevLoopSummary is the row-sized projection of watch.State: just enough
// of the agent's dev-loop progress to render on a deck row's meta line.
// It is pure data — the full unit list, gate lights, and churn detail live
// in the `w` watch overlay (internal/watch.State).
type DevLoopSummary struct {
	Done  int    // completed todos / units
	Total int    // total todos / units (0 when the agent emitted no list)
	Phase string // current dev-loop phase (explore/implement/test/gates/commit)
	Task  string // the in-progress unit's content ("" when none is in progress)
}

// Scope controls which items are shown in the deck list. Cycled with `P`;
// not persisted unless an initial scope is supplied via `awp deck --scope`.
// Declaration order is the cycle order (all → attention → inbox → all).
type Scope int

const (
	ScopeAll       Scope = iota // every known workspace across all projects
	ScopeAttention              // matches the mini-deck filter: active agent or unread notification
	ScopeInbox                  // open-PR workspaces sectioned by next-move bucket (GitHub-inbox style)
)

// ScopeCount is the number of scopes, for the `P` cycle.
const ScopeCount = 3

// ParseScope maps the user-facing names accepted by `awp deck --scope`
// onto Scope values. Names are matched case-insensitively; hyphens and
// spaces are interchangeable. `pr` and the legacy `open-pr` are accepted
// as aliases for `inbox`.
func ParseScope(s string) (Scope, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, " ", "-"))) {
	case "all":
		return ScopeAll, true
	case "attention":
		return ScopeAttention, true
	case "inbox", "pr", "open-pr":
		return ScopeInbox, true
	}
	return ScopeAll, false
}

// InboxBucket sections the inbox scope the way GitHub's pull-request
// inbox does: by what the deck owner's next move is. Declaration order
// is render order — most urgent next-move first.
type InboxBucket int

const (
	InboxNeedsYourReview InboxBucket = iota // someone else's PR, your review is the blocker
	InboxNeedsAction                        // your PR, something to fix (feedback, CI, conflicts)
	InboxReadyToMerge                       // your PR, approved + green — go press the button
	InboxOtherOpen                          // open PR that's neither yours nor awaiting you
	InboxMine                               // your PR, ball in someone else's court — waiting for review or still a draft
	InboxBucketCount
)

// InboxBucketLabel is the header text for a bucket.
func InboxBucketLabel(b InboxBucket) string {
	switch b {
	case InboxNeedsYourReview:
		return "Needs your review"
	case InboxNeedsAction:
		return "Needs action"
	case InboxReadyToMerge:
		return "Ready to merge"
	case InboxOtherOpen:
		return "Other open PRs"
	default:
		return "Mine"
	}
}

// BucketFromHeaderLabel recovers the bucket from a header label like
// "Needs action (2)" — the count suffix is stripped and the base
// matched against InboxBucketLabel. Both sides go through the same
// label function, so the round-trip is exact.
func BucketFromHeaderLabel(label string) (InboxBucket, bool) {
	base := label
	if i := strings.LastIndex(label, " ("); i >= 0 {
		base = label[:i]
	}
	for b := InboxBucket(0); b < InboxBucketCount; b++ {
		if InboxBucketLabel(b) == base {
			return b, true
		}
	}
	return 0, false
}

// PinGroupDefault is the register key for the gg chord — the "default"
// pinned register. Other registers are single lowercase letters a–z.
const PinGroupDefault = "default"

// PRInboxBucket classifies an OPEN PR into its inbox section. Callers
// filter merged/closed PRs out of the inbox scope before classifying.
//
// Precedence, locked by tests: a review request always wins (it names
// you regardless of the PR's own state); within your own PRs the draft
// check precedes CI/decision checks — a draft isn't submitted for
// review yet, so its CI state is informational, not actionable, and it
// belongs in the bottom "Mine" pile rather than "Needs action".
// Anything of yours that isn't broken, ready, or a draft (i.e. waiting
// on reviewers) also lands in "Mine".
func PRInboxBucket(s prstatus.PRStatus) InboxBucket {
	if s.ReviewRequested || s.ReviewRerequested {
		return InboxNeedsYourReview
	}
	if !s.Mine {
		return InboxOtherOpen
	}
	if s.IsDraft {
		return InboxMine
	}
	if s.ReviewDecision == prstatus.PRReviewChangesRequested ||
		s.CIState == prstatus.PRCIFailing ||
		s.MergeStateStatus == prstatus.PRMergeStateDirty ||
		s.MergeStateStatus == prstatus.PRMergeStateBehind {
		return InboxNeedsAction
	}
	if s.IsInMergeQueue ||
		(s.ReviewDecision == prstatus.PRReviewApproved &&
			(s.CIState == prstatus.PRCIPassing || s.CIState == prstatus.PRCINone) &&
			s.MergeStateStatus == prstatus.PRMergeStateClean) {
		return InboxReadyToMerge
	}
	return InboxMine
}

// PinnedCount returns how many leading items in a pinned-first ordering
// carry a register. View.Items sorts pinned rows ahead of unpinned ones
// in the all / attention scopes, so this is the length of that prefix.
func PinnedCount(items []Item) int {
	n := 0
	for _, it := range items {
		if strings.TrimSpace(it.PinGroup) != "" {
			n++
		}
	}
	return n
}

// PinGroupLabel is the display label for a register: its alias when one
// is set, otherwise "pinned" for the default register or the bare letter
// for a lettered register.
func PinGroupLabel(aliases map[string]string, key string) string {
	if alias := strings.TrimSpace(aliases[key]); alias != "" {
		return alias
	}
	if key == PinGroupDefault {
		return "pinned"
	}
	return key
}

// PinGroupSortKey orders registers: the default register first, then the
// rest case-insensitively by display label (alias or letter).
func PinGroupSortKey(aliases map[string]string, key string) string {
	if key == PinGroupDefault {
		return "\x00"
	}
	return "\x01" + strings.ToLower(PinGroupLabel(aliases, key))
}

// repoBaseName returns the last path segment of a repo root, used as a
// fallback project label for a virtual row when no sibling workspace
// supplies one.
func repoBaseName(repo string) string {
	repo = strings.TrimRight(repo, "/")
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}
