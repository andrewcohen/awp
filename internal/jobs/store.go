package jobs

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// lockTimeout bounds how long we wait for the per-job advisory lock
// before giving up. Each subprocess owns its own job file; the lock is
// only contested when the deck is doing orphan-rewrite or GC at the
// same instant the subprocess heartbeats. Two seconds is plenty.
const lockTimeout = 2 * time.Second

// Store reads and writes job records under a directory, defaulting to
// ~/.awp/jobs/. All writes are atomic (flock + temp file + rename).
type Store struct {
	dir string
}

// NewStore returns a Store rooted at ~/.awp/jobs/. The directory is
// created lazily on first write.
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("resolve user home dir: %w", err)
	}
	return &Store{dir: filepath.Join(home, ".awp", "jobs")}, nil
}

// NewStoreWithDir returns a Store rooted at a caller-supplied directory.
// Used by tests so they don't touch ~/.awp.
func NewStoreWithDir(dir string) *Store { return &Store{dir: dir} }

// Dir returns the directory the store reads from and writes to.
func (s *Store) Dir() string { return s.dir }

// Path returns the JSON record path for a job ID. The file may not
// exist yet.
func (s *Store) Path(id JobID) string {
	return filepath.Join(s.dir, string(id)+".json")
}

// LogPath returns the sidecar log file path for a job ID.
func (s *Store) LogPath(id JobID) string {
	return filepath.Join(s.dir, string(id)+".log")
}

// NewID returns a fresh YYYYMMDD-<rand4> ID. Collision is improbable
// (4 chars × 36 alphabet = ~1.7M combinations per day) but callers
// should still be prepared for Save to refuse if the file exists.
func NewID(now time.Time) (JobID, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("rand: %w", err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return JobID(fmt.Sprintf("%s-%s", now.Format("20060102"), string(b))), nil
}

// Get loads a single job record by ID. Returns os.ErrNotExist when no
// record exists.
func (s *Store) Get(id JobID) (Job, error) {
	data, err := os.ReadFile(s.Path(id))
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return Job{}, fmt.Errorf("parse job %s: %w", id, err)
	}
	return j, nil
}

// List enumerates every job record in the directory. Records that fail
// to parse are skipped (logged as warnings inside ErrMsg-style notes
// would couple this package to a logger; instead we silently skip and
// surface bad records via Get for callers that care).
func (s *Store) List() ([]Job, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read jobs dir: %w", err)
	}
	out := make([]Job, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := JobID(strings.TrimSuffix(e.Name(), ".json"))
		j, err := s.Get(id)
		if err != nil {
			continue
		}
		out = append(out, j)
	}
	return out, nil
}

// Save writes the record to disk atomically. The job's ID is required
// and must match its filename.
func (s *Store) Save(job Job) error {
	if job.ID == "" {
		return errors.New("job id is empty")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create jobs dir: %w", err)
	}
	return s.withLock(job.ID, func() error {
		return s.writeLocked(job)
	})
}

// Update atomically loads, mutates via fn, and re-saves the record.
// fn is called under the per-job advisory lock so concurrent updaters
// (subprocess heartbeat + deck orphan-rewrite) don't drop each other's
// changes. fn may return an error to abort the write.
func (s *Store) Update(id JobID, fn func(*Job) error) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create jobs dir: %w", err)
	}
	return s.withLock(id, func() error {
		j, err := s.Get(id)
		if err != nil {
			return err
		}
		if err := fn(&j); err != nil {
			return err
		}
		return s.writeLocked(j)
	})
}

// Delete removes the JSON record and its sidecar log file (if any).
// Missing files are not errors.
func (s *Store) Delete(id JobID) error {
	if err := os.Remove(s.Path(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.LogPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.lockPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// AppendLog adds a single line to a job's inline log buffer (capped at
// MaxInlineLogLines, oldest dropped). Updates LastHeartbeat as a side
// effect — any work the subprocess does is implicit liveness.
func (s *Store) AppendLog(id JobID, line string) error {
	return s.Update(id, func(j *Job) error {
		j.LogsInline = append(j.LogsInline, line)
		if len(j.LogsInline) > MaxInlineLogLines {
			j.LogsInline = j.LogsInline[len(j.LogsInline)-MaxInlineLogLines:]
		}
		j.LastHeartbeat = time.Now()
		return nil
	})
}

// AppendStep marks the current trailing step as done (if running) and
// pushes a new running step. Mirrors the existing chanReporter
// behavior in deckui/model.go so the UI semantics are identical.
func (s *Store) AppendStep(id JobID, label string) error {
	return s.Update(id, func(j *Job) error {
		if n := len(j.Steps); n > 0 && j.Steps[n-1].State == StepRunning {
			j.Steps[n-1].State = StepDone
		}
		j.Steps = append(j.Steps, Step{Label: label, State: StepRunning})
		j.LastHeartbeat = time.Now()
		return nil
	})
}

// MarkRunning transitions a pending job to running. Used by the
// subprocess once it starts the actual work.
func (s *Store) MarkRunning(id JobID) error {
	return s.Update(id, func(j *Job) error {
		if j.Status.IsTerminal() {
			return nil
		}
		j.Status = StatusRunning
		j.LastHeartbeat = time.Now()
		return nil
	})
}

// MarkDone transitions to a terminal status. The trailing running step
// (if any) is moved to a final state matching the outcome. errKind is
// optional and tags the error for the UI's typed-recovery flows (see
// ErrorKind* constants); pass "" for generic failures. errWorkspace
// names the workspace the failure attached to — empty for failures
// that don't target a specific workspace.
func (s *Store) MarkDone(id JobID, status JobStatus, errMsg, errKind, errWorkspace string) error {
	return s.Update(id, func(j *Job) error {
		if j.Status.IsTerminal() {
			return nil
		}
		j.Status = status
		j.ErrMsg = errMsg
		j.ErrorKind = errKind
		j.ErrorWorkspace = errWorkspace
		now := time.Now()
		j.EndedAt = &now
		j.LastHeartbeat = now
		if n := len(j.Steps); n > 0 && j.Steps[n-1].State == StepRunning {
			if status == StatusDone {
				j.Steps[n-1].State = StepDone
			} else {
				j.Steps[n-1].State = StepError
			}
		}
		return nil
	})
}

// Heartbeat bumps LastHeartbeat. Cheap and called frequently.
func (s *Store) Heartbeat(id JobID) error {
	return s.Update(id, func(j *Job) error {
		if j.Status.IsTerminal() {
			return nil
		}
		j.LastHeartbeat = time.Now()
		return nil
	})
}

func (s *Store) writeLocked(job Job) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("encode job: %w", err)
	}
	target := s.Path(job.ID)
	tmp, err := os.CreateTemp(s.dir, "."+string(job.ID)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (s *Store) lockPath(id JobID) string {
	return filepath.Join(s.dir, "."+string(id)+".lock")
}

// withLock takes a per-job advisory lock so concurrent writers
// serialize. Times out after lockTimeout to avoid deadlocking the
// deck on a stuck subprocess.
func (s *Store) withLock(id JobID, fn func() error) error {
	lock := s.lockPath(id)
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open job lock: %w", err)
	}
	defer func() { _ = f.Close() }()

	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("flock job: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("flock job %s: timed out after %s", id, lockTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
