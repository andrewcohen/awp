package deckui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
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

// Without a loader the key keeps its old meaning — opening the tuicr review
// window — so the deck degrades to the pre-modal behavior instead of
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
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		} else {
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
		if got := updated.(Model).active; got != nil {
			t.Fatalf("%s: expected modal closed, got %T", key, got)
		}
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
	view := m.View()
	if h := lipgloss.Height(view); h > m.height {
		t.Fatalf("view is %d rows, viewport is %d", h, m.height)
	}
	if !strings.Contains(view, "foo.go") {
		t.Fatalf("expected the changed file in the view, got:\n%s", view)
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
