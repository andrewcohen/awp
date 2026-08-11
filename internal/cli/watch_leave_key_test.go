package cli

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/deckui"
)

// quitsOn reports whether the watch view asks to quit on this key.
func quitsOn(t *testing.T, key tea.KeyPressMsg) bool {
	t.Helper()
	_, cmd := watchModel{}.Update(key)
	if cmd == nil {
		return false
	}
	_, quit := cmd().(tea.QuitMsg)
	return quit
}

// TestTheWatchViewLeavesOnThePaneLeaveKey. `W` runs this view, and under a deck
// that hands the terminal over it is the program holding the pane — the deck is
// suspended, so the key that closes every other pane is not being read by
// anything but this program. Without answering it here the pane has no exit on
// the key the chrome advertises, and the only way out is knowing that `q` also
// works.
func TestTheWatchViewLeavesOnThePaneLeaveKey(t *testing.T) {
	if !quitsOn(t, tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}) {
		t.Fatalf("%s did not leave the watch view", deckui.PaneLeaveKey)
	}
}

// TestTheWatchViewsOwnKeysStillLeave, so the above is an addition rather than a
// replacement: a watch view opened from a shell has no pane around it and q is
// what anyone would reach for.
func TestTheWatchViewsOwnKeysStillLeave(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: tea.KeyEsc},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		if !quitsOn(t, key) {
			t.Errorf("%q no longer leaves the watch view", key.String())
		}
	}
}

// TestTheWatchViewKeepsEveryOtherKey. The view is read-only and has no bindings
// of its own, but that is not a licence to quit on anything: a key it does not
// know is one a later version might use, and a pane that closes on a stray
// keystroke loses whatever the user was reading.
func TestTheWatchViewKeepsEveryOtherKey(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'j', Text: "j"},
		{Code: tea.KeyEnter},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: ' ', Text: " "},
	} {
		if quitsOn(t, key) {
			t.Errorf("%q left the watch view", key.String())
		}
	}
}
