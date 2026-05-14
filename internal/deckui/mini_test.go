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
	rows := []MiniRow{
		{Project: "redwood", Workspace: "react-compiler", Status: "working"},
		{Project: "redwood", Workspace: "router-rewrite", Status: "waiting"},
	}
	m := NewMiniModel(rows)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = next.(MiniModel)
	// Both share "r" as first letter, so both should get 2-char hints
	// and "r" should register as a pending prefix.
	if !m.findPrefix['r'] {
		t.Fatalf("expected r to be a pending prefix, got: %v", m.findPrefix)
	}
	// Press "r" — should not jump yet, should set pending.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(MiniModel)
	if m.findPending != 'r' {
		t.Fatalf("expected pending=r, got %q", m.findPending)
	}
	if !m.FindMode() {
		t.Fatal("should still be in find mode after first key of two-char hint")
	}
	// Press the second char of whichever hint row 0 got.
	hint := m.findHints[0]
	if len([]rune(hint)) != 2 {
		t.Fatalf("expected row 0 to have a 2-char hint, got %q", hint)
	}
	second := []rune(hint)[1]
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{second}})
	m = next.(MiniModel)
	if m.FindMode() {
		t.Fatal("find mode should exit after the second char completes")
	}
	if m.Cursor() != 0 {
		t.Fatalf("cursor should be 0, got %d", m.Cursor())
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
