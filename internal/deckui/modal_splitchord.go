package deckui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// splitChordModal is the `|` key: a keys-only menu whose answer names the right
// half of a split. Like the PR chord it renders the row list beneath itself and
// puts its menu in the status bar, so the thing you are about to split is still
// on screen while you pick.
//
// `|` because in the diff viewer it already means "two things side by side", and
// at deck level it was free. The gesture is the same one either way.
type splitChordModal struct {
	// item is the row the chord was armed on, captured so the second key acts on
	// what you were pointing at rather than on wherever the cursor has since
	// been moved by a refresh.
	item Item
}

// splitAction is one answer to the chord: the key you press and the kind it puts
// on the right.
type splitAction struct {
	key   string
	kind  string
	label string
}

// splitActions is the menu, in the order it reads. The keys are the deck's own
// window keys, so `|c` is `c`-beside-the-agent and nothing has to be learned
// twice.
//
// `a` is deliberately absent: the left half is the agent, so `|a` would be the
// agent beside itself. It is refused by name rather than left to fall through to
// the cancel branch, because silently cancelling on a key that looks like it
// should work reads as the chord being broken.
var splitActions = []splitAction{
	{key: "c", kind: SplitKindDiff, label: "diff"},
	{key: "v", kind: "vcs", label: "vcs"},
	{key: "e", kind: "editor", label: "editor"},
	{key: "s", kind: "", label: "shell"},
	{key: "i", kind: PaneKindCI, label: "ci"},
	{key: "W", kind: PaneKindWatch, label: "watch"},
}

// splitChordHint is the menu as the status bar shows it.
func splitChordHint() string {
	parts := make([]string, 0, len(splitActions)+1)
	for _, a := range splitActions {
		parts = append(parts, a.key+" "+a.label)
	}
	parts = append(parts, "esc cancel")
	return "split with agent: " + strings.Join(parts, " · ")
}

// splitHelpKeys is the chord as the `?` overlay lists it.
func splitHelpKeys() [][2]string {
	var keys [][2]string
	for _, a := range splitActions {
		keys = append(keys, [2]string{"|" + a.key, "agent beside " + a.label})
	}
	return keys
}

func (c *splitChordModal) update(m *Model, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	pressed := key.String()
	if pressed == "a" {
		m.active = nil
		m.status = "split: the left half is the agent — pick something to put beside it"
		return nil
	}
	for _, a := range splitActions {
		if a.key != pressed {
			continue
		}
		m.active = nil
		cmd, _ := m.openSplit(c.item, a.kind)
		return cmd
	}
	// Anything else cancels, esc included. A mistyped key must not fall through
	// to the row list and do something else instead.
	m.active = nil
	m.status = ""
	return nil
}

func (c *splitChordModal) footerHelp() string { return "" }

func (c *splitChordModal) view(m *Model, b box) (left, right string) {
	return m.renderList(b.w), ""
}
