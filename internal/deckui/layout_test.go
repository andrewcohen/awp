package deckui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func chromeProbeModel(w, h int) Model {
	m := New([]Item{
		{ProjectName: "awp", WorkspaceName: "test", Path: "/w/awp/test", RepoRoot: "/r/awp"},
		{ProjectName: "site", WorkspaceName: "hero", Path: "/w/site/hero", RepoRoot: "/r/site"},
	}, nil)
	m.width, m.height = w, h
	(&m).clampDeckViewport()
	return m
}

// What the deck spends on itself, stated as rows rather than as a feeling.
//
// The frame is: the title row, the blank under it, the workspace list, then the
// status bar on the terminal's last row. Nothing else. A regression here is not
// a crash — it is a row of the list quietly going missing — so it is pinned.
func TestTheDeckSpendsThreeRowsOnChrome(t *testing.T) {
	const h = 24
	m := chromeProbeModel(80, h)
	lines := strings.Split(m.render(), "\n")
	if len(lines) != h {
		t.Fatalf("frame is %d rows, terminal is %d", len(lines), h)
	}

	if got := strings.TrimSpace(ansi.Strip(lines[0])); !strings.Contains(got, "scope:") {
		t.Errorf("row 0 should be the title row, got %q — a blank here is a workspace row spent on nothing", got)
	}
	if got := strings.TrimSpace(ansi.Strip(lines[1])); got != "" {
		t.Errorf("row 1 should be the one gap the deck keeps, got %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(lines[h-1])); got == "" {
		t.Errorf("the status bar should be on the last row; it is blank")
	}

	// deckHeaderRows + panelRows + footerRows, said the other way round.
	if want := h - deckHeaderRows - panelRows - footerRows; m.deckBodyCapacity() != want {
		t.Errorf("capacity is %d, want %d — the constant and the render disagree", m.deckBodyCapacity(), want)
	}
}

// The capacity constant is only right if the list actually gets those rows. An
// over-reserving chrome does not shrink the frame, it converts list rows into a
// dead band above the footer, which is how the diff modal's version of this bug
// went unnoticed.
func TestCapacityMatchesTheRowsTheListGets(t *testing.T) {
	for _, h := range []int{12, 24, 40, 60} {
		m := chromeProbeModel(80, h)
		body := m.renderList(m.width)
		rows := len(strings.Split(body, "\n"))
		want := deckHeaderRows + panelRows + m.deckBodyCapacity()
		if rows != want {
			t.Errorf("h=%d: the row-list panel renders %d rows, capacity math says %d", h, rows, want)
		}
	}
}
