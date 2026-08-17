package deckui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/vterm"
)

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

// openCaptain floats the captain over whatever the deck is showing.
//
// Whatever the cursor is on, and whatever is on screen: the row list, a pane, a
// split. Nothing is closed on the way — that is the difference between a modal and
// a screen, and it is the whole reason the captain is worth opening from inside a
// pane at all. What is behind it is what you are checking it against.
//
// Reports the refusal itself rather than returning a "not handled" for a caller to
// translate: there is no tmux fallback for the captain the way there is for a
// review window. A deck with no pane backend simply cannot host one, and saying so
// is the whole answer.
func (m *Model) openCaptain() tea.Cmd {
	if m.panes == nil || !m.panes.Describes(PaneKindCaptain) {
		m.status = "captain: this deck cannot host panes, so there is nowhere to run it"
		return nil
	}
	if m.captain != nil {
		// Already up, and the keys are already in it. Opening a second one would
		// start a second process against the same session.
		return nil
	}
	// remember=false: the captain is not an arrangement to come back to. `L` means
	// the pair of panes you were working in, and the captain is what you opened
	// *over* them — recording it would make going back go somewhere you never left.
	p, cmd, handled := m.newPane(CaptainItem(), PaneKindCaptain, m.captainBox(), false)
	if !handled || p == nil {
		return cmd
	}
	m.captain = p
	return cmd
}

// captainBox is where the captain floats: its fraction of the screen, centred on
// the screen, clamped out of the cells the deck is drawing its own chrome in.
//
// Derived from childBox — what the deck's current screen leaves — rather than from
// the terminal, so a captain over a pane with the sidebar up clears the strip, and
// one over the row list clears the title row. Every path that places the captain
// asks this, so the render, the cursor and the mouse cannot come to disagree.
func (m *Model) captainBox() box {
	return captainRegion(m.screenBox(), m.childBox())
}

// captainWidthNum / captainWidthDen and captainHeightNum / captainHeightDen are
// how much of its region the captain gets: four fifths of the width, three fifths
// of the height.
//
// Wider than tall because the captain's output is prose, and prose wants columns
// more than it wants rows. Spelled as integer fractions rather than as a float so
// the arithmetic is the same on every terminal and a test can name the answer.
const (
	captainWidthNum, captainWidthDen   = 4, 5
	captainHeightNum, captainHeightDen = 3, 5
)

// captainMinW / captainMinH are the terminal the captain will not go below —
// what its box needs, chrome included.
//
// 60 columns is a readable measure for prose and the width the deck's fixed-width
// popovers already chose. Below the floor the fraction is abandoned rather than
// honoured: a small terminal gives the captain its whole region, which is what
// every other pane gets, because there is no size at which a smaller box reads as
// deliberate rather than as clipping.
const (
	captainMinW = 60 + paneChromeW
	captainMinH = 15 + paneChromeH
)

// captainRegion is the box the captain's pane draws in: a fraction of the screen,
// centred on the screen, clamped into the region it was given.
//
// The captain is not a workspace — it has no repository — so wearing a workspace
// pane's full-screen chrome said it was one. The modal is the whole of that
// difference: same border, same host bar, less screen, so what is around it reads
// as awp rather than as a working copy. See #385.
//
// Measured and centred against the screen rather than against the region, because
// the region is the screen less the deck's own chrome — the top row's one line, the
// sidebar's columns — and a modal centred in *that* sits a row low and, with the
// strip up, well right of the middle. What the eye centres a floating box against
// is the display it is floating over, so that is what the arithmetic uses.
//
// The clamp is what keeps the two compatible: the box may not start above the
// region or left of it, because those cells belong to the top row and the strip.
// A modal that has to be pushed is a modal that is no longer centred, and that is
// the right trade — chrome the deck is drawing must not be drawn over.
//
// Returns region unchanged when the fraction would land under the floor or would
// not fit, and when region is shared: a split's half is already not the screen,
// and halving it again would leave a program too little to be worth showing.
func captainRegion(screen, region box) box {
	if region.shared {
		return region
	}
	w := screen.w * captainWidthNum / captainWidthDen
	h := screen.h * captainHeightNum / captainHeightDen
	if w < captainMinW || h < captainMinH || w > region.w || h > region.h {
		return region
	}
	// The origin moves with it: the mouse and the cursor are both placed from this
	// box, so a box that reported the region's origin would put both of them where
	// the pane used to be.
	region.x = clampCaptain(screen.x+(screen.w-w)/2, region.x, region.x+region.w-w)
	region.y = clampCaptain(screen.y+(screen.h-h)/2, region.y, region.y+region.h-h)
	region.w, region.h = w, h
	return region
}

func clampCaptain(v, lo, hi int) int { return min(max(v, lo), hi) }

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

// deliverToCaptain gives the captain the messages that are its while it is up, and
// says whether this was one of them.
//
// Keys and paste, because it has modal focus. Mouse, when the pointer is inside its
// box — outside it belongs to nothing, since a click on a program you cannot type at
// would move a cursor you cannot see the effect of. And its own terminal's output
// and exit, matched on generation so the pane behind it keeps its own.
func (m *Model) deliverToCaptain(msg tea.Msg) (tea.Cmd, bool) {
	p := m.captain
	switch msg := msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		return p.update(m, msg), true
	case vterm.OutputMsg:
		if msg.Gen != p.term.Gen() {
			return nil, false
		}
		return p.update(m, msg), true
	case vterm.ExitMsg:
		if msg.Gen != p.term.Gen() {
			return nil, false
		}
		return p.update(m, msg), true
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		mouse := msg.(tea.MouseMsg).Mouse()
		b := m.captainBox()
		if mouse.X < b.x || mouse.X >= b.x+b.w || mouse.Y < b.y || mouse.Y >= b.y+b.h {
			// Swallowed rather than passed on: what is behind a modal is being read,
			// not operated, and a wheel that scrolled it would scroll the thing you
			// were checking the captain's claims against out from under you.
			return nil, true
		}
		return p.update(m, msg), true
	}
	return nil, false
}

// captainOverPane is the pane menu's `a`: the captain floated over the pane the
// menu belongs to, which stays exactly where it was.
//
// It used to close that pane first, and taking a live agent's screen down to ask
// awp a question about it was the wrong trade every time — you came back to the row
// list rather than to the thing you had been reading, and the agent's pane had to
// be reopened by hand. Now nothing comes down: the captain is a box over it, and
// dismissing the box gives the pane back.
//
// A thin wrapper rather than a call to openCaptain at the two menu sites, because
// the menus' key handlers are where a reader looks for what `a` does from inside a
// pane, and "over" is the part of the answer that changed.
func (m *Model) captainOverPane() tea.Cmd { return m.openCaptain() }
