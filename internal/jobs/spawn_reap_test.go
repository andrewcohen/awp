//go:build unix

package jobs

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestASpawnedJobIsReapedWhenItEnds.
//
// Detaching a job is about who it obeys, not about who its parent is: Setsid
// gives it its own session, and it stays this process's child either way. A
// parent that never waits leaves every finished job as a <defunct> entry in the
// process table — and the deck spawns a pr-status job every few seconds, so
// "never" is measured in hundreds per hour.
//
// The check is that the pid stops existing rather than that it stopped running.
// A zombie is still a process: signal 0 finds one, and only the wait that reaps
// it makes the pid go away.
func TestASpawnedJobIsReapedWhenItEnds(t *testing.T) {
	yes, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` on this machine to spawn as a job: %v", err)
	}
	s := NewStoreWithDir(t.TempDir())
	job, err := s.Spawn(Spec{Action: ActionPRStatus}, "reap me", SpawnOptions{
		Binary: yes,
		Args:   []string{"ignored"},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if job.PID == 0 {
		t.Fatal("the job recorded no pid, so there is nothing to look for")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(job.PID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is still in the process table five seconds after it exited (kill(0) says %v) — nothing waited on it", job.PID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
