//go:build ghosttyvt

package vterm

import "io"

// The package's shared machinery, wired to the emulator: the registry that makes
// sure no pane outlives the deck, and the byte log's two directions.
//
// Tagged, because a build without an emulator creates no terminals — so nothing
// registers and nothing is logged, and a function nobody can reach is one the
// linter is right to call unused. Their halves that the deck does reach (CloseAll,
// TapTerminal) stay untagged where they are.

func register(t interface{ Close() error }) {
	live.Lock()
	defer live.Unlock()
	if live.terms == nil {
		live.terms = map[interface{ Close() error }]struct{}{}
	}
	live.terms[t] = struct{}{}
}

func unregister(t interface{ Close() error }) {
	live.Lock()
	defer live.Unlock()
	delete(live.terms, t)
}

// tapPair returns writers that record each direction of a pane's traffic, or
// the originals when no log is configured.
func tapPair(sink *logSink, toEmulator, toProcess io.Writer) (io.Writer, io.Writer) {
	if sink == nil {
		return toEmulator, toProcess
	}
	return &tap{mu: sink.mu, f: sink.f, dir: "out", next: toEmulator},
		&tap{mu: sink.mu, f: sink.f, dir: "in", next: toProcess}
}
