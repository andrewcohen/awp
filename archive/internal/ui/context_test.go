package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// contextViewer is a viewer whose loader records what context it was asked for and
// answers with a diff that says so, so a test can tell which reload it is looking
// at.
func contextViewer(t *testing.T) (*Model, *[]int) {
	t.Helper()
	var asked []int
	m := New("/repo", func(contextLines int) (string, error) {
		asked = append(asked, contextLines)
		return contextFixture(contextLines), nil
	}, nil)
	m.SetSize(120, 40)
	return &m, &asked
}

// contextFixture is a one-hunk diff with n lines of context around the change, so
// widening it changes the text the way jj's would.
func contextFixture(n int) string {
	var b strings.Builder
	b.WriteString("diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n")
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", 10-n, 2*n+1, 10-n, 2*n+1)
	for i := range n {
		fmt.Fprintf(&b, " before %d\n", i)
	}
	b.WriteString("+changed\n")
	for i := range n {
		fmt.Fprintf(&b, " after %d\n", i)
	}
	return b.String()
}

// tap sends a key the way the program would, keeping the returned command so the
// caller can run it — a reload is a command, so a test that drops it sees nothing.
func tap(m *Model, key string) tea.Cmd {
	next, cmd := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	*m = next.(Model)
	return cmd
}

// runCmd runs a command and feeds its message back, which is what the program does
// and what makes a reload observable.
func runCmd(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	next, _ := m.Update(msg)
	*m = next.(Model)
}

// TestTheViewerOpensOnJJsOwnContext. Pressing nothing has to show the diff jj
// would have printed, or the view and the command line disagree about what the
// change is.
func TestTheViewerOpensOnJJsOwnContext(t *testing.T) {
	m, asked := contextViewer(t)
	runCmd(t, m, loadDiffCmd(m.LoadDiff, m.contextLines))
	if len(*asked) != 1 || (*asked)[0] != contextDefault {
		t.Errorf("the first load asked for %v context, want %d", *asked, contextDefault)
	}
}

// TestPlusAsksJJForMoreContext is the whole feature: the number has to reach the
// loader, because widening is jj's job rather than something spliced in here.
func TestPlusAsksJJForMoreContext(t *testing.T) {
	m, asked := contextViewer(t)
	runCmd(t, m, loadDiffCmd(m.LoadDiff, m.contextLines))

	runCmd(t, m, tap(m, "+"))
	runCmd(t, m, tap(m, "+"))

	want := []int{contextDefault, 6, 12}
	if fmt.Sprint(*asked) != fmt.Sprint(want) {
		t.Errorf("the loader was asked for %v, want %v", *asked, want)
	}
	if m.contextLines != 12 {
		t.Errorf("the viewer thinks the context is %d, want 12", m.contextLines)
	}
}

// TestUnderscoreAsksForLess, down to hunks with nothing around them at all — a
// real way to re-read a diff you have already read once.
func TestUnderscoreAsksForLess(t *testing.T) {
	m, asked := contextViewer(t)
	runCmd(t, m, loadDiffCmd(m.LoadDiff, m.contextLines))

	runCmd(t, m, tap(m, "_"))
	if m.contextLines != 0 {
		t.Errorf("the viewer thinks the context is %d, want 0", m.contextLines)
	}
	if got := (*asked)[len(*asked)-1]; got != 0 {
		t.Errorf("the loader was last asked for %d, want 0", got)
	}
}

// TestTheLaddersEndsRefuseOutLoud. A key that quietly does nothing reads as a key
// that has stopped working, and the number is what the reader wants either way.
func TestTheLaddersEndsRefuseOutLoud(t *testing.T) {
	m, _ := contextViewer(t)
	runCmd(t, m, loadDiffCmd(m.LoadDiff, m.contextLines))

	for range len(contextSteps) + 2 {
		runCmd(t, m, tap(m, "+"))
	}
	if want := contextSteps[len(contextSteps)-1]; m.contextLines != want {
		t.Errorf("+ walked past the top of the ladder to %d, want %d", m.contextLines, want)
	}
	status, isErr := m.Status()
	if !strings.Contains(status, "48") {
		t.Errorf("the top of the ladder says %q, want it to name the count", status)
	}
	if isErr {
		t.Errorf("the top of the ladder reads as an error: %q", status)
	}

	for range len(contextSteps) + 2 {
		runCmd(t, m, tap(m, "_"))
	}
	if m.contextLines != 0 {
		t.Errorf("_ walked past the bottom of the ladder to %d, want 0", m.contextLines)
	}
}

// TestTheCursorStaysOnItsLineWhenTheContextChanges. Widening changes how many rows
// every hunk has, so a view that kept the row index would jump — usually into a
// different file.
func TestTheCursorStaysOnItsLineWhenTheContextChanges(t *testing.T) {
	m, _ := contextViewer(t)
	runCmd(t, m, loadDiffCmd(m.LoadDiff, m.contextLines))
	// Onto the changed line, which is the one worth staying on.
	for range 20 {
		if cursorText(*m) == "changed" {
			break
		}
		runCmd(t, m, tap(m, "j"))
	}
	before := cursorText(*m)
	if before != "changed" {
		t.Fatalf("could not park the cursor on the changed line; it is on %q", before)
	}

	runCmd(t, m, tap(m, "+"))

	if after := cursorText(*m); after != before {
		t.Errorf("widening the context moved the cursor from %q to %q", before, after)
	}
}

// TestTheChromeSaysSoOnlyWhenItIsNotTheDefault, matching the pane header's rule for
// which emulator is behind it: a widened diff looks like a different change if you
// have forgotten you widened it, and the default is not news.
func TestTheChromeSaysSoOnlyWhenItIsNotTheDefault(t *testing.T) {
	m, _ := contextViewer(t)
	runCmd(t, m, loadDiffCmd(m.LoadDiff, m.contextLines))
	if got := m.ContextChrome(); got != "" {
		t.Errorf("the default context is announced as %q, want nothing", got)
	}
	runCmd(t, m, tap(m, "+"))
	if got := m.ContextChrome(); !strings.Contains(got, "6") {
		t.Errorf("a widened context is announced as %q, want it to name the count", got)
	}
}
