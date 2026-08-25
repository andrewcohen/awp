package jobs

import (
	"os"
	"testing"
	"time"
)

func TestIsOrphanTerminalSkipsCheck(t *testing.T) {
	if IsOrphan(Job{Status: StatusDone}, time.Now()) {
		t.Fatal("done job flagged as orphan")
	}
	if IsOrphan(Job{Status: StatusError}, time.Now()) {
		t.Fatal("error job flagged as orphan")
	}
	if IsOrphan(Job{Status: StatusOrphaned}, time.Now()) {
		t.Fatal("already-orphan job re-flagged")
	}
}

func TestIsOrphanFreshHeartbeatSkipsCheck(t *testing.T) {
	now := time.Now()
	j := Job{
		Status:        StatusRunning,
		PID:           1, // exists but heartbeat is fresh
		LastHeartbeat: now,
	}
	if IsOrphan(j, now) {
		t.Fatal("fresh heartbeat flagged as orphan")
	}
}

func TestIsOrphanForeignHostUnknown(t *testing.T) {
	now := time.Now()
	j := Job{
		Status:        StatusRunning,
		Host:          "some-other-machine-that-isnt-this-one",
		PID:           1,
		LastHeartbeat: now.Add(-1 * time.Hour),
	}
	if IsOrphan(j, now) {
		t.Fatal("foreign host job declared orphan")
	}
}

func TestIsOrphanDeadPID(t *testing.T) {
	host, _ := os.Hostname()
	now := time.Now()
	// PID 0 is a special "every process in our group" sentinel and
	// kill(0, 0) returns success — not what we want for a dead-pid
	// test. Use a very high PID unlikely to exist.
	j := Job{
		Status:        StatusRunning,
		Host:          host,
		PID:           99999999,
		LastHeartbeat: now.Add(-1 * time.Hour),
	}
	if !IsOrphan(j, now) {
		t.Fatal("dead pid not flagged as orphan")
	}
}

func TestCheckLivenessNoPID(t *testing.T) {
	if got := CheckLiveness(Job{}); got != LivenessUnknown {
		t.Fatalf("want Unknown, got %v", got)
	}
}

func TestCheckLivenessSelfAlive(t *testing.T) {
	host, _ := os.Hostname()
	j := Job{Host: host, PID: os.Getpid()}
	if got := CheckLiveness(j); got != LivenessAlive {
		t.Fatalf("want Alive for self, got %v", got)
	}
}

func TestCheckLivenessReused(t *testing.T) {
	host, _ := os.Hostname()
	// Pretend our own pid was started at a wildly different time —
	// liveness should report Reused. This test only runs meaningfully
	// on platforms where pidStartTime returns a real value.
	cur, ok := pidStartTime(os.Getpid())
	if !ok {
		t.Skip("pidStartTime unsupported on this platform")
	}
	j := Job{Host: host, PID: os.Getpid(), PIDStartedAt: cur + 1e6}
	if got := CheckLiveness(j); got != LivenessReused {
		t.Fatalf("want Reused, got %v", got)
	}
}
