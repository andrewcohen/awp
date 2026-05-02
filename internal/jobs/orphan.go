package jobs

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// LivenessResult is the outcome of a single pid liveness probe.
type LivenessResult int

const (
	// LivenessAlive means the pid is alive and (where available) its
	// process start time matches the recorded value.
	LivenessAlive LivenessResult = iota
	// LivenessDead means the pid is no longer present.
	LivenessDead
	// LivenessReused means the pid is alive but belongs to a different
	// process than the one we spawned (start time differs).
	LivenessReused
	// LivenessUnknown means we couldn't decide (foreign host, no
	// permission to probe, platform unsupported). Callers should treat
	// this as "don't touch" — never declare orphan on Unknown.
	LivenessUnknown
)

// CheckLiveness returns the liveness state of the recorded subprocess.
// Returns LivenessUnknown when the record is from a different host —
// we can't probe pids on another machine. PID start time check is
// skipped if either the recorded or current value is zero (e.g. on
// platforms where capture failed).
func CheckLiveness(j Job) LivenessResult {
	host, _ := os.Hostname()
	if j.Host != "" && host != "" && j.Host != host {
		return LivenessUnknown
	}
	if j.PID <= 0 {
		return LivenessUnknown
	}
	switch err := syscall.Kill(j.PID, 0); {
	case err == nil:
		// Process exists. Confirm identity via start time if we have one.
		if j.PIDStartedAt > 0 {
			cur, ok := pidStartTime(j.PID)
			if ok && !startTimesMatch(cur, j.PIDStartedAt) {
				return LivenessReused
			}
		}
		return LivenessAlive
	case errors.Is(err, syscall.ESRCH):
		return LivenessDead
	case errors.Is(err, syscall.EPERM):
		// Process exists but we lack permission to signal it. Treat
		// as alive — orphaning a process we can't even verify is
		// dead would be a worse failure.
		return LivenessAlive
	default:
		return LivenessUnknown
	}
}

// startTimesMatch compares two start-time floats with a small epsilon
// to absorb format-conversion error (e.g. /proc reports jiffies, we
// store fractional seconds since boot).
func startTimesMatch(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.5
}

// IsOrphan reports whether a job should be declared orphaned: it's in
// a non-terminal state, its heartbeat is stale, and a liveness probe
// confirms the subprocess is gone (or pid was reused).
func IsOrphan(j Job, now time.Time) bool {
	if j.Status.IsTerminal() {
		return false
	}
	if !j.LastHeartbeat.IsZero() && now.Sub(j.LastHeartbeat) < HeartbeatStale {
		return false
	}
	switch CheckLiveness(j) {
	case LivenessDead, LivenessReused:
		return true
	}
	return false
}

// PIDStartTime returns the start time of the supplied pid as a float
// (seconds since boot, or platform-specific epoch). Used at spawn time
// to capture the value that orphan detection later compares against.
// Returns (0, false) if the platform isn't supported or capture
// failed; the orphan check skips the comparison in that case.
func PIDStartTime(pid int) (float64, bool) { return pidStartTime(pid) }
