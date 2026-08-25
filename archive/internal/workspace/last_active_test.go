package workspace

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// The status and the moment it arrived are written together, because a
// timestamp that is only sometimes updated is a wrong answer rather than a
// missing one — the entry would go on claiming the agent last did something at
// whatever moment the previous caller happened to remember.
func TestSetStatusDatesTheStatus(t *testing.T) {
	at := time.Date(2026, 8, 6, 9, 30, 0, 0, time.UTC)
	var e Entry
	e.SetStatus("waiting", at)
	if e.Status != "waiting" {
		t.Errorf("Status = %q, want waiting", e.Status)
	}
	if !e.LastActiveAt.Equal(at) {
		t.Errorf("LastActiveAt = %v, want %v", e.LastActiveAt, at)
	}
}

// Touch records activity that changed no status: summoning the workspace, or
// clearing its badge because you went and looked.
func TestTouchRecordsActivityWithoutAStatus(t *testing.T) {
	at := time.Date(2026, 8, 6, 9, 30, 0, 0, time.UTC)
	e := Entry{Status: "idle"}
	e.Touch(at)
	if e.Status != "idle" {
		t.Errorf("Touch changed the status to %q", e.Status)
	}
	if !e.LastActiveAt.Equal(at) {
		t.Errorf("LastActiveAt = %v, want %v", e.LastActiveAt, at)
	}
}

// "I don't know when" must not overwrite "I know when". A caller with no clock
// to hand leaves the last real answer standing rather than erasing it — a zero
// timestamp reads as unknown everywhere downstream, so storing one would turn a
// recently-active workspace into one nothing can date.
func TestAZeroMomentLeavesTheLastRealOneStanding(t *testing.T) {
	at := time.Date(2026, 8, 6, 9, 30, 0, 0, time.UTC)
	e := Entry{LastActiveAt: at}
	e.Touch(time.Time{})
	if !e.LastActiveAt.Equal(at) {
		t.Errorf("a zero moment overwrote %v with %v", at, e.LastActiveAt)
	}
	e.SetStatus("working", time.Time{})
	if !e.LastActiveAt.Equal(at) {
		t.Errorf("SetStatus with a zero moment overwrote %v with %v", at, e.LastActiveAt)
	}
	// The status still lands — only the dating is declined.
	if e.Status != "working" {
		t.Errorf("Status = %q, want working", e.Status)
	}
}

// The three service calls that constitute activity all date it: the agent
// reporting a status, and the two ways you open a workspace.
//
// Asserted through the service rather than on Entry directly, because the bug
// this guards against is a mutator that forgets to stamp — which is invisible
// from the method that does.
func TestTheServiceDatesEveryKindOfActivity(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(*service) error
	}{
		{"UpdateStatus", func(s *service) error { return s.UpdateStatus("qa", "waiting") }},
		{"MarkRead", func(s *service) error { return s.MarkRead("qa") }},
		{"RecordSession", func(s *service) error { return s.RecordSession("qa", "sid", "sname") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: repoRoot + "/qa"}}}
			svc := NewService(Dependencies{
				JJ:    &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"qa": true}},
				Tmux:  &fakeTmux{windows: map[string]bool{}},
				Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard,
			})

			before := time.Now()
			if err := tc.act(svc); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got := store.entries["qa"].LastActiveAt
			if got.IsZero() {
				t.Fatalf("%s left the workspace undated", tc.name)
			}
			if got.Before(before) || got.After(time.Now()) {
				t.Errorf("%s dated the workspace %v, outside the call", tc.name, got)
			}
		})
	}
}

// An entry written before the field existed loads with a zero time, and that
// has to stay readable as "unknown" rather than as a real moment in 1970 — the
// scope work that consumes this treats unknown as no opinion, which only works
// if nothing has quietly filled the field in.
func TestAnUndatedEntryStaysUndated(t *testing.T) {
	repoRoot := t.TempDir()
	store := &fakeStore{entries: map[string]Entry{"qa": {Name: "qa", Path: repoRoot + "/qa"}}}
	svc := NewService(Dependencies{
		JJ:    &fakeJJ{repoRoot: repoRoot, existing: map[string]bool{"qa": true}},
		Tmux:  &fakeTmux{windows: map[string]bool{}},
		Store: store, Input: bytes.NewBuffer(nil), Out: io.Discard,
	})
	// A mutation that is not activity: renaming the bookmark says nothing about
	// when the agent last did anything.
	if err := svc.RecordBookmark("qa", "andrew/thing"); err != nil {
		t.Fatalf("RecordBookmark: %v", err)
	}
	if got := store.entries["qa"].LastActiveAt; !got.IsZero() {
		t.Errorf("a non-activity mutation dated the workspace %v", got)
	}
}
