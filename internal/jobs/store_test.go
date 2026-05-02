package jobs

import (
	"errors"
	"io/fs"
	"sync"
	"testing"
	"time"
)

func TestStoreSaveAndGet(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	job := Job{
		ID:        "20260502-aaaa",
		Title:     "create · feat/x",
		Spec:      Spec{Action: ActionCreateWorkspace, Name: "feat/x"},
		Status:    StatusPending,
		StartedAt: time.Now(),
	}
	if err := s.Save(job); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Get(job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != job.ID || got.Title != job.Title || got.Spec.Name != job.Spec.Name {
		t.Fatalf("round-trip mismatch: got %+v", got)
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	_, err := s.Get("nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestStoreList(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	for _, id := range []JobID{"20260502-aaaa", "20260502-bbbb", "20260502-cccc"} {
		if err := s.Save(Job{ID: id, Status: StatusPending, StartedAt: time.Now()}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 jobs, got %d", len(got))
	}
}

func TestStoreListMissingDir(t *testing.T) {
	// Pointing at a path that doesn't exist must return an empty
	// slice, not an error — first deck launch shouldn't blow up.
	s := NewStoreWithDir(t.TempDir() + "/does-not-exist")
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestStoreUpdateAtomic(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	id := JobID("20260502-aaaa")
	if err := s.Save(Job{ID: id, Status: StatusRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Many concurrent goroutines each append a step; final count must
	// equal the number of writers (no lost updates).
	const writers = 20
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			if err := s.AppendStep(id, "step"); err != nil {
				t.Errorf("AppendStep: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Steps) != writers {
		t.Fatalf("want %d steps, got %d", writers, len(got.Steps))
	}
}

func TestStoreAppendLogTrim(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	id := JobID("20260502-aaaa")
	if err := s.Save(Job{ID: id, Status: StatusRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for i := 0; i < MaxInlineLogLines+50; i++ {
		if err := s.AppendLog(id, "line"); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.LogsInline) != MaxInlineLogLines {
		t.Fatalf("logs not trimmed: got %d, want %d", len(got.LogsInline), MaxInlineLogLines)
	}
}

func TestStoreMarkDoneIdempotent(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	id := JobID("20260502-aaaa")
	if err := s.Save(Job{ID: id, Status: StatusRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.MarkDone(id, StatusDone, ""); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	first, _ := s.Get(id)
	// Second MarkDone should be a no-op (don't overwrite a terminal state).
	if err := s.MarkDone(id, StatusError, "boom"); err != nil {
		t.Fatalf("MarkDone twice: %v", err)
	}
	second, _ := s.Get(id)
	if second.Status != first.Status || second.ErrMsg != first.ErrMsg {
		t.Fatalf("terminal state was overwritten: %+v -> %+v", first, second)
	}
}

func TestStoreMarkDoneFinalizesTrailingStep(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	id := JobID("20260502-aaaa")
	if err := s.Save(Job{ID: id, Status: StatusRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.AppendStep(id, "doing thing"); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}
	if err := s.MarkDone(id, StatusError, "boom"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	got, _ := s.Get(id)
	if got.Steps[0].State != StepError {
		t.Fatalf("trailing step state: %s", got.Steps[0].State)
	}
}

func TestStoreDelete(t *testing.T) {
	s := NewStoreWithDir(t.TempDir())
	id := JobID("20260502-aaaa")
	if err := s.Save(Job{ID: id, Status: StatusDone}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.Get(id)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist after Delete, got %v", err)
	}
	// Deleting again is fine.
	if err := s.Delete(id); err != nil {
		t.Fatalf("re-Delete: %v", err)
	}
}

func TestNewIDFormat(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	id, err := NewID(now)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if got := string(id); len(got) != len("20260502-abcd") || got[:9] != "20260502-" {
		t.Fatalf("unexpected id format: %q", id)
	}
}
