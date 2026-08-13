package deckui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// The host bar is the deck's own row above whatever is hosting a terminal: one
// pane, or a split of two.
//
// It is the deck's rather than the pane's because the questions it answers are
// the deck's. The attention badge counts every workspace, not the one on screen;
// the leave key leaves the arrangement, not the half under the cursor. Both were
// once rendered by the pane, inside its border, which put them one row down and
// one column in — and then a split, which owns the row above both halves, drew
// its own copy in a different place. Same three things, two addresses, and
// neither configuration could be glanced at the way the other had trained you
// to.
//
// So there is one row, on row 0, spanning the terminal, in the same cells in
// both arrangements. A pane in a split renders no header at all; a lone pane
// renders no header either. The border is what says where a pane ends.
const hostBarRows = 1

// hostsBar reports whether the active modal is one the deck draws its bar above.
//
// The two that host someone else's full-screen program. Every other popover —
// help, the jobs overlay, a confirm — is awp's own text in a box on a blank
// canvas, and has nothing to say about a workspace you are sitting inside.
func (m *Model) hostsBar() bool {
	switch m.active.(type) {
	case *panePopover, *splitModal:
		return true
	}
	return false
}

// hostBarSubject is the pane the bar names: the one the keys are in, or the
// other half when the focused one is not a pane at all.
//
// A split's halves are the same workspace, so either answers the "where am I"
// question; the focused one is preferred because its kind is the one whose keys
// you are pressing.
func (m *Model) hostBarSubject() *panePopover {
	switch a := m.active.(type) {
	case *panePopover:
		return a
	case *splitModal:
		for _, child := range []modal{a.focused(), a.left, a.right} {
			if p, ok := child.(*panePopover); ok {
				return p
			}
		}
	}
	return nil
}

// hostBarLabel is what is on screen, named: "<kind> · <project>/<workspace>",
// and which emulator is behind it when that is not the default one.
//
// Running on an alternative emulator is a thing you need to see to trust a
// comparison of the two; running on the usual one is not news.
func (m *Model) hostBarLabel() string {
	p := m.hostBarSubject()
	if p == nil {
		if s, ok := m.active.(*splitModal); ok {
			return s.label
		}
		return ""
	}
	if vt := p.term.Emulator(); vt != vterm.EmulatorXVT {
		return vt + " · " + p.label
	}
	return p.label
}

// hostBarRow is the row list's entry for the workspace this pane is of, so the
// bar can report the same state the row would.
//
// Matched forward by project and workspace name, on the rows the deck already
// holds — the same pass the badge makes. Missing means the workspace is gone
// (deleted from under the pane, or a refresh that has not landed yet), and the
// bar simply says less rather than inventing a row.
func (m *Model) hostBarRow() (Item, bool) {
	p := m.hostBarSubject()
	if p == nil || p.workspace == "" {
		return Item{}, false
	}
	for _, it := range m.mergedItemsAll() {
		if it.ProjectName == p.project && it.WorkspaceName == p.workspace {
			return it, true
		}
	}
	return Item{}, false
}

// hostBarState is what the hosted workspace's own row says about it: its PR
// number and glyph cluster, and how its dev loop is going.
//
// Glyphs and numbers, no words. The glyphs are the row list's, in the row list's
// order (prGlyphCluster), and the PR number wears the hue that cluster's leading
// glyph does — so a red 412 is a PR whose CI is failing, and it is red for the
// same reason and in the same shade as the row two keystrokes away. A vocabulary
// already read a hundred times a day beats a second one invented for this row.
//
// This is the part of the bar that earns it. The badge says something wants you
// somewhere; this says whether the thing you are looking at is broken — which
// from inside a pane you would otherwise have to leave to find out.
func (m *Model) hostBarState() string {
	item, ok := m.hostBarRow()
	if !ok {
		return ""
	}
	segs := make([]string, 0, 2)
	if pr := m.hostBarPR(item); pr != "" {
		segs = append(segs, pr)
	}
	if loop := m.hostBarLoop(item); loop != "" {
		segs = append(segs, loop)
	}
	return strings.Join(segs, " ")
}

// hostBarPR is "#412" plus the row's glyph cluster, tinted the way the row tints
// it. Empty when the workspace has no PR, or none whose status has been fetched.
func (m *Model) hostBarPR(item Item) string {
	if item.PRNumber <= 0 {
		return ""
	}
	num := "#" + strconv.Itoa(item.PRNumber)
	if status, ok := m.resolvePRStatus(item); ok {
		if c := prGlyphColor(status); c != "" {
			num = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(num)
		}
	} else {
		// A number with no fetched status is a number we cannot colour honestly.
		num = m.styles.Muted.Render(num)
	}
	if glyphs := m.prGlyphCluster(item); glyphs != "" {
		return num + " " + glyphs
	}
	return num
}

// hostBarLoop is the agent's dev-loop progress as two numbers and one glyph:
// units done over units total, and the gates' worst result with how many are in
// it — green tick for all passing, red cross for the ones that fail, muted ring
// for the ones not yet run.
//
// A digest rather than one glyph per gate, because a bare run of ticks and
// crosses with no room for names says nothing about *which* gate failed, and the
// order that would carry that is the loop's — which the deck does not have here,
// since a persisted snapshot stores the gates as a map. "Two failing" is the
// question a glance is asking anyway; the `w` watch view names them.
func (m *Model) hostBarLoop(item Item) string {
	dl := item.DevLoop
	if dl == nil {
		return ""
	}
	segs := make([]string, 0, 2)
	if dl.Total > 0 {
		segs = append(segs, m.styles.Muted.Render(strconv.Itoa(dl.Done)+"/"+strconv.Itoa(dl.Total)))
	}
	var pass, fail, pending int
	for _, result := range dl.Gates {
		switch result {
		case "pass":
			pass++
		case "fail":
			fail++
		default:
			pending++
		}
	}
	switch {
	case fail > 0:
		segs = append(segs, m.styles.Danger.Render(gateGlyphFail+strconv.Itoa(fail)))
	case pending > 0:
		segs = append(segs, m.styles.Muted.Render(gateGlyphPending+strconv.Itoa(pending)))
	case pass > 0:
		segs = append(segs, m.styles.Success.Render(gateGlyphPass+strconv.Itoa(pass)))
	}
	return strings.Join(segs, " ")
}

// The gate glyphs, matching the `w` watch view's gate lights so the two surfaces
// spell a pass the same way (internal/watch.gateLine).
const (
	gateGlyphPass    = "✔"
	gateGlyphFail    = "✗"
	gateGlyphPending = "○"
)

// hostBarHint is the way out, which differs between the two arrangements
// because in a split the reserved key is a prefix rather than a door.
func (m *Model) hostBarHint() string {
	if _, split := m.active.(*splitModal); split {
		return PaneLeaveKey + " keys · " + PaneLeaveKey + " q deck"
	}
	return PaneLeaveKey + " deck"
}

// renderHostBar draws the row: what wants you and what you are looking at on
// the left, how to leave on the right.
//
// State is glyphs and numbers, never words. The attention badge has always been
// a coloured dot and a count — see renderAttentionSummary, where the argument is
// written down — and everything else the row reports about state is held to the
// same rule, so the row can be read at a glance rather than parsed. The only
// text on it is the name of what is on screen and the key that leaves.
//
// An armed prefix takes the whole row. That menu used to be painted over the
// split's bottom border for want of a row to put it on; this is that row.
func (m *Model) renderHostBar(w int) string {
	if s, ok := m.active.(*splitModal); ok && s.prefixArmed {
		return padBar(m.styles.FindHeader.Render(truncate(splitPrefixHint, max(1, w))), w)
	}

	sep := m.styles.PaneHint.Render(" · ")
	// Counted every frame rather than cached, and deliberately: an agent finishing
	// its turn while you are in a pane is the whole reason the badge is up here, so
	// a number that only moved when the row list was on screen would be worse than
	// no number. The tally is a pass over rows the deck already holds.
	segs := make([]string, 0, 3)
	if badge := m.renderAttentionSummary(countAttention(m.mergedItemsAll())); badge != "" {
		segs = append(segs, badge)
	}
	if state := m.hostBarState(); state != "" {
		segs = append(segs, state)
	}
	hint := m.styles.PaneHint.Render(m.hostBarHint())

	if label := m.hostBarLabel(); label != "" {
		room := w - lipgloss.Width(strings.Join(segs, sep)) - lipgloss.Width(hint) - lipgloss.Width(sep) - 1
		if room >= hostBarLabelMin {
			segs = append(segs, m.styles.PaneTitle.Render(truncate(label, room)))
		}
	}

	left := strings.Join(segs, sep)
	gap := w - lipgloss.Width(left) - lipgloss.Width(hint)
	if gap < 1 {
		// Too narrow for both. The badge is a thing leaving will show you anyway;
		// the leave key is how you leave.
		return padBar(hint, w)
	}
	return padBar(left+strings.Repeat(" ", gap)+hint, w)
}

// hostBarLabelMin is the narrowest the label is worth showing at. Below it there
// is room for a word and an ellipsis, which names nothing.
const hostBarLabelMin = 10

// padBar fills a bar row out to the width, so the row is opaque rather than
// letting whatever the frame put in those cells show through the ones it did not
// write.
func padBar(bar string, w int) string {
	if pad := w - lipgloss.Width(bar); pad > 0 {
		return bar + strings.Repeat(" ", pad)
	}
	return bar
}
