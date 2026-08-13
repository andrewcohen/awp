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
// One press, two adjacent fingers, no shift, and no operating system in the way.
// That last part is what decided it: the previous spelling was ctrl+| —
// ctrl+shift+\ in the hand, a three-finger stretch for the key you press most —
// and the attempt before this one was ctrl+b, which macOS binds system-wide to
// switching input sources, so the key never reaches the terminal at all.
//
// ctrl+b is the tmux prefix and readline's backward-char, and a pane's program
// therefore stops receiving it. That is a real cost, accepted deliberately: awp
// reserves exactly two keys, and a reserved key is only reserved if something gives
// it up. ctrl+\ (SIGQUIT) was free because nothing interactive binds it; there is
// no second such key that is also comfortable, so the menu takes one that is.
//
// It needs nothing from the terminal either. 0x02 is 0x02 everywhere, with or
// without the Kitty keyboard protocol — where ctrl+| was indistinguishable from
// ctrl+\ on a plain terminal, so such a terminal had no menu at all.
//
// PaneLeaveKey is untouched: one press, always back to the deck, on every surface.
const PaneMenuKey = "ctrl+b"
