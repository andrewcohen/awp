package deckui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// The top row is the deck's, on row 0, spanning the terminal, in the same cells
// on every screen that is about your workspaces: the row list, a pane, and a
// split of two.
//
// It got there in three steps, each of which had drawn its own. A lone pane drew
// a header inside its own border, one row down and one column in. A split drew a
// copy above both halves, spanning the terminal. The row list drew a title row
// inside its panel. Three surfaces you move between constantly, and the badge —
// the thing you are actually glancing at — sat in a different cell on each, so
// none of them could be read the way the last one had trained you to.
//
// Now it is rendered once, by the deck, above whatever the body is. What changes
// between the screens is only what the row has to say: over a pane it names what
// is on screen, that workspace's own state, and the key that leaves; over the row
// list it names the scope, because there is nothing to leave and no one workspace
// to report on. The badge is on the left in all three.
//
// Panes therefore render no header at all — their border is what says where they
// end — and the row list's panel no longer starts with a title.
const topRowRows = 1

// showsTopRow reports whether this screen wears the deck's top row.
//
// The row list (no modal at all), a pane, and a split. Not the modes you are
// *inside* of rather than looking out from: a form, a picker, the diff viewer,
// the help overlay. Those carry their own chrome — the diff viewer has a whole
// status line of its own — and a row about which workspaces want you, above a
// screen whose subject is one file, is a row spent on the wrong question.
// A chord is on the list too — it is a menu about the row you are pointing at,
// with that row on screen beneath it, so the row it would drop is the one its own
// menu goes on. Dropping it also moved the frame: one row fewer above the body
// pushes every line up, so arming `|` visibly jumped the whole deck.
func (m *Model) showsTopRow() bool {
	switch m.active.(type) {
	case nil, *panePopover, *splitModal:
		return true
	}
	_, chord := m.active.(chordModal)
	return chord
}

// hostsTerminal is whether the top row has a hosted program to talk about, as
// opposed to sitting over the row list.
//
// Separate from showsTopRow because it is the question that decides what the row
// says, where showsTopRow decides whether there is a row. Over the list there is
// no label to print, no PR to report and nothing to leave.
func (m *Model) hostsTerminal() bool {
	switch m.active.(type) {
	case *panePopover, *splitModal:
		return true
	}
	return false
}

// topRowSubject is the pane the bar names: the one the keys are in, or the
// other half when the focused one is not a pane at all.
//
// A split's halves are the same workspace, so either answers the "where am I"
// question; the focused one is preferred because its kind is the one whose keys
// you are pressing.
func (m *Model) topRowSubject() *panePopover {
	// The captain while it is up, for the same reason the focused half of a split
	// wins: the bar names where the keys are, and they are in the modal. Naming the
	// pane behind it would say "agent · proj/ws" above a box the agent cannot hear
	// you through, which is the row telling you the wrong thing about the keyboard.
	if m.captain != nil {
		return m.captain
	}
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

// topRowLabel is what is on screen, named: "<kind> · <project>/<workspace>".
//
// It does not name the emulator behind the pane. That was worth a segment while
// libghostty-vt was a thing being tried — a comparison you cannot confirm you are
// inside is not a comparison — and it stopped being one once the answer was
// settled. Which emulator is running is still askable (Hosted.Emulator, and the
// byte log), just not on every frame of the row.
func (m *Model) topRowLabel() string {
	p := m.topRowSubject()
	if p == nil {
		if s, ok := m.active.(*splitModal); ok {
			return s.label
		}
		return ""
	}
	return p.label
}

// topRowRow is the row list's entry for the workspace this pane is of, so the
// bar can report the same state the row would.
//
// Matched forward by project and workspace name, on the rows the deck already
// holds — the same pass the badge makes. Missing means the workspace is gone
// (deleted from under the pane, or a refresh that has not landed yet), and the
// bar simply says less rather than inventing a row.
func (m *Model) topRowRow() (Item, bool) {
	p := m.topRowSubject()
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

// topRowState is what the hosted workspace's own row says about it: its PR
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
func (m *Model) topRowState() string {
	item, ok := m.topRowRow()
	if !ok {
		return ""
	}
	segs := make([]string, 0, 2)
	if pr := m.topRowPR(item); pr != "" {
		segs = append(segs, pr)
	}
	if loop := m.topRowLoop(item); loop != "" {
		segs = append(segs, loop)
	}
	return strings.Join(segs, " ")
}

// topRowPR is "#412" plus the row's glyph cluster, tinted the way the row tints
// it. Empty when the workspace has no PR, or none whose status has been fetched.
func (m *Model) topRowPR(item Item) string {
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

// topRowLoop is the agent's dev-loop progress as two numbers and one glyph:
// units done over units total, and the gates' worst result with how many are in
// it — green tick for all passing, red cross for the ones that fail, muted ring
// for the ones not yet run.
//
// A digest rather than one glyph per gate, because a bare run of ticks and
// crosses with no room for names says nothing about *which* gate failed, and the
// order that would carry that is the loop's — which the deck does not have here,
// since a persisted snapshot stores the gates as a map. "Two failing" is the
// question a glance is asking anyway; the `w` watch view names them.
func (m *Model) topRowLoop(item Item) string {
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

// topRowActivity is the background work in flight, as the status bar's own chips.
//
// It is on this row because a pane surface renders no status bar at all — the
// popover branch of view() returns the child and this row, and nothing else. So a
// background user action started from ctrl+b x (an install, a build) had no
// surface anywhere: the menu closed, a job ran, and the only way to learn that
// was to leave the pane. The chips are what the row list has always shown for
// the same work, in the same shape, so nothing new has to be learned to read
// them.
//
// Bounded twice, because a job's title is "<action> · <workspace>" and two of
// those would have the label and the way out off the row: at most
// topRowActivityMax chips, each label truncated, with a "+N" for the rest.
func (m *Model) topRowActivity() string {
	if len(m.activities) == 0 {
		return ""
	}
	shown := m.activities
	extra := 0
	if len(shown) > topRowActivityMax {
		extra, shown = len(shown)-topRowActivityMax, shown[:topRowActivityMax]
	}
	// Rendered from copies: truncating the label is this row's business and not
	// something the status bar's activities should carry away with them.
	short := make([]Activity, len(shown))
	for i, a := range shown {
		a.Label = truncate(a.Label, topRowActivityLabelMax)
		short[i] = a
	}
	seg := renderActivitiesCompact(short, m.spinner.View())
	if extra > 0 {
		seg += m.styles.Muted.Render(" +" + strconv.Itoa(extra))
	}
	return seg
}

// topRowActivityMax is how many activities the row names before counting the
// rest, and topRowActivityLabelMax how much of one it spells.
const (
	topRowActivityMax      = 2
	topRowActivityLabelMax = 28
)

// topRowScrollback says the pane is not showing its live tail, and how many rows
// of history sit above what is on screen.
//
// It earns its place on the row because nothing else on screen says it. A pane
// scrolled back looks exactly like a pane whose program has stopped printing:
// same border, same frame, output apparently frozen. The glyph is the direction
// you moved and the number is how far, in the row's own vocabulary of glyphs and
// numbers — and it is Warning, the hue the deck spends on "this is waiting for
// something", because a view that is not live is the one state here that makes
// everything else on the pane stale.
func (m *Model) topRowScrollback() string {
	p := m.topRowSubject()
	if p == nil {
		return ""
	}
	rows, behind := p.paneBehind()
	if !behind {
		return ""
	}
	return m.styles.Warning.Render(scrollbackGlyph + strconv.Itoa(rows))
}

// scrollbackGlyph marks the count as rows of history above the view, in the
// direction you pressed to get there.
const scrollbackGlyph = "↑"

// topRowHint is what sits at the right end: over the row list, the scope it is
// showing. Over a pane, nothing.
//
// The row used to spell the way out there — `ctrl+b menu · ctrl+\ deck`. Two
// keys, on every frame, for the whole time you are in a pane, which is most of
// the time the deck is open: a beginner's card that never came down. They are on
// the `?` overlay and in the ctrl+b menu itself, which is where a key you have to
// look up belongs. Dropping it also gives the row's right half back to the
// label, which is now centred against the terminal rather than against whatever
// was left over.
//
// Over the list it stays, because the scope is not a hint — it is state, and the
// one thing about the list that is invisible from the rows themselves.
func (m *Model) topRowHint() string {
	if m.hostsTerminal() {
		return ""
	}
	return "scope: " + scopeLabel(m.scope)
}

// renderTopRow draws the row: what wants you on the left, then what you are
// looking at, and on the right the way out — or which scope the list is showing,
// when the list is what is below.
//
// State is glyphs and numbers, never words. The attention badge has always been
// a coloured dot and a count — see renderAttentionSummary, where the argument is
// written down — and everything else the row reports about state is held to the
// same rule, so the row can be read at a glance rather than parsed. The only
// text on it is the name of what is on screen and the key that leaves.
//
// An armed menu does not touch this row. It used to take the whole of it (#328),
// which meant the badge and the label went away for as long as you were reading the
// menu, and the menu itself was a ribbon squeezed into one line. Since #344 the menu
// floats over the frame instead — see menu.go — so the row says the same thing
// whether or not something is waiting for a key.
func (m *Model) renderTopRow(w int) string {
	// The row starts on the body's own text column, so the badge's dots land in
	// the same column as the status dots of the rows below them. That alignment is
	// why the indent exists, and it is why a pane's row is indented too even
	// though the pane's border below it starts at column 0 — the row belongs to
	// the deck, and it is the same row on both screens or it is not one row.
	indent := deckIndent
	avail := w - len(indent)

	// Counted every frame rather than cached, and deliberately: an agent finishing
	// its turn while you are in a pane is the whole reason the badge is up here, so
	// a number that only moved when the row list was on screen would be worse than
	// no number. The tally is a pass over rows the deck already holds.
	segs := make([]string, 0, 3)
	if badge := m.renderAttentionSummary(countAttention(m.mergedItemsAll())); badge != "" {
		segs = append(segs, badge)
	}
	if state := m.topRowState(); state != "" {
		segs = append(segs, state)
	}
	if back := m.topRowScrollback(); back != "" {
		segs = append(segs, back)
	}
	// Only over a hosted terminal. Over the row list the status bar below is
	// already showing these, and the same chips twice on one screen reads as two
	// things happening.
	if m.hostsTerminal() {
		if act := m.topRowActivity(); act != "" {
			segs = append(segs, act)
		}
	}
	// Empty over a pane, and rendered through the style only when there is
	// something to style: a style applied to "" still emits its escapes, which
	// would make an empty right end measure as a segment that is there.
	var right string
	if hint := m.topRowHint(); hint != "" {
		right = m.styles.PaneHint.Render(hint)
	}

	left := joinTopRowSegs(segs)
	if row, ok := m.centreLabel(indent+left, right, w); ok {
		return row
	}

	gap := avail - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Too narrow for both. Over the list the right side is the scope and the
		// left is the badge that is the reason the row exists, so the badge wins.
		// A pane has no right side to lose.
		return padTopRow(truncate(indent+left, w), w)
	}
	return padTopRow(indent+left+strings.Repeat(" ", gap)+right, w)
}

// centreLabel puts what is on screen in the middle of the row, between the state
// on the left and the way out on the right. ok is false when there is no label or
// no room for one, and the caller falls back to a row of two ends.
//
// Centred rather than packed in after the state, because everything to its left
// changes width on its own: the badge is counted every frame, a PR's glyph
// cluster grows when CI goes red, the scrollback count is however far you
// scrolled, and an activity chip appears the moment you start a background
// action. Packed, each of those slid the label sideways — the one thing on the
// row you read as text, moving under your eyes for reasons that have nothing to
// do with it. Anchored to the middle of the terminal it stays put, and the
// segments that do change grow into the space beside it instead.
//
// It gives way rather than being pushed: when the left side grows into the
// middle the label is clamped away from it and then truncated, so a long-titled
// pane loses characters before the state loses a glyph. Both are held off by
// topRowSep's width so nothing ever touches.
func (m *Model) centreLabel(left, right string, w int) (string, bool) {
	label := m.topRowLabel()
	if label == "" {
		return "", false
	}
	leftW, rightW := lipgloss.Width(left), lipgloss.Width(right)
	gap := len(topRowSep)
	room := w - leftW - rightW - 2*gap
	if room < topRowLabelMin {
		return "", false
	}
	rendered := m.styles.PaneTitle.Render(truncate(label, room))
	labelW := lipgloss.Width(rendered)

	start := (w - labelW) / 2
	if max := w - rightW - gap - labelW; start > max {
		start = max
	}
	if min := leftW + gap; start < min {
		start = min
	}
	row := left + strings.Repeat(" ", start-leftW) + rendered
	tail := w - lipgloss.Width(row) - rightW
	if tail < 0 {
		tail = 0
	}
	return padTopRow(row+strings.Repeat(" ", tail)+right, w), true
}

// topRowLabelMin is the narrowest the label is worth showing at. Below it there
// is room for a word and an ellipsis, which names nothing.
const topRowLabelMin = 10

// topRowSep is the gap between the row's top-level segments.
//
// Whitespace rather than a ` · ` bullet. The row carries the badge, the hosted
// workspace's state and the label, and each of those is internally punctuated
// already — the badge spaces its dots, the state has a `#` and a `/`, the label
// has its own bullet between kind and path. Gluing them with more bullets read as
// crowded because most of the ink on the row was separator: five segments meant
// four bullets and eight spaces spent saying nothing. Space groups just as well
// when the things being grouped are visually distinct, and these are — a coloured
// dot, a number, a word.
const topRowSep = "   "

// joinTopRowSegs puts the row's segments together, dropping the empty ones so a
// missing segment costs no gap.
func joinTopRowSegs(segs []string) string {
	kept := make([]string, 0, len(segs))
	for _, s := range segs {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, topRowSep)
}

// padTopRow fills the row out to the width, so it is opaque rather than letting
// whatever the frame put in those cells show through the ones it did not write.
//
// Padding only — a caller that might exceed the width truncates first. A row one
// column over does not wrap harmlessly: it pushes the frame's every later line
// down by one, which is a whole screen misaligned by the narrowest terminal
// anyone opens.
func padTopRow(bar string, w int) string {
	if pad := w - lipgloss.Width(bar); pad > 0 {
		return bar + strings.Repeat(" ", pad)
	}
	return bar
}
