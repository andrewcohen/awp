package deckdata

import (
	"fmt"
	"strings"

	"github.com/andrewcohen/awp/internal/prstatus"
)

// View is the read model's input: the raw rows plus the lookup tables the
// selection logic joins against. It is a cheap value (maps and slices are
// reference types), so callers rebuild it per query rather than caching a
// derived list.
type View struct {
	// All is every known workspace row across all projects, unfiltered.
	All []Item
	// Scope selects which rows Items returns and how they sort.
	Scope Scope
	// Filter is the case-insensitive substring applied to workspace name,
	// project name, and display label. Empty = no filter.
	Filter string
	// PRStatusByRepo is the PR-status cache: repoRoot → headRefName → status.
	PRStatusByRepo map[string]map[string]prstatus.PRStatus
	// PinAliases is the register → display-alias map used for pin sorting.
	PinAliases map[string]string
	// Attention decides whether a row belongs to the attention scope. It
	// is injected because the underlying rule (mini-deck inclusion) lives
	// in deckui alongside the status vocabulary. When nil, the attention
	// scope shows nothing.
	Attention func(status string, unread, active bool) bool
}

// Items applies the scope filter (plus the inbox virtual rows), the text
// filter, and the scope-appropriate sort, returning the visible rows in
// render order.
func (v View) Items() []Item {
	src := v.All
	switch v.Scope {
	case ScopeInbox:
		filtered := make([]Item, 0, len(src))
		for _, it := range src {
			if _, ok := v.OpenPRStatus(it); ok {
				filtered = append(filtered, it)
			}
		}
		// Surface review-requested PRs you haven't checked out yet as
		// synthetic read-only rows, so "Needs your review" isn't limited
		// to PRs that already have a local workspace.
		filtered = append(filtered, v.inboxVirtualReviewItems(filtered)...)
		// Likewise surface your own open PRs that have no local workspace
		// so the Mine / Needs action / Ready to merge buckets aren't
		// limited to PRs you happen to have checked out. Passing the
		// review virtuals in too dedups against them.
		filtered = append(filtered, v.inboxVirtualMineItems(filtered)...)
		// Complete any partially-shown stack: pull in the open PRs that
		// connect the rows above (ancestors + descendants) even when they're
		// not yours and not awaiting your review, so a stack never renders
		// with a hole in the chain. Runs last so it only fills real gaps.
		filtered = append(filtered, v.inboxVirtualStackItems(filtered)...)
		src = filtered
	case ScopeAttention:
		filtered := make([]Item, 0, len(src))
		for _, it := range src {
			// Always keep the current workspace (the one the deck was opened
			// from), even if it doesn't otherwise qualify — otherwise the
			// cursor can't land on it and selection jitters to another row
			// after the first tmux refresh settles.
			if it.Current || (v.Attention != nil && v.Attention(it.Status, it.Unread, it.Active)) {
				filtered = append(filtered, it)
			}
		}
		src = filtered
	}
	f := strings.ToLower(strings.TrimSpace(v.Filter))
	if f != "" {
		out := make([]Item, 0, len(src))
		for _, it := range src {
			if strings.Contains(strings.ToLower(it.WorkspaceName), f) ||
				strings.Contains(strings.ToLower(it.ProjectName), f) ||
				strings.Contains(strings.ToLower(v.DisplayLabel(it)), f) {
				out = append(out, it)
			}
		}
		src = out
	}
	// Sort by (project, displayed label) so rows alphabetize by what the
	// user actually sees — PR title when one is resolved from the cache,
	// workspace name otherwise. Stable sort preserves the upstream
	// ordering for ties. The inbox scope sorts by bucket first so rows
	// section under the bucket headers in next-move order.
	sorted := append([]Item(nil), src...)
	if v.Scope == ScopeInbox {
		// Stack layout owns the inbox ordering: it sections by bucket (a
		// stack sections under its most-actionable member), surfaces
		// re-reviews first within "Needs your review", and orders by
		// project + label — the same order the old bucket sort produced
		// when nothing is stacked — while keeping each stack's members
		// contiguous and depth-annotated for the render indent.
		return v.stackOrderedInbox(sorted)
	}
	// All / attention scopes: pinned rows float to the top (register order),
	// unpinned rows group by project, and PR stacks stay contiguous root →
	// tip. A pin drags its whole stack into the register so the chain never
	// splits across the pinned/project regions. The result is pinned-first,
	// which deckui's pinnedCount + deckBodyRowsPinned rely on.
	return v.stackOrderedDeck(sorted)
}

// ResolvePRStatus finds the PR status for an item: by pinned PRNumber
// when set, otherwise by a bookmark → headRefName lookup (a compat path
// for workspaces created before the PRNumber migration).
func (v View) ResolvePRStatus(item Item) (prstatus.PRStatus, bool) {
	repo := strings.TrimSpace(item.RepoRoot)
	if repo == "" {
		return prstatus.PRStatus{}, false
	}
	byHead, ok := v.PRStatusByRepo[repo]
	if !ok {
		return prstatus.PRStatus{}, false
	}
	if item.PRNumber > 0 {
		for _, s := range byHead {
			if s.Number == item.PRNumber {
				return s, true
			}
		}
		return prstatus.PRStatus{}, false
	}
	if bm := strings.TrimSpace(item.Bookmark); bm != "" {
		if s, ok := byHead[bm]; ok {
			return s, true
		}
	}
	return prstatus.PRStatus{}, false
}

// OpenPRStatus returns the item's PR status only when the PR is open;
// closed/merged PRs are filtered out of the inbox scope.
func (v View) OpenPRStatus(it Item) (prstatus.PRStatus, bool) {
	st, ok := v.ResolvePRStatus(it)
	if !ok || st.State != prstatus.PRStateOpen {
		return prstatus.PRStatus{}, false
	}
	return st, true
}

// InboxBucket classifies a workspace for the inbox scope. Items the scope
// filter would exclude (no open PR resolvable) land in the catch-all
// bucket; the filter runs first, so that's defensive only.
func (v View) InboxBucket(it Item) InboxBucket {
	st, ok := v.OpenPRStatus(it)
	if !ok {
		return InboxOtherOpen
	}
	return PRInboxBucket(st)
}

// NeedsReReview reports whether the row is a re-request: you reviewed the
// PR before and the author pushed and asked again. Used to sort these to
// the top of the "Needs your review" bucket.
func (v View) NeedsReReview(it Item) bool {
	st, ok := v.OpenPRStatus(it)
	return ok && st.ReviewRerequested
}

// DisplayLabel returns the text that renders on a row: "#N title" when a
// PR is resolvable from the cache, falling back to the workspace name.
func (v View) DisplayLabel(it Item) string {
	if pr, ok := v.ResolvePRStatus(it); ok {
		if t := strings.TrimSpace(pr.Title); t != "" {
			return fmt.Sprintf("#%d %s", pr.Number, t)
		}
	}
	return it.WorkspaceName
}

// inboxVirtualReviewItems synthesizes read-only inbox rows for
// review-requested PRs that have no local workspace, so "Needs your
// review" surfaces PRs you haven't pulled down yet. The PR status cache
// only holds repos where you already have at least one workspace, so a
// virtual row is always a not-yet-checked-out PR in a repo you work in;
// its project name is borrowed from a sibling workspace in that repo.
//
// real is the inbox scope's already-filtered workspace rows; PRs they
// resolve to are skipped so a checked-out PR never doubles up. Each
// virtual Item resolves its status via PRNumber (no bookmark on file)
// and carries the PR head ref so the meta line can show the branch.
func (v View) inboxVirtualReviewItems(real []Item) []Item {
	// PRs already represented by a real workspace row, by repo → PR#.
	seen := map[string]map[int]bool{}
	for _, it := range real {
		if st, ok := v.ResolvePRStatus(it); ok {
			if seen[it.RepoRoot] == nil {
				seen[it.RepoRoot] = map[int]bool{}
			}
			seen[it.RepoRoot][st.Number] = true
		}
	}
	projectByRepo := map[string]string{}
	for _, it := range v.All {
		if it.RepoRoot != "" && projectByRepo[it.RepoRoot] == "" {
			projectByRepo[it.RepoRoot] = it.ProjectName
		}
	}
	var out []Item
	for repo, byHead := range v.PRStatusByRepo {
		for _, st := range byHead {
			if st.State != prstatus.PRStateOpen {
				continue
			}
			if !st.ReviewRequested && !st.ReviewRerequested {
				continue
			}
			if seen[repo][st.Number] {
				continue
			}
			project := projectByRepo[repo]
			if project == "" {
				project = repoBaseName(repo)
			}
			out = append(out, Item{
				ProjectName:   project,
				WorkspaceName: fmt.Sprintf("#%d", st.Number),
				RepoRoot:      repo,
				PRNumber:      st.Number,
				Bookmark:      st.HeadRefName, // drives the branch token on the meta line
				Virtual:       true,
			})
		}
	}
	return out
}

// inboxVirtualMineItems synthesizes read-only inbox rows for your own
// open PRs that have no local workspace yet — the authored-by-you
// counterpart to inboxVirtualReviewItems. Without it, the Mine / Needs
// action / Ready to merge buckets only show PRs you happen to have
// checked out; a PR you opened from another machine (or whose workspace
// you deleted) would silently vanish from your inbox.
//
// Review-requested PRs are intentionally skipped here — inboxVirtualReviewItems
// already covers them (you can't request review from yourself, so this is
// belt-and-suspenders). PRInboxBucket later sorts each row into its
// section by PR state. existing should be the real workspace rows plus
// the review virtuals so we dedup against both, by repo → PR#.
func (v View) inboxVirtualMineItems(existing []Item) []Item {
	seen := map[string]map[int]bool{}
	for _, it := range existing {
		if st, ok := v.ResolvePRStatus(it); ok {
			if seen[it.RepoRoot] == nil {
				seen[it.RepoRoot] = map[int]bool{}
			}
			seen[it.RepoRoot][st.Number] = true
		}
	}
	projectByRepo := map[string]string{}
	for _, it := range v.All {
		if it.RepoRoot != "" && projectByRepo[it.RepoRoot] == "" {
			projectByRepo[it.RepoRoot] = it.ProjectName
		}
	}
	var out []Item
	for repo, byHead := range v.PRStatusByRepo {
		for _, st := range byHead {
			if st.State != prstatus.PRStateOpen {
				continue
			}
			if !st.Mine {
				continue
			}
			if st.ReviewRequested || st.ReviewRerequested {
				continue // covered by inboxVirtualReviewItems
			}
			if seen[repo][st.Number] {
				continue
			}
			project := projectByRepo[repo]
			if project == "" {
				project = repoBaseName(repo)
			}
			out = append(out, Item{
				ProjectName:   project,
				WorkspaceName: fmt.Sprintf("#%d", st.Number),
				RepoRoot:      repo,
				PRNumber:      st.Number,
				Bookmark:      st.HeadRefName, // drives the branch token on the meta line
				Virtual:       true,
			})
		}
	}
	return out
}

// inboxVirtualStackItems completes partially-shown PR stacks. Given the
// rows already in the inbox (real workspaces + review/mine virtuals), it
// walks the base/head graph over each repo's open PRs — both directions,
// ancestors and descendants — from the already-visible heads, and
// synthesizes a read-only virtual row for every connected stack member
// that isn't shown yet, regardless of ownership. Without this a stack
// whose middle (or base) link is someone else's PR would render with a
// gap, breaking the visual chain.
//
// existing must be the rows already assembled (workspaces + review + mine
// virtuals) so we seed from and dedup against them, by repo → PR#.
func (v View) inboxVirtualStackItems(existing []Item) []Item {
	visibleHead := map[string]map[string]bool{}
	seenNum := map[string]map[int]bool{}
	for _, it := range existing {
		st, ok := v.ResolvePRStatus(it)
		if !ok {
			continue
		}
		if visibleHead[it.RepoRoot] == nil {
			visibleHead[it.RepoRoot] = map[string]bool{}
			seenNum[it.RepoRoot] = map[int]bool{}
		}
		visibleHead[it.RepoRoot][st.HeadRefName] = true
		seenNum[it.RepoRoot][st.Number] = true
	}
	projectByRepo := map[string]string{}
	for _, it := range v.All {
		if it.RepoRoot != "" && projectByRepo[it.RepoRoot] == "" {
			projectByRepo[it.RepoRoot] = it.ProjectName
		}
	}
	var out []Item
	for repo, byHead := range v.PRStatusByRepo {
		heads := visibleHead[repo]
		if len(heads) == 0 {
			continue // no visible PR in this repo → no stack to complete
		}
		// Index descendants: base branch → heads of the PRs stacked on it.
		childrenOf := map[string][]string{}
		for h, st := range byHead {
			if st.State == prstatus.PRStateOpen {
				childrenOf[st.BaseRefName] = append(childrenOf[st.BaseRefName], h)
			}
		}
		// BFS both directions (ancestor via base, descendants via childrenOf)
		// from every visible head to reach the whole connected stack.
		seen := make(map[string]bool, len(heads))
		queue := make([]string, 0, len(heads))
		for h := range heads {
			seen[h] = true
			queue = append(queue, h)
		}
		for len(queue) > 0 {
			h := queue[0]
			queue = queue[1:]
			st, ok := byHead[h]
			if !ok || st.State != prstatus.PRStateOpen {
				continue
			}
			if base := st.BaseRefName; base != "" && !seen[base] {
				if anc, ok := byHead[base]; ok && anc.State == prstatus.PRStateOpen {
					seen[base] = true
					queue = append(queue, base)
				}
			}
			for _, c := range childrenOf[h] {
				if !seen[c] {
					seen[c] = true
					queue = append(queue, c)
				}
			}
		}
		for h := range seen {
			if heads[h] {
				continue // already visible
			}
			st := byHead[h]
			if st.State != prstatus.PRStateOpen || seenNum[repo][st.Number] {
				continue
			}
			project := projectByRepo[repo]
			if project == "" {
				project = repoBaseName(repo)
			}
			out = append(out, Item{
				ProjectName:   project,
				WorkspaceName: fmt.Sprintf("#%d", st.Number),
				RepoRoot:      repo,
				PRNumber:      st.Number,
				Bookmark:      st.HeadRefName,
				Virtual:       true,
			})
		}
	}
	return out
}
