package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/review"
)

// The comment index: the bottom of the left column, listing every conversation
// in the change so reaching one is a jump rather than a scroll.
//
// It is derived from the *placed* stream, not from the comment set. Where a
// comment sits is the result of relocating its anchor against the current diff
// (see comments.go), so building the index from the same placement is what
// guarantees the two agree — and that a comment whose anchor no longer resolves
// is listed as detached rather than at a line it does not occupy any more.

// minFileListHeight is how short the file list may get to make room for the
// index: a header plus two rows. Below that the list stops being usable, and
// the file list is the primary index — a long conversation must not crowd it
// out.
const minFileListHeight = 3

// commentEntry is one row of the index: a conversation, not a message. Replies
// fold into their parent's entry as a count, because jumping to a reply and
// jumping to the remark it answers are the same jump.
type commentEntry struct {
	id string
	// row is the stream row the conversation starts at — what selecting this
	// entry seeks to.
	row  int
	path string
	// lines is the anchor's line or line range as text — "12", or "12-18" for a
	// comment covering a block. Rendered at build time from review.Anchor.LineRange
	// so the index spells a location exactly the way the compose box, the agent
	// prompt and the publish log do.
	lines   string
	author  string
	kind    review.Kind
	summary string
	replies int
	state   review.State
	// detached marks a conversation whose anchor could not be located, so it
	// lives in the stream's trailing section instead of beside code.
	detached bool
	// changeWide marks a review-level conversation — about the change as a whole,
	// anchored to no file — which leads the stream instead of sitting in it.
	changeWide bool
	// outdated marks a mirrored GitHub thread whose line no longer exists in the
	// diff. GitHub's own word for it, and the reason such a thread is detached —
	// so it is worth more than the generic "anchor could not be found".
	outdated bool
	// proposal is the state of a proposal anywhere in the conversation, empty when
	// there is none. On the parent's row because the index lists conversations and
	// folds replies into a count — and a proposal is always a reply, so a state
	// carried only by the message that holds it would never be listed at all.
	//
	// Pending wins over approved when a conversation holds both: what the list is
	// for is finding what is waiting on you, and one settled proposal does not
	// settle the exchange.
	proposal review.Proposal
}

// commentEntries walks the placed rows in stream order and returns one entry
// per conversation. Stream order rather than sorted, so the index reads top to
// bottom the same way the diff does.
//
// A Model method rather than a streamIndex one because a remote conversation's
// state lives on the thread, not on the comment the stream adapted it into.
func (m Model) commentEntries(idx streamIndex) []commentEntry {
	var out []commentEntry
	// slot maps a conversation's id to its entry, so a reply can find its
	// parent and be counted rather than listed.
	slot := make(map[string]int, len(idx.comments))
	for row, r := range idx.rows {
		if !isCommentRow(r.kind) {
			continue
		}
		// One entry per comment, anchored at its first display row.
		if r.commentLine != 0 || r.comment < 0 || r.comment >= len(idx.comments) {
			continue
		}
		c := idx.comments[r.comment]
		if c.ReplyTo != "" {
			if at, ok := slot[c.ReplyTo]; ok {
				out[at].replies++
				out[at].proposal = strongerProposal(out[at].proposal, c.Proposal)
				continue
			}
			// A reply whose parent is not in the stream is a conversation in its
			// own right as far as the reader is concerned — listing it is the
			// only way to reach it.
		}
		slot[c.ID] = len(out)
		e := commentEntry{
			id:         c.ID,
			row:        row,
			path:       c.Anchor.Path,
			lines:      c.Anchor.LineRange(),
			author:     c.Author,
			kind:       c.Kind.OrDefault(),
			summary:    entrySummary(c),
			state:      c.State,
			detached:   r.kind == rowOrphan,
			changeWide: r.kind == rowReview,
			// A proposal is a reply, so this is normally set by the fold above. It is
			// read off the parent too for the one case that is not: a reply whose
			// parent is not in the stream is listed as a conversation in its own
			// right, and it would otherwise be the only proposal the list omits.
			proposal: c.Proposal,
		}
		if t, ok := m.threadFor(c.ID); ok {
			e.outdated = t.Outdated
		}
		out = append(out, e)
	}
	return out
}

// strongerProposal is which of two proposal states an index row should report.
//
// A conversation can hold several — an agent that proposed twice, or proposed
// again after you replied — and the row has one slot. Pending outranks approved
// outranks none, because the list is read to find what is waiting on you and a
// settled proposal beside a live one does not settle the exchange.
func strongerProposal(a, b review.Proposal) review.Proposal {
	if a == review.ProposalPending || b == review.ProposalPending {
		return review.ProposalPending
	}
	if a == review.ProposalApproved || b == review.ProposalApproved {
		return review.ProposalApproved
	}
	return ""
}

// entrySummary is an index row's one-line body preview, carrying the robot
// marker so a glance down the list shows which conversations an agent started
// rather than requiring you to open each one.
func entrySummary(c review.Comment) string {
	summary := firstLine(c.Body)
	if robotAuthored(c) && summary != "" {
		return review.RobotMarker + " " + summary
	}
	return summary
}

// firstLine is the first line of a body with anything to show, for the index's
// one-line summary. Skipping leading blanks matters because a comment written
// in an editor often starts with one.
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// commentPaneHeight is how many rows the index gets: enough for its entries,
// capped at half the column, and never at the cost of shortening the file list
// past usability. Zero means it is not shown at all.
//
// height is the whole left column's budget, less the two rows a second border
// costs — stacking two bordered panes must leave the column the same overall
// height as the hunk pane beside it.
// hidden is how many conversations the visibility setting is holding back, which
// earns the pane its header even with nothing to list: "0 listed, 3 hidden" is the
// difference between a change nobody has commented on and one whose conversation is
// entirely off screen, and those must not look the same.
func commentPaneHeight(entries, hidden, height int) int {
	if entries <= 0 && hidden <= 0 {
		return 0
	}
	room := min(height/2, height-2-minFileListHeight)
	if room < 2 { // a header and at least one entry
		return 0
	}
	if entries <= 0 {
		// Header only. It is a notice, not a list — there is nothing to select.
		return 2
	}
	return min(entries+1, room)
}

// commentPaneVisible reports whether the index is a keyboard target — which is what
// decides if it belongs in the tab rotation. Tabbing to a pane that isn't rendered
// would strand the keyboard, and so would tabbing to one with nothing in it.
//
// So this is not "is the pane drawn": the pane also appears carrying only a
// hidden-conversation notice, and that has no rows to select. Drawn and selectable
// are different questions with different answers.
func (m Model) commentPaneVisible() bool {
	return !m.hideLeft && len(m.commentIndex) > 0 &&
		commentPaneHeight(len(m.commentIndex), m.hiddenThreads(), m.bodyHeight) > 0
}

// renderLeftColumn stacks the file list over the comment index.
//
// Cached as one string between frames: it changes only when a selection moves,
// focus shifts, or the column is resized, and rebuilding it costs a lipgloss
// Render per row — which was half the allocation in a frame while scrolling the
// diff, where this column does not change at all.
func (m Model) renderLeftColumn(width, height int) string {
	if m.cache == nil {
		return m.buildLeftColumn(width, height)
	}
	key := leftKey{
		width: width, height: height,
		files: m.filesCursor, comments: m.commentsCursor,
		focus: m.focus, entries: len(m.commentIndex), hidden: m.hideLeft,
		hiddenThreads: m.hiddenThreads(),
	}
	if m.cache.left.ok && m.cache.left.key == key {
		return m.cache.left.out
	}
	out := m.buildLeftColumn(width, height)
	m.cache.left.key = key
	m.cache.left.out = out
	m.cache.left.ok = true
	return out
}

func (m Model) buildLeftColumn(width, height int) string {
	h := commentPaneHeight(len(m.commentIndex), m.hiddenThreads(), height)
	if h <= 0 {
		return m.renderFileList(width, height)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderFileList(width, height-2-h),
		m.renderCommentList(width, h),
	)
}

// commentListHeader is the index's title: how many conversations it lists, and how
// many the visibility setting is holding back.
//
// The second half is the whole point of this function existing. `T` is what changes
// it and the key is not discoverable from a list that simply lacks the rows — so the
// list says what it is not showing, and names the key that shows it. Without that, a
// thread wrongly marked resolved is indistinguishable from a thread that was never
// there.
func commentListHeader(listed, hidden int) string {
	if hidden <= 0 {
		return fmt.Sprintf(" Comments (%d)", listed)
	}
	return fmt.Sprintf(" Comments (%d) · %d hidden · T", listed, hidden)
}

func (m Model) renderCommentList(width, height int) string {
	border := styleNormalBorder
	if m.focus == FocusComments {
		border = styleFocusBorder
	}
	rows := []string{styleDim.Render(truncate(commentListHeader(len(m.commentIndex), m.hiddenThreads()), width-4))}
	start, end := visibleRange(m.commentsCursor, max(1, height-1), len(m.commentIndex))
	contentWidth := width - 4
	for i := start; i < end; i++ {
		selected := i == m.commentsCursor
		// Painted only with the keyboard here, as in the file list and the diff.
		band := selected && m.focus == FocusComments
		row := renderCommentEntry(m.commentIndex[i], contentWidth, selected, band)
		if band {
			row = bandRow(row, width-2)
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return border.Width(width - 2).Height(height).Render(strings.Join(rows, "\n"))
}

// commentEntryStyles picks an index row's hues: one for the location, one for
// the summary text.
//
// Keyed off the kind the same way the blocks in the stream are, so a conversation
// is the same colour in the index as it is in the diff. Factored out for the same
// reason commentStyles is — lipgloss strips colour with no TTY, so the choice
// cannot be observed in rendered output.
func commentEntryStyles(kind review.Kind, selected, band bool) (loc, text lipgloss.Style) {
	switch {
	case band:
		return styleSelectedCursor, styleSelectedCursor
	case selected:
		// Selection wins over kind: the app-wide marker has to read as the
		// selection wherever it lands.
		return styleSelected, styleSelected
	}
	return kindStyles(kind), styleMuted
}

func renderCommentEntry(e commentEntry, width int, selected, band bool) string {
	// The `┃ ` bar is the app-wide selection marker; unselected rows reserve the
	// same columns so labels line up down the list.
	prefix := selectionPrefixBlank
	switch {
	case band:
		prefix = styleSelectedCursor.Render(selectionPrefixBar)
	case selected:
		prefix = styleSelected.Render(selectionPrefixBar)
	}
	loc, text := commentEntryStyles(e.kind, selected, band)

	head := entryLocation(e)
	avail := max(1, width-lipgloss.Width(selectionPrefixBlank))
	head = truncate(head, avail)
	out := prefix + loc.Render(head)
	// The summary only gets whatever the location left, so a deep path can't
	// push the row past the pane.
	if rest := avail - lipgloss.Width(head) - 1; rest > 0 && e.summary != "" {
		out += gap(band) + text.Render(truncate(e.summary, rest))
	}
	return out
}

// entryLocation is the "where" half of an index row, plus whatever the
// conversation is waiting on.
//
// The suffix is appended here rather than inside entryWhere because that
// function returns from two branches — a review-level row leaves early — and a
// chip added to one of them is a chip the other silently drops.
func entryLocation(e commentEntry) string {
	return entryWhere(e) + proposalSuffix(e)
}

// entryWhere names where a conversation is. The basename rather than the full
// path: the column is a third of the body and the file list above already shows
// paths, so spending the width here buys nothing.
func entryWhere(e commentEntry) string {
	if e.changeWide {
		// No file to name. "review" is what the section it lives in is called, so
		// the index row and the stream agree on where selecting it will take you.
		loc := "review"
		if e.replies > 0 {
			loc += fmt.Sprintf("·%d", e.replies)
		}
		return loc
	}
	name := filepath.Base(e.path)
	if name == "." || name == string(filepath.Separator) {
		name = e.path
	}
	loc := name
	if e.lines != "" {
		loc += ":" + e.lines
	}
	if e.detached && !e.outdated {
		// The line number is where it used to be, so say the anchor is gone
		// rather than presenting a stale position as current. Not for an outdated
		// thread: the chip below is GitHub's own word for the same situation and
		// says more than the glyph, so both would be saying it twice.
		loc = "⚠ " + loc
	}
	if e.replies > 0 {
		loc += fmt.Sprintf("·%d", e.replies)
	}
	if e.outdated {
		// Last, after the conversation is identified: this is a fact about the
		// thread's line, not part of naming where it is.
		loc += " · " + chipOutdated
	}
	return loc
}

// proposalSuffix is what an index row says about a proposal in its conversation.
//
// Only the pending one is worth a row's width. The list is scanned to find what
// is waiting on you, and an approved proposal is not — it is the agent's turn,
// which the exchange already shows by having gone quiet. The full state is in the
// conversation itself, and in `awp review list`.
func proposalSuffix(e commentEntry) string {
	if e.proposal != review.ProposalPending {
		return ""
	}
	return " · " + chipAwaitingApproval
}

// clampCommentsCursor keeps the index selection inside the list, and hands the
// keyboard back when the pane it belongs to has gone away — comments can be
// deleted, filtered out, or collapsed behind a reviewed file while it has
// focus.
func (m *Model) clampCommentsCursor() {
	m.commentsCursor = min(max(m.commentsCursor, 0), max(0, len(m.commentIndex)-1))
	if m.focus == FocusComments && !m.commentPaneVisible() {
		m.focus = FocusHunks
	}
}

// seekToComment points the cursor at a conversation's first row. The index is a
// jump index, like the file list: moving the selection seeks the stream, so the
// diff always shows what the selection names.
func (m *Model) seekToComment(i int) {
	if i < 0 || i >= len(m.commentIndex) {
		return
	}
	m.commentsCursor = i
	m.cursorRow = m.commentIndex[i].row
	m.hunkHScroll = 0
	m.clampCursor()
	// Centred rather than merely scrolled into view: a conversation reached from
	// the index is what you want to read, and the minimum scroll would leave it
	// on the pane's last row.
	m.centerCursor()
	m.syncFileCursorToCursor()
}

// deleteFromIndex removes the selected conversation and re-seeks, so the cursor
// lands on whatever took its place rather than on a row that just shifted under
// it. An index row is a whole conversation, so deleting one takes its replies too
// (see review.DeleteComment).
func (m Model) deleteFromIndex() (tea.Model, tea.Cmd) {
	updated, cmd := m.deleteCommentAtCursor()
	next, ok := updated.(Model)
	if !ok {
		return updated, cmd
	}
	// clampCommentsCursor has already pulled the selection into range (and handed
	// focus back if the index emptied); this points the diff at it.
	next.seekToComment(next.commentsCursor)
	return next, cmd
}

// resolveFromIndex settles the selected conversation on GitHub without leaving
// the list. The index is where you scan conversations and decide which are done,
// so `R` has to be one key repeated down the list rather than a seek into the
// diff and back for each one.
//
// The selection holds its position rather than following the thread. Under the
// default visibility a resolved thread leaves the list, so holding the same index
// puts the next unresolved thread under the cursor — which is what makes walking
// the list work. Following the thread instead would scroll to wherever it went,
// or nowhere, since it is no longer listed.
func (m Model) resolveFromIndex() (tea.Model, tea.Cmd) {
	// Seek before acting rather than trusting the diff cursor to be on the
	// selection already. It normally is — every path into this pane seeks (see
	// cycleFocus) — but resolving the wrong conversation is not a failure the
	// reader can see, so this does not rest on every one of those paths.
	m.seekToComment(m.commentsCursor)
	updated, cmd := m.toggleResolved()
	next, ok := updated.(Model)
	if !ok {
		return updated, cmd
	}
	// toggleResolved rebuilt the stream, so the entry at this index is whatever
	// took the resolved thread's place; clampCommentsCursor has already pulled the
	// index into range. This points the diff at it.
	next.seekToComment(next.commentsCursor)
	return next, cmd
}

// cycleFocus rotates focus files → comments → diff, and back the other way.
func (m *Model) cycleFocus(forward bool) {
	if m.hideLeft {
		// Nothing to cycle to: the diff is the only pane on screen, and cycling
		// into a hidden one would take the keyboard somewhere invisible.
		m.focus = FocusHunks
		return
	}
	order := []Focus{FocusFiles, FocusHunks}
	if m.commentPaneVisible() {
		order = []Focus{FocusFiles, FocusComments, FocusHunks}
	}
	at := 0
	for i, f := range order {
		if f == m.focus {
			at = i
		}
	}
	if forward {
		at = (at + 1) % len(order)
	} else {
		at = (at - 1 + len(order)) % len(order)
	}
	m.focus = order[at]
	if m.focus == FocusComments {
		// Land with the diff cursor already on the selected conversation. Anything
		// acting from the index acts through the cursor, so the two have to agree
		// the moment focus arrives — not only after the first j/k.
		m.seekToComment(m.commentsCursor)
	}
}
