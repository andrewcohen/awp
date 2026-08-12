package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// namedEmulator is a hosted terminal that only answers which emulator it is. The
// header asks nothing else of it.
type namedEmulator struct {
	vterm.Hosted
	name string
}

func (n namedEmulator) Emulator() string { return n.name }

// TestTheHeaderIsSilentAboutTheDefaultEmulator. Almost every pane runs on the
// default, so naming it would spend a column of a one-row header telling you what
// is always true.
func TestTheHeaderIsSilentAboutTheDefaultEmulator(t *testing.T) {
	m, p := openedPane(t, allKinds())
	header := p.header(&m, 200)
	if strings.Contains(header, vterm.EmulatorXVT) || strings.Contains(header, vterm.EmulatorGhostty) {
		t.Errorf("the header names the default emulator: %q", header)
	}
}

// TestTheHeaderNamesAnUnusualEmulator, because a comparison you cannot confirm
// you are inside is not a comparison. Asking "is this the ghostty build?" of a
// running deck was the whole reason for this.
func TestTheHeaderNamesAnUnusualEmulator(t *testing.T) {
	m, p := openedPane(t, allKinds())
	p.term = namedEmulator{Hosted: p.term, name: vterm.EmulatorGhostty}

	header := p.header(&m, 200)
	if !strings.Contains(header, vterm.EmulatorGhostty) {
		t.Errorf("the header does not say which emulator is behind the pane: %q", header)
	}
	if !strings.Contains(header, PaneLeaveKey) {
		t.Errorf("the header lost the leave key to make room: %q", header)
	}
}

// TestANamedEmulatorNeverCostsTheLeaveKey. The name is the least important thing
// on the row; the way out is the most. A narrow pane drops the label, then the
// name, and keeps the key.
func TestANamedEmulatorNeverCostsTheLeaveKey(t *testing.T) {
	m, p := openedPane(t, allKinds())
	p.term = namedEmulator{Hosted: p.term, name: vterm.EmulatorGhostty}

	for _, w := range []int{200, 80, 40, 24, 12, 4} {
		header := p.header(&m, w)
		if strings.Contains(header, "\n") {
			t.Errorf("at %d columns the header wrapped: %q", w, header)
		}
		if !strings.Contains(header, PaneLeaveKey) {
			t.Errorf("at %d columns the header dropped the leave key: %q", w, header)
		}
	}
}

// Compile-time proof that a Hosted is all the header needs, and that tea's types
// are what the interface speaks — if either drifts, this stops building.
var _ = func(h vterm.Hosted) (tea.Cmd, string) { return h.AwaitOutput(), h.Emulator() }
