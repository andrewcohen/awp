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
