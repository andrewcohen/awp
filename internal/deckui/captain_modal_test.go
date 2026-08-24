package deckui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// captainOverPane is a deck with an agent pane up, the sidebar showing beside it,
// and the captain floated over both — the arrangement the modal exists for.
func captainOverPane(t *testing.T) Model {
	t.Helper()
	m := splitDeck(t)
	m.sidebar = true
	// A strip wide enough that the screen-centred box would start inside it, which
	// is what makes this fixture about the clamp. At the default width the captain
	// clears the strip on a 200-column terminal and nothing is pushed — a fine
	// screen, and not the one this is asking about.
	m.sidebarW = 60
	m = pressDeck(t, m, agentKey())
	under, ok := m.active.(*panePopover)
	if !ok {
		t.Fatalf("precondition: enter opened %T (status %q)", m.active, m.status)
	}
	t.Cleanup(func() { under.close(&m) })
	if !m.showsSidebar() {
		t.Fatal("precondition: the strip is not up, so there is no chrome to be pushed off")
	}
	m = pressDeck(t, m, menuKey())
	m = pressDeck(t, m, runeKey(captainKey))
	return m
}

// The captain is a modal, not a pane the size of the screen.
//
// Everything else full-screen in awp is a workspace's program, so the captain —
// which has no repository — wearing the same chrome said it was one. These are
// about the size and about the three paths that have to agree on it. See #385.

func TestTheCaptainIsSmallerThanTheScreen(t *testing.T) {
	m := captainDeck(t)
	m = pressDeck(t, m, runeKey(captainKey))
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })

	region, got := m.childBox(), m.boxOf(p)
	if got.w >= region.w || got.h >= region.h {
		t.Fatalf("the captain got %dx%d of a %dx%d region — that is the whole screen",
			got.w, got.h, region.w, region.h)
	}
	// A fraction of the screen, not of the region: the region is the screen less
	// the deck's own chrome, and sizing against it makes the modal a different
	// shape depending on whether the sidebar is up.
	if want := m.width * captainWidthNum / captainWidthDen; got.w != want {
		t.Errorf("the captain is %d columns, want %d", got.w, want)
	}
	if want := m.height * captainHeightNum / captainHeightDen; got.h != want {
		t.Errorf("the captain is %d rows, want %d", got.h, want)
	}
	// Taller than wide, in the sense that matters: it keeps more of its height than
	// of its width, because what is in it is a conversation. It grows downward and
	// is read by scrolling, so rows are what it runs out of; past roughly 120
	// columns a line of prose gets harder to read rather than easier.
	if captainHeightNum*captainWidthDen <= captainWidthNum*captainHeightDen {
		t.Error("the captain keeps no more of its height than of its width")
	}
}

// Centred on the screen, not in the region.
//
// The region is the screen less the top row and the sidebar's columns, so a modal
// centred in it sits a row low and, with the strip up, right of the middle. What
// the eye centres a floating box against is the display it floats over.
//
// Asserted against the drawn frame as well as against the box, because those are
// two different claims: the box is what the cursor and the mouse are placed from,
// the frame is what you see, and #339 is what happens when they disagree.
func TestTheCaptainIsCentredOnTheScreen(t *testing.T) {
	m := captainDeck(t)
	m = pressDeck(t, m, runeKey(captainKey))
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })

	got := m.boxOf(p)
	if want := (m.width - got.w) / 2; got.x != want {
		t.Errorf("the captain starts at column %d, want %d", got.x, want)
	}
	if want := (m.height - got.h) / 2; got.y != want {
		t.Errorf("the captain starts at row %d, want %d — the top row's line is not its to give up",
			got.y, want)
	}

	// And the frame agrees, which is a second claim: the box is what the cursor and
	// the mouse are placed from, the frame is what you see, and #339 is what happens
	// when the two disagree. Its corners are where the box says they are.
	rows := frameRows(t, m)
	top, bottom := rows[got.y], rows[got.y+got.h-1]
	if r := []rune(top); string(r[got.x]) != "╭" || string(r[got.x+got.w-1]) != "╮" {
		t.Errorf("the modal's top row does not start and end at the box: %q", string(r[got.x:got.x+got.w]))
	}
	if r := []rune(bottom); string(r[got.x]) != "╰" || string(r[got.x+got.w-1]) != "╯" {
		t.Errorf("the modal's bottom row does not start and end at the box: %q", string(r[got.x:got.x+got.w]))
	}
}

// frameRows is the rendered frame as rows of plain runes, each padded to the
// terminal's width so a column index means the same thing on every one of them.
func frameRows(t *testing.T, m Model) []string {
	t.Helper()
	var rows []string
	for _, line := range strings.Split(m.render(), "\n") {
		line = ansi.Strip(line)
		if w := lipgloss.Width(line); w < m.width {
			line += strings.Repeat(" ", m.width-w)
		}
		rows = append(rows, line)
	}
	if len(rows) != m.height {
		t.Fatalf("the frame is %d rows, the terminal is %d", len(rows), m.height)
	}
	return rows
}

// The clamp: the deck's own chrome is not drawn over.
//
// Centring on the screen puts the box where the top row and the sidebar are, on a
// terminal where the modal is large relative to the chrome. A modal that has to be
// pushed off centre is the right trade — the row and the strip are the deck
// talking, and a pane has never been allowed in their cells.
func TestTheCaptainDoesNotCoverTheDecksOwnChrome(t *testing.T) {
	m := captainOverPane(t)
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })

	region, got := m.childBox(), m.boxOf(p)
	if want := (m.width - got.w) / 2; got.x <= want {
		t.Errorf("the captain starts at column %d — the strip ends at %d, so it should have been pushed past the screen's centre at %d",
			got.x, region.x, want)
	}
	if got.x < region.x || got.y < region.y {
		t.Errorf("the captain starts at %d,%d, over chrome that ends at %d,%d",
			got.x, got.y, region.x, region.y)
	}
	if got.x+got.w > region.x+region.w || got.y+got.h > region.y+region.h {
		t.Errorf("the captain runs to %d,%d, past a region ending at %d,%d",
			got.x+got.w, got.y+got.h, region.x+region.w, region.y+region.h)
	}
}

// The pty is started at the modal's size, not at the screen's.
//
// A pane opened full-width and resized on the first frame repaints itself in
// front of you, and an agent that had already wrapped a paragraph to 200 columns
// re-wraps it — which is the one thing a reader notices.
func TestTheCaptainsProcessStartsAtTheModalsSize(t *testing.T) {
	m := captainDeck(t)
	m = pressDeck(t, m, runeKey(captainKey))
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })

	b := m.boxOf(p)
	w, h := paneDims(b.w, b.h)
	if p.setW != w || p.setH != h {
		t.Errorf("the captain's process was started at %dx%d, drawn at %dx%d",
			p.setW, p.setH, w, h)
	}
}

// What the captain was opened over is still on screen around it.
//
// This is the modal's whole claim. The captain answers questions about the deck,
// and the thing you want while reading its claims is what it is claiming about —
// the strip, the row list, the agent you asked it about — so the frame behind it
// goes on saying what it said. It used to replace that frame, which meant asking a
// question cost you the answer's context. See #385.
func TestWhatIsBehindTheCaptainStaysOnScreen(t *testing.T) {
	m := captainOverPane(t)
	p := captainPane(t, &m)
	t.Cleanup(func() { p.close(&m) })

	b := m.boxOf(p)
	rows := frameRows(t, m)
	// The sidebar's columns are left of the modal and still drawn.
	strip := strings.TrimSpace(strings.Join(sliceCols(rows, 0, m.sidebarWidth()), ""))
	if strip == "" {
		t.Error("the sidebar is blank behind the captain")
	}
	// So is the pane it floated over: its border still spans the region it was
	// given, on a row the modal does not cover.
	region := m.childBox()
	under := []rune(rows[len(rows)-1])
	if string(under[region.x]) != "╰" || string(under[region.x+region.w-1]) != "╯" {
		t.Errorf("the pane behind the captain is not drawn on the frame's last row: %q",
			string(under[region.x:region.x+region.w]))
	}
	if b.y+b.h >= len(rows) {
		t.Fatalf("the modal reaches the frame's last row (%d of %d), so that row proves nothing",
			b.y+b.h, len(rows))
	}
}

// sliceCols takes the same column range out of every row.
func sliceCols(rows []string, from, to int) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		r := []rune(row)
		if to > len(r) {
			to = len(r)
		}
		out = append(out, string(r[from:to]))
	}
	return out
}

// Every other pane is untouched. The modal is the captain's, not panes'.
func TestAWorkspacePaneStillFillsTheScreen(t *testing.T) {
	m, p := openedPane(t, allKinds())
	region, got := m.childBox(), m.boxOf(p)
	if got != region {
		t.Errorf("an agent pane got %+v, want the whole region %+v", got, region)
	}
}

// captainRegion's own arithmetic, at the sizes a live deck is awkward to build.
func TestTheCaptainsRegionHasAFloor(t *testing.T) {
	for _, tc := range []struct {
		what   string
		screen box
		in     box
		want   box
	}{{
		what:   "a big terminal gets the fraction",
		screen: box{w: 200, h: 40},
		in:     box{w: 200, h: 40},
		want:   box{x: 40, y: 4, w: 120, h: 32},
	}, {
		// Sized and centred against the screen; the region only clamps. The
		// screen-centred column, 40, is inside a strip 60 wide, so it is pushed to
		// the strip's edge — a modal off centre, rather than one drawn over chrome.
		what:   "the chrome's cells are not taken",
		screen: box{w: 200, h: 40},
		in:     box{x: 60, y: 1, w: 140, h: 39},
		want:   box{x: 60, y: 4, w: 120, h: 32},
	}, {
		// Under the floor on either axis the fraction is abandoned rather than
		// honoured — a clipped captain reads as a bug, not as a modal.
		what:   "too narrow for the floor keeps the whole region",
		screen: box{w: 70, h: 40},
		in:     box{w: 70, h: 40},
		want:   box{w: 70, h: 40},
	}, {
		what:   "too short for the floor keeps the whole region",
		screen: box{w: 200, h: 20},
		in:     box{w: 200, h: 20},
		want:   box{w: 200, h: 20},
	}, {
		// A half is already not the screen, and halving it again leaves a program
		// too little to be worth showing.
		what:   "a split's half is left alone",
		screen: box{w: 200, h: 40},
		in:     box{w: 100, h: 40, shared: true},
		want:   box{w: 100, h: 40, shared: true},
	}} {
		if got := captainRegion(tc.screen, tc.in); got != tc.want {
			t.Errorf("%s: captainRegion(%+v) = %+v, want %+v", tc.what, tc.in, got, tc.want)
		}
	}
}
