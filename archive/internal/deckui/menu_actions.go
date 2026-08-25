package deckui

import (
	tea "charm.land/bubbletea/v2"
)

// The user actions arm of the ctrl+b menus.
//
// A user action was already reachable from the row list under `x`, and already a
// pane kind (PaneKindForAction) rather than a thing of its own — which is the whole
// reason this is short. From inside a pane there was no way to reach one at all: the
// only key that meant anything was ctrl+\ back to the deck, and pressing `x` there
// acted on whichever row the cursor was on rather than on the workspace you were
// looking at.
//
// So it is a second door onto the same actions, not a second implementation of them.
// The submenu is a two-step — ctrl+b x, then the action's alias — rather than the
// aliases sitting on the ctrl+b menu directly, because the aliases are the user's to
// choose and the window keys are not: a config that names an action `c` would
// otherwise take the diff key away from itself and there would be nothing the deck
// could do about it. Behind `x` they collide with nothing.

// userActionsMenuKey opens the submenu from an armed ctrl+b. `x` because that is
// what the row list has always used, so the gesture is the same one from either
// place. It cost the split menu its close-the-half key, which moved to `q`.
const userActionsMenuKey = "x"

// userActionsVerb is the submenu's row on a ctrl+b menu, or an empty row when this
// workspace has no actions configured.
//
// Empty rather than listed-and-inert: menu() drops a verb with no key, and a menu
// that offers a door onto nothing is worse than one that does not mention it. A repo
// with no `actions` block in its config sees the menu it saw before.
func userActionsVerb(actions []UserAction) [2]string {
	if len(aliasLookup(actions)) == 0 {
		return [2]string{}
	}
	return [2]string{userActionsMenuKey, "user actions, by alias"}
}

// userActionsMenu is the submenu itself: one row per action, keyed by its alias.
//
// The alias is the key and the name is the description, which is all the config
// carries — deliberately not the command, which is a shell line long enough to wrap
// the box and says nothing the name should not already say.
func userActionsMenu(actions []UserAction) deckMenu {
	verbs := make([][2]string, 0, len(actions)+1)
	for _, a := range actions {
		// An action with no alias is unreachable by definition, since the alias is
		// how one is pressed. It stays in the config's list so `x`'s status line
		// still names it, but a menu row with no key is a row you cannot press.
		verbs = append(verbs, [2]string{a.Alias, a.Name})
	}
	return menu(append(verbs, menuCancelVerb)...)
}

// userActionsFor is the actions the pane or split in front of you should offer:
// those configured for the repo of the workspace it is hosting.
//
// The hosted row rather than m.selected(), because the deck's cursor is free to be
// somewhere else — a refresh reorders the list, or you left the pane, moved, and came
// back. A menu opened over a pane names what it can do to that pane.
func (m *Model) userActionsFor() []UserAction {
	var repoRoot string
	if item, ok := m.topRowRow(); ok {
		repoRoot = item.RepoRoot
	} else if item, ok := m.selected(); ok {
		repoRoot = item.RepoRoot
	}
	return m.userActionsForRepo(repoRoot)
}

// resolveActionKey answers one key pressed at the submenu.
func resolveActionKey(actions []UserAction, pressed string) (UserAction, bool) {
	ua, ok := aliasLookup(actions)[pressed]
	return ua, ok
}

// startBackgroundAction runs a background user action from a ctrl+b menu, leaving
// what is on screen where it is.
//
// Through trigger, the same entry point `x` uses, so a background action is routed to
// the job substrate by the one piece of code that knows how — see trigger's
// ActionCustom branch, which declines to make a pane kind of one for exactly this
// reason. The model comes back as a tea.Model because trigger is on the value; a menu
// handler holds the pointer, so it is written back rather than returned.
func startBackgroundAction(m *Model, name string) tea.Cmd {
	next, cmd := m.trigger(ActionCustom, name)
	if mm, ok := next.(Model); ok {
		*m = mm
	}
	return cmd
}
