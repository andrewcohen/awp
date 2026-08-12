package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/charm"
	"github.com/andrewcohen/awp/internal/review"
)

func TestQuestionMarkOpensAndClosesTheHelp(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	if m.HelpVisible() {
		t.Fatal("help should start closed")
	}
	m = press(m, "?")
	if !m.HelpVisible() {
		t.Fatal("expected ? to open the help")
	}
	// Every documented way out closes it, and nothing else does.
	for _, k := range []string{"?", "esc", "q", "enter"} {
		m.showHelp = true
		m = press(m, k)
		if m.HelpVisible() {
			t.Fatalf("expected %q to close the help", k)
		}
	}
}

// The overlay owns the keyboard: keys behind it would move a cursor nobody can
// see, and `q` in particular must close the help rather than the whole view.
func TestHelpSwallowsKeysWhileOpen(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	m.focus = FocusHunks
	m = press(m, "?")
	before := m.cursorRow
	for _, k := range []string{"j", "k", "G", "g", "tab", "w"} {
		m.showHelp = true
		m = press(m, k)
		if m.cursorRow != before {
			t.Fatalf("key %q moved the cursor behind the help: %d → %d", k, before, m.cursorRow)
		}
	}
}

// It replaces the panes rather than floating over them, so the body keeps
// exactly the height the host budgeted and the footer does not shift.
func TestHelpOverlayKeepsTheBodySize(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	const width, height = 100, 14
	panes := m.Body(width, height)
	m = press(m, "?")
	overlay := m.Body(width, height)
	if got, want := strings.Count(overlay, "\n"), strings.Count(panes, "\n"); got != want {
		t.Fatalf("help body is %d lines, panes are %d", got+1, want+1)
	}
	for _, line := range strings.Split(overlay, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("help line exceeds width %d (got %d): %q", width, w, line)
		}
	}
}

// Narrowing must truncate, not wrap — a wrapping overlay grows taller as the
// terminal shrinks and then overflows the body it was sized to.
func TestHelpOverlayTruncatesInsteadOfWrapping(t *testing.T) {
	const height = 20
	wide := strings.Count(renderHelpOverlay(newHelpViewport(200, height, nil, nil), 200, height), "\n")
	narrow := strings.Count(renderHelpOverlay(newHelpViewport(60, height, nil, nil), 60, height), "\n")
	if narrow != wide {
		t.Fatalf("overlay height depends on width: 200 → %d lines, 60 → %d", wide+1, narrow+1)
	}
}

// The reference scrolls. There are more bindings than fit a short terminal, and
// clipping them would hide part of the keymap while looking complete.
func TestHelpScrollsWhenItDoesNotFit(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetSize(100, 12)
	m = press(m, "?")
	if m.helpVP.AtBottom() {
		t.Fatal("fixture is wrong: the reference should overflow a 12-row body")
	}
	top := stripANSI(m.Body(100, 12))
	m = press(m, "ctrl+d")
	if m.helpVP.YOffset() == 0 {
		t.Fatal("expected ctrl+d to scroll the reference")
	}
	if scrolled := stripANSI(m.Body(100, 12)); scrolled == top {
		t.Fatal("expected the visible content to change after scrolling")
	}
	// Reopening starts at the top again — the reader asked for the reference, not
	// for wherever they were in it last time.
	m = press(m, "esc")
	m = press(m, "?")
	if m.helpVP.YOffset() != 0 {
		t.Fatalf("expected a fresh open to start at the top, got offset %d", m.helpVP.YOffset())
	}
}

// A resize keeps the reader's place rather than snapping back to the top.
func TestHelpKeepsItsScrollAcrossAResize(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.SetSize(100, 12)
	m = press(m, "?")
	m = press(m, "ctrl+d")
	at := m.helpVP.YOffset()
	if at == 0 {
		t.Fatal("fixture is wrong: expected to have scrolled")
	}
	m.SetSize(90, 12)
	if got := m.helpVP.YOffset(); got != at {
		t.Fatalf("resize moved the reader from offset %d to %d", at, got)
	}
}

// The overlay is the only place the keymap is written down, so a binding the
// switch in handleKey answers has to appear in it.
func TestHelpListsTheBindingsThatExist(t *testing.T) {
	// Read the content directly rather than through the viewport, which shows only
	// what fits — this asserts the reference is complete, not what is on screen.
	view := stripANSI(helpContent(120, nil, nil))
	for _, k := range []string{"c", "i", "D", "r", "R", "T", "w", "/", "e", "ctrl+r", "ctrl+s", "tab"} {
		if !strings.Contains(view, k) {
			t.Fatalf("expected %q documented in the help:\n%s", k, view)
		}
	}
}

// A host's own bindings are listed after the view's. The deck intercepts its keys
// before the view sees them, so this is the only place they can be documented —
// and a host with none (standalone `awp diff`) adds nothing, so the reference
// never advertises a key that does nothing.
func TestHelpListsTheHostSKeysAfterItsOwn(t *testing.T) {
	plain := stripANSI(helpContent(120, nil, nil))
	if strings.Contains(plain, "In the deck") {
		t.Fatalf("standalone must not advertise a host's keys:\n%s", plain)
	}
	withHost := stripANSI(helpContent(120, nil, []charm.KeyGroup{{
		Title: "In the deck",
		Keys:  [][2]string{{"esc / q", "back to the deck"}},
	}}))
	if !strings.Contains(withHost, "In the deck") || !strings.Contains(withHost, "back to the deck") {
		t.Fatalf("expected the host's group in the reference:\n%s", withHost)
	}
	// After the view's own, not instead of them.
	if i, j := strings.Index(withHost, "Review"), strings.Index(withHost, "In the deck"); i < 0 || j < i {
		t.Fatalf("expected the host's group last, got Review at %d and the host at %d", i, j)
	}
}

// The footer is an affordance now, not a legend.
func TestFooterPointsAtHelpInsteadOfListingKeys(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	footer := stripANSI(m.renderFooter())
	if !strings.Contains(footer, "? help") {
		t.Fatalf("expected the footer to point at ?:\n%s", footer)
	}
	// The old legend's giveaways.
	for _, gone := range []string{"j/k:scroll", "c:comment", "e:$EDITOR", "file(s) changed"} {
		if strings.Contains(footer, gone) {
			t.Fatalf("expected %q gone from the footer:\n%s", gone, footer)
		}
	}
}

// The file count went with it: the file list is already headed "Files (n)".
func TestLoadedStatusDoesNotRepeatTheFileCount(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"), fileWith("b.go", 1, "beta"))
	status, isErr := m.Status()
	if isErr {
		t.Fatalf("unexpected error status %q", status)
	}
	if strings.Contains(status, "file") {
		t.Fatalf("expected no file count in the status, got %q", status)
	}
	if !strings.Contains(stripANSI(m.renderFileList(30, 8)), "Files (2)") {
		t.Fatal("expected the file list to carry the count instead")
	}
}

// An empty diff still says so — otherwise it reads as a failure to load.
func TestEmptyDiffStillReportsNoChanges(t *testing.T) {
	m := commentModel(t)
	status, _ := m.Status()
	if status != "no changes" {
		t.Fatalf("expected \"no changes\", got %q", status)
	}
}

// `\` gives the stream the whole width for reading wide code.
func TestBackslashHidesAndShowsTheLeftColumn(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	const width, height = 120, 14
	m.SetSize(width, height)

	withColumn := stripANSI(m.Body(width, height))
	if !strings.Contains(withColumn, "Files (") {
		t.Fatal("fixture is wrong: the file list should be visible")
	}
	wideBefore := m.hunkWidth

	m = press(m, `\`)
	hidden := stripANSI(m.Body(width, height))
	if strings.Contains(hidden, "Files (") {
		t.Fatalf("expected the file list gone:\n%s", hidden)
	}
	if m.hunkWidth <= wideBefore {
		t.Fatalf("expected the stream to gain width: %d → %d", wideBefore, m.hunkWidth)
	}
	// The body still fills its box, so the footer does not move.
	if got, want := strings.Count(hidden, "\n"), strings.Count(withColumn, "\n"); got != want {
		t.Fatalf("hidden body is %d lines, shown is %d", got+1, want+1)
	}

	m = press(m, `\`)
	if shown := stripANSI(m.Body(width, height)); !strings.Contains(shown, "Files (") {
		t.Fatalf("expected the column back:\n%s", shown)
	}
	if m.hunkWidth != wideBefore {
		t.Fatalf("expected the original stream width back, got %d want %d", m.hunkWidth, wideBefore)
	}
}

// Hiding the column while it holds the keyboard has to move focus to the diff,
// or the keys drive a selection nobody can see.
func TestHidingTheColumnTakesFocusToTheDiff(t *testing.T) {
	for _, focus := range []Focus{FocusFiles, FocusComments} {
		m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
		m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "a finding")})
		m.focus = focus
		m = press(m, `\`)
		if m.focus != FocusHunks {
			t.Fatalf("focus %v: expected the diff to take the keyboard, got %v", focus, m.focus)
		}
	}
}

// And tab must not cycle back into a hidden pane.
func TestTabDoesNotReachAHiddenColumn(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "a finding")})
	m = press(m, `\`)
	for i := 0; i < 4; i++ {
		m = press(m, "tab")
		if m.focus != FocusHunks {
			t.Fatalf("tab %d: focus left the diff for %v with the column hidden", i+1, m.focus)
		}
	}
	m = press(m, "shift+tab")
	if m.focus != FocusHunks {
		t.Fatalf("shift+tab left the diff for %v with the column hidden", m.focus)
	}
}

// Your place in the change survives hiding, so `\` is a way to look at the code
// wider rather than a way to lose the row you were on.
func TestHidingTheColumnKeepsYourPlace(t *testing.T) {
	m := commentModel(t,
		fileWith("a.go", 1, "alpha", "beta"),
		fileWith("b.go", 1, "gamma", "delta"),
	)
	m.SetComments([]review.Comment{commentOn("b.go", 1, "gamma", "a finding")})
	m.SetSize(120, 14)
	// Walk into the second file, so there is a position worth preserving. The file
	// cursor is derived from the diff cursor, so moving one moves both.
	for m.cursorRow < len(m.stream.rows)-1 && cursorText(m) != "delta" {
		m = press(m, "j")
	}
	wantRow, wantFile, wantText := m.cursorRow, m.filesCursor, cursorText(m)
	if wantText != "delta" {
		t.Fatalf("fixture is wrong: expected to reach delta, sat on %q", wantText)
	}

	m = press(m, `\`)
	if got := cursorText(m); got != wantText {
		t.Fatalf("hiding moved the cursor from %q to %q", wantText, got)
	}
	m = press(m, `\`)
	if m.cursorRow != wantRow || m.filesCursor != wantFile {
		t.Fatalf("expected row %d file %d back, got row %d file %d", wantRow, wantFile, m.cursorRow, m.filesCursor)
	}
	shown := stripANSI(m.Body(120, 14))
	if !strings.Contains(shown, "Files (") || !strings.Contains(shown, "b.go") {
		t.Fatalf("expected the file list back showing the file you were in:\n%s", shown)
	}
}

// The binding has to be in the reference, which is the only place it is written
// down now that the footer stopped listing keys.
func TestHelpDocumentsTheColumnToggle(t *testing.T) {
	if view := stripANSI(helpContent(120, nil, nil)); !strings.Contains(view, `\`) {
		t.Fatalf("expected the column toggle documented:\n%s", view)
	}
}

// The standalone header is the only chrome above the panes, so it answers what
// the deck's footer answers: what am I looking at, whose change, which PR, and
// against what. It used to say `awp diff  repo: awp` and stop there — which left
// "is this the PR I think it is" unanswerable without leaving the view.
func TestStandaloneHeaderNamesTheSubject(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.RepoRoot = "/src/awp"
	m.Subject = Subject{Workspace: "pr-2336-dev", PR: "awp#2336"}
	m.baseLabel = "main"
	header := stripANSI(m.renderHeader())
	for _, want := range []string{"awp review", "awp", "pr-2336-dev", "awp#2336", "vs main"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected %q in the header:\n%s", want, header)
		}
	}
	// "review", not "diff": it is the same surface `c` opens, and calling it a diff
	// undersold what the keys do.
	if strings.Contains(header, "awp diff") {
		t.Fatalf("expected the surface named as a review:\n%s", header)
	}
}

// TestStandaloneHeaderNamesTheProjectNotTheWorkspaceDirectory.
//
// RepoRoot is the workspace the diff's paths are rooted at, so inside a PR
// workspace its directory name is the workspace's, not the project's. Left to
// filepath.Base the header said `pr-2336-dev` twice and never named the repo the
// change belongs to.
func TestStandaloneHeaderNamesTheProjectNotTheWorkspaceDirectory(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.RepoRoot = "/ws/pr-2336-dev"
	m.Subject = Subject{Project: "alpha", Workspace: "pr-2336-dev", PR: "alpha#2336"}
	header := stripANSI(m.renderHeader())
	if !strings.Contains(header, "alpha ·") {
		t.Errorf("the header does not name the project:\n%s", header)
	}
	if strings.Count(header, "pr-2336-dev") != 1 {
		t.Errorf("the workspace is named %d times:\n%s", strings.Count(header, "pr-2336-dev"), header)
	}
}

// A plain repo is a legitimate place to read a change, so the segments that have
// no answer are dropped rather than shown blank.
func TestStandaloneHeaderOmitsWhatItDoesNotKnow(t *testing.T) {
	m := commentModel(t, fileWith("a.go", 1, "alpha"))
	m.RepoRoot = "/src/awp"
	header := stripANSI(m.renderHeader())
	if strings.Contains(header, " ·  · ") || strings.HasSuffix(strings.TrimSpace(header), "·") {
		t.Fatalf("expected no empty segments:\n%s", header)
	}
	if !strings.Contains(header, "awp review") || !strings.Contains(header, "awp") {
		t.Fatalf("expected the surface and the repo still named:\n%s", header)
	}
}
