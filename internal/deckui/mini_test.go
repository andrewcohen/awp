package deckui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMiniModelNavigationAndSelect(t *testing.T) {
	rows := []MiniRow{
		{Project: "alpha", Workspace: "one", Status: "working"},
		{Project: "alpha", Workspace: "two", Status: "waiting"},
		{Project: "beta", Workspace: "three", Unread: true, Status: "idle"},
	}
	m := NewMiniModel(rows)
	// j moves down, k moves back up.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = next.(MiniModel)
	if m.Cursor() != 1 {
		t.Fatalf("after j: want cursor=1 got %d", m.Cursor())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = next.(MiniModel)
	if m.Cursor() != 0 {
		t.Fatalf("after k: want cursor=0 got %d", m.Cursor())
	}
	// G jumps to end, then enter selects.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = next.(MiniModel)
	if m.Cursor() != 2 {
		t.Fatalf("after G: want cursor=2 got %d", m.Cursor())
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(MiniModel)
	if m.Chosen() == nil || m.Chosen().Workspace != "three" {
		t.Fatalf("expected chosen=three, got %+v", m.Chosen())
	}
	if cmd == nil {
		t.Fatal("enter should return tea.Quit")
	}
}

func TestMiniModelQuitWithoutSelection(t *testing.T) {
	m := NewMiniModel([]MiniRow{{Project: "a", Workspace: "b", Status: "working"}})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(MiniModel)
	if m.Chosen() != nil {
		t.Fatalf("esc should not select, got %+v", m.Chosen())
	}
	if cmd == nil {
		t.Fatal("esc should return tea.Quit")
	}
}

func TestMiniModelViewEmpty(t *testing.T) {
	m := NewMiniModel(nil)
	view := m.View()
	if !strings.Contains(view, "Nothing waiting") {
		t.Fatalf("empty view should explain itself, got: %q", view)
	}
}

func TestMiniModelFindModeJumpsCursor(t *testing.T) {
	rows := []MiniRow{
		{Project: "alpha", Workspace: "one", Status: "working"},
		{Project: "beta", Workspace: "two", Status: "waiting"},
		{Project: "gamma", Workspace: "three", Unread: true, Status: "idle"},
	}
	m := NewMiniModel(rows)
	// f enters find mode and assigns hints.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(MiniModel)
	if !m.FindMode() {
		t.Fatal("expected find mode after pressing f")
	}
	if len(m.findHints) != len(rows) {
		t.Fatalf("expected hint per row, got %d", len(m.findHints))
	}
	// Distinct first-letters should give 1-char hints ("a", "b", "g").
	wantHints := map[int]string{0: "a", 1: "b", 2: "g"}
	for i, want := range wantHints {
		if got := m.findHints[i]; got != want {
			t.Fatalf("row %d hint: want %q got %q", i, want, got)
		}
	}
	// Typing 'g' should jump to gamma/three and exit find mode.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = next.(MiniModel)
	if m.FindMode() {
		t.Fatal("find mode should exit after a successful match")
	}
	if m.Cursor() != 2 {
		t.Fatalf("cursor should be on gamma/three (idx 2), got %d", m.Cursor())
	}
}

func TestMiniModelFindModeTwoCharHint(t *testing.T) {
	// The hint keys are "<project>/<workspace>": "a/a" and "a/aa" share
	// their only letter, so the first claims the single 'a' and the second
	// overflows to a two-key hint — exercising the pending-prefix flow.
	rows := []MiniRow{
		{Project: "a", Workspace: "a", Status: "working"},
		{Project: "a", Workspace: "aa", Status: "waiting"},
	}
	m := NewMiniModel(rows)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(MiniModel)

	// Find the row that overflowed to a two-key hint.
	twoKeyIdx, runes := -1, []rune(nil)
	for idx, hint := range m.findHints {
		if r := []rune(hint); len(r) == 2 {
			twoKeyIdx, runes = idx, r
			break
		}
	}
	if twoKeyIdx < 0 {
		t.Fatalf("expected one row on a two-key hint, got %v", m.findHints)
	}
	if !m.findPrefix[runes[0]] {
		t.Fatalf("expected %q to be a pending prefix, got: %v", string(runes[0]), m.findPrefix)
	}

	// Press the first key — should not jump yet, should set pending.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{runes[0]}})
	m = next.(MiniModel)
	if m.findPending != runes[0] {
		t.Fatalf("expected pending=%q, got %q", string(runes[0]), m.findPending)
	}
	if !m.FindMode() {
		t.Fatal("should still be in find mode after first key of two-char hint")
	}

	// Press the second key — completes the hint and jumps.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{runes[1]}})
	m = next.(MiniModel)
	if m.FindMode() {
		t.Fatal("find mode should exit after the second char completes")
	}
	if m.Cursor() != twoKeyIdx {
		t.Fatalf("cursor should be %d, got %d", twoKeyIdx, m.Cursor())
	}
}

func TestMiniModelFindModeEscCancels(t *testing.T) {
	rows := []MiniRow{
		{Project: "a", Workspace: "x", Status: "working"},
		{Project: "b", Workspace: "y", Status: "working"},
	}
	m := NewMiniModel(rows)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(MiniModel)
	if !m.FindMode() {
		t.Fatal("expected find mode")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(MiniModel)
	if m.FindMode() {
		t.Fatal("esc should leave find mode")
	}
	if m.Chosen() != nil {
		t.Fatal("esc inside find should not select")
	}
}

func TestMiniModelFindModeUnknownKeyStaysInMode(t *testing.T) {
	rows := []MiniRow{
		{Project: "alpha", Workspace: "one", Status: "working"},
	}
	m := NewMiniModel(rows)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(MiniModel)
	// "z" is not assigned to any row and not a known prefix → no-op.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = next.(MiniModel)
	if !m.FindMode() {
		t.Fatal("unknown key should not exit find mode")
	}
}

func TestMiniModelViewIncludesProjectAndWorkspace(t *testing.T) {
	m := NewMiniModel([]MiniRow{
		{Project: "proj", Workspace: "myws", Status: "waiting"},
	})
	m, _ = func() (MiniModel, tea.Cmd) {
		nm, cmd := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
		return nm.(MiniModel), cmd
	}()
	view := m.View()
	if !strings.Contains(view, "proj") {
		t.Fatalf("expected project in view, got: %q", view)
	}
	if !strings.Contains(view, "myws") {
		t.Fatalf("expected workspace in view, got: %q", view)
	}
}
