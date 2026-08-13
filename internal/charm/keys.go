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
// split it, move the keyboard between halves, resize, zoom, close a half.
//
// The shifted form of the leave key, and that is the whole argument: the two
// gestures are the pair you reach for from inside a pane, so they should be one
// key apart rather than one arbitrary key each. It also keeps PaneLeaveKey a door
// on every surface — a single press, always back to the deck — instead of a
// prefix in a split and a door in a pane, which made the same key mean two
// things depending on how many panes were up.
//
// Only reachable where the terminal reports shifted control keys as distinct: a
// plain terminal sends 0x1c for ctrl+shift+\ exactly as for ctrl+\, so there is
// nothing to tell apart. See Model.keysEnhanced, and paneMenuPressed, which is
// where the ambiguity is resolved rather than at each call site.
const PaneMenuKey = "ctrl+|"
