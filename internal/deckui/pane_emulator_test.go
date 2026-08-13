package deckui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

// namedEmulator is a hosted terminal that only answers which emulator it is,
// which is all a test about naming one needs.
type namedEmulator struct {
	vterm.Hosted
	name string
}

func (n namedEmulator) Emulator() string { return n.name }

// TestTheBarNeverNamesTheEmulator, whichever one is behind the pane.
//
// It used to name an unusual one, which was right while libghostty-vt was a thing
// being tried: a comparison you cannot confirm you are inside is not a
// comparison. The answer is settled, and the row was crowded — so the segment is
// gone, and it should not come back on a whim, because it costs the columns the
// hosted workspace's own state is now spending.
func TestTheBarNeverNamesTheEmulator(t *testing.T) {
	m, p := openedPane(t, allKinds())
	for _, name := range []string{vterm.EmulatorXVT, vterm.EmulatorGhostty} {
		p.term = namedEmulator{Hosted: p.term, name: name}
		if bar := m.renderHostBar(200); strings.Contains(bar, name) {
			t.Errorf("the bar names the %s emulator: %q", name, bar)
		}
	}
}

// Compile-time proof that a Hosted is all the bar needs, and that tea's types
// are what the interface speaks — if either drifts, this stops building.
var _ = func(h vterm.Hosted) (tea.Cmd, string) { return h.AwaitOutput(), h.Emulator() }
