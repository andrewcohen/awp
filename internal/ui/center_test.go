package ui

import (
	"strings"
	"testing"
)

// zzModel is a viewer on a change tall enough to scroll, focused on the diff.
func zzModel(t *testing.T) Model {
	t.Helper()
	lines := make([]string, 0, 200)
	for i := range 200 {
		lines = append(lines, "line "+string(rune('a'+i%26))+strings.Repeat("x", i%7))
	}
	m := commentModel(t, fileWith("a.go", 1, lines...))
	m.focus = FocusHunks
	if len(m.stream.rows) < 100 {
		t.Fatalf("fixture is wrong: only %d rows to scroll through", len(m.stream.rows))
	}
	return m
}

// zz puts the cursor's row in the middle of the pane. Scrolling with j leaves it
// pinned to the bottom margin, which is the whole reason to want this: you have
// read down to something and now want to see what is around it.
func TestZZCentresTheDiffOnTheCursor(t *testing.T) {
	m := zzModel(t)
	// Walk down until the cursor is against the bottom of the pane.
	m = pressTimes(m, "j", 60)
	before := m.streamScroll
	if m.cursorRow-before < m.streamContentHeight()/2 {
		t.Fatalf("fixture is wrong: the cursor is already in the top half (row %d, scroll %d, height %d)",
			m.cursorRow, before, m.streamContentHeight())
	}

	row := m.cursorRow
	m = press(m, "z")
	if !m.pendingZ {
		t.Fatal("z should open the chord and wait")
	}
	if m.streamScroll != before {
		t.Fatalf("z on its own scrolled the pane from %d to %d", before, m.streamScroll)
	}
	m = press(m, "z")

	if m.pendingZ {
		t.Error("the chord is still open after its second key")
	}
	// The cursor stays where it was: zz is about the scroll, not the selection.
	if m.cursorRow != row {
		t.Errorf("zz moved the cursor from %d to %d", row, m.cursorRow)
	}
	if want := row - m.streamContentHeight()/2; m.streamScroll != want {
		t.Errorf("after zz the scroll is %d, want %d — the cursor is not centred", m.streamScroll, want)
	}
}

// Near the top there is nothing to scroll away, so zz clamps rather than
// scrolling to a negative offset and blanking the pane.
func TestZZNearTheTopClampsInsteadOfOverscrolling(t *testing.T) {
	m := zzModel(t)
	m = pressTimes(m, "j", 2)
	m = press(m, "z")
	m = press(m, "z")
	if m.streamScroll != 0 {
		t.Errorf("zz two rows in scrolled to %d, want 0", m.streamScroll)
	}
}

// Any other second key cancels. A mistyped chord must not fall through and do
// whatever that letter means on its own — `zc` opening the compose box would be
// a comment nobody asked to write.
func TestAnyOtherKeyCancelsTheZChord(t *testing.T) {
	for _, k := range []string{"esc", "c", "j", "q", "G"} {
		m := zzModel(t)
		m = pressTimes(m, "j", 40)
		row, scroll := m.cursorRow, m.streamScroll

		m = press(m, "z")
		m = press(m, k)
		if m.pendingZ {
			t.Errorf("%q left the chord open", k)
		}
		if m.editing {
			t.Errorf("z%s fell through and opened the compose box", k)
		}
		if m.cursorRow != row {
			t.Errorf("z%s moved the cursor from %d to %d", k, row, m.cursorRow)
		}
		if m.streamScroll != scroll {
			t.Errorf("z%s scrolled the pane from %d to %d", k, scroll, m.streamScroll)
		}
	}
}

// The chord is the diff's. In the two lists `z` is not a binding, so it must not
// arm a chord there that then swallows the next key.
func TestZIsNotAChordInTheLists(t *testing.T) {
	for _, focus := range []Focus{FocusFiles, FocusComments} {
		m := zzModel(t)
		m.focus = focus
		m = press(m, "z")
		if m.pendingZ {
			t.Errorf("z armed the chord in pane %v, where it is not a binding", focus)
		}
	}
}

// It is in the reference, or nobody finds it.
func TestZZIsInTheHelp(t *testing.T) {
	found := false
	for _, g := range viewerKeyGroups(nil) {
		for _, k := range g.Keys {
			if strings.Contains(k[0], "zz") {
				found = true
			}
		}
	}
	if !found {
		t.Error("zz is not listed in the ? reference")
	}
}
