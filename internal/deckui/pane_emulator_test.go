package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// namedEmulator is a hosted terminal that only answers which emulator it is. The
// bar asks nothing else of it.
type namedEmulator struct {
	vterm.Hosted
	name string
}

func (n namedEmulator) Emulator() string { return n.name }

// TestTheBarIsSilentAboutTheDefaultEmulator. Almost every pane runs on the
// default, so naming it would spend a column of a one-row bar telling you what
// is always true.
func TestTheBarIsSilentAboutTheDefaultEmulator(t *testing.T) {
	m, _ := openedPane(t, allKinds())
	bar := m.renderHostBar(200)
	if strings.Contains(bar, vterm.EmulatorXVT) || strings.Contains(bar, vterm.EmulatorGhostty) {
		t.Errorf("the bar names the default emulator: %q", bar)
	}
}

// TestTheBarNamesAnUnusualEmulator, because a comparison you cannot confirm
// you are inside is not a comparison. Asking "is this the ghostty build?" of a
// running deck was the whole reason for this.
func TestTheBarNamesAnUnusualEmulator(t *testing.T) {
	m, p := openedPane(t, allKinds())
	p.term = namedEmulator{Hosted: p.term, name: vterm.EmulatorGhostty}

	bar := m.renderHostBar(200)
	if !strings.Contains(bar, vterm.EmulatorGhostty) {
		t.Errorf("the bar does not say which emulator is behind the pane: %q", bar)
	}
	if !strings.Contains(bar, PaneLeaveKey) {
		t.Errorf("the bar lost the leave key to make room: %q", bar)
	}
}

// TestANamedEmulatorNeverCostsTheLeaveKey. The name is the least important thing
// on the row; the way out is the most. A narrow pane drops the label, then the
// name, and keeps the key.
func TestANamedEmulatorNeverCostsTheLeaveKey(t *testing.T) {
	m, p := openedPane(t, allKinds())
	p.term = namedEmulator{Hosted: p.term, name: vterm.EmulatorGhostty}

	for _, w := range []int{200, 80, 40, 24, 12, 4} {
		bar := m.renderHostBar(w)
		if strings.Contains(bar, "\n") {
			t.Errorf("at %d columns the bar wrapped: %q", w, bar)
		}
		if !strings.Contains(bar, PaneLeaveKey) {
			t.Errorf("at %d columns the bar dropped the leave key: %q", w, bar)
		}
	}
}

// Compile-time proof that a Hosted is all the bar needs, and that tea's types
// are what the interface speaks — if either drifts, this stops building.
var _ = func(h vterm.Hosted) (tea.Cmd, string) { return h.AwaitOutput(), h.Emulator() }
