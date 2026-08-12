package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/charm"
)

// paneLeave is the key as a program actually receives it. Not runeKey: the
// binding is matched on msg.String(), which is derived from the code and the
// modifier rather than from any text.
func paneLeave() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}
}

// TestPaneLeaveKeyLeavesTheViewer. This view can be the whole program inside a
// pane — `awp diff` in one, or a workspace's review window — and there the key
// that gives the terminal back is the pane's, not q's. Under a handed-over pane
// the deck is suspended and reading nothing, so nothing above the viewer can
// answer for it.
func TestPaneLeaveKeyLeavesTheViewer(t *testing.T) {
	if got := paneLeave().String(); got != charm.PaneLeaveKey {
		t.Fatalf("the test's key spells %q, the binding is %q", got, charm.PaneLeaveKey)
	}
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	_, cmd := m.Update(paneLeave())
	if cmd == nil {
		t.Fatal("ctrl+\\ scheduled nothing, so the view stays up")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+\\ produced %T, want tea.QuitMsg", cmd())
	}
}

// TestPaneLeaveIsInTheHelp. viewerKeyGroups is the canonical binding surface for
// this view, so a key bound without a row there is a key nobody will find.
//
// Asserted against the groups rather than the rendered overlay: the overlay
// scrolls, so a row can be perfectly present and simply below the fold.
func TestPaneLeaveIsInTheHelp(t *testing.T) {
	for _, g := range viewerKeyGroups(nil) {
		for _, row := range g.Keys {
			if strings.Contains(row[0], charm.PaneLeaveKey) {
				return
			}
		}
	}
	t.Errorf("no help row mentions %q", charm.PaneLeaveKey)
}

// TestTheComposeBoxKeepsThePaneLeaveKey. Every other close key is swallowed
// while a modal owns the keyboard, and this one is no different: leaving the view
// mid-comment would discard what was typed without asking.
func TestTheComposeBoxKeepsThePaneLeaveKey(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.focus = FocusHunks
	m = press(m, "c")
	if !m.Filtering() {
		t.Fatal("expected the compose box to own the keyboard")
	}
	updated, cmd := m.Update(paneLeave())
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("ctrl+\\ quit the view out from under an open compose box")
		}
	}
	if !updated.(Model).Filtering() {
		t.Error("ctrl+\\ closed the compose box")
	}
}
