package jobs

import (
	"context"
	"time"
)

// Heartbeater periodically bumps a job's LastHeartbeat timestamp so
// the deck-side orphan check has a recent value to compare against.
// Started by the run-job subprocess; stopped via context cancellation
// when the job reaches a terminal state.
type Heartbeater struct {
	store    *Store
	id       JobID
	interval time.Duration
}

// NewHeartbeater returns a Heartbeater for the given job. Use
// HeartbeatInterval as a sensible default for interval.
func NewHeartbeater(store *Store, id JobID, interval time.Duration) *Heartbeater {
	if interval <= 0 {
		interval = HeartbeatInterval
	}
	return &Heartbeater{store: store, id: id, interval: interval}
}

// Run blocks until ctx is cancelled, sending a heartbeat every
// interval. Errors from the store (e.g. lock contention) are swallowed
// — heartbeat is best-effort. The next interval will retry.
func (h *Heartbeater) Run(ctx context.Context) {
	t := time.NewTicker(h.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = h.store.Heartbeat(h.id)
		}
	}
}
