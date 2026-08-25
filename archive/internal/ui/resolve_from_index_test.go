package ui

import (
	"testing"

	"github.com/andrewcohen/awp/internal/review"
)

// resolvableIndex is the viewer with the comment index holding the keyboard and a
// recorder in place of the GitHub call, so a test can read back what was asked
// for rather than only what the mirror ended up saying.
func resolvableIndex(t *testing.T, threads ...review.Thread) (Model, *[]string) {
	t.Helper()
	m := commentModel(t, fileWith("a.go", 1, "alpha", "beta", "gamma"))
	var asked []string
	m.ResolveThread = func(id string, resolve bool) error {
		verb := "resolve "
		if !resolve {
			verb = "reopen "
		}
		asked = append(asked, verb+id)
		return nil
	}
	m.SetThreads(threads)
	m.focus = FocusComments
	return m, &asked
}

// selectEntry puts the index's selection on entry i, the state a reviewer is in
// while walking the list.
func selectEntry(t *testing.T, m Model, i int) Model {
	t.Helper()
	if len(m.commentIndex) <= i {
		t.Fatalf("precondition: index has %d entries, wanted the selection on %d", len(m.commentIndex), i)
	}
	m.seekToComment(i)
	return m
}

// R settles the selected conversation from the index. Deciding a thread is done
// is what you are doing while you read the list, so it has to be reachable
// without seeking into the diff and back for each one.
func TestRResolvesTheSelectedThreadFromTheIndex(t *testing.T) {
	m, asked := resolvableIndex(t,
		remoteThread("T1", "a.go", 1, false, "first point"),
		remoteThread("T2", "a.go", 2, false, "second point"),
	)
	m = selectEntry(t, m, 1)

	m = press(m, "R")

	if len(*asked) != 1 || (*asked)[0] != "resolve T2" {
		t.Fatalf("R from the index asked for %v, want [resolve T2] (status %q)", *asked, m.status)
	}
	// The mirror has to agree, or the row comes back on the next render as if
	// nothing happened.
	for _, tr := range m.threads {
		if tr.ID == "T2" && !tr.Resolved {
			t.Error("T2 is still unresolved in the mirror")
		}
	}
}

// The selection holds its index rather than following the thread. Resolving hides
// it under the default visibility, so staying put is what puts the next
// unresolved thread under the cursor — one key, repeated, down the list.
func TestResolvingFromTheIndexKeepsTheSelectionInPlace(t *testing.T) {
	m, _ := resolvableIndex(t,
		remoteThread("T1", "a.go", 1, false, "first point"),
		remoteThread("T2", "a.go", 2, false, "second point"),
		remoteThread("T3", "a.go", 3, false, "third point"),
	)
	m = selectEntry(t, m, 0)
	if len(m.commentIndex) != 3 {
		t.Fatalf("precondition: %d entries listed, want 3", len(m.commentIndex))
	}

	m = press(m, "R")

	if len(m.commentIndex) != 2 {
		t.Fatalf("the resolved thread is still listed: %d entries", len(m.commentIndex))
	}
	if m.commentsCursor != 0 {
		t.Errorf("the selection moved to %d, want it held at 0", m.commentsCursor)
	}
	if got := m.commentIndex[m.commentsCursor].summary; got != "second point" {
		t.Errorf("the selection names %q, want the next unresolved thread", got)
	}
	// And the diff followed the selection: the index is a jump index, so a row it
	// points at that the diff is not showing is the selection lying.
	if want := m.commentIndex[m.commentsCursor].row; m.cursorRow != want {
		t.Errorf("the diff cursor is on row %d, want %d — the selected conversation", m.cursorRow, want)
	}
}

// It acts on the selection, not on wherever the diff cursor happens to be. Every
// path into this pane seeks the cursor to the selection, but resolving the wrong
// conversation is invisible when it happens — so this must not rest on all of
// them remembering.
func TestRFromTheIndexActsOnTheSelectionNotTheDiffCursor(t *testing.T) {
	m, asked := resolvableIndex(t,
		remoteThread("T1", "a.go", 1, false, "first point"),
		remoteThread("T2", "a.go", 2, false, "second point"),
	)
	m = selectEntry(t, m, 1)
	m.cursorRow = m.commentIndex[0].row // stale: parked on the other conversation

	m = press(m, "R")

	if len(*asked) != 1 || (*asked)[0] != "resolve T2" {
		t.Fatalf("R acted on %v, want [resolve T2] — the selected entry", *asked)
	}
}

// A second R reopens it, the same way it does in the diff — reachable from here
// too, since with threads shown the list is where you notice one you closed by
// mistake.
func TestRFromTheIndexReopensAResolvedThread(t *testing.T) {
	m, asked := resolvableIndex(t, remoteThread("T1", "a.go", 1, true, "settled point"))
	// Shown rather than hidden: the default visibility keeps settled threads out of
	// the list, so reopening one from here is only reachable after T.
	m.threadVisibility = ThreadsAll
	m.rebuildStream()
	m = selectEntry(t, m, 0)

	m = press(m, "R")

	if len(*asked) != 1 || (*asked)[0] != "reopen T1" {
		t.Fatalf("R on a resolved thread asked for %v, want [reopen T1] (status %q)", *asked, m.status)
	}
}

// A local comment has nothing to resolve — resolving is a state GitHub records —
// and the refusal has to say that rather than tell you to move a cursor that is
// not what the index is driven by.
func TestRInTheIndexOnALocalCommentSaysWhyNot(t *testing.T) {
	m, asked := resolvableIndex(t, remoteThread("T1", "a.go", 2, false, "a point"))
	m.SetComments([]review.Comment{commentOn("a.go", 1, "alpha", "my own remark")})
	m = selectEntry(t, m, 0)
	if got := m.commentIndex[0].summary; got != "my own remark" {
		t.Fatalf("precondition: entry 0 is %q, want the local comment first", got)
	}

	m = press(m, "R")

	if len(*asked) != 0 {
		t.Errorf("R tried to resolve a local comment: %v", *asked)
	}
	if m.status == "" {
		t.Error("R did nothing and said nothing")
	}
}
