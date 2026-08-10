package vterm

import "sync"

// live is every Term that has been started and not yet closed.
//
// Package-level state, which this repo otherwise avoids, and the invariant is
// exactly why it earns the exception: a process on a pty must not outlive the
// process that started it, and a guarantee resting on every exit path
// remembering to close is not a guarantee. What a forgotten close looks like is
// two `zmx attach` clients with ppid 1 — one of them holding a defunct agent —
// accumulating one per pane per deck run, each holding a pty for a deck that no
// longer exists.
//
// The deck cannot enumerate them itself: it holds the one pane in its active
// slot, so any Term reached by another path, or dropped by a future one, is
// invisible to it. Registering at the only place a Term is created is the one
// spelling that cannot be forgotten.
var live struct {
	sync.Mutex
	terms map[*Term]struct{}
}

func register(t *Term) {
	live.Lock()
	defer live.Unlock()
	if live.terms == nil {
		live.terms = map[*Term]struct{}{}
	}
	live.terms[t] = struct{}{}
}

func unregister(t *Term) {
	live.Lock()
	defer live.Unlock()
	delete(live.terms, t)
}

// CloseAll tears down every Term still open, and is what a program hosting them
// defers so no exit it returns from can leave one behind.
//
// That covers a normal quit, a SIGINT or SIGTERM (Bubble Tea turns both into a
// message and returns from Run), a SIGHUP the host converts into a quit, and a
// panic unwinding through the deferred call. It cannot cover SIGKILL: nothing
// in the process runs then, and the hosted client is left to notice the pty
// master closing under it.
func CloseAll() {
	live.Lock()
	terms := make([]*Term, 0, len(live.terms))
	for t := range live.terms {
		terms = append(terms, t)
	}
	live.Unlock()

	// Outside the lock: Close unregisters, which takes it.
	for _, t := range terms {
		_ = t.Close()
	}
}
