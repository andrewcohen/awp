package deckui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Clicking a row on the strip.
//
// The thing that can silently go wrong is the coordinate: a click arrives as a screen
// row, and the strip's lines are at an offset — the box's top plus the strip's own
// padding — with headers and separators between the rows. Off by one and you open the
// workspace above the one you pointed at, which nothing about the screen would
// explain.

// sidebarRowLine is the screen row a given workspace's name is drawn on, found by
// walking the same layout the renderer does. Read from the layout rather than
// computed, so the test does not encode the pattern it is checking.
func sidebarRowLine(t *testing.T, m Model, workspace string) int {
	t.Helper()
	b := m.childBox()
	for i, l := range m.sidebarLines(box{w: m.sidebarWidth(), h: b.h}) {
		if l.item != nil && l.item.WorkspaceName == workspace && strings.Contains(ansi.Strip(l.text), workspace) {
			return b.y + sidebarPadY + i
		}
	}
	t.Fatalf("%q is not on the strip", workspace)
	return 0
}

// TestClickingARowGoesToIt. The whole point: the strip stops being read-only for the
// one gesture that carries its own target.
func TestClickingARowGoesToIt(t *testing.T) {
	m, _ := sidebarPane(t)
	y := sidebarRowLine(t, m, "busy")
	if _, ok := m.sidebarMouse(clickAtXY(4, y)); !ok {
		t.Fatalf("a click on the row at line %d was not consumed", y)
	}
	p, isPane := m.active.(*panePopover)
	if !isPane {
		t.Fatalf("no pane opened: active is %T, status %q", m.active, m.status)
	}
	if p.workspace != "busy" {
		t.Errorf("the click opened %q, want busy", p.workspace)
	}
}

// TestClickingARowMovesTheCursorToIt, so leaving the pane lands the row list on the
// workspace you were just in. Without this the two surfaces desync silently: you come
// back out and the selection is wherever it was before.
func TestClickingARowMovesTheCursorToIt(t *testing.T) {
	m, _ := sidebarPane(t)
	m.cursor = 0
	y := sidebarRowLine(t, m, "read-me")
	m.sidebarMouse(clickAtXY(4, y))
	it, ok := m.selected()
	if !ok {
		t.Fatal("nothing is selected after the click")
	}
	if it.WorkspaceName != "read-me" {
		t.Errorf("the cursor is on %q, want read-me", it.WorkspaceName)
	}
}

// TestClickingEitherLineOfARowIsTheSameRow. A row is two lines and reads as one
// thing; a target half the size of what it looks like is a target you miss.
func TestClickingEitherLineOfARowIsTheSameRow(t *testing.T) {
	m, _ := sidebarPane(t)
	name := sidebarRowLine(t, m, "busy")
	for _, y := range []int{name, name + 1} {
		fresh, _ := sidebarPane(t)
		fresh.sidebarMouse(clickAtXY(4, y))
		p, isPane := fresh.active.(*panePopover)
		if !isPane {
			t.Fatalf("line %d opened nothing: active is %T, status %q", y, fresh.active, fresh.status)
		}
		if p.workspace != "busy" {
			t.Errorf("line %d opened %q, want busy", y, p.workspace)
		}
	}
}

// TestClickingAHeaderDoesNothingButIsStillOurs. The strip's columns belong to the
// strip whether or not the line under the pointer is a row — a press that fell
// through would reach the child at a negative column.
func TestClickingAHeaderDoesNothingButIsStillOurs(t *testing.T) {
	m, _ := sidebarPane(t)
	b := m.childBox()
	header := -1
	for i, l := range m.sidebarLines(box{w: m.sidebarWidth(), h: b.h}) {
		if l.item == nil && strings.TrimSpace(ansi.Strip(l.text)) != "" {
			header = b.y + sidebarPadY + i
			break
		}
	}
	if header < 0 {
		t.Fatal("the strip has no section header to click")
	}
	before := m.active
	if _, ok := m.sidebarMouse(clickAtXY(4, header)); !ok {
		t.Error("a click on a header fell through to the child")
	}
	if m.active != before {
		t.Errorf("a click on a header changed what is on screen: %T", m.active)
	}
}

// TestAClickBesideTheStripIsNotOurs. The columns to the right of the edge are the
// child's, and a press there has to reach the program in it.
func TestAClickBesideTheStripIsNotOurs(t *testing.T) {
	m, _ := sidebarPane(t)
	y := sidebarRowLine(t, m, "busy")
	if _, ok := m.sidebarMouse(clickAtXY(m.sidebarWidth()+4, y)); ok {
		t.Error("a click beside the strip was consumed by it")
	}
}

// TestTheWheelOverTheStripIsNotClaimed. Nothing in here scrolls — overflow is a
// count — so claiming the wheel would make it dead over these columns rather than
// merely ineffective.
func TestTheWheelOverTheStripIsNotClaimed(t *testing.T) {
	m, _ := sidebarPane(t)
	y := sidebarRowLine(t, m, "busy")
	if _, ok := m.sidebarMouse(wheelAtXY(4, y)); ok {
		t.Error("the wheel over the strip was consumed")
	}
}

// TestClickingARowOnANarrowerStripStillHitsIt. The hit test derives its origin from
// the same width the renderer does, so a dragged strip must not shift the rows out
// from under the pointer.
func TestClickingARowOnANarrowerStripStillHitsIt(t *testing.T) {
	m, _ := sidebarPane(t)
	m = m.WithSidebarWidth(sidebarMinWidth)
	y := sidebarRowLine(t, m, "busy")
	m.sidebarMouse(clickAtXY(2, y))
	p, isPane := m.active.(*panePopover)
	if !isPane {
		t.Fatalf("no pane opened: active is %T, status %q", m.active, m.status)
	}
	if p.workspace != "busy" {
		t.Errorf("the click opened %q, want busy", p.workspace)
	}
}
