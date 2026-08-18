package main

import (
	"context"
	"log/slog"
)

// Probe is how the frontend reports a POC result back to something that can be
// read without a human looking at the window.
//
// The questions this surface exists to answer are mostly answered in the
// webview — wasm instantiates or it does not, a pane keeps up or it does not —
// and the obvious way to check is to look at the screen. That does not survive
// being run twice: nobody diffs two screenshots, and a result nobody recorded is
// a result that gets remembered as better than it was. So each step reports
// through here and the answer lands in gdeck's log next to the timings.
//
// It is not a substitute for looking. Whether a pane *feels* right is exactly
// the thing a pass/fail line cannot carry, which is why latency is reported as a
// number rather than a verdict.
type Probe struct{}

// Report records the outcome of one POC check. detail carries the error when ok
// is false, and whatever measurement the check produced when it is true.
func (p *Probe) Report(check string, ok bool, detail string) {
	level := slog.LevelInfo
	result := "pass"
	if !ok {
		level, result = slog.LevelError, "FAIL"
	}
	slog.Log(context.Background(), level, "gdeck probe",
		"check", check, "result", result, "detail", detail)
}
