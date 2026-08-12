package deckui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/andrewcohen/awp/internal/ui"
)

const diffModalSample = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package foo
-var old = 1
+var next = 2
+var more = 3
`

// diffModalModel builds a deck with one real workspace row and the diff
// viewer wired to a canned loader.
func diffModalModel(t *testing.T, load DiffLoader) Model {
	t.Helper()
	m := New([]Item{{
		ProjectName:   "proj",
		WorkspaceName: "ws",
		RepoRoot:      "/repo",
		Path:          "/repo/ws",
	}}, func(ActionRequest) error { return nil }).WithDiffViewer(load, nil)
	m.width, m.height = 120, 40
	return m
}

func pressKey(m Model, s string) (Model, tea.Cmd) {
	updated, cmd := m.Update(runeKey(s))
	return updated.(Model), cmd
}

// drain runs cmd and feeds the resulting message back into the model,
// expanding tea.Batch one level (modal entry batches the load with a
// ClearScreen). Follow-up commands are dropped so a self-rescheduling tick
// can't spin.
func drain(m Model, cmd tea.Cmd) Model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case nil:
		return m
	case tea.BatchMsg:
		for _, c := range msg {
			m = drain(m, c)
		}
		return m
	default:
		updated, _ := m.Update(msg)
		return updated.(Model)
	}
}

func TestDiffModalOpensOnC(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, cmd := pressKey(m, "c")
	if _, ok := m.active.(*diffModal); !ok {
		t.Fatalf("expected diff modal active, got %T", m.active)
	}
	if cmd == nil {
		t.Fatal("expected a load command on open")
	}
}

// Without a loader the key keeps its old meaning — opening a named review
// window in tmux — so the deck degrades to the pre-modal behavior instead of
// swallowing the key.
func TestDiffModalFallsBackToReviewWindowWhenUnwired(t *testing.T) {
	var got ActionRequest
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", Path: "/repo/ws"}},
		func(r ActionRequest) error { got = r; return nil })
	m.width, m.height = 120, 40
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	if m.active != nil {
		t.Fatalf("expected no modal without a loader, got %T", m.active)
	}
	if got.Action != ActionOpenWindow || got.Arg != "review" {
		t.Fatalf("expected review-window fallback, got action=%v arg=%q", got.Action, got.Arg)
	}
}

// A virtual inbox row has no working copy to diff, so `c` must not open the
// viewer on it — it falls through to trigger, which owns the virtual-row
// gestures.
func TestDiffModalFallsBackForVirtualRow(t *testing.T) {
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "pr-1", Virtual: true, PRNumber: 1}},
		func(ActionRequest) error { return nil }).
		WithDiffViewer(func(Item, DiffScope) (string, error) { return diffModalSample, nil }, nil)
	m.width, m.height = 120, 40
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	if _, ok := m.active.(*diffModal); ok {
		t.Fatal("expected no diff modal for a virtual row")
	}
}

func TestDiffModalClosesOnEscAndQ(t *testing.T) {
	for _, key := range []string{"esc", "q"} {
		m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
		m, _ = pressKey(m, "c")
		if m.active == nil {
			t.Fatalf("%s: expected modal open before close", key)
		}
		var updated tea.Model
		if key == "esc" {
			updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		} else {
			updated, _ = m.Update(runeKey(key))
		}
		if got := updated.(Model).active; got != nil {
			t.Fatalf("%s: expected modal closed, got %T", key, got)
		}
	}
}

// TestDiffModalClosesOnThePaneLeaveKey. ctrl+\ means "give the keyboard back to
// whatever put me here" everywhere else in awp, and the deck is what put the
// viewer here. The viewer binds it too, for when it is the whole program in a
// pane; this is the same key doing the same thing when it is a modal.
func TestDiffModalClosesOnThePaneLeaveKey(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, _ = pressKey(m, "c")
	if m.active == nil {
		t.Fatal("expected the modal open before closing it")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if got := updated.(Model).active; got != nil {
		t.Fatalf("expected the modal closed, got %T", got)
	}
}

// While the viewer's filter has focus, keys belong to the filter — `q` and
// `c` must type into it rather than close the modal.
func TestDiffModalFilterSwallowsCloseKeys(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, _ = pressKey(m, "c")
	m, _ = pressKey(m, "/")
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected diff modal, got %T", m.active)
	}
	if !dm.inner.Filtering() {
		t.Fatal("expected the viewer's filter to have focus after /")
	}
	m, _ = pressKey(m, "q")
	if m.active == nil {
		t.Fatal("expected q to type into the filter, not close the modal")
	}
}

// The modal renders through the deck's body/footer composition, so its
// frame must fit the viewport — otherwise the footer scrolls off.
func TestDiffModalViewFitsViewport(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, cmd := pressKey(m, "c")
	// Run the load command and feed the result back, so the modal renders
	// a populated diff rather than the loading state.
	m = drain(m, cmd)
	view := m.render()
	if h := lipgloss.Height(view); h > m.height {
		t.Fatalf("view is %d rows, viewport is %d", h, m.height)
	}
	if !strings.Contains(view, "foo.go") {
		t.Fatalf("expected the changed file in the view, got:\n%s", view)
	}
}

// blankRowsAboveFooter counts the blank rows between the last row carrying
// content and the footer bar.
func blankRowsAboveFooter(view string) int {
	lines := strings.Split(view, "\n")
	footer := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			footer = i
			break
		}
	}
	n := 0
	for i := footer - 1; i >= 0 && strings.TrimSpace(lines[i]) == ""; i-- {
		n++
	}
	return n
}

// The viewer fills its height budget, so any row diffModalChrome over-reserves
// shows up as a blank band above the status line rather than as a smaller frame.
// The body panels spend nothing on vertical padding now, so the intent is zero:
// the viewer's bottom border, then the status bar. Three was the original bug,
// one was the padding this cleanup removed.
func TestDiffModalLeavesNoDeadRowsAboveTheFooter(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	view := m.render()
	if got := blankRowsAboveFooter(view); got != 0 {
		t.Fatalf("expected no blank rows above the footer, got %d:\n%s", got, view)
	}
	// And the frame must still fit — the fix takes the reclaimed rows as diff
	// content, so an off-by-one the other way would push the footer off screen.
	if h := lipgloss.Height(view); h > m.height {
		t.Fatalf("view is %d rows, viewport is %d", h, m.height)
	}
}

func TestDiffModalSurfacesLoadError(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return "", errors.New("boom") })
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected diff modal, got %T", m.active)
	}
	status, isErr := dm.inner.Status()
	if !isErr || !strings.Contains(status, "boom") {
		t.Fatalf("expected the load error in status, got %q (isErr=%v)", status, isErr)
	}
}

// `c` opened the modal from the row list, but inside it the key belongs to the
// viewer's comment gesture — intercepting it here made commenting unreachable.
func TestDiffModalDoesNotCloseOnC(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	if _, ok := m.active.(*diffModal); !ok {
		t.Fatalf("expected the modal open, got %T", m.active)
	}
	m, _ = pressKey(m, "c")
	if _, ok := m.active.(*diffModal); !ok {
		t.Fatal("expected c to reach the viewer rather than closing the modal")
	}
}

// "vs stack base" describes how the base was picked, not what it is. Once the
// resolver answers, the footer names the branch you are reading against.
func TestDiffModalFooterNamesTheResolvedBase(t *testing.T) {
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/repo", Path: "/repo/ws"}},
		func(ActionRequest) error { return nil }).
		WithDiffViewer(func(Item, DiffScope) (string, error) { return diffModalSample, nil }, nil).
		WithDiffBaseResolver(func(_ Item, scope DiffScope) string {
			if scope == ScopeStackBase {
				return "andrew/parent-change"
			}
			return ""
		})
	m.width, m.height = 120, 40

	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatal("expected the diff modal open")
	}
	footer := ansi.Strip(dm.footerHelp())
	if !strings.Contains(footer, "vs andrew/parent-change") {
		t.Fatalf("expected the footer to name the base:\n%s", footer)
	}
	if strings.Contains(footer, "vs stack base") {
		t.Fatalf("expected the generic wording gone once resolved:\n%s", footer)
	}
}

// Until the resolver answers — and forever, when none is installed — the footer
// falls back to the scope's own wording rather than showing a blank.
func TestDiffModalFooterFallsBackBeforeTheBaseResolves(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	// Deliberately not drained: this is the state right after open, before any
	// command has run.
	m, _ = pressKey(m, "c")
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatal("expected the diff modal open")
	}
	if footer := ansi.Strip(dm.footerHelp()); !strings.Contains(footer, "vs stack base") {
		t.Fatalf("expected the fallback wording with no resolver:\n%s", footer)
	}
}

// The working-copy scope has no base to name, so the footer shows the scope's own
// wording rather than a base label — even with a resolver that would answer.
func TestDiffModalWorkingScopeKeepsItsWording(t *testing.T) {
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/repo", Path: "/repo/ws"}},
		func(ActionRequest) error { return nil }).
		WithDiffViewer(func(Item, DiffScope) (string, error) { return diffModalSample, nil }, nil).
		WithDiffScopes(func(Item) []ui.ScopeOption {
			return []ui.ScopeOption{
				// A base for the stack scope, none for the working copy: the working copy
				// is diffed against @ itself, which its own wording already says.
				{Key: "c", Label: "vs stack base",
					Load: func() (string, error) { return diffModalSample, nil },
					Base: func() string { return "andrew/parent-change" }},
				{Key: "w", Label: "working copy",
					Load: func() (string, error) { return diffModalSample, nil }},
			}
		})
	m.width, m.height = 120, 40

	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	m, cmd = pressKey(m, "-")
	m = drain(m, cmd)
	m, cmd = pressKey(m, "w")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatal("expected the diff modal open")
	}
	footer := ansi.Strip(dm.footerHelp())
	if strings.Contains(footer, "andrew/parent-change") {
		t.Fatalf("working-copy scope must not carry the other scope's base:\n%s", footer)
	}
	if !strings.Contains(footer, "working copy") {
		t.Fatalf("expected the working-copy wording:\n%s", footer)
	}
}

// The footer names the PR when the workspace is pinned to one. Reading a change
// and knowing which PR it is are the same question often enough to answer both.
func TestDiffModalFooterNamesThePR(t *testing.T) {
	m := New([]Item{{
		ProjectName:   "awp",
		WorkspaceName: "ws",
		RepoRoot:      "/repo",
		Path:          "/repo/ws",
		PRNumber:      1234,
	}}, func(ActionRequest) error { return nil }).
		WithDiffViewer(func(Item, DiffScope) (string, error) { return diffModalSample, nil }, nil)
	m.width, m.height = 120, 40

	// Not drained: the label comes from the item, so it is there from the frame
	// the modal opens on, with no command having run.
	m, _ = pressKey(m, "c")
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatal("expected the diff modal open")
	}
	if footer := ansi.Strip(dm.footerHelp()); !strings.Contains(footer, "awp#1234") {
		t.Fatalf("expected the PR named as awp#1234:\n%s", footer)
	}
}

// And says nothing when there is no PR — an empty segment would leave a stray
// separator in the footer.
func TestDiffModalFooterOmitsAnAbsentPR(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, _ = pressKey(m, "c")
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatal("expected the diff modal open")
	}
	footer := ansi.Strip(dm.footerHelp())
	if strings.Contains(footer, "#") {
		t.Fatalf("expected no PR segment for an unpinned workspace:\n%s", footer)
	}
	if strings.Contains(footer, " ·  · ") {
		t.Fatalf("footer has an empty segment:\n%s", footer)
	}
}

// One entry key. `c` opens the review of the whole change against its stack base
// — what a review is normally of — rather than the working copy alone.
func TestDiffModalOpensAtTheStackBase(t *testing.T) {
	var asked []DiffScope
	m := diffModalModel(t, func(_ Item, scope DiffScope) (string, error) {
		asked = append(asked, scope)
		return diffModalSample, nil
	})
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected the diff modal open, got %T", m.active)
	}
	if dm.scope != ScopeStackBase {
		t.Fatalf("expected ScopeStackBase, got %v", dm.scope)
	}
	if len(asked) != 1 || asked[0] != ScopeStackBase {
		t.Fatalf("expected the loader asked for the stack base, got %v", asked)
	}
}

// `C` is the same review in a tmux window beside the agent rather than in the
// deck's popup. It emits a sentinel rather than a built command: naming the base
// runs jj, which belongs in the action handler, not in the TUI.
func TestShiftCOpensTheReviewWindowRatherThanTheModal(t *testing.T) {
	var got ActionRequest
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/repo", Path: "/repo/ws"}},
		func(r ActionRequest) error { got = r; return nil }).
		WithDiffViewer(func(Item, DiffScope) (string, error) { return diffModalSample, nil }, nil)
	m.width, m.height = 120, 40

	m, cmd := pressKey(m, "C")
	m = drain(m, cmd)
	if _, ok := m.active.(*diffModal); ok {
		t.Fatal("expected no in-deck modal for C — it opens the tmux window")
	}
	if got.Action != ActionOpenWindow || got.Arg != ReviewStackArg {
		t.Fatalf("expected the review-window sentinel, got action=%v arg=%q", got.Action, got.Arg)
	}
}

// The `-` chord lives in the viewer now (internal/ui/scope.go), installed here by
// the scope provider. The deck had its own copy and standalone `awp diff` had
// none, so the same surface answered the same key two different ways depending on
// which door you came through — these assert the deck installs the list and the
// viewer owns the switching.
func TestScopeProviderInstallsTheMenu(t *testing.T) {
	var asked []string
	m := New([]Item{{ProjectName: "proj", WorkspaceName: "ws", RepoRoot: "/repo", Path: "/repo/ws"}},
		func(ActionRequest) error { return nil }).
		WithDiffViewer(func(Item, DiffScope) (string, error) { return diffModalSample, nil }, nil).
		WithDiffScopes(func(item Item) []ui.ScopeOption {
			return []ui.ScopeOption{
				{Key: "c", Label: "vs stack base", Load: func() (string, error) { asked = append(asked, "base"); return diffModalSample, nil }},
				{Key: "w", Label: "working copy", Load: func() (string, error) { asked = append(asked, "working"); return diffModalSample, nil }},
			}
		})
	m.width, m.height = 120, 40

	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected the diff modal open, got %T", m.active)
	}
	if got := dm.inner.ScopeLabel(); got != "vs stack base" {
		t.Fatalf("expected the first scope to be the one open, got %q", got)
	}
	// `-` then `w` switches, and the viewer reports the new label — so the deck's
	// footer follows without knowing anything about how the switch happened.
	m, cmd = pressKey(m, "-")
	m = drain(m, cmd)
	m, cmd = pressKey(m, "w")
	m = drain(m, cmd)
	dm, ok = m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected the modal still open, got %T", m.active)
	}
	if got := dm.inner.ScopeLabel(); got != "working copy" {
		t.Fatalf("expected the switched scope, got %q", got)
	}
	if !strings.Contains(ansi.Strip(dm.footerHelp()), "working copy") {
		t.Fatalf("expected the footer to follow:\n%s", ansi.Strip(dm.footerHelp()))
	}
	if len(asked) == 0 || asked[len(asked)-1] != "working" {
		t.Fatalf("expected the working-copy loader called, got %v", asked)
	}
}

// Without a provider there is nothing to offer, so `-` does nothing rather than
// opening a menu with one answer in it.
func TestNoScopeProviderLeavesTheChordInert(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	m, cmd = pressKey(m, "-")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatalf("expected the modal open, got %T", m.active)
	}
	if got := dm.inner.ScopeLabel(); got != "" {
		t.Fatalf("expected no scope installed, got %q", got)
	}
	// And the footer still reads as the ordinary one rather than as a menu.
	if strings.Contains(ansi.Strip(dm.footerHelp()), "esc cancel") {
		t.Fatalf("expected no menu:\n%s", ansi.Strip(dm.footerHelp()))
	}
}

// The `?` reference lists the keys the deck takes before the viewer sees them.
// The deck's own overlay is unreachable while the diff is open, so this is the
// only place they can be found.
func TestDiffModalDocumentsTheDeckSKeys(t *testing.T) {
	m := diffModalModel(t, func(Item, DiffScope) (string, error) { return diffModalSample, nil })
	m, cmd := pressKey(m, "c")
	m = drain(m, cmd)
	dm, ok := m.active.(*diffModal)
	if !ok {
		t.Fatal("expected the diff modal open")
	}
	groups := dm.inner.HostKeys
	if len(groups) != 1 {
		t.Fatalf("expected one host group, got %#v", groups)
	}
	var keys []string
	for _, k := range groups[0].Keys {
		keys = append(keys, k[0])
	}
	// `-` is not among them any more: the viewer owns that key and documents it
	// itself, so listing it here would say it twice. ctrl+\ is: the deck takes it
	// before the viewer, and it is the key that leaves a pane, so a reader looking
	// for the way out has to find it here too.
	if want := "esc / q / " + PaneLeaveKey; strings.Join(keys, ",") != want {
		t.Fatalf("host keys are %v, want %q", keys, want)
	}
}

// The project name is the repo half, and a row with no project still names the
// PR rather than dropping it.
func TestPRLabelFormat(t *testing.T) {
	cases := []struct {
		item Item
		want string
	}{
		{Item{ProjectName: "awp", PRNumber: 1234}, "awp#1234"},
		{Item{ProjectName: "", PRNumber: 7}, "#7"},
		{Item{ProjectName: "awp", PRNumber: 0}, ""},
		{Item{ProjectName: "awp", PRNumber: -1}, ""},
	}
	for _, c := range cases {
		if got := PRLabel(c.item); got != c.want {
			t.Errorf("PRLabel(%+v) = %q, want %q", c.item, got, c.want)
		}
	}
}
