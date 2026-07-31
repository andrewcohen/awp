package ui

import (
	"fmt"
	"strings"
)

// Searching the diff's content.
//
// `/` means "search" to every pair of hands that has used less/vim/man, and in
// this view it meant "filter the file list" — from inside the diff, where nearly
// all the time goes. So `/` searches the diff when the diff holds the keyboard,
// and still filters files from the lists, where filtering is the thing you want.
//
// Matches are found through the geometry index, never by rendering: the row set
// already knows every line's content, and searching rendered output would mean
// styling rows nobody is looking at and then matching against escape sequences.
//
// Code lines only, not comment prose. A conversation is reachable through the
// comment index, which is a better way to find one than stepping past it with
// `n`; searching both would make `n` walk through remarks while you are looking
// for an identifier.
//
// Nothing derived is stored — no match list, no current index. A stored row
// index goes stale the moment the stream is rebuilt, which happens on every
// refresh tick, and the scan is one pass over rows that are already in memory.
// Only the query itself is state.

// searchMatches is every stream row whose line contains the query, in stream
// order. Empty when there is no query.
func (m Model) searchMatches() []int {
	needle := strings.ToLower(strings.TrimSpace(m.searchQuery))
	if needle == "" {
		return nil
	}
	var out []int
	for i, r := range m.stream.rows {
		if r.kind != rowLine {
			continue
		}
		// seg 0 only: a wrapped line occupies several rows, and matching each of
		// them would make `n` step through one long line as if it were several
		// hits.
		if r.seg != 0 {
			continue
		}
		if strings.Contains(strings.ToLower(m.lineText(r)), needle) {
			out = append(out, i)
		}
	}
	return out
}

// seekMatch moves the cursor to the next or previous match relative to the row it
// is on, wrapping at the ends, and reports what it found for the status line.
//
// from is exclusive when stepping, so pressing `n` twice on a line holding two
// matches still advances. It is inclusive when a search is first applied, so
// typing a query that matches the line you are already on does not jump away
// from it.
func (m *Model) seekMatch(forward, inclusive bool) {
	matches := m.searchMatches()
	if len(matches) == 0 {
		m.status = m.searchStatus(0, 0)
		return
	}
	at := -1
	if forward {
		for i, row := range matches {
			if row > m.cursorRow || (inclusive && row == m.cursorRow) {
				at = i
				break
			}
		}
		if at < 0 {
			at = 0 // wrapped past the end
		}
	} else {
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i] < m.cursorRow || (inclusive && matches[i] == m.cursorRow) {
				at = i
				break
			}
		}
		if at < 0 {
			at = len(matches) - 1 // wrapped past the start
		}
	}
	m.cursorRow = matches[at]
	m.hunkHScroll = 0
	// Centred, for the same reason selecting a conversation from the index is: a
	// match you were sent to wants context around it, and the minimum scroll puts
	// it on the pane's last row.
	m.centerCursor()
	m.syncFileCursorToCursor()
	m.status = m.searchStatus(at+1, len(matches))
}

// searchStatus is what the footer says about a search. The prompt and the result
// both go through the status line because that is the one piece of viewer state
// every host already renders — the standalone footer and the deck's own footer
// alike — so search does not need a surface of its own to be usable in the deck.
func (m Model) searchStatus(at, total int) string {
	q := strings.TrimSpace(m.searchQuery)
	if q == "" {
		return ""
	}
	if total == 0 {
		msg := "/" + q + " · no match"
		// A folded file contributes no line rows, so its content genuinely is not
		// searchable. Say so rather than letting a hit inside a file you reviewed
		// look like an absence.
		if n := m.collapsedCount(); n > 0 {
			msg += fmt.Sprintf(" (%d file%s folded)", n, plural(n))
		}
		return msg
	}
	return fmt.Sprintf("/%s · %d of %d", q, at, total)
}

// collapsedCount is how many of the shown files are folded, for the no-match
// message to account for.
func (m Model) collapsedCount() int {
	n := 0
	for _, f := range m.filtered {
		if m.isCollapsed(pathOf(f)) {
			n++
		}
	}
	return n
}

// beginSearch opens the prompt, remembering where the cursor was so esc can put
// it back — the search is previewed as you type, and abandoning it should leave
// you where you started rather than at whatever the last keystroke matched.
func (m *Model) beginSearch() {
	m.focus = FocusSearch
	m.searchOrigin = m.cursorRow
	m.searchInput.SetValue("")
	m.searchInput.Focus()
	m.searchQuery = ""
	m.status = "/"
}

// applySearchInput previews the query as it is typed, jumping from where the
// search started rather than from the last match — otherwise each keystroke would
// walk the cursor further down the file as the query narrowed.
func (m *Model) applySearchInput() {
	m.searchQuery = m.searchInput.Value()
	if strings.TrimSpace(m.searchQuery) == "" {
		m.cursorRow = m.searchOrigin
		m.clampCursor()
		m.followCursor()
		m.status = "/"
		return
	}
	m.cursorRow = m.searchOrigin
	m.seekMatch(true, true)
}

// endSearch leaves the prompt. Keeping the query is what makes `n` useful after
// confirming; discarding it also restores the cursor, since an abandoned search
// should leave no trace.
func (m *Model) endSearch(keep bool) {
	m.searchInput.Blur()
	m.focus = FocusHunks
	if keep {
		return
	}
	m.searchQuery = ""
	m.searchInput.SetValue("")
	m.cursorRow = m.searchOrigin
	m.clampCursor()
	m.followCursor()
	m.status = ""
}
