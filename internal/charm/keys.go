package charm

// PaneLeaveKey gives the keyboard back to whatever hosted this program.
//
// It has to be a key nothing inside a pane wants, because everything else
// belongs to the program: esc, q and ctrl+c all mean something to an agent.
// ctrl+\ is normally SIGQUIT, which is exactly why nothing interactive binds it,
// and a Bubble Tea program reads it as a key because its terminal is in raw mode.
//
// Declared here, in the package both TUIs already share, for the same reason
// KeyGroup is: internal/ui cannot import internal/deckui, and this key is a
// promise every awp surface makes rather than the deck's private business. A
// program that can be the thing inside a pane has to answer for it itself —
// under a handed-over pane the deck is suspended and reading nothing — so the
// spelling has to be reachable from all of them, and there has to be only one of
// it. The hint a pane's chrome prints and the key those programs bind are the
// same string or the hint is a lie.
const PaneLeaveKey = "ctrl+\\"

// PaneMenuKey opens the menu of things you can do to the arrangement on screen —
// split it, move the keyboard between halves, resize, zoom, close a half, show
// the attention sidebar.
//
// Two adjacent fingers and no shift, for the key you reach for most from inside a
// pane. It was ctrl+| — the shifted leave key — on the argument that the two
// gestures should be one key apart; what that spelled in the hand was
// ctrl+shift+\, a three-finger stretch next to a door that costs one press. The
// pairing was a nice property of the notation rather than of the gesture.
//
// Nothing inside a pane claims it. 0x00 is ctrl+@ historically, which no shell or
// agent binds — emacs' set-mark is the only common claim, and emacs is not what
// runs in these panes.
//
// It also works on every terminal, which ctrl+| did not: a plain terminal sends
// 0x1c for ctrl+shift+\ exactly as for ctrl+\, so there was nothing to tell apart
// and such a terminal simply had no menu — the one surface awp had that some
// terminals could not reach at all. 0x00 decodes to {Code: KeySpace, Mod: ModCtrl}
// whether or not the Kitty keyboard protocol is in play (ultraviolet's legacy key
// table and its Kitty map agree), so the menu no longer depends on a flag the
// terminal may not grant. keysEnhanced is still asked for, and still decides whether
// a held key can be told from a tapped one; the menu simply no longer needs it.
//
// The old spelling is gone rather than kept as an alias. Two keys for one gesture is
// two things to document and a second path through the predicate that reads them,
// and the reason the old one existed — the pairing with the leave key — is the thing
// being given up on purpose.
//
// PaneLeaveKey is untouched: one press, always back to the deck, on every surface.
const PaneMenuKey = "ctrl+space"
