package deckui

import (
	"testing"
	"time"
)

// attentionDeck is a deck in the attention scope, whose order is therefore what
// each row wants from you rather than its name.
func attentionDeck(t *testing.T, items []Item) Model {
	t.Helper()
	m := New(items, func(ActionRequest) error { return nil })
	m.scope = ScopeAttention
	m.width, m.height = 120, 40
	return m
}

// selectedName is the workspace under the cursor, or "" — what the next key the
// user presses would be aimed at.
func selectedName(m Model) string {
	it, ok := m.selected()
	if !ok {
		return ""
	}
	return it.WorkspaceName
}

// TestARefreshKeepsTheCursorOnItsRow. The attention scope sorts on live signals,
// so the list re-orders under a cursor that is only an index — and the next key
// pressed then goes to whatever slid into the slot, which is how `D` ends up
// aimed at the wrong workspace.
func TestARefreshKeepsTheCursorOnItsRow(t *testing.T) {
	// Two rows in the scope for the weakest reason there is — you were just in
	// them — so they sort by name, and the first thing to happen re-orders them.
	now := time.Now()
	m := attentionDeck(t, []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "idle", LastActiveAt: now},
		{ProjectName: "p", WorkspaceName: "beta", Status: "idle", LastActiveAt: now},
	})
	if got := selectedName(m); got != "alpha" {
		t.Fatalf("cursor starts on %q, want alpha", got)
	}

	// You start an agent in beta, which puts it above alpha.
	updated, _ := m.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "idle", LastActiveAt: now},
		{ProjectName: "p", WorkspaceName: "beta", Status: "working", Active: true, LastActiveAt: now},
	}})
	m = updated.(Model)
	if rows := m.items(); rows[0].WorkspaceName != "beta" {
		t.Fatalf("the list did not re-order, so this test proves nothing: %q first", rows[0].WorkspaceName)
	}
	if got := selectedName(m); got != "alpha" {
		t.Errorf("after the refresh the cursor is on %q, want it still on alpha", got)
	}
}

// TestAnExplicitJumpStillWinsOverStayingPut. pendingSelect is set by the flows
// that mean "put the cursor here when the row lands" — a create, a rename, a
// delete. Those have to beat holding position, or the row you just made is not
// the one you are on.
func TestAnExplicitJumpStillWinsOverStayingPut(t *testing.T) {
	m := attentionDeck(t, []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "waiting", Unread: true},
	})
	m.pendingSelect = Item{ProjectName: "p", WorkspaceName: "beta"}
	updated, _ := m.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "waiting", Unread: true},
		{ProjectName: "p", WorkspaceName: "beta", Status: "working", Active: true},
	}})
	m = updated.(Model)
	if got := selectedName(m); got != "beta" {
		t.Errorf("cursor is on %q, want the row pendingSelect asked for (beta)", got)
	}
}

// TestAVanishedRowLeavesTheCursorWhereItWas. Once the row the cursor named does
// not exist there is no right answer, and staying at the same place in the list
// is the best of the available ones — better than jumping to the top, which
// moves the selection further than the deletion did.
func TestAVanishedRowLeavesTheCursorWhereItWas(t *testing.T) {
	m := attentionDeck(t, []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "working", Active: true},
		{ProjectName: "p", WorkspaceName: "beta", Status: "working", Active: true},
		{ProjectName: "p", WorkspaceName: "gamma", Status: "working", Active: true},
	})
	m.cursor = 1
	updated, _ := m.Update(refreshDoneMsg{items: []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "working", Active: true},
		{ProjectName: "p", WorkspaceName: "gamma", Status: "working", Active: true},
	}})
	m = updated.(Model)
	if m.cursor != 1 {
		t.Errorf("cursor is on row %d, want it left at 1", m.cursor)
	}
}

// TestPRStatusLandingKeepsTheCursorOnItsRow. The fan-out arrives seconds after
// the deck opens and re-orders the scope on its own — the cursor was on a row
// before it landed and has to be on the same row after.
func TestPRStatusLandingKeepsTheCursorOnItsRow(t *testing.T) {
	// Both rows are in the scope only for having been worked in recently, which
	// is the lowest claim there is — so the first PR fact to land outranks it.
	now := time.Now()
	m := attentionDeck(t, []Item{
		{ProjectName: "p", WorkspaceName: "alpha", RepoRoot: "/repo", Bookmark: "alpha", Status: "idle", LastActiveAt: now},
		{ProjectName: "p", WorkspaceName: "beta", RepoRoot: "/repo", Bookmark: "beta", Status: "idle", LastActiveAt: now},
	})
	if got := selectedName(m); got != "alpha" {
		t.Fatalf("cursor starts on %q, want alpha", got)
	}
	// beta's PR turns out to need action, which outranks "recently active" and
	// moves beta above alpha.
	updated, _ := m.Update(PRStatusRepoDoneMsg{
		Repo: "/repo",
		ByHead: map[string]PRStatus{
			"beta": {Number: 1, State: "OPEN", HeadRefName: "beta", Mine: true, CIState: "FAILING"},
		},
	})
	m = updated.(Model)
	if rows := m.items(); rows[0].WorkspaceName != "beta" {
		t.Fatalf("the list did not re-order, so this test proves nothing: %q first", rows[0].WorkspaceName)
	}
	if got := selectedName(m); got != "alpha" {
		t.Errorf("after PR status landed the cursor is on %q, want it still on alpha", got)
	}
}

// TestAKeystrokeIsNotUndoneByTheNextRefresh. The row is read inside the handler
// rather than remembered across Updates, so what it holds on to is wherever the
// cursor was last left — including by the user, a moment ago.
func TestAKeystrokeIsNotUndoneByTheNextRefresh(t *testing.T) {
	items := []Item{
		{ProjectName: "p", WorkspaceName: "alpha", Status: "working", Active: true},
		{ProjectName: "p", WorkspaceName: "beta", Status: "working", Active: true},
	}
	m := attentionDeck(t, items)
	moved, _ := m.Update(runeKey("j"))
	m = moved.(Model)
	if got := selectedName(m); got != "beta" {
		t.Fatalf("j moved the cursor to %q, want beta", got)
	}
	updated, _ := m.Update(refreshDoneMsg{items: items})
	m = updated.(Model)
	if got := selectedName(m); got != "beta" {
		t.Errorf("the refresh moved the cursor to %q, want it left on beta", got)
	}
}
