package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/andrewcohen/awp/internal/charm"
)

// What the diff pane shows while the keyboard is in another pane.
//
// The band already goes away (see the focus tests around rowBanded). What was left
// was the rest of the selection treatment at full strength: a bold yellow `┃` and a
// full-width yellow file divider, both following the cursor as the file list or
// comment index seeked — motion, in the selection hue, in a pane whose keys are
// dead and right next to the pane that actually has them.

func TestTheSelectionBarGoesMutedWhileTheDiffPaneIsUnfocused(t *testing.T) {
	banded := selectionBarStyle(true).GetForeground()
	idle := selectionBarStyle(false).GetForeground()
	if banded != lipgloss.Color(charm.Warning) {
		t.Errorf("the focused marker is the app-wide selection hue; got %v", banded)
	}
	if idle != lipgloss.Color(charm.Muted) {
		t.Errorf("the unfocused marker should be muted; got %v", idle)
	}
	if banded == idle {
		t.Error("the two tiers must be distinguishable, or unfocusing changes nothing")
	}
}

// The marker itself stays. It is where the keys come back to, so muting it is the
// whole of the change — removing it would lose the place instead.
func TestTheSelectionBarIsStillDrawnWhileUnfocused(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.cursorRow = 3
	m.focus = FocusFiles
	row := stripANSI(m.renderStreamRowAt(3, 60))
	if !strings.HasPrefix(row, strings.TrimSpace(selectionPrefixBar)) {
		t.Fatalf("expected the cursor row to keep its marker while unfocused, got %q", row)
	}
}

func TestTheFileDividerCarriesTheSelectionHueOnlyWhileFocused(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	m.filesCursor = 0

	m.focus = FocusHunks
	if !m.fileRuleActive(0) {
		t.Error("the cursor's file should carry the hue while the diff pane is focused")
	}
	if m.fileRuleActive(1) {
		t.Error("only the cursor's file carries it")
	}

	m.focus = FocusFiles
	if m.fileRuleActive(0) || m.fileRuleActive(1) {
		t.Error("no divider carries the selection hue while another pane has the keyboard")
	}
}

// The divider's hue follows filesCursor and the focus, and neither of those goes
// through rebuildStream — so both have to be in the row's cache key or the pane
// keeps serving the previous frame's divider. Asserted as the key changing rather
// than as rendered output: lipgloss strips colour with no TTY, so a stale hue is
// invisible to a string comparison and this test would pass while the bug was live.
func TestTheDividerHueIsInTheRowCacheKey(t *testing.T) {
	m := streamModel(t, twoFiles()...)
	headerOfB := m.stream.fileStart[1]

	// Park the cursor inside the first file's body, so file b's divider is neither
	// the cursor row nor selected — nothing else in the key moves, which is what
	// makes this the case a key without fileRule got wrong.
	m.cursorRow = 2
	m.syncFileCursorToCursor()
	before := m.rowKeyAt(headerOfB, 60)

	// Walking the cursor into file b's body moves filesCursor without touching the
	// stream, so nothing drops the cache.
	m.cursorRow = headerOfB + 2
	m.syncFileCursorToCursor()
	if m.filesCursor != 1 {
		t.Fatalf("expected the cursor to be in file b, filesCursor = %d", m.filesCursor)
	}
	if got := m.rowKeyAt(headerOfB, 60); got == before {
		t.Fatal("moving the cursor into a file must change its divider's cache key")
	}

	// And the same row, same cursor, with the keyboard moved to another pane.
	focused := m.rowKeyAt(headerOfB, 60)
	m.focus = FocusFiles
	if got := m.rowKeyAt(headerOfB, 60); got == focused {
		t.Fatal("unfocusing the diff pane must change the divider's cache key")
	}
}
