package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andrewcohen/awp/internal/diff"
	"github.com/andrewcohen/awp/internal/review"
)

const sampleDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,2 @@
-old
+new
`

func TestModelInitReturnsCmd(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("expected init cmd")
	}
}

// `r` marks a file reviewed now that live refresh made manual refresh redundant;
// ctrl+r is the explicit refresh that remains.
func TestCtrlRForcesARefresh(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("expected ctrl+r to issue a refresh command")
	}
}

func TestRDoesNotRefresh(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd != nil {
		t.Fatal("expected r to mark reviewed, not refresh")
	}
}

func TestModelFilterMode(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M"}}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if got.focus != FocusFilter {
		t.Fatalf("expected filter focus, got %v", got.focus)
	}
	if !strings.Contains(got.filterInput.Value(), "f") {
		t.Fatalf("expected filter input to update, got %q", got.filterInput.Value())
	}
}

// enter is deliberately not an open binding — `e` is the only one. On a file
// row it drills into the hunk pane instead.
func TestModelEnterFocusesHunkPaneWithoutOpening(t *testing.T) {
	opened := false
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, func(string, int) tea.Cmd {
		opened = true
		return nil
	})
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M", Hunks: []diff.Hunk{{NewStart: 5}}}}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if opened {
		t.Fatal("expected enter not to open a file")
	}
	if got.focus != FocusHunks {
		t.Fatalf("expected enter to focus the hunk pane, got %v", got.focus)
	}
}

// Filter-mode enter still confirms the filter rather than changing panes.
func TestFilterEnterConfirmsAndReturnsToFiles(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M"}}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.focus != FocusFiles {
		t.Fatalf("expected filter enter to return to the file list, got %v", got.focus)
	}
	if got.filterInput.Value() != "f" {
		t.Fatalf("expected the filter to be kept on confirm, got %q", got.filterInput.Value())
	}
}

func TestModelEAlsoOpensCurrentFile(t *testing.T) {
	openedPath := ""
	openedLine := 0
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, func(path string, line int) tea.Cmd {
		openedPath = path
		openedLine = line
		return nil
	})
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{{NewPath: "foo.go", Status: "M", Hunks: []diff.Hunk{{NewStart: 7}}}}})
	_, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if openedPath == "" || openedLine != 7 {
		t.Fatalf("unexpected open via e: %q:%d", openedPath, openedLine)
	}
}

func TestModelErrorStatus(t *testing.T) {
	m := New("/repo", func() (string, error) { return "", errors.New("boom") }, nil)
	updated, _ := m.Update(diffLoadedMsg{err: errors.New("boom")})
	got := updated.(Model)
	if !got.statusErr || !strings.Contains(got.status, "boom") {
		t.Fatalf("unexpected status: %q err=%t", got.status, got.statusErr)
	}
}

func TestScheduleRefreshDisabledWhenZero(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.RefreshInterval = 0
	m.loaded = true
	updated, cmd := m.Update(diffLoadedMsg{})
	_ = updated
	if cmd != nil {
		t.Fatal("expected no refresh scheduling when disabled")
	}
}

// The viewer opens on the diff pane, so tab's first press goes to the file list.
func TestOpensOnTheDiffPane(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	if m.focus != FocusHunks {
		t.Fatalf("expected the diff pane focused on open, got %v", m.focus)
	}
}

func TestTabTogglesPaneFocusBothWays(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.focus != FocusFiles {
		t.Fatalf("expected tab to move to the file list, got %v", got.focus)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.focus != FocusHunks {
		t.Fatalf("expected tab to toggle back to the diff, got %v", got.focus)
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := updated.(Model).focus; got != FocusFiles {
		t.Fatalf("expected shift+tab to switch pane, got %v", got)
	}
}

// h/l no longer switch panes — they pan the diff.
func TestHAndLDoNotSwitchPanes(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.focus = FocusFiles
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if got := updated.(Model).focus; got != FocusFiles {
		t.Fatalf("expected l to leave focus on the file list, got %v", got)
	}
}

func TestFilterFooterIsStableHeight(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.width = 100
	m.bodyHeight = 16
	base := m.renderFooter()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	withFilter := updated.(Model).renderFooter()
	if len(strings.Split(base, "\n")) != len(strings.Split(withFilter, "\n")) {
		t.Fatalf("expected stable footer height, got %d vs %d", len(strings.Split(base, "\n")), len(strings.Split(withFilter, "\n")))
	}
	if !strings.Contains(withFilter, "Filter files:") {
		t.Fatalf("expected filter prompt in footer, got %q", withFilter)
	}
}

// Live refresh is on by default now that reloads preserve the reading position.
func TestLiveRefreshOnByDefault(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	if m.RefreshInterval != DefaultRefreshInterval {
		t.Fatalf("got %v want %v", m.RefreshInterval, DefaultRefreshInterval)
	}
	if m.RefreshInterval <= 0 {
		t.Fatalf("expected live refresh enabled, got %v", m.RefreshInterval)
	}
	if cmd := m.Init(); cmd == nil {
		t.Fatal("expected Init to schedule a refresh")
	}
}

// singleHunkModel is a file with one hunk taller than the pane — the case
// that used to be unscrollable with j/k.
// Scrolling past a hunk boundary must move the cursor with it, so the
// highlighted header and `e`'s target line match what's on screen.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// wrapModel is one file with a single long line, wider than the pane.
// With wrap on there is more content than pane, so scrolling must be able to
// reach the end — the bug this refactor prevents is a clamp computed from
// unwrapped row counts.
// Continuation rows must not repeat the gutter — only the first row carries
// the line numbers and +/- marker.
// longLineModel is one file whose single hunk holds a line far wider than
// the pane — the case h/l exists for.
// Panning must stop before the longest line leaves the pane entirely.
// The gutter stays pinned while the code shifts left.
// Moving to another file must not carry the pan across.
// The selected file row must be the most emphasized row, not the least: it
// carries the app-wide `┃ ` bar, and unselected rows reserve the same width
// so labels stay aligned.
func TestSelectedFileRowCarriesSelectionBar(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.SetSize(120, 12)
	updated, _ := m.Update(diffLoadedMsg{files: []diff.FileDiff{
		{NewPath: "internal/ui/model.go", Status: "M"},
		{NewPath: "internal/ui/other.go", Status: "A"},
	}})
	got := updated.(Model)

	selected := stripANSI(got.renderFileRow(got.filtered[0], 60, true))
	unselected := stripANSI(got.renderFileRow(got.filtered[1], 60, false))

	if !strings.HasPrefix(selected, selectionPrefixBar) {
		t.Fatalf("expected the selected row to start with the bar, got %q", selected)
	}
	if !strings.HasPrefix(unselected, selectionPrefixBlank) {
		t.Fatalf("expected unselected rows to reserve the bar width, got %q", unselected)
	}
	// Same offset for the badge on both rows.
	if len(selected)-len(strings.TrimLeft(selected, "┃ ")) == 0 {
		t.Fatalf("selected row lost its prefix: %q", selected)
	}
	if !strings.Contains(selected, "model.go") {
		t.Fatalf("selected row lost its path: %q", selected)
	}
	if !strings.Contains(unselected, "other.go") {
		t.Fatalf("unselected row lost its path: %q", unselected)
	}
}

// twoFileModel gives two files, each with a hunk taller than the pane, with
// the hunk pane focused on the first.
// At the bottom of a file, the next j moves to the following file's top.
// Going back lands at the bottom of the previous file, so reading is
// continuous across the boundary.
// Mid-file navigation must not change file.
// Paging crosses the same way j/k does.
// Crossing resets the horizontal pan — the next file starts at column 0.

// The base label is chrome, resolved off the open path — the host asks for it
// and the answer arrives as its own message, so a slow jj cannot delay the
// first frame of the diff.
func TestBaseIsEmptyUntilResolved(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	m.ResolveBase = func() string { return "andrew/parent" }
	if got := m.Base(); got != "" {
		t.Fatalf("expected no label before resolving, got %q", got)
	}
	updated, _ := m.Update(baseResolvedMsg{label: "andrew/parent"})
	if got := updated.(Model).Base(); got != "andrew/parent" {
		t.Fatalf("expected the resolved label, got %q", got)
	}
}

// A resolve that comes back empty must not blank a label we already have: jj
// failing once in a workspace that was fine a moment ago should leave the chrome
// saying what it said.
func TestEmptyResolveKeepsTheKnownBase(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	updated, _ := m.Update(baseResolvedMsg{label: "main"})
	m = updated.(Model)
	updated, _ = m.Update(baseResolvedMsg{label: ""})
	if got := updated.(Model).Base(); got != "main" {
		t.Fatalf("expected the known label kept, got %q", got)
	}
	updated, _ = m.Update(baseResolvedMsg{label: "   "})
	if got := updated.(Model).Base(); got != "main" {
		t.Fatalf("whitespace is not an answer either, got %q", got)
	}
}

// No resolver means no command to run — Init must not schedule one, or a nil
// call would panic on the first frame.
func TestInitWithoutAResolverIsSafe(t *testing.T) {
	m := New("/repo", func() (string, error) { return sampleDiff, nil }, nil)
	if cmd := resolveBaseCmd(m.ResolveBase); cmd != nil {
		t.Fatal("expected no command without a resolver")
	}
	if m.Init() == nil {
		t.Fatal("expected Init to still load the diff")
	}
}

// The host calls SetSize once per frame — it cannot know whether the terminal
// changed — so SetSize must be free when the layout has not moved. It used to end
// in an unconditional rebuildStream, which made every frame a full geometry and
// placement pass: about a millisecond on a change with no comments, twenty on one
// with many, since placement is O(comments × rows).
//
// Observed through commentIndex, which only a rebuild repopulates.
func TestSetSizeIsFreeWhenTheLayoutHasNotMoved(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "a finding")})
	m.SetSize(120, 20)
	if len(m.commentIndex) == 0 {
		t.Fatal("fixture is wrong: expected a comment in the index")
	}

	m.commentIndex = nil
	m.SetSize(120, 20)
	if m.commentIndex != nil {
		t.Fatal("SetSize rebuilt the stream when nothing about the layout changed")
	}

	// A real resize still rebuilds: wrap geometry depends on the width.
	m.SetSize(90, 20)
	if len(m.commentIndex) == 0 {
		t.Fatal("expected a width change to rebuild")
	}

	// And so does a height change, which the comment pane's own height depends on.
	m.commentIndex = nil
	m.SetSize(90, 30)
	if len(m.commentIndex) == 0 {
		t.Fatal("expected a height change to rebuild")
	}
}

// `\` changes the pane split without changing the terminal size, so the guard has
// to compare the derived geometry rather than the arguments it was handed.
func TestHidingTheColumnStillRebuilds(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "a finding")})
	m.SetSize(120, 20)

	m.commentIndex = nil
	m = press(m, `\`)
	if len(m.commentIndex) == 0 {
		t.Fatal("hiding the left column must rebuild: the stream is wider now")
	}
	if m.hunkWidth <= 0 {
		t.Fatalf("unexpected hunk width %d", m.hunkWidth)
	}
}
