package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestTheDeckCannotExitWithoutTearingDownItsPanes.
//
// The leak this guards is invisible: the deck quits, the terminal comes back,
// and a `zmx attach` client keeps running with ppid 1, holding a pty for a deck
// that no longer exists — one per pane per deck run, and one of them was found
// holding a defunct agent nobody reaped.
//
// Both halves have to be present and both have to be deferred:
//
//   - vterm.CloseAll closes every Term still open. Deferred rather than called
//     after Run so a panic unwinding through here is covered too.
//   - quitOnHangup turns SIGHUP into a quit. Bubble Tea already turns SIGINT and
//     SIGTERM into messages and returns from Run, but SIGHUP keeps its default
//     disposition and kills the process outright, so a closed terminal window
//     would skip the teardown entirely.
//
// Pinned structurally because nothing fails when it goes: the deck exits
// cleanly either way and the orphan is only visible in ps, days later.
func TestTheDeckCannotExitWithoutTearingDownItsPanes(t *testing.T) {
	const file = "deck.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	fn := findFunc(f, "runDeckWithCharm")
	if fn == nil {
		t.Fatal("runDeckWithCharm is gone; this guard is measuring nothing")
	}

	deferred := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		d, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		deferred[calleeName(d.Call.Fun)] = true
		return true
	})
	// stopHangup is quitOnHangup's own returned stopper, so its presence in the
	// deferred set is how the listener being installed shows up here.
	for name, why := range map[string]string{
		"CloseAll":   "nothing closes a hosted pane when the deck exits, so its client outlives the deck",
		"stopHangup": "quitOnHangup is not installed, so closing the terminal window skips the teardown",
	} {
		if !deferred[name] {
			t.Errorf("runDeckWithCharm does not defer %s — %s", name, why)
		}
	}
}
