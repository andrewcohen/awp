package deckdata

import (
	"fmt"
	"sort"
	"strings"

	"github.com/andrewcohen/awp/internal/prstatus"
)

// stackGraph is the PR-stack forest over a set of inbox/deck items: which
// item each item is stacked on (parent), the reverse (children), each
// item's depth from its stack root, the root itself, and whether the item
// is blocked by an ancestor that isn't ready to merge. Index space is the
// input items slice.
type stackGraph struct {
	status   []prstatus.PRStatus
	hasPR    []bool
	parent   []int   // parent[i] = item i is stacked on, or -1
	children [][]int // children[i] = items stacked directly on i
	depth    []int   // 0 for a standalone PR or a stack root, 1+ otherwise
	rootOf   []int   // topmost ancestor of i (i itself if a root)
	blocked  []bool  // true when some open ancestor isn't ready to merge
}

// buildStackGraph derives the stack forest from item PR base/head refs. A
// stack edge exists when one item's PR base branch equals another item's
// PR head branch, both within the same repo and both open (only visible
// PRs stack). Every node has at most one parent, so this is a forest;
// pathological base cycles are broken by a visited guard.
func (v View) buildStackGraph(items []Item) stackGraph {
	n := len(items)
	g := stackGraph{
		status:   make([]prstatus.PRStatus, n),
		hasPR:    make([]bool, n),
		parent:   make([]int, n),
		children: make([][]int, n),
		depth:    make([]int, n),
		rootOf:   make([]int, n),
		blocked:  make([]bool, n),
	}
	headIdx := map[string]int{}
	key := func(repo, ref string) string { return repo + "\x00" + ref }
	for i, it := range items {
		st, ok := v.OpenPRStatus(it)
		g.status[i], g.hasPR[i] = st, ok
		if ok {
			headIdx[key(it.RepoRoot, st.HeadRefName)] = i
		}
	}
	for i := range g.parent {
		g.parent[i] = -1
	}
	for i, it := range items {
		if !g.hasPR[i] {
			continue
		}
		base := strings.TrimSpace(g.status[i].BaseRefName)
		if base == "" {
			continue
		}
		if j, ok := headIdx[key(it.RepoRoot, base)]; ok && j != i {
			g.parent[i] = j
			g.children[j] = append(g.children[j], i)
		}
	}
	for i := range items {
		// depth + root: walk parents with a visited guard.
		d, r, seen, cur := 0, i, map[int]bool{i: true}, g.parent[i]
		for cur != -1 && !seen[cur] {
			d++
			r = cur
			seen[cur] = true
			// Blocked: any open ancestor that isn't ready to merge stalls
			// this PR — you can't land it until that ancestor lands.
			if g.hasPR[cur] && PRInboxBucket(g.status[cur]) != InboxReadyToMerge {
				g.blocked[i] = true
			}
			cur = g.parent[cur]
		}
		g.depth[i] = d
		g.rootOf[i] = r
	}
	return g
}

// stackOrderedInbox annotates and reorders the inbox scope's items so PR
// stacks read as cohesive, visually indented units while still sectioning
// under the state buckets.
//
// Each stack is treated atomically: it sections under the bucket of its
// most-actionable member (the lowest InboxBucket value), and its members
// render contiguously root → tip with StackDepth driving the indent.
// Standalone PRs are stacks of one and keep their own bucket, so the
// ordering degrades to the old bucket sort when nothing is stacked.
func (v View) stackOrderedInbox(items []Item) []Item {
	n := len(items)
	if n == 0 {
		return items
	}
	g := v.buildStackGraph(items)

	label := func(i int) string { return strings.ToLower(v.DisplayLabel(items[i])) }

	// A stack sections under its most-actionable member (lowest bucket).
	stackBucket := map[int]InboxBucket{}
	for i := range items {
		b := InboxOtherOpen
		if g.hasPR[i] {
			b = PRInboxBucket(g.status[i])
		}
		r := g.rootOf[i]
		if cur, ok := stackBucket[r]; !ok || b < cur {
			stackBucket[r] = b
		}
	}
	sectionBucket := make([]InboxBucket, n)
	for i := range items {
		sectionBucket[i] = stackBucket[g.rootOf[i]]
	}

	order := v.orderStackUnits(items, g, func(root int) string {
		// Section by bucket; within "Needs your review", re-reviews first.
		rerank := 1
		if sectionBucket[root] == InboxNeedsYourReview && v.NeedsReReview(items[root]) {
			rerank = 0
		}
		return fmt.Sprintf("%d%d", sectionBucket[root], rerank)
	}, label)

	out := make([]Item, 0, n)
	for _, i := range order {
		it := items[i]
		it.StackDepth = g.depth[i]
		it.StackBlocked = g.blocked[i]
		it.SectionBucket = sectionBucket[i]
		out = append(out, it)
	}
	return out
}

// stackOrderedDeck lays out the all / attention scopes: pinned rows float
// to the top (register order), unpinned rows group by project, and PR
// stacks stay contiguous root → tip with StackDepth/StackBlocked
// annotated. Crucially, a pin drags the whole stack: if any member of a
// stack is pinned, the entire stack is lifted into that register (the
// unpinned members ride along, their PinGroup set to the stack's register)
// so the chain never splits across the pinned/project regions. When
// nothing is stacked this degrades to the old pinned-first, then
// (project, label) order.
//
// The result is pinned-first (all dragged/real pinned rows form the
// prefix), which deckui's pinnedCount + deckBodyRowsPinned rely on.
func (v View) stackOrderedDeck(items []Item) []Item {
	n := len(items)
	if n == 0 {
		return items
	}
	g := v.buildStackGraph(items)
	label := func(i int) string { return strings.ToLower(v.DisplayLabel(items[i])) }

	// Each stack's register: a pinned member drags its stack into that
	// register. With several pinned members, the lowest PinGroupSortKey
	// (default before lettered, then alphabetical) wins.
	rootReg := map[int]string{}
	for i := range items {
		pg := strings.TrimSpace(items[i].PinGroup)
		if pg == "" {
			continue
		}
		r := g.rootOf[i]
		if cur, ok := rootReg[r]; !ok || PinGroupSortKey(v.PinAliases, pg) < PinGroupSortKey(v.PinAliases, cur) {
			rootReg[r] = pg
		}
	}

	order := v.orderStackUnits(items, g, func(root int) string {
		// "0"+register sorts pinned stacks first (by register); "1" puts
		// unpinned stacks after, ordered by the (project, label) fallback.
		if reg, ok := rootReg[root]; ok {
			return "0" + PinGroupSortKey(v.PinAliases, reg)
		}
		return "1"
	}, label)

	out := make([]Item, 0, n)
	for _, i := range order {
		it := items[i]
		it.StackDepth = g.depth[i]
		it.StackBlocked = g.blocked[i]
		if reg, ok := rootReg[g.rootOf[i]]; ok {
			it.PinGroup = reg // drag unpinned members into the stack's register
		}
		out = append(out, it)
	}
	return out
}

// orderStackUnits flattens the stack forest into a render order: one unit
// per stack root, units sorted by (primary, project, root label), and each
// unit's members emitted root-first (children by label) so a stack stays
// contiguous and indentation reads top-down. primary is the caller's
// per-root primary sort key (bucket+rerank for the inbox, register/pin
// bucketing for the deck); pass a constant for a pure project sort.
func (v View) orderStackUnits(items []Item, g stackGraph, primary func(root int) string, label func(int) string) []int {
	var emit func(i int, out *[]int)
	emit = func(i int, out *[]int) {
		*out = append(*out, i)
		kids := append([]int(nil), g.children[i]...)
		sort.SliceStable(kids, func(a, b int) bool { return label(kids[a]) < label(kids[b]) })
		for _, c := range kids {
			emit(c, out)
		}
	}
	type unit struct {
		primary string
		project string
		label   string
		order   []int
	}
	var units []unit
	for i := range items {
		if g.parent[i] != -1 {
			continue // only roots seed a unit
		}
		var o []int
		emit(i, &o)
		units = append(units, unit{
			primary: primary(i),
			project: items[i].ProjectName,
			label:   label(i),
			order:   o,
		})
	}
	sort.SliceStable(units, func(a, b int) bool {
		if units[a].primary != units[b].primary {
			return units[a].primary < units[b].primary
		}
		if units[a].project != units[b].project {
			return units[a].project < units[b].project
		}
		return units[a].label < units[b].label
	})
	out := make([]int, 0, len(items))
	for _, u := range units {
		out = append(out, u.order...)
	}
	return out
}
