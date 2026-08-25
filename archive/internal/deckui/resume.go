package deckui

import (
	tea "charm.land/bubbletea/v2"
)

// Opening the deck where you left it.
//
// `ctrl+\` has always resumed the pane you were last in, and `L` the one before
// that — but only within a run. Quitting threw the answer away, so every launch put
// you on the row list and the first thing you did was press a key to get back to the
// workspace you had been working in a minute earlier.
//
// The information was already there and startup did not read it. What was missing is
// this file: the arrangement in a form that can be written down, and the moment to act
// on it.
//
// **The row list becomes the way out rather than the way in.** That is the real change,
// and it is what makes the deck read as a place you return to instead of a menu you
// pass through. `ctrl+\` is already the door in both directions, so nothing new has to
// be learned.

// Arrangement is what was on screen last, in a form a caller can store.
//
// Exported and a struct, unlike ScopeSaver / SidebarSaver / SplitFracSaver, which each
// take a single value. SidebarSaver's comment named the condition for breaking that
// pattern — "reconsider if a setting ever has to be saved together with another" — and
// this is it: a workspace, a kind, and whether there was a second half are one answer.
// Stored apart they could be written by different runs and read back as an arrangement
// that never existed.
//
// Deliberately not paneArrangement itself. That has unexported fields, which is right
// for a type the deck mutates on every pane change, and wrong for one crossing a
// package boundary into a JSON file. The two conversions below are the seam.
type Arrangement struct {
	Project   string
	Workspace string
	Kind      string
	// RightKind is the kind beside it, and Split is what says there was one. An empty
	// kind is real — it is the shell — so "no split" cannot be inferred from the
	// string, which is the same trap paneArrangement.hasRight exists for.
	RightKind string
	Split     bool
	LeftFrac  float64
}

// Set reports whether this names anything. The zero value is "nothing remembered",
// which is what a first run and a corrupt file both come to.
func (a Arrangement) Set() bool { return a.Workspace != "" }

// ArrangementSaver records what is on screen, for the next deck to open into.
//
// Called on every change to the arrangement rather than at exit, because the deck has
// no reliable exit: it is killed, its terminal closes, the machine restarts. A save at
// exit is a save that does not happen on the occasions you most wanted it.
type ArrangementSaver func(Arrangement) error

// WithArrangement opens the deck into the arrangement it was left in.
//
// A caller that wants the row list simply does not call this — which is how `awp deck
// --scope=<scope>` keeps working: a flag naming which slice of the list to show is an
// instruction to show the list.
func (m Model) WithArrangement(a Arrangement) Model {
	if !a.Set() {
		return m
	}
	m.lastPane = paneArrangementFrom(a)
	m.resumePending = true
	return m
}

// WithArrangementSaver sets the hook called when the arrangement changes.
func (m Model) WithArrangementSaver(save ArrangementSaver) Model {
	m.saveArrangement = save
	return m
}

// exported is the arrangement as a caller can store it.
func (a paneArrangement) exported() Arrangement {
	return Arrangement{
		Project:   a.left.project,
		Workspace: a.left.workspace,
		Kind:      a.left.kind,
		RightKind: a.rightKind,
		Split:     a.hasRight,
		LeftFrac:  a.leftFrac,
	}
}

// paneArrangementFrom is the way back in.
func paneArrangementFrom(a Arrangement) paneArrangement {
	return paneArrangement{
		left:      paneRef{project: a.Project, workspace: a.Workspace, kind: a.Kind},
		rightKind: a.RightKind,
		hasRight:  a.Split,
		leftFrac:  a.LeftFrac,
	}
}

// resumeOnLaunch opens the remembered arrangement, once, as soon as the deck knows how
// big it is.
//
// Not from Init, because a pane has to be given a size and the terminal has not said
// what it is yet — a pty sized from a zero-value model is a program laid out for an
// 0x0 screen. The first WindowSizeMsg is the first moment the answer exists, and it
// arrives before anything is drawn.
//
// Once: the flag is cleared whether or not the open succeeds. A resize is a
// WindowSizeMsg too, so a flag left set would drag you back into the pane every time
// the terminal changed shape — including the resize that happens when you leave the
// pane on purpose.
//
// It reuses reopenPane, so a workspace that has since been deleted lands on its
// "not on the deck any more" status with the row list on screen. That is the escape
// hatch, and it is the same one `ctrl+\` already has: a deck that could not open
// because the thing it wanted to resume was gone would be a deck you had to repair a
// state file to use.
func (m *Model) resumeOnLaunch() tea.Cmd {
	if !m.resumePending {
		return nil
	}
	m.resumePending = false
	if m.panes == nil || m.width <= 0 || m.height <= 0 || m.active != nil {
		return nil
	}
	cmd, _ := m.reopenPane(m.lastPane)
	return cmd
}
