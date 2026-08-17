package deckui

import tea "charm.land/bubbletea/v2"

// The captain: an agent whose subject is awp itself.
//
// Every other agent in the deck lives in a workspace and works on a repository.
// The captain has neither. Its job is the work *between* workspaces — noticing
// that a PR wants a repair, that a finished workspace wants deleting, that the
// thing blocking one agent is something another agent knows — and its tools are
// awp's own CLI verbs rather than files.
//
// It is a place, not a row. `a` reaches it from anywhere in the row list, because
// there is exactly one of it and nothing to select; it is deliberately absent from
// the list, from the attention scopes and from every count, so adding it costs the
// deck no rows and no guards. What it is, structurally, is one pane the deck knows
// how to open without a workspace behind it.

// PaneKindCaptain is the window kind whose process is the captain's agent.
//
// Named for the same reason PaneKindAgent is: the deck asks whether the backend
// describes it, and the backend maps it to a command, so the two have to mean the
// same string without either writing it out twice.
const PaneKindCaptain = "captain"

// CaptainProject and CaptainWorkspace are the identity the captain wears wherever
// a pane wants a project and a workspace — its label, its session name, the
// arrangement the deck remembers.
//
// It is not a workspace and there is no entry by this name in any state file. The
// two exist because a pane is addressed by that pair and the captain needs an
// answer that collides with nothing: no project is called `awp` in a deck whose
// projects are repositories, and the pair reads correctly wherever it surfaces.
const (
	CaptainProject   = "awp"
	CaptainWorkspace = "captain"
)

// CaptainItem is the row-shaped value the captain presents to a pane host.
//
// Path is deliberately empty, and so is RepoRoot: the captain has no working copy,
// and a pane host that resolved a directory from this Item would be guessing. The
// backend names the captain's own directory — see the captain's paneSpec — which is
// the one place that knows awp owns it.
func CaptainItem() Item {
	return Item{ProjectName: CaptainProject, WorkspaceName: CaptainWorkspace}
}

// openCaptain puts the captain's pane on screen, whatever the cursor is on.
//
// Reports the refusal itself rather than returning a "not handled" for a caller to
// translate: there is no tmux fallback for the captain the way there is for a
// review window. A deck with no pane backend simply cannot host one, and saying so
// is the whole answer.
func (m Model) openCaptain() (tea.Model, tea.Cmd) {
	if m.panes == nil || !m.panes.Describes(PaneKindCaptain) {
		m.status = "captain: this deck cannot host panes, so there is nowhere to run it"
		return m, nil
	}
	cmd, _ := m.openPane(CaptainItem(), PaneKindCaptain)
	return m, cmd
}

// captainKey is the verb that reaches the captain from inside a pane's menu.
//
// The same letter the row list uses, and that is the point rather than a
// coincidence: inside a pane every key belongs to the hosted program, so `a` there
// is the agent's `a`. The menu is the only door, and a hub you can only reach by
// first leaving what you are doing is a hub you will not use. Spelling it with a
// different letter would make the captain two keys depending on where you stood.
const captainKey = "a"

// captainVerb is the menu row, worded as a place rather than a program.
func captainVerb() [2]string {
	return [2]string{captainKey, "the captain — an agent that drives awp itself"}
}

// captainFrom opens the captain in place of whatever pane is on screen, closing it
// first.
//
// close comes from the caller because what has to come down differs: a single pane
// closes itself, a split closes both halves. Not closing it leaks a live process
// and its pty with nothing on screen to say so — the defect
// TestAlternatingAwayClosesThePaneItLeft exists for.
func (m *Model) captainFrom(close func() tea.Cmd) tea.Cmd {
	if m.panes == nil || !m.panes.Describes(PaneKindCaptain) {
		m.status = "captain: this deck cannot host panes, so there is nowhere to run it"
		return nil
	}
	closed := close()
	opened, _ := m.openPane(CaptainItem(), PaneKindCaptain)
	return tea.Batch(closed, opened)
}
