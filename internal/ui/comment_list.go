package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	row     int
	path    string
	line    int
	author  string
	kind    review.Kind
	summary string
	replies int
	state   review.State
	// detached marks a conversation whose anchor could not be located, so it
	// lives in the stream's trailing section instead of beside code.
	detached bool
}

// commentEntries walks the placed rows in stream order and returns one entry
// per conversation. Stream order rather than sorted, so the index reads top to
// bottom the same way the diff does.
func (idx streamIndex) commentEntries() []commentEntry {
	var out []commentEntry
	// slot maps a conversation's id to its entry, so a reply can find its
	// parent and be counted rather than listed.
	slot := make(map[string]int, len(idx.comments))
	for row, r := range idx.rows {
		if r.kind != rowComment && r.kind != rowOrphan {
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
				continue
			}
			// A reply whose parent is not in the stream is a conversation in its
			// own right as far as the reader is concerned — listing it is the
			// only way to reach it.
		}
		slot[c.ID] = len(out)
		out = append(out, commentEntry{
			id:       c.ID,
			row:      row,
			path:     c.Anchor.Path,
			line:     c.Anchor.LineHint,
			author:   c.Author,
			kind:     c.Kind.OrDefault(),
			summary:  entrySummary(c),
			state:    c.State,
			detached: r.kind == rowOrphan,
		})
	}
	return out
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
func commentPaneHeight(entries, height int) int {
	if entries <= 0 {
		return 0
	}
	room := min(height/2, height-2-minFileListHeight)
	if room < 2 { // a header and at least one entry
		return 0
	}
	return min(entries+1, room)
}

// commentPaneVisible reports whether the index is on screen, which is what
// decides if it belongs in the tab rotation. Tabbing to a pane that isn't
// rendered would strand the keyboard.
func (m Model) commentPaneVisible() bool {
	return commentPaneHeight(len(m.commentIndex), m.bodyHeight) > 0
}

// renderLeftColumn stacks the file list over the comment index.
func (m Model) renderLeftColumn(width, height int) string {
	h := commentPaneHeight(len(m.commentIndex), height)
	if h <= 0 {
		return m.renderFileList(width, height)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderFileList(width, height-2-h),
		m.renderCommentList(width, h),
	)
}

func (m Model) renderCommentList(width, height int) string {
	border := styleNormalBorder
	if m.focus == FocusComments {
		border = styleFocusBorder
	}
	rows := []string{styleDim.Render(fmt.Sprintf(" Comments (%d)", len(m.commentIndex)))}
	start, end := visibleRange(m.commentsCursor, max(1, height-1), len(m.commentIndex))
	contentWidth := width - 4
	for i := start; i < end; i++ {
		rows = append(rows, renderCommentEntry(m.commentIndex[i], contentWidth, i == m.commentsCursor))
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
func commentEntryStyles(kind review.Kind, selected bool) (loc, text lipgloss.Style) {
	if selected {
		// Selection wins over kind: the app-wide marker has to read as the
		// selection wherever it lands.
		return styleSelected, styleSelected
	}
	return kindStyles(kind), styleMuted
}

func renderCommentEntry(e commentEntry, width int, selected bool) string {
	// The `┃ ` bar is the app-wide selection marker; unselected rows reserve the
	// same columns so labels line up down the list.
	prefix := selectionPrefixBlank
	if selected {
		prefix = styleSelected.Render(selectionPrefixBar)
	}
	loc, text := commentEntryStyles(e.kind, selected)

	head := entryLocation(e)
	avail := max(1, width-lipgloss.Width(selectionPrefixBlank))
	head = truncate(head, avail)
	out := prefix + loc.Render(head)
	// The summary only gets whatever the location left, so a deep path can't
	// push the row past the pane.
	if rest := avail - lipgloss.Width(head) - 1; rest > 0 && e.summary != "" {
		out += " " + text.Render(truncate(e.summary, rest))
	}
	return out
}

// entryLocation is the "where" half of an index row. The basename rather than
// the full path: the column is a third of the body and the file list above
// already shows paths, so spending the width here buys nothing.
func entryLocation(e commentEntry) string {
	name := filepath.Base(e.path)
	if name == "." || name == string(filepath.Separator) {
		name = e.path
	}
	loc := name
	if e.line > 0 {
		loc += ":" + fmt.Sprint(e.line)
	}
	if e.detached {
		// The line number is where it used to be, so say the anchor is gone
		// rather than presenting a stale position as current.
		loc = "⚠ " + loc
	}
	if e.replies > 0 {
		loc += fmt.Sprintf("·%d", e.replies)
	}
	return loc
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
	m.followCursor()
	m.syncFileCursorToCursor()
}

// deleteFromIndex removes the selected conversation and re-seeks, so the cursor
// lands on whatever took its place rather than on a row that just shifted under
// it. Deleting a parent deletes its replies with it, which is what the store
// does — the list and the record must not disagree about what is left.
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

// cycleFocus rotates focus files → comments → diff, and back the other way.
func (m *Model) cycleFocus(forward bool) {
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
