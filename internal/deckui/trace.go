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
// Nil unless the CLI layer installs it (AWP_TRACE=1): a frame that is not being
// traced should not pay to format or write a line about itself.
//
// The writer used to reopen the log file per line, which made the tracer the
// most expensive thing in a traced session — 38% of the process — and quietly
// inflated every number it reported. It now holds the file open, so a traced
// frame costs one write. Tracing is still opt-in, because one write per frame is
// not free either, and nothing needs it when nobody is looking.
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
	traceFrameRate(lastFrameEnd)
}

// frameBudget is how many frames a second is more than anything the deck shows
// can justify. The spinner is the fastest thing on screen at 10/s, and a message
// loop renders in the hundreds — there is nothing legitimate in between.
const frameBudget = 60

var (
	frameWindow time.Time
	frameCount  int
)

// traceFrameRate says so when frames are being drawn faster than anything on
// screen changes.
//
// It exists because the defect it catches is invisible from the outside: an idle
// deck rendering 430 frames a second looks exactly like an idle deck, and the
// only symptom is a warm laptop. Finding it took a CPU profile; this makes the
// next one a line in the trace log. Behind AWP_TRACE, so it costs nothing when
// nobody is looking.
func traceFrameRate(now time.Time) {
	if frameWindow.IsZero() {
		frameWindow, frameCount = now, 1
		return
	}
	frameCount++
	if elapsed := now.Sub(frameWindow); elapsed >= time.Second {
		if rate := float64(frameCount) / elapsed.Seconds(); rate > frameBudget {
			Trace("frame rate %.0f/s over budget %d — something is emitting messages in a loop", rate, frameBudget)
		}
		frameWindow, frameCount = now, 0
	}
}
