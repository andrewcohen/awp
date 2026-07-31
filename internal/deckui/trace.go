package deckui

import (
	"fmt"
	"time"
)

// Frame tracing.
//
// Every measurement of the viewer in isolation said a frame costs about a
// millisecond, while the running deck was reported as too slow to scroll. That
// gap is the thing this answers, and it answers it with two numbers per frame
// rather than one:
//
//   - how long our code took (update, body, pad, view)
//   - how long passed since the previous frame *ended* — the gap
//
// A large gap with small costs means the time is not being spent in Go at all:
// it is the terminal, tmux, or Bubble Tea's own loop, and no amount of
// optimising a render path will touch it. A large cost with a small gap means the
// opposite. Guessing between those two is what wasted the first three attempts.
//
// Nil unless the CLI layer installs it (AWP_TRACE=1), because the writer opens
// the log file per line — fine for a diagnostic session, not for every frame of
// every deck.
var Trace func(format string, args ...any)

// lastFrameEnd is when the previous View returned, for the gap measurement.
var lastFrameEnd time.Time

func sinceMS(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}

// traceSince logs how long something took. Deferred at the top of a function:
//
//	defer traceSince(time.Now(), "diff.update %T", msg)
func traceSince(start time.Time, format string, args ...any) {
	if Trace == nil {
		return
	}
	Trace("%s %.1fms", fmt.Sprintf(format, args...), sinceMS(start))
}

// traceSteps returns a stepper that logs the time since the previous step, for
// finding which part of a frame is expensive. A no-op when tracing is off.
func traceSteps() func(string) {
	if Trace == nil {
		return func(string) {}
	}
	last := time.Now()
	return func(name string) {
		Trace("  step %-10s %.1fms", name, sinceMS(last))
		last = time.Now()
	}
}

// traceFrame logs one frame's cost and the gap since the previous one.
func traceFrame(start time.Time, bytes int) {
	if Trace == nil {
		return
	}
	gap := 0.0
	if !lastFrameEnd.IsZero() {
		gap = float64(time.Since(lastFrameEnd).Microseconds())/1000 - sinceMS(start)
	}
	Trace("frame view %.1fms gap %.1fms bytes %d", sinceMS(start), gap, bytes)
	lastFrameEnd = time.Now()
}
