package deckui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/workspace"
)

// The deck's top row reports what wants you, rather than which program you
// opened. "awp deck" was a title nobody had ever needed: you typed the command,
// and the row it occupied is the first thing the eye lands on.

// attentionCounts is how many workspaces are in each bucket.
type attentionCounts struct {
	Waiting  int
	Working  int
	Notified int
}

func (c attentionCounts) empty() bool {
	return c.Waiting == 0 && c.Working == 0 && c.Notified == 0
}

// countAttention tallies every workspace by bucket.
//
// Takes all of them, not the current scope's rows: the summary answers "how
// much is waiting on me", and that number cannot depend on which scope you are
// looking through. A summary that dropped from 3 to 1 because you pressed P
// would be reporting the filter, not the work — and the filter is already named
// on the same row, a few words to the right.
func countAttention(all []Item) attentionCounts {
	var c attentionCounts
	for _, it := range all {
		// A workspace still being created is a spinner on its own row and
		// nothing to act on yet.
		if it.Optimistic {
			continue
		}
		switch workspace.Classify(it.Status, it.Unread) {
		case workspace.AttentionWaiting:
			c.Waiting++
		case workspace.AttentionWorking:
			c.Working++
		case workspace.AttentionNotified:
			c.Notified++
		case workspace.AttentionNone:
		}
	}
	return c
}

// renderAttentionSummary is the top row's left half: a coloured dot and a
// number per bucket, and nothing else. Empty string when nothing wants you.
//
// No words, because the dot already is the word. It is the same glyph in the
// same colour that the matching rows wear two lines below — yellow waiting,
// green working, grey unread — and the same badge `awp internal unread-summary`
// puts in the tmux status bar, so this is a vocabulary already read a hundred
// times a day rather than a new one. Spelling it out again turned three numbers
// into a sentence sitting across the top of the screen.
//
// Ordered by how much it is your problem — waiting (you are the blocker),
// working (in flight), unread (finished a turn since you looked).
//
// The zero state renders nothing at all. There is no such thing as a neutral
// way to say "no", and every phrasing tried read as either an error or a
// greeting; the row keeps the scope label at its right either way, so an empty
// badge still cannot be mistaken for a frame that has not loaded.
func (m Model) renderAttentionSummary(c attentionCounts) string {
	if c.empty() {
		return ""
	}
	segs := make([]string, 0, 3)
	seg := func(n int, dotStyle lipgloss.Style) {
		if n == 0 {
			return
		}
		segs = append(segs, dotStyle.Render("●")+fmt.Sprintf(" %d", n))
	}
	seg(c.Waiting, m.styles.Warning)
	seg(c.Working, m.styles.Success)
	seg(c.Notified, m.styles.Muted)
	return strings.Join(segs, "  ")
}
