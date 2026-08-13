package deckui

import (
	"fmt"
	"strings"
	"testing"
)

// What a traced frame says about the pane cursor.
//
// #339 — the cursor a column from the insertion point — outlived a fix aimed at a
// divergence that is real but was not it, and no synthetic payload reproduces the
// symptom. Three columns can be wrong and look the same from outside: where the
// program put its cursor, where the emulator says it is, and where the deck drew
// it. So the trace reports all three against the frame that was composed, and
// these are the checks that it reports them usefully rather than plausibly.

// tracedFrame renders m with tracing on and returns the cursor line.
func tracedFrame(t *testing.T, m Model) string {
	t.Helper()
	var lines []string
	Trace = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { Trace = nil })
	_ = m.View()
	for _, l := range lines {
		if strings.HasPrefix(l, "cursor ") {
			return l
		}
	}
	t.Fatalf("no cursor line in the trace; got %v", lines)
	return ""
}

// TestATracedFrameSaysWhereThePaneCursorWent, in all three coordinate systems and
// with the row it landed on. A line missing any of them sends the next reader back
// to guessing, which is what this exists to end.
func TestATracedFrameSaysWhereThePaneCursorWent(t *testing.T) {
	m, s := openedSplit(t, "v")
	p, ok := s.focused().(*panePopover)
	if !ok {
		t.Fatalf("the focused half is a %T", s.focused())
	}
	f, ok := p.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", p.term)
	}
	f.setView("> hello")
	f.moveCursor(7, 0)

	line := tracedFrame(t, m)
	for _, want := range []string{"pane=(7,0)", "screen=", "box=", "rowtext ends "} {
		if !strings.Contains(line, want) {
			t.Errorf("the trace line does not say %s: %s", want, line)
		}
	}
}

// TestTheTraceReportsTheFramesOwnRow, not the pane's. The pane's screen is what a
// test can already read directly; the frame is what the terminal is handed, and a
// defect that lives in the composition between them is invisible in the pane's own
// view — which is precisely where #339 has not yet been ruled out.
func TestTheTraceReportsTheFramesOwnRow(t *testing.T) {
	m, s := openedSplit(t, "v")
	p, ok := s.focused().(*panePopover)
	if !ok {
		t.Fatalf("the focused half is a %T", s.focused())
	}
	f, ok := p.term.(*fakeTerm)
	if !ok {
		t.Fatalf("the terminal is a %T", p.term)
	}
	f.setView("marker")
	f.moveCursor(6, 0)

	line := tracedFrame(t, m)
	// The frame's row carries the pane's border, so a row that were only the
	// pane's own view could not contain one.
	if !strings.Contains(line, "│") {
		t.Errorf("the row in the trace has no pane border, so it is not the frame's: %s", line)
	}
	if !strings.Contains(line, "marker") {
		t.Errorf("the row in the trace does not contain the pane's text: %s", line)
	}
}
