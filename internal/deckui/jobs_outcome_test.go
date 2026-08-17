package deckui

import (
	"strings"
	"testing"
	"time"
)

// TestAFinishedJobSaysHowItWent. A background user action's whole failure mode
// was silence: the menu closed, the job ran, and the deck never said whether it
// worked.
func TestAFinishedJobSaysHowItWent(t *testing.T) {
	prev := []Job{{ID: "j1", Title: "install · ws", Status: JobRunning}}
	cur := []Job{{ID: "j1", Title: "install · ws", Status: JobDone}}
	if got := jobOutcomeStatus(prev, cur); got != "install · ws: done" {
		t.Errorf("a finished job reported %q", got)
	}
}

// TestAFailedJobSaysWhereTheOutputIs. "failed" with no next step is a dead end;
// the log is in the J overlay and nothing else on screen says so.
func TestAFailedJobSaysWhereTheOutputIs(t *testing.T) {
	prev := []Job{{ID: "j1", Title: "install · ws", Status: JobRunning}}
	cur := []Job{{ID: "j1", Title: "install · ws", Status: JobError, ErrMsg: "exit 1"}}
	got := jobOutcomeStatus(prev, cur)
	if !strings.Contains(got, "exit 1") || !strings.Contains(got, "J") {
		t.Errorf("a failed job reported %q", got)
	}
}

// TestTheDeckDoesNotAnnounceLastSessionsJobs. The first poll of a session reads
// records that are already terminal; announcing those would make the status bar
// a log of work the user has finished with.
func TestTheDeckDoesNotAnnounceLastSessionsJobs(t *testing.T) {
	cur := []Job{{ID: "j1", Title: "install · ws", Status: JobDone}}
	if got := jobOutcomeStatus(nil, cur); got != "" {
		t.Errorf("the deck announced a job it never saw run: %q", got)
	}
	if got := jobOutcomeStatus(cur, cur); got != "" {
		t.Errorf("the deck re-announced a job that had not moved: %q", got)
	}
}

// TestACancelledJobSaysNothing, matching the deck's status convention: the user
// pressed the key, so echoing the fact is noise.
func TestACancelledJobSaysNothing(t *testing.T) {
	prev := []Job{{ID: "j1", Title: "install · ws", Status: JobRunning}}
	cur := []Job{{ID: "j1", Title: "install · ws", Status: JobCancelled}}
	if got := jobOutcomeStatus(prev, cur); got != "" {
		t.Errorf("a cancellation was echoed back: %q", got)
	}
}

// TestAFinishedJobsChipOutlastsAGlance. Work you started and walked away from
// needs a ✓ that is still there when you look up.
func TestAFinishedJobsChipOutlastsAGlance(t *testing.T) {
	if jobDoneLingerDelay <= time.Second {
		t.Errorf("a job's ✓ lingers only %s", jobDoneLingerDelay)
	}
}

// TestAJobThatNeverStartedNamesItself. Every async action's spawn failure used
// to report itself as a create, which sends the reader to the wrong key.
func TestAJobThatNeverStartedNamesItself(t *testing.T) {
	m := New(nil, func(ActionRequest) error { return nil })
	spec := AsyncJobSpec{Action: "custom", Title: "install · ws", Arg: "install"}
	next, _ := m.Update(asyncJobDispatchedMsg{spec: spec, err: errFakeSpawn{}})
	got := next.(Model).status
	if !strings.Contains(got, "install · ws") || strings.Contains(got, "create") {
		t.Errorf("a failed user action reported %q", got)
	}
}

type errFakeSpawn struct{}

func (errFakeSpawn) Error() string { return "no such file" }
