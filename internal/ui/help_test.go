package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestQuestionMarkOpensAndClosesTheHelp(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	if m.HelpVisible() {
		t.Fatal("help should start closed")
	}
	m = press(m, "?")
	if !m.HelpVisible() {
		t.Fatal("expected ? to open the help")
	}
	// Every documented way out closes it, and nothing else does.
	for _, k := range []string{"?", "esc", "q", "enter"} {
		m.showHelp = true
		m = press(m, k)
		if m.HelpVisible() {
			t.Fatalf("expected %q to close the help", k)
		}
	}
}

// The overlay owns the keyboard: keys behind it would move a cursor nobody can
// see, and `q` in particular must close the help rather than the whole view.
func TestHelpSwallowsKeysWhileOpen(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.focus = FocusHunks
	m = press(m, "?")
	before := m.cursorRow
	for _, k := range []string{"j", "k", "G", "g", "tab", "w"} {
		m.showHelp = true
		m = press(m, k)
		if m.cursorRow != before {
			t.Fatalf("key %q moved the cursor behind the help: %d → %d", k, before, m.cursorRow)
		}
	}
}

// It replaces the panes rather than floating over them, so the body keeps
// exactly the height the host budgeted and the footer does not shift.
func TestHelpOverlayKeepsTheBodySize(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	const width, height = 100, 14
	panes := m.Body(width, height)
	m = press(m, "?")
	overlay := m.Body(width, height)
	if got, want := strings.Count(overlay, "\n"), strings.Count(panes, "\n"); got != want {
		t.Fatalf("help body is %d lines, panes are %d", got+1, want+1)
	}
	for _, line := range strings.Split(overlay, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("help line exceeds width %d (got %d): %q", width, w, line)
		}
	}
}

// Narrowing must truncate, not wrap — a wrapping overlay grows taller as the
// terminal shrinks and then overflows the body it was sized to.
func TestHelpOverlayTruncatesInsteadOfWrapping(t *testing.T) {
	const height = 20
	wide := strings.Count(renderHelpOverlay(newHelpViewport(200, height), 200, height), "\n")
	narrow := strings.Count(renderHelpOverlay(newHelpViewport(60, height), 60, height), "\n")
	if narrow != wide {
		t.Fatalf("overlay height depends on width: 200 → %d lines, 60 → %d", wide+1, narrow+1)
	}
}

// The reference scrolls. There are more bindings than fit a short terminal, and
// clipping them would hide part of the keymap while looking complete.
func TestHelpScrollsWhenItDoesNotFit(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetSize(100, 12)
	m = press(m, "?")
	if m.helpVP.AtBottom() {
		t.Fatal("fixture is wrong: the reference should overflow a 12-row body")
	}
	top := stripANSI(m.Body(100, 12))
	m = press(m, "ctrl+d")
	if m.helpVP.YOffset == 0 {
		t.Fatal("expected ctrl+d to scroll the reference")
	}
	if scrolled := stripANSI(m.Body(100, 12)); scrolled == top {
		t.Fatal("expected the visible content to change after scrolling")
	}
	// Reopening starts at the top again — the reader asked for the reference, not
	// for wherever they were in it last time.
	m = press(m, "esc")
	m = press(m, "?")
	if m.helpVP.YOffset != 0 {
		t.Fatalf("expected a fresh open to start at the top, got offset %d", m.helpVP.YOffset)
	}
}

// A resize keeps the reader's place rather than snapping back to the top.
func TestHelpKeepsItsScrollAcrossAResize(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetSize(100, 12)
	m = press(m, "?")
	m = press(m, "ctrl+d")
	at := m.helpVP.YOffset
	if at == 0 {
		t.Fatal("fixture is wrong: expected to have scrolled")
	}
	m.SetSize(90, 12)
	if got := m.helpVP.YOffset; got != at {
		t.Fatalf("resize moved the reader from offset %d to %d", at, got)
	}
}

// The overlay is the only place the keymap is written down, so a binding the
// switch in handleKey answers has to appear in it.
func TestHelpListsTheBindingsThatExist(t *testing.T) {
	// Read the content directly rather than through the viewport, which shows only
	// what fits — this asserts the reference is complete, not what is on screen.
	view := stripANSI(helpContent(120))
	for _, k := range []string{"c", "i", "D", "r", "R", "T", "w", "/", "e", "ctrl+r", "ctrl+s", "tab"} {
		if !strings.Contains(view, k) {
			t.Fatalf("expected %q documented in the help:\n%s", k, view)
		}
	}
}

// The footer is an affordance now, not a legend.
func TestFooterPointsAtHelpInsteadOfListingKeys(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	footer := stripANSI(m.renderFooter())
	if !strings.Contains(footer, "? help") {
		t.Fatalf("expected the footer to point at ?:\n%s", footer)
	}
	// The old legend's giveaways.
	for _, gone := range []string{"j/k:scroll", "c:comment", "e:$EDITOR", "file(s) changed"} {
		if strings.Contains(footer, gone) {
			t.Fatalf("expected %q gone from the footer:\n%s", gone, footer)
		}
	}
}

// The file count went with it: the file list is already headed "Files (n)".
func TestLoadedStatusDoesNotRepeatTheFileCount(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"), fileWith("b.go", 1, "beta"))
	status, isErr := m.Status()
	if isErr {
		t.Fatalf("unexpected error status %q", status)
	}
	if strings.Contains(status, "file") {
		t.Fatalf("expected no file count in the status, got %q", status)
	}
	if !strings.Contains(stripANSI(m.renderFileList(30, 8)), "Files (2)") {
		t.Fatal("expected the file list to carry the count instead")
	}
}

// An empty diff still says so — otherwise it reads as a failure to load.
func TestEmptyDiffStillReportsNoChanges(t *testing.T) {
	m := commentModel(t)
	status, _ := m.Status()
	if status != "no changes" {
		t.Fatalf("expected \"no changes\", got %q", status)
	}
}
