//go:build !linux && !darwin

package jobs

// pidStartTime is a no-op on platforms we haven't implemented. The
// orphan check tolerates this and falls back to pure pid liveness.
func pidStartTime(pid int) (float64, bool) { return 0, false }
